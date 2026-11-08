package transport

import (
	"context"
	"errors"
	"net"
	"testing"
	"time"

	"github.com/LFenX/lg-monitor/internal/collector"
	"github.com/LFenX/lg-monitor/internal/config"
	lgmonv1 "github.com/LFenX/lg-monitor/proto/lgmon/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/test/bufconn"
)

type serviceSwitchSourceFunc func(context.Context) (config.ServiceSwitchSnapshot, error)

func (f serviceSwitchSourceFunc) ReadServiceSwitch(ctx context.Context) (config.ServiceSwitchSnapshot, error) {
	return f(ctx)
}

func TestServiceSwitchRoundTripCapabilityAndErrors(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	listener := bufconn.Listen(2 * MaxMessageSize)
	receiver := NewReceiver(nil)
	server := NewServer(receiver)
	go server.Serve(listener)
	defer server.Stop()
	defer listener.Close()
	defer receiver.Close()
	connection, err := grpc.NewClient("passthrough:///fake-service-switch", grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) { return listener.Dial() }), grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Close()
	summary, err := config.ParseServiceSwitch([]byte(`{"schemaVersion":1,"apps":[{"id":"fake-web","ports":[3000],"env":{"secret":"fake-private-secret"}}]}`))
	if err != nil {
		t.Fatal(err)
	}
	sha, _, _ := config.ServiceSwitchFingerprint(summary)
	snapshot := config.ServiceSwitchSnapshot{Summary: summary, SHA256: sha, ReadAt: 2000000000000}
	source := serviceSwitchSourceFunc(func(context.Context) (config.ServiceSwitchSnapshot, error) { return snapshot, nil })
	writer, err := NewWriterWithSources(ctx, connection, Hello{CollectorID: "fake-host", Hostname: "fake-pc"}, nil, nil, source)
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Close()
	actual, err := receiver.QueryServiceSwitch(ctx, "fake-host")
	if err != nil || actual.SHA256 != sha || len(actual.Summary.Apps) != 1 || actual.Summary.Apps[0].ID != "fake-web" || writer.ResumeFrom() != 1 {
		t.Fatalf("actual=%+v %v", actual, err)
	}
	if _, err := receiver.QueryServiceSwitch(ctx, "offline"); !errors.Is(err, ErrCollectorOffline) {
		t.Fatal(err)
	}
	legacy, err := NewWriter(ctx, connection, Hello{CollectorID: "old-host", Hostname: "old-pc"})
	if err != nil {
		t.Fatal(err)
	}
	defer legacy.Close()
	if _, err := receiver.QueryServiceSwitch(ctx, "old-host"); !errors.Is(err, ErrCollectorOutdated) {
		t.Fatal(err)
	}
	for _, failure := range []error{collector.ErrServiceSwitchNotConfigured, &collector.ServiceSwitchError{Message: "虚构文件不存在"}} {
		answer := answerServiceSwitch(ctx, serviceSwitchSourceFunc(func(context.Context) (config.ServiceSwitchSnapshot, error) {
			return config.ServiceSwitchSnapshot{}, failure
		}), &lgmonv1.ServiceSwitchQuery{RequestId: "1"})
		_, decoded := decodeServiceSwitch(answer)
		if decoded == nil || decoded.Error() != failure.Error() {
			t.Fatalf("wire=%+v error=%v", answer, decoded)
		}
	}
	if _, err := decodeServiceSwitch(&lgmonv1.ServiceSwitchSummary{ReadAt: snapshot.ReadAt, Sha256: sha, SummaryJson: []byte(`{"apps":[],"skipped":[],"env":{"secret":"fake-private"}}`)}); err == nil {
		t.Fatal("unknown wire field accepted")
	}
	cancelled, stop := context.WithCancel(ctx)
	stop()
	if _, err := receiver.QueryServiceSwitch(cancelled, "fake-host"); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}

func TestServiceSwitchSlowSourceDoesNotBlockACKOrEnterRecordQueue(t *testing.T) {
	ctx, receiver, connection, _ := linkFixture(t)
	started, release := make(chan struct{}), make(chan struct{})
	released := false
	defer func() {
		if !released {
			close(release)
		}
	}()
	summary, err := config.ParseServiceSwitch([]byte(`{"schemaVersion":1}`))
	if err != nil {
		t.Fatal(err)
	}
	sha, _, _ := config.ServiceSwitchFingerprint(summary)
	source := serviceSwitchSourceFunc(func(ctx context.Context) (config.ServiceSwitchSnapshot, error) {
		close(started)
		select {
		case <-ctx.Done():
			return config.ServiceSwitchSnapshot{}, ctx.Err()
		case <-release:
			return config.ServiceSwitchSnapshot{Summary: summary, SHA256: sha, ReadAt: 2000000000000}, nil
		}
	})
	writer, err := NewWriterWithSources(ctx, connection, Hello{CollectorID: "fake-collector"}, nil, nil, source)
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Close()
	queried := make(chan error, 1)
	go func() { _, err := receiver.QueryServiceSwitch(ctx, "fake-collector"); queried <- err }()
	select {
	case <-started:
	case <-ctx.Done():
		t.Fatal("source did not start")
	}
	writeCtx, stopWrite := context.WithTimeout(ctx, 2*time.Second)
	defer stopWrite()
	written := make(chan error, 1)
	go func() { written <- writer.Write(writeCtx, wireMessage(1)) }()
	record, err := receiver.Read(writeCtx)
	if err != nil || record.Seq != 1 {
		t.Fatal("source blocked normal record", err)
	}
	receiver.Acknowledge(record, nil)
	select {
	case err := <-written:
		if err != nil {
			t.Fatal("source blocked ACK", err)
		}
	case <-writeCtx.Done():
		t.Fatal("source blocked ACK")
	}
	released = true
	close(release)
	select {
	case err := <-queried:
		if err != nil {
			t.Fatal(err)
		}
	case <-ctx.Done():
		t.Fatal("query did not complete")
	}
	emptyCtx, stopEmpty := context.WithTimeout(ctx, 20*time.Millisecond)
	defer stopEmpty()
	if _, err := receiver.ReadBatch(emptyCtx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal("control reply entered record queue", err)
	}
}
