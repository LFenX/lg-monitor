package app

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/LFenX/lg-monitor/internal/config"
	"github.com/LFenX/lg-monitor/internal/notify"
	"github.com/LFenX/lg-monitor/internal/rules"
)

type notificationSecrets map[string]string

func (s notificationSecrets) Get(_ context.Context, id string) ([]byte, error) {
	return []byte(s[id]), nil
}

func TestSlowWebhookDoesNotBlockFeishuAndKeepsChannelOrder(t *testing.T) {
	f := newRuntimeFixture(t)
	close(f.client.release)
	started, release, wire := make(chan struct{}, 2), make(chan struct{}), make(chan config.WebhookPayload, 2)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body config.WebhookPayload
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		started <- struct{}{}
		select {
		case <-release:
		case <-r.Context().Done():
			return
		}
		wire <- body
		w.WriteHeader(200)
	}))
	defer server.Close()
	defer close(release)
	q := policyQueue(t, f)
	q.active.Notifiers = append(q.active.Notifiers, config.Notifier{ID: "slow-hook", Type: "webhook", URLRef: "slow-url"})
	q.active.Rules[0].Notify = []string{"slow-hook", "test-channel"}
	q.secrets = notificationSecrets{"slow-url": server.URL, "fake-runtime-ref": "fake-secret"}
	q.jsonWebhookFactory = func(endpoint, secret []byte) (notify.JSONWebhookClient, error) {
		return notify.NewJSONWebhookClient(endpoint, secret, notify.JSONWebhookOptions{Now: f.clock.Now})
	}
	alert := seedUnsent(t, f, false)
	if err := q.Enqueue(context.Background(), alert); err != nil {
		t.Fatal(err)
	}
	alert.State = "resolved"
	alert.ResolvedAt = f.clock.Now().UnixMilli() + 1
	alert.NotificationAt = alert.ResolvedAt
	alert.NotificationKey = "resolved:1000001"
	if err := f.db.SaveTransition(context.Background(), alert, rules.Event{AlertID: alert.ID, Kind: "resolved", TS: alert.ResolvedAt}); err != nil {
		t.Fatal(err)
	}
	if err := q.Enqueue(context.Background(), alert); err != nil {
		t.Fatal(err)
	}
	close(q.jobs)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- q.run(ctx) }()
	receiveRuntime(t, started)
	for _, state := range []string{"firing", "resolved"} {
		select {
		case message := <-f.client.wire:
			if message.State != state {
				t.Fatalf("Feishu order=%s want=%s", message.State, state)
			}
		case <-time.After(time.Second):
			t.Fatal("slow Webhook blocked Feishu for over one second")
		}
	}
	// Permit both requests only after the fast destination has already completed.
	release <- struct{}{}
	release <- struct{}{}
	for _, state := range []string{"firing", "resolved"} {
		if payload := receiveRuntime(t, wire); payload.Data.Alert.State != state {
			t.Fatal("Webhook FIFO order changed")
		}
	}
	if err := receiveRuntime(t, done); err != nil {
		t.Fatal(err)
	}
}

func TestNotificationFiltersUseCurrentConfigBeforeSendAndReplay(t *testing.T) {
	f := newRuntimeFixture(t)
	close(f.client.release)
	alert := seedUnsent(t, f, false)
	newQueue := func() *notificationQueue {
		q := policyQueue(t, f)
		q.active.Notifiers[0].Filter = &config.NotifierFilter{Tags: []string{"web"}}
		return q
	}
	q := newQueue()
	if err := q.Enqueue(context.Background(), alert); err != nil {
		t.Fatal(err)
	}
	// The target gained the matching tag after the event was queued.
	updated := f.opts.Config.Snapshot()
	updated.Notifiers[0].Filter = &config.NotifierFilter{Tags: []string{"web"}}
	updated.Targets[0].Tags = []string{"web"}
	q.setConfig(updated)
	close(q.jobs)
	if err := q.run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(f.client.wire) != 1 || len(f.db.attempts) != 1 {
		t.Fatal("current target tags were not used")
	}
	<-f.client.wire
	<-f.db.attempts
	alert.State = "resolved"
	alert.ResolvedAt = f.clock.Now().UnixMilli() + 1
	alert.NotificationAt = alert.ResolvedAt
	alert.NotificationKey = "resolved:1000001"
	if err := f.db.SaveTransition(context.Background(), alert, rules.Event{AlertID: alert.ID, Kind: "resolved", TS: alert.ResolvedAt}); err != nil {
		t.Fatal(err)
	}
	q = newQueue()
	q.active.Targets[0].Tags = []string{"web"}
	if err := q.Enqueue(context.Background(), alert); err != nil {
		t.Fatal(err)
	}
	updated = f.opts.Config.Snapshot()
	updated.Notifiers[0].Filter = &config.NotifierFilter{Tags: []string{"web"}}
	q.setConfig(updated)
	var logs bytes.Buffer
	q.logger = slog.New(slog.NewTextHandler(&logs, nil))
	close(q.jobs)
	if err := q.run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(f.client.wire) != 0 || len(f.db.attempts) != 0 || strings.Count(logs.String(), "All notification channels filtered out") != 1 {
		t.Fatalf("filtered notification sent, retried or incorrectly logged: %s", logs.String())
	}
	q = newQueue()
	f.clock.millis.Add(1)
	if err := q.replay(context.Background()); err != nil {
		t.Fatal(err)
	}
	close(q.jobs)
	if err := q.run(context.Background()); err != nil || len(f.client.wire) != 0 || len(f.db.attempts) != 0 {
		t.Fatalf("replay ignored current filter: %v", err)
	}
	q = newQueue()
	q.active.Targets[0].Tags = []string{"web"}
	if err := q.replay(context.Background()); err != nil {
		t.Fatal(err)
	}
	close(q.jobs)
	if err := q.run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(f.client.wire) != 1 || (<-f.client.wire).State != "resolved" {
		t.Fatal("previously filtered recovery did not replay when current filter matched")
	}
}

func TestNotificationFilterCannotAddDestinationsAndTaglessNeverMatches(t *testing.T) {
	f := newRuntimeFixture(t)
	q := policyQueue(t, f)
	alert := seedUnsent(t, f, false)
	other := q.active.Notifiers[0]
	other.ID = "not-in-notify"
	q.active.Notifiers = append(q.active.Notifiers, other)
	job, err := q.job(alert)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := q.prepareDelivery(context.Background(), job, other); ok {
		t.Fatal("filter added destination outside rule.notify")
	}
	q.active.Notifiers[0].Filter = &config.NotifierFilter{Tags: []string{"web"}}
	q.active.Targets[0].Tags = []string{"web"}
	job.input.Alert.TargetID = "missing-sql-target"
	if _, ok := q.prepareDelivery(context.Background(), job, q.active.Notifiers[0]); ok {
		t.Fatal("unknown SQL target matched tags")
	}
	offline := config.OfflineRules(q.active)[0]
	job.input.Alert.RuleID, job.input.Alert.TargetID = offline.ID, rules.CollectorTargetID
	if _, ok := q.prepareDelivery(context.Background(), job, q.active.Notifiers[0]); ok {
		t.Fatal("offline target matched tags")
	}
	q.active.Notifiers[0].Filter = &config.NotifierFilter{Collectors: []string{"FAKE"}}
	job.input.Alert = alert
	if _, ok := q.prepareDelivery(context.Background(), job, q.active.Notifiers[0]); !ok {
		t.Fatal("known hostname did not match without case sensitivity")
	}
}
