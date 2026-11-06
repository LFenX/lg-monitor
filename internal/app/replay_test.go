package app

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/LFenX/lg-monitor/internal/config"
	"github.com/LFenX/lg-monitor/internal/notify"
	"github.com/LFenX/lg-monitor/internal/rules"
	"testing/fstest"
)

func seedUnsent(t *testing.T, f *runtimeFixture, resolved bool) rules.Alert {
	t.Helper()
	ctx := context.Background()
	alert := rules.Alert{ID: "fake-unsent", RuleID: "down", CollectorID: f.opts.ID, TargetID: "demo", State: "firing", Severity: "critical", StartedAt: f.clock.Now().UnixMilli()}
	if err := f.db.SaveTransition(ctx, alert, rules.Event{AlertID: alert.ID, TS: alert.StartedAt, Kind: "firing", Message: "虚构触发"}); err != nil {
		t.Fatal(err)
	}
	if resolved {
		alert.State, alert.ResolvedAt = "resolved", alert.StartedAt
		if err := f.db.SaveTransition(ctx, alert, rules.Event{AlertID: alert.ID, TS: alert.StartedAt, Kind: "resolved", Message: "虚构恢复"}); err != nil {
			t.Fatal(err)
		}
	}
	return alert
}

func TestRuntimeReplaysCommittedUnsentTransitionsBeforeLiveData(t *testing.T) {
	for _, resolved := range []bool{false, true} {
		t.Run(map[bool]string{false: "firing", true: "firing then resolved"}[resolved], func(t *testing.T) {
			f := newRuntimeFixture(t)
			alert := seedUnsent(t, f, resolved)
			close(f.client.release)
			runtime, err := NewRuntime(f.opts)
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			done := make(chan error, 1)
			go func() { done <- runtime.Run(ctx) }()
			firing := receiveRuntime(t, f.client.wire)
			if firing.AlertID != alert.ID || firing.State != "firing" {
				t.Fatalf("committed unsent firing was not replayed: %+v", firing)
			}
			if attempt := receiveRuntime(t, f.db.attempts); !attempt.OK || attempt.State != "firing" {
				t.Fatalf("firing replay was not audited: %+v", attempt)
			}
			if resolved {
				if recovery := receiveRuntime(t, f.client.wire); recovery.AlertID != alert.ID || recovery.State != "resolved" {
					t.Fatalf("historical recovery overtook firing: %+v", recovery)
				}
				if attempt := receiveRuntime(t, f.db.attempts); !attempt.OK || attempt.State != "resolved" {
					t.Fatal("recovery replay was not audited separately")
				}
			}
			cancel()
			if err := receiveRuntime(t, done); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestReplayOnlyRetriesDestinationsWithoutSuccess(t *testing.T) {
	f := newRuntimeFixture(t)
	f.files["notifiers.yaml"].Data = append(f.files["notifiers.yaml"].Data, []byte("- id: second-channel\n  type: feishu_app\n  app: default\n  chatId: oc_fake_second\n")...)
	f.files["rules.yaml"].Data = bytes.Replace(f.files["rules.yaml"].Data, []byte("[test-channel]"), []byte("[test-channel, second-channel]"), 1)
	if err := f.opts.Config.(*config.Manager).Reload(); err != nil {
		t.Fatal(err)
	}
	alert := seedUnsent(t, f, false)
	if err := f.db.RecordNotification(context.Background(), notify.Attempt{ID: "fake-already-delivered", AlertID: alert.ID, State: "firing", NotifierID: "test-channel", TS: f.clock.Now().UnixMilli(), OK: true, MessageID: "om_fake_existing"}); err != nil {
		t.Fatal(err)
	}
	receiveRuntime(t, f.db.attempts)
	runtime, err := NewRuntime(f.opts)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- runtime.Run(ctx) }()
	if message := receiveRuntime(t, f.client.wire); message.ChatID != "oc_fake_second" {
		t.Fatalf("replay resent a successful destination: %+v", message)
	}
	if attempt := receiveRuntime(t, f.db.attempts); !attempt.OK || attempt.NotifierID != "second-channel" {
		t.Fatalf("wrong replay destination audit: %+v", attempt)
	}
	cancel()
	if err := receiveRuntime(t, done); err != nil || len(f.client.wire) != 0 {
		t.Fatalf("replay duplicated a notification: %v", err)
	}
}

func TestFullNotificationQueueReturnsWithoutBlockingRules(t *testing.T) {
	manager, err := config.NewManager(config.FSLoader{FS: fstest.MapFS{
		"targets.yaml": {Data: []byte("targets: [{id: demo, match: {process: demo.exe}}]\n")},
	}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	cfg := manager.Snapshot()
	cfg.Rules = []config.Rule{{ID: "down", Target: "demo", Notify: []string{"fake"}}}
	cfg.Notifiers = []config.Notifier{{ID: "fake"}}
	queue := &notificationQueue{active: cfg, jobs: make(chan notificationJob, 1), logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	alert := rules.Alert{ID: "fake", RuleID: "down", TargetID: "demo", State: "firing"}
	if err := queue.Enqueue(ctx, alert); err != nil {
		t.Fatal(err)
	}
	returned := make(chan error, 1)
	go func() { returned <- queue.Enqueue(ctx, alert) }()
	select {
	case err := <-returned:
		if !errors.Is(err, ErrNotificationQueueFull) {
			t.Fatalf("full queue did not report retryable pressure: %v", err)
		}
	case <-time.After(time.Second):
		cancel()
		t.Fatal("full queue blocked rule evaluation")
	}
}

func TestReplayDoesNotResendSuccessfullyAuditedTransition(t *testing.T) {
	f := newRuntimeFixture(t)
	alert := seedUnsent(t, f, false)
	if err := f.db.RecordNotification(context.Background(), notify.Attempt{ID: "fake-success", AlertID: alert.ID, State: "firing", NotifierID: "test-channel", TS: f.clock.Now().UnixMilli(), OK: true, MessageID: "om_fake_success"}); err != nil {
		t.Fatal(err)
	}
	receiveRuntime(t, f.db.attempts)
	runtime, err := NewRuntime(f.opts)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- runtime.Run(ctx) }()
	receiveRuntime(t, f.source.requests)
	cancel()
	if err := receiveRuntime(t, done); err != nil || len(f.client.wire) != 0 || f.factories.Load() != 0 {
		t.Fatalf("audited transition was resent: %v", err)
	}
}
