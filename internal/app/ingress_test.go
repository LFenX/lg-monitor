// Copyright (c) 2026 LFen (https://lfen.ink) — lg-monitor

package app

import (
	"context"
	"errors"
	"net"
	"sync/atomic"
	"testing"
	"testing/fstest"
	"time"

	"github.com/LFenX/lg-monitor/internal/config"
	"github.com/LFenX/lg-monitor/internal/pipeline"
	"github.com/LFenX/lg-monitor/internal/rules"
	"github.com/LFenX/lg-monitor/internal/store"
	"github.com/LFenX/lg-monitor/internal/transport"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/test/bufconn"
)

func TestPendingOfflineNotificationDoesNotBlockCollectorCommitOrReplay(t *testing.T) {
	for _, replay := range []bool{false, true} {
		t.Run(map[bool]string{false: "live batch", true: "durable inbox"}[replay], func(t *testing.T) {
			f := newRuntimeFixture(t)
			close(f.client.release)
			files := fstest.MapFS{"targets.yaml": f.files["targets.yaml"]}
			manager, err := config.NewManager(config.FSLoader{FS: files}, f.opts.Logger)
			if err != nil {
				t.Fatal(err)
			}
			f.opts.Config = manager
			f.opts.MinimumAlertDuration = nil
			lastSeen := f.clock.Now().Add(-65 * time.Second).UnixMilli()
			var connected atomic.Bool
			f.opts.Collectors = presenceSourceFunc(func(context.Context) ([]rules.CollectorPresence, error) {
				online, seen := connected.Load(), lastSeen
				if online {
					seen = f.clock.Now().UnixMilli()
				}
				return []rules.CollectorPresence{{ID: f.opts.ID, Hostname: "虚构主机", LastSeen: seen, Connected: online}}, nil
			})
			if err := f.db.RegisterCollector(context.Background(), store.CollectorRecord{ID: f.opts.ID, FirstSeen: lastSeen, LastSeen: lastSeen}); err != nil {
				t.Fatal(err)
			}
			receiver := transport.NewReceiver(nil)
			f.opts.Source = receiver
			server := transport.NewServer(receiver)
			listener := bufconn.Listen(1024 * 1024)
			go server.Serve(listener)
			t.Cleanup(func() { server.Stop(); listener.Close() })
			runtime, err := NewRuntime(f.opts)
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			finished := make(chan error, 1)
			go func() { finished <- runtime.Run(ctx) }()
			defer func() {
				cancel()
				if err := <-finished; err != nil {
					t.Error(err)
				}
			}()
			ticker := receiveRuntime(t, f.clock.created)
			f.clock.tick(ticker, time.Second)
			for {
				alerts, err := f.db.ActiveAlerts(ctx)
				if err != nil {
					t.Fatal(err)
				}
				if len(alerts) == 1 && alerts[0].RuleID == config.DefaultOfflineRuleID {
					break
				}
				select {
				case <-ctx.Done():
					t.Fatal("offline alert did not persist")
				case <-time.After(time.Millisecond):
				}
			}
			connected.Store(true)
			message := pipeline.Message{CollectorID: f.opts.ID, Seq: 1, TS: f.clock.Now().UnixMilli(), Kind: pipeline.KindTargetStatus, TargetStatus: &pipeline.TargetStatus{TargetID: "demo", State: "up", Process: "found"}}
			lastAck := uint64(0)
			if replay {
				if _, _, err := f.db.Ingest(ctx, message); err != nil {
					t.Fatal(err)
				}
				f.clock.tick(ticker, time.Second)
				for {
					watermark, err := f.db.LastCommitted(ctx, f.opts.ID)
					if err != nil {
						t.Fatal(err)
					}
					if watermark == 1 {
						break
					}
					select {
					case <-ctx.Done():
						t.Fatal("pending notification blocked durable inbox replay")
					case <-time.After(time.Millisecond):
					}
				}
				lastAck, message.Seq = 1, 2
			}
			connection, err := grpc.NewClient("passthrough:///fixture", grpc.WithTransportCredentials(insecure.NewCredentials()), grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) { return listener.DialContext(ctx) }))
			if err != nil {
				t.Fatal(err)
			}
			defer connection.Close()
			writer, err := transport.NewWriterWithProcesses(ctx, connection, transport.Hello{CollectorID: f.opts.ID, LastAckedSeq: lastAck}, nil, nil, f.clock.Now)
			if err != nil {
				t.Fatal(err)
			}
			defer writer.Close()
			probe := pipeline.Message{CollectorID: f.opts.ID, Seq: message.Seq + 1, TS: message.TS, Kind: pipeline.KindProbeResult, ProbeResult: &pipeline.ProbeResult{TargetID: "demo", Probe: 0, TS: message.TS, OK: true, StatusCode: 200}}
			if err := writer.WriteBatch(ctx, []pipeline.Message{message, probe}); err != nil {
				t.Fatalf("pending notification blocked collector acknowledgement: %v", err)
			}
			if watermark, err := f.db.LastCommitted(ctx, f.opts.ID); err != nil || watermark != probe.Seq {
				t.Fatalf("collector watermark=%d, error=%v", watermark, err)
			}
			pending, err := f.db.PendingNotifications(ctx, 0, f.clock.Now().UnixMilli(), 0, 10)
			if err != nil || len(pending) != 1 {
				t.Fatalf("deferred notification was lost: %+v %v", pending, err)
			}
			files["notifiers.yaml"] = f.files["notifiers.yaml"]
			if err := manager.Reload(); err != nil {
				t.Fatal(err)
			}
			// Reload publishes asynchronously to Runtime; keep evaluating until it applies the channel.
			for len(f.client.wire) == 0 {
				f.clock.tick(ticker, time.Second)
				select {
				case <-ctx.Done():
					t.Fatal("channel addition did not retry the deferred notification")
				case <-time.After(time.Millisecond):
				}
			}
			firing := receiveRuntime(t, f.client.wire)
			resolved := receiveRuntime(t, f.client.wire)
			if firing.State != rules.StateFiring || resolved.State != rules.StateResolved || firing.AlertID != resolved.AlertID {
				t.Fatalf("deferred notification order: %+v, %+v", firing, resolved)
			}
		})
	}
}

