package transport

import (
	"context"
	"math"
	"path/filepath"
	"testing"
	"time"

	"github.com/LFenX/lg-monitor/internal/collector"
	"github.com/LFenX/lg-monitor/internal/pipeline"
	lgmonv1 "github.com/LFenX/lg-monitor/proto/lgmon/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type heartbeatClock struct{ created chan *configTicker }

func (c heartbeatClock) NewTicker(interval time.Duration) collector.ProbeTicker {
	if interval != HeartbeatInterval {
		panic("incorrect heartbeat interval")
	}
	ticker := &configTicker{ticks: make(chan time.Time, 1)}
	c.created <- ticker
	return ticker
}

func TestHeartbeatRunsWithoutSamplesAndDoesNotConsumeRecordSequence(t *testing.T) {
	ctx, receiver, connection, _ := linkFixture(t)
	received := make(chan Hello, 4)
	receiver.SetHeartbeat(func(_ context.Context, identity Hello) error { received <- identity; return nil })
	writer, err := NewWriter(ctx, connection, Hello{CollectorID: "fake-collector", Hostname: "fake-host"})
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Close()
	clock := heartbeatClock{created: make(chan *configTicker, 1)}
	box, err := OpenOutbox(ctx, OutboxOptions{Path: filepath.Join(t.TempDir(), "outbox.db"), CollectorID: "fake-collector", Now: func() time.Time { return time.UnixMilli(123000) }, Locker: fakeCacheLocker{}})
	if err != nil {
		t.Fatal(err)
	}
	defer box.Close()
	delivery := &Delivery{options: DeliveryOptions{Clock: clock, Outbox: box}}
	beatCtx, cancel := context.WithCancel(ctx)
	done := make(chan error, 1)
	go func() { done <- delivery.heartbeats(beatCtx, writer) }()
	defer func() {
		cancel()
		if err := <-done; err != nil {
			t.Error(err)
		}
	}()
	var ticker *configTicker
	select {
	case ticker = <-clock.created:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	for i := 0; i < 2; i++ {
		ticker.ticks <- time.Time{}
		select {
		case identity := <-received:
			if identity.CollectorID != "fake-collector" || identity.Cache == nil || identity.Cache.MaxBytes != DefaultCacheBytes ||
				identity.Cache.BytesPerHour != nil || identity.Cache.ProjectedHours != nil {
				t.Fatal("wrong heartbeat identity")
			}
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		}
	}
	writeResult := make(chan error, 1)
	go func() { writeResult <- writer.Write(ctx, wireMessage(1)) }()
	message, err := receiver.Read(ctx)
	if err != nil {
		t.Fatal(err)
	}
	ticker.ticks <- time.Time{}
	receiver.Acknowledge(message, nil)
	select {
	case err := <-writeResult:
		if err != nil {
			t.Fatal(err)
		}
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	select {
	case <-received:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	if writer.next != 2 {
		t.Fatal("heartbeat advanced data sequence")
	}
}

func TestHeartbeatCarriesWarmedCacheAndLegacyClearsIt(t *testing.T) {
	ctx, receiver, connection, _ := linkFixture(t)
	received := make(chan Hello, 2)
	receiver.SetHeartbeat(func(_ context.Context, hello Hello) error { received <- hello; return nil })
	writer, err := NewWriter(ctx, connection, Hello{CollectorID: "fake-collector"})
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Close()
	rate := 25000000.0
	usage, err := pipeline.NewCacheUsage(123456, 500000000, &rate)
	if err != nil {
		t.Fatal(err)
	}
	if err := writer.Heartbeat(ctx, 123000, usage); err != nil {
		t.Fatal(err)
	}
	select {
	case hello := <-received:
		if hello.Cache == nil || hello.Cache.Bytes != usage.Bytes || *hello.Cache.ProjectedHours != *usage.ProjectedHours {
			t.Fatalf("cache heartbeat: %+v", hello.Cache)
		}
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	for receiver.CacheUsage("fake-collector") == nil {
		select {
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		case <-time.After(time.Millisecond):
		}
	}
	if err := writer.Heartbeat(ctx, 124000); err != nil {
		t.Fatal(err)
	}
	select {
	case hello := <-received:
		if hello.Cache != nil {
			t.Fatal("legacy heartbeat invented statistics")
		}
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	for receiver.CacheUsage("fake-collector") != nil {
		select {
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		case <-time.After(time.Millisecond):
		}
	}
}

func TestHeartbeatRejectsInvalidCacheTelemetryBeforePersistence(t *testing.T) {
	nan := math.NaN()
	for _, invalid := range []struct {
		name      string
		heartbeat *lgmonv1.Heartbeat
	}{
		{"negative", &lgmonv1.Heartbeat{Ts: 123000, CacheBytes: -1, CacheMaxBytes: DefaultCacheBytes}},
		{"missing-limit", &lgmonv1.Heartbeat{Ts: 123000, CacheBytes: 100}},
		{"non-finite", &lgmonv1.Heartbeat{Ts: 123000, CacheMaxBytes: DefaultCacheBytes, CacheBytesPerHour: &nan}},
	} {
		t.Run(invalid.name, func(t *testing.T) {
			ctx, receiver, connection, _ := linkFixture(t)
			persisted := make(chan struct{}, 1)
			receiver.SetHeartbeat(func(context.Context, Hello) error { persisted <- struct{}{}; return nil })
			stream, err := lgmonv1.NewCollectorLinkClient(connection).Connect(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if err := stream.Send(&lgmonv1.CollectorMessage{Body: &lgmonv1.CollectorMessage_Hello{Hello: &lgmonv1.Hello{CollectorId: "fake-collector"}}}); err != nil {
				t.Fatal(err)
			}
			if _, err := stream.Recv(); err != nil {
				t.Fatal(err)
			}
			if err := stream.Send(&lgmonv1.CollectorMessage{Body: &lgmonv1.CollectorMessage_Heartbeat{Heartbeat: invalid.heartbeat}}); err != nil {
				t.Fatal(err)
			}
			if _, err := stream.Recv(); status.Code(err) != codes.InvalidArgument {
				t.Fatalf("invalid cache heartbeat: %v", err)
			}
			select {
			case <-persisted:
				t.Fatal("invalid telemetry reached persistence")
			default:
			}
		})
	}
}
