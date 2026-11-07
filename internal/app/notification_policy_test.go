package app

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/LFenX/lg-monitor/internal/notify/cards"
	"github.com/LFenX/lg-monitor/internal/pipeline"
	"github.com/LFenX/lg-monitor/internal/rules"
)

func TestQueuedFourthTriggerRendersOneDigestWithItsEventIdentity(t *testing.T) {
	f := newRuntimeFixture(t)
	close(f.client.release)
	var alert rules.Alert
	for i := 0; i < 4; i++ {
		at := f.clock.Now().UnixMilli()
		alert = rules.Alert{ID: fmt.Sprintf("fake-flap-%d", i), RuleID: "down", CollectorID: f.opts.ID, TargetID: "demo", State: "firing", Severity: "critical", StartedAt: at, NotificationAt: at, NotificationKey: fmt.Sprintf("firing:%d", at), Summary: "fake flapping condition"}
		if err := f.db.SaveTransition(context.Background(), alert, rules.Event{AlertID: alert.ID, TS: at, Kind: "firing"}); err != nil {
			t.Fatal(err)
		}
		if i < 3 {
			f.clock.millis.Add(time.Second.Milliseconds())
			at = f.clock.Now().UnixMilli()
			alert.State, alert.ResolvedAt, alert.NotificationAt, alert.NotificationKey = "resolved", at, at, fmt.Sprintf("resolved:%d", at)
			if err := f.db.SaveTransition(context.Background(), alert, rules.Event{AlertID: alert.ID, TS: at, Kind: "resolved"}); err != nil {
				t.Fatal(err)
			}
			f.clock.millis.Add(time.Second.Milliseconds())
		}
	}
	q := policyQueue(t, f)
	if err := q.Enqueue(context.Background(), alert); err != nil {
		t.Fatal(err)
	}
	close(q.jobs)
	if err := q.run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(f.client.wire) != 1 {
		t.Fatal("flapping did not produce exactly one queued message")
	}
	message := <-f.client.wire
	if !strings.Contains(string(message.Card), "告警汇总") || message.EventKey != alert.NotificationKey || message.AlertID != alert.ID {
		t.Fatalf("digest message: %+v", message)
	}
	attempt := <-f.db.attempts
	if attempt.Template != "digest" || attempt.EventKey != alert.NotificationKey {
		t.Fatalf("digest audit: %+v", attempt)
	}
}

func TestQueuedNotificationHonorsSilenceAddedAfterEnqueue(t *testing.T) {
	f := newRuntimeFixture(t)
	alert := seedUnsent(t, f, false)
	q := policyQueue(t, f)
	if err := q.Enqueue(context.Background(), alert); err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.AddSilence(context.Background(), "down", "", time.Hour, "test queued silence"); err != nil {
		t.Fatal(err)
	}
	close(q.jobs)
	if err := q.run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(f.client.wire) != 0 || len(f.db.attempts) != 0 {
		t.Fatal("queued silenced message was sent or recorded as a network attempt")
	}
	pending, err := f.db.PendingNotifications(context.Background(), 0, f.clock.Now().UnixMilli(), 0, 100)
	if err != nil || len(pending) != 0 {
		t.Fatalf("legacy suppressed event replayed: %+v %v", pending, err)
	}
}

func policyQueue(t *testing.T, f *runtimeFixture) *notificationQueue {
	t.Helper()
	renderer, err := cards.New(f.opts.Config, cards.Options{Now: f.clock.Now, Logger: f.opts.Logger})
	if err != nil {
		t.Fatal(err)
	}
	return &notificationQueue{active: f.opts.Config.Snapshot(), secrets: f.opts.Secrets, factory: f.opts.NewClient, webhookFactory: f.opts.NewWebhook, jsonWebhookFactory: f.opts.NewJSONWebhook, toast: f.opts.Toast, store: f.db, renderer: renderer, host: cards.Host{Name: "fake", CollectorID: f.opts.ID}, now: f.clock.Now, logger: f.opts.Logger, jobs: make(chan notificationJob, 10), targets: make(map[targetKey]pipeline.TargetStatus), clients: make(map[string]cachedClient)}
}