type ingressTransitionFailure struct {
	*runtimeStore
	fail atomic.Bool
}

func (s *ingressTransitionFailure) SaveTransition(ctx context.Context, alert rules.Alert, event rules.Event) error {
	if s.fail.Load() {
		return errors.New("fake failed alert transaction")
	}
	return s.runtimeStore.SaveTransition(ctx, alert, event)
}

func TestFailedAlertPersistenceStillWithholdsCollectorWatermark(t *testing.T) {
	f := newRuntimeFixture(t)
	message := pipeline.Message{CollectorID: f.opts.ID, Seq: 1, TS: f.clock.Now().UnixMilli(), Kind: pipeline.KindTargetStatus, TargetStatus: &pipeline.TargetStatus{TargetID: "demo", State: "down", Process: "missing"}}
	if _, _, err := f.db.Ingest(context.Background(), message); err != nil {
		t.Fatal(err)
	}
	failure := &ingressTransitionFailure{runtimeStore: f.db}
	failure.fail.Store(true)
	f.opts.Store = failure
	f.opts.Source = transport.NewReceiver(nil)
	runtime, err := NewRuntime(f.opts)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	finished := make(chan error, 1)
	go func() { finished <- runtime.Run(ctx) }()
	defer func() {
		cancel()
		if err := <-finished; err != nil {
			t.Error(err)
		}
	}()
	ticker := receiveRuntime(t, f.clock.created)
	if watermark, err := f.db.LastCommitted(ctx, f.opts.ID); err != nil || watermark != 0 {
		t.Fatalf("failed alert transaction was acknowledged: %d %v", watermark, err)
	}
	if _, exists, err := f.db.PendingIngress(ctx, f.opts.ID); err != nil || !exists {
		t.Fatalf("failed alert transaction lost its durable inbox: %v %v", exists, err)
	}
	failure.fail.Store(false)
	f.clock.tick(ticker, time.Second)
	if card := receiveRuntime(t, f.client.wire); card.State != rules.StateFiring {
		t.Fatalf("repaired alert transaction did not notify: %+v", card)
	}
	for {
		watermark, err := f.db.LastCommitted(ctx, f.opts.ID)
		if err != nil {
			t.Fatal(err)
		}
		if watermark == 1 {
			break
		}
		select {
		case <-ctx.Done():
			t.Fatal("repaired alert transaction did not advance the watermark")
		case <-time.After(time.Millisecond):
		}
	}
}

func TestRuntimeRestoresUnappliedIngressBeforeAnyCollectorReconnects(t *testing.T) {
	f := newRuntimeFixture(t)
	message := pipeline.Message{CollectorID: f.opts.ID, Seq: 1, TS: f.clock.Now().UnixMilli(), Kind: pipeline.KindTargetStatus, TargetStatus: &pipeline.TargetStatus{TargetID: "demo", State: "down", Process: "missing", Reason: "虚构已持久化但未应用的变化"}}
	if _, needed, err := f.db.Ingest(context.Background(), message); err != nil || !needed {
		t.Fatalf("seed ingress: %v %v", needed, err)
	}
	if identity, exists, err := f.db.Collector(context.Background(), f.opts.ID); err != nil || !exists || identity.Hostname != "" || identity.Version != "" {
		t.Fatalf("sampling-only collector metadata: %+v %v %v", identity, exists, err)
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
