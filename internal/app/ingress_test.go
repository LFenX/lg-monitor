package app

import (
	"context"
	"testing"
	"time"

	"github.com/LFenX/lg-monitor/internal/pipeline"
	"github.com/LFenX/lg-monitor/internal/transport"
)

func TestRuntimeRestoresUnappliedIngressBeforeAnyCollectorReconnects(t *testing.T) {
	f := newRuntimeFixture(t)
	message := pipeline.Message{CollectorID: f.opts.ID, Seq: 1, TS: f.clock.Now().UnixMilli(), Kind: pipeline.KindTargetStatus, TargetStatus: &pipeline.TargetStatus{TargetID: "demo", State: "down", Process: "missing", Reason: "虚构已持久化但未应用的变化"}}
	if _, needed, err := f.db.Ingest(context.Background(), message); err != nil || !needed {
		t.Fatalf("seed ingress: %v %v", needed, err)
	}
	f.opts.Source = transport.NewReceiver(nil)
	runtime, err := NewRuntime(f.opts)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	finished := make(chan error, 1)
	go func() { finished <- runtime.Run(ctx) }()
	defer func() {
		cancel()
		select {
		case err := <-finished:
			if err != nil {
				t.Error(err)
			}
		case <-time.After(5 * time.Second):
			t.Error("runtime did not stop")
		}
	}()
	messageSent := receiveRuntime(t, f.client.wire)
	if messageSent.State != "firing" {
		t.Fatalf("unapplied ingress was not consumed: %+v", messageSent)
	}
	if attempt := receiveRuntime(t, f.db.attempts); !attempt.OK {
		t.Fatal("restored transition was not audited")
	}
	receiveRuntime(t, f.clock.created)
	if watermark, err := f.db.LastCommitted(ctx, f.opts.ID); err != nil || watermark != 1 {
		t.Fatalf("restored ingress watermark: %d %v", watermark, err)
	}
	if _, exists, err := f.db.PendingIngress(ctx, f.opts.ID); err != nil || exists {
		t.Fatalf("restored inbox not cleared: %v %v", exists, err)
	}
	select {
	case <-f.source.requests:
		t.Fatal("remote monitor started a local collector")
	default:
	}
}
