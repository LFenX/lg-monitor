package pipeline

import (
	"context"
	"errors"
	"io"
	"sync"
	"testing"
	"time"

	"github.com/LFenX/lg-monitor/internal/collector"
)

func statusMessage() Message {
	return Message{CollectorID: "demo", Seq: 1, TS: 1000, Kind: KindTargetStatus, TargetStatus: &TargetStatus{TargetID: "web", State: collector.StateUp, Processes: []ProcessRef{{PID: 42, CreatedAt: 1}}, Ports: []collector.Port{{PID: 42, Port: 3001}}}}
}

func TestBufferedChannelOwnsRecordsAndDrainsOnClose(t *testing.T) {
	channel := NewChannel(1)
	message := statusMessage()
	if err := channel.Write(context.Background(), message); err != nil {
		t.Fatal(err)
	}
	message.TargetStatus.State = collector.StateDown
	message.TargetStatus.Processes[0].PID = 99
	message.TargetStatus.Ports[0].Port = 9999
	if err := channel.Close(); err != nil {
		t.Fatal(err)
	}
	got, err := channel.Read(context.Background())
	if err != nil || got.TargetStatus.State != collector.StateUp || got.TargetStatus.Processes[0].PID != 42 || got.TargetStatus.Ports[0].Port != 3001 {
		t.Fatalf("record changed after enqueue: %+v, %v", got, err)
	}
	if _, err := channel.Read(context.Background()); !errors.Is(err, io.EOF) {
		t.Fatalf("closed channel read = %v", err)
	}
	if err := channel.Write(context.Background(), statusMessage()); !errors.Is(err, io.ErrClosedPipe) {
		t.Fatalf("closed channel write = %v", err)
	}
	if err := channel.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestBackpressureCancellationAndConcurrentClose(t *testing.T) {
	for _, cancelWrite := range []bool{true, false} {
		channel := NewChannel(1)
		if err := channel.Write(context.Background(), statusMessage()); err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithCancel(context.Background())
		done := make(chan error, 1)
		go func() { done <- channel.Write(ctx, statusMessage()) }()
		if cancelWrite {
			cancel()
		} else {
			if err := channel.Close(); err != nil {
				t.Fatal(err)
			}
		}
		select {
		case err := <-done:
			want := io.ErrClosedPipe
			if cancelWrite {
				want = context.Canceled
			}
			if !errors.Is(err, want) {
				t.Fatalf("write = %v, want %v", err, want)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("blocked writer did not stop")
		}
		cancel()
		if err := channel.Close(); err != nil {
			t.Fatal(err)
		}
	}
	channel := NewChannel(1)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := channel.Read(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("read cancellation = %v", err)
	}
}

func TestConcurrentEmitterMaintainsWireOrder(t *testing.T) {
	const count = 80
	channel := NewChannel(count)
	emitter, err := NewEmitter("collector-demo", channel, func() time.Time { return time.UnixMilli(123456) })
	if err != nil {
		t.Fatal(err)
	}
	var workers sync.WaitGroup
	for i := 0; i < count; i++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			if err := emitter.Emit(context.Background(), Message{Kind: KindProbeResult, ProbeResult: &ProbeResult{TargetID: "web"}}); err != nil {
				t.Error(err)
			}
		}()
	}
	workers.Wait()
	if err := channel.Close(); err != nil {
		t.Fatal(err)
	}
	for seq := uint64(1); seq <= count; seq++ {
		message, err := channel.Read(context.Background())
		if err != nil || message.Seq != seq || message.CollectorID != "collector-demo" || message.TS != 123456 {
			t.Fatalf("out of order record: %+v, %v", message, err)
		}
	}
}

func TestInvalidMessagesAreRejected(t *testing.T) {
	for _, mutate := range []func(*Message){
		func(m *Message) { m.Seq = 0 },
		func(m *Message) { m.CollectorID = "" },
		func(m *Message) { m.Kind = KindProbeResult },
		func(m *Message) { m.ProbeResult = &ProbeResult{TargetID: "web"} },
		func(m *Message) { m.TargetStatus.State = "invalid" },
	} {
		message := statusMessage()
		mutate(&message)
		if err := message.Validate(); err == nil {
			t.Fatalf("invalid message accepted: %+v", message)
		}
	}
}
