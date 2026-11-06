package transport

import (
	"context"
	"testing"
	"time"

	"github.com/LFenX/lg-monitor/internal/collector"
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
	delivery := &Delivery{options: DeliveryOptions{Clock: clock, Outbox: &Outbox{options: OutboxOptions{Now: func() time.Time { return time.UnixMilli(123000) }}}}}
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
			if identity.CollectorID != "fake-collector" {
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
