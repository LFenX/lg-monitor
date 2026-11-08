package app

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/LFenX/lg-monitor/internal/config"
	"github.com/LFenX/lg-monitor/internal/feishu"
	"github.com/LFenX/lg-monitor/internal/notify"
	"github.com/LFenX/lg-monitor/internal/rules"
	"github.com/LFenX/lg-monitor/internal/store"
)

type pauseWaiter struct{}

func (pauseWaiter) Wait(ctx context.Context, _ time.Duration) error { return ctx.Err() }

type pauseRetryClient struct {
	*runtimeClient
	messages []feishu.CardMessage
	failures int
}

func (c *pauseRetryClient) SendCard(_ context.Context, m feishu.CardMessage) (feishu.SendResult, error) {
	c.messages = append(c.messages, m)
	if len(c.messages) <= c.failures {
		return feishu.SendResult{}, &feishu.Error{Kind: feishu.RateLimited, Message: "fake retry"}
	}
	return feishu.SendResult{MessageID: "om_fake_resume"}, nil
}

func TestPauseCatchupQueueDurablePartialDeliveryAndStableRetries(t *testing.T) {
	ctx := context.Background()
	f := newRuntimeFixture(t)
	q := policyQueue(t, f)
	q.active.Rules[0].Template = "standard"
	q.active.Rules[0].Notify = append(q.active.Rules[0].Notify, "second")
	q.active.Notifiers = append(q.active.Notifiers, config.Notifier{ID: "second", Type: "feishu_app", App: "default", ChatID: "oc_fake_second"})
	if _, err := f.db.SetNotificationPause(ctx, time.Minute, "test", "local"); err != nil {
		t.Fatal(err)
	}
	a := seedUnsent(t, f, false)
	if d, err := f.db.EvaluateNotification(ctx, a, f.clock.Now()); err != nil || d.Suppressed != "paused" {
		t.Fatalf("pause: %+v %v", d, err)
	}
	f.clock.millis.Add(time.Minute.Milliseconds())
	if err := f.db.PreparePauseResumes(ctx); err != nil {
		t.Fatal(err)
	}
	items, err := f.db.PendingPauseNotifications(ctx)
	if err != nil || len(items) != 1 {
		t.Fatalf("pending: %+v %v", items, err)
	}
	a = items[0].Alert
	if err := f.db.RecordNotification(ctx, notify.Attempt{ID: "fake-first-success", AlertID: a.ID, State: a.State, NotifierID: "test-channel", EventKey: a.NotificationKey, TS: f.clock.Now().UnixMilli(), OK: true}); err != nil {
		t.Fatal(err)
	}
	client := &pauseRetryClient{runtimeClient: f.client, failures: 4}
	q.factory = func(config.FeishuApp, []byte) (feishu.Client, error) { return client, nil }
	q.waiter = pauseWaiter{}
	if err := q.advancePause(ctx); err != nil {
		t.Fatal(err)
	}
	if err := q.advancePause(ctx); err != nil || len(q.jobs) != 1 {
		t.Fatalf("duplicate in-flight catch-up: %d %v", len(q.jobs), err)
	}
	close(q.jobs)
	if err := q.run(ctx); err != nil {
		t.Fatal(err)
	}
	if len(client.messages) != 4 {
		t.Fatalf("retry attempts=%d", len(client.messages))
	}
	if err := f.db.Store.Close(); err != nil {
		t.Fatal(err)
	}
	db, err := store.Open(ctx, store.Options{Path: f.path, Now: f.clock.Now})
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	f.db.Store = db
	restarted := policyQueue(t, f)
	restarted.active = q.active
	restarted.factory = q.factory
	restarted.waiter = pauseWaiter{}
	if err := restarted.advancePause(ctx); err != nil {
		t.Fatal(err)
	}
	close(restarted.jobs)
	if err := restarted.run(ctx); err != nil {
		t.Fatal(err)
	}
	if len(client.messages) != 5 {
		t.Fatalf("restart did not retry only failed channel: %d", len(client.messages))
	}
	var identity string
	for _, m := range client.messages {
		if m.ChatID != "oc_fake_second" || m.EventKey != a.NotificationKey || !strings.Contains(string(m.Card), "通知暂停期间发生，恢复通知后补发") {
			t.Fatalf("catch-up content/identity: %+v", m)
		}
		uuid := feishu.MessageUUID(m.AlertID, m.EventKey, m.ChatID)
		if identity != "" && identity != uuid {
			t.Fatal("retry changed provider UUID")
		}
		identity = uuid
		if uuid == feishu.MessageUUID(m.AlertID, "firing:"+strconv.FormatInt(a.StartedAt, 10), m.ChatID) {
			t.Fatal("catch-up reused original identity")
		}
	}
	final := policyQueue(t, f)
	final.active = q.active
	if err := final.advancePause(ctx); err != nil || len(final.jobs) != 0 {
		t.Fatalf("successful channel duplicated: %v", err)
	}
	items, err = f.db.PendingPauseNotifications(ctx)
	if err != nil || len(items) != 0 {
		t.Fatalf("completion not persisted: %+v %v", items, err)
	}
}

func TestPauseCatchupFailureSettlesAndRetriesOnlyAtStartupWithin24Hours(t *testing.T) {
	for _, elapsed := range []time.Duration{time.Hour, 24 * time.Hour, 24*time.Hour + time.Millisecond} {
		t.Run(elapsed.String(), func(t *testing.T) {
			ctx := context.Background()
			f := newRuntimeFixture(t)
			if _, err := f.db.SetNotificationPause(ctx, time.Minute, "test", "local"); err != nil {
				t.Fatal(err)
			}
			alert := seedUnsent(t, f, false)
			if decision, err := f.db.EvaluateNotification(ctx, alert, f.clock.Now()); err != nil || decision.Suppressed != "paused" {
				t.Fatalf("pause decision: %+v %v", decision, err)
			}
			f.clock.millis.Add(time.Minute.Milliseconds())
			eventAt := f.clock.Now()
			client := &pauseRetryClient{runtimeClient: f.client, failures: 100000}
			makeQueue := func() *notificationQueue {
				q := policyQueue(t, f)
				q.factory = func(config.FeishuApp, []byte) (feishu.Client, error) { return client, nil }
				q.waiter = pauseWaiter{}
				return q
			}
			complete := func(q *notificationQueue) {
				t.Helper()
				close(q.jobs)
				if err := q.run(ctx); err != nil {
					t.Fatal(err)
				}
				for check := 0; check < 3; check++ {
					if err := q.advancePause(ctx); err != nil {
						t.Fatal(err)
					}
				}
				items, err := f.db.PendingPauseNotifications(ctx)
				if err != nil || len(items) != 0 {
					t.Fatalf("catch-up did not settle: %+v %v", items, err)
				}
				reader, err := sql.Open("sqlite", f.path)
				if err != nil {
					t.Fatal(err)
				}
				defer reader.Close()
				var incomplete int
				if err := reader.QueryRowContext(ctx, "SELECT COUNT(*) FROM notification_pauses WHERE resumed_at IS NULL").Scan(&incomplete); err != nil || incomplete != 0 {
					t.Fatalf("resumed_at not saved: incomplete=%d %v", incomplete, err)
				}
			}
			first := makeQueue()
			if err := first.advancePause(ctx); err != nil || len(first.jobs) != 1 {
				t.Fatalf("initial catch-up: queued=%d %v", len(first.jobs), err)
			}
			complete(first)
			if len(client.messages) != 4 {
				t.Fatalf("same run repeated delivery: attempts=%d", len(client.messages))
			}
			if err := f.db.Store.Close(); err != nil {
				t.Fatal(err)
			}
			f.clock.millis.Store(eventAt.Add(elapsed).UnixMilli())
			db, err := store.Open(ctx, store.Options{Path: f.path, Now: f.clock.Now})
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			f.db.Store = db
			restarted := makeQueue()
			if err := restarted.advancePause(ctx); err != nil {
				t.Fatal(err)
			}
			wantQueued, wantAttempts := 0, 4
			if elapsed <= 24*time.Hour {
				wantQueued, wantAttempts = 1, 8
			}
			if len(restarted.jobs) != wantQueued {
				t.Fatalf("startup retry queued=%d want=%d", len(restarted.jobs), wantQueued)
			}
			complete(restarted)
			if len(client.messages) != wantAttempts {
				t.Fatalf("startup attempts=%d want=%d", len(client.messages), wantAttempts)
			}
			for _, message := range client.messages {
				if message.EventKey != client.messages[0].EventKey {
					t.Fatal("startup changed catch-up delivery identity")
				}
			}
		})
	}
}

func TestPauseAPIValidationIdentityAndTestBypass(t *testing.T) {
	f := newAPIFixture(t)
	path := "/api/v1/notifications/pause"
	for _, body := range []string{`{"for":"59s"}`, `{"for":"24h1s"}`, `{"for":"invalid"}`, `{"for":"1h","reason":"` + strings.Repeat("会", 201) + `"}`} {
		f.request(t, "PUT", path, "config", body, 400)
	}
	f.request(t, "PUT", path, "read", `{"for":"1h"}`, 403)
	f.request(t, "DELETE", path, "read", "", 403)
	response := f.request(t, "PUT", path, "config", `{"for":"1h","reason":"开会"}`, 200)
	var state store.NotificationPause
	if err := json.Unmarshal(response.Body.Bytes(), &state); err != nil || !state.Paused || *state.By != "fake-config" {
		t.Fatalf("pause identity: %s", response.Body.String())
	}
	// Explicit test sends use their own path and do not enter alert suppression policy.
	f.request(t, "POST", "/api/v1/notifiers/fake-channel/test", "config", "{}", 200)
	if len(f.client.sent) != 1 {
		t.Fatal("explicit test was suppressed by pause")
	}
	f.request(t, "DELETE", path, "config", "", 200)
	response = f.request(t, "GET", path, "read", "", 200)
	if err := json.Unmarshal(response.Body.Bytes(), &state); err != nil || state.Paused || state.Until != nil {
		t.Fatalf("resume: %s", response.Body.String())
	}
}

func TestPauseSuppressesQueuedFiringAndRecoveryOnEveryChannel(t *testing.T) {
	f := newRuntimeFixture(t)
	ctx := context.Background()
	if _, err := f.db.SetNotificationPause(ctx, time.Hour, "", "local"); err != nil {
		t.Fatal(err)
	}
	a := seedUnsent(t, f, true)
	q := policyQueue(t, f)
	q.active.Notifiers = append(q.active.Notifiers, config.Notifier{ID: "paused-hook", Type: "webhook", URLRef: "fake-ref"}, config.Notifier{ID: "paused-toast", Type: "toast"})
	q.active.Rules[0].Notify = append(q.active.Rules[0].Notify, "paused-hook", "paused-toast")
	toast := &testToastClient{}
	q.toast = toast
	q.jsonWebhookFactory = func([]byte, []byte) (notify.JSONWebhookClient, error) {
		t.Error("paused webhook constructed")
		return nil, context.Canceled
	}
	a.State = rules.StateFiring
	a.NotificationAt = a.StartedAt
	if err := q.Enqueue(ctx, a); err != nil {
		t.Fatal(err)
	}
	a.State = rules.StateResolved
	a.NotificationAt = a.ResolvedAt
	if err := q.Enqueue(ctx, a); err != nil {
		t.Fatal(err)
	}
	close(q.jobs)
	if err := q.run(ctx); err != nil {
		t.Fatal(err)
	}
	p, err := f.db.NotificationPause(ctx)
	if err != nil || *p.Suppressed != 2 || len(f.client.wire) != 0 || len(f.db.attempts) != 0 || len(toast.values) != 0 {
		t.Fatalf("paused queue sent: %+v %v", p, err)
	}
}

func TestRuntimeDetectsCLIPauseResumeAtFiveSecondCheck(t *testing.T) {
	f := newRuntimeFixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if _, err := f.db.SetNotificationPause(ctx, time.Minute, "cli", "local"); err != nil {
		t.Fatal(err)
	}
	a := seedUnsent(t, f, false)
	if _, err := f.db.EvaluateNotification(ctx, a, f.clock.Now()); err != nil {
		t.Fatal(err)
	}
	ready := make(chan struct{})
	f.opts.Ready = func() { close(ready) }
	runtime, err := NewRuntime(f.opts)
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- runtime.Run(ctx) }()
	t.Cleanup(func() { cancel(); receiveRuntime(t, done) })
	receiveRuntime(t, ready)
	var evaluation *fakeTicker
	for range 2 {
		ticker := receiveRuntime(t, f.clock.created)
		if ticker.interval == time.Second {
			evaluation = ticker
		}
	}
	if evaluation == nil {
		t.Fatal("missing evaluation clock")
	}
	if _, err := f.db.ResumeNotifications(ctx); err != nil {
		t.Fatal(err)
	}
	f.clock.tick(evaluation, 5*time.Second)
	message := receiveRuntime(t, f.client.wire)
	attempt := receiveRuntime(t, f.db.attempts)
	if !strings.HasPrefix(message.EventKey, "resume:") || !attempt.OK || attempt.EventKey != message.EventKey {
		t.Fatalf("CLI change was not recovered: %+v %+v", message, attempt)
	}
}

func TestPauseCatchupAdvancesPastAFullPageOfBlockedDeliveries(t *testing.T) {
	f := newRuntimeFixture(t)
	ctx := context.Background()
	if _, err := f.db.SetNotificationPause(ctx, time.Minute, "", "local"); err != nil {
		t.Fatal(err)
	}
	for i := range 257 {
		a := rules.Alert{ID: fmt.Sprintf("fake-page-%03d", i), RuleID: "down", CollectorID: f.opts.ID, TargetID: fmt.Sprintf("demo-%03d", i), State: rules.StateFiring, Severity: "critical", StartedAt: f.clock.Now().UnixMilli(), NotificationAt: f.clock.Now().UnixMilli(), NotificationKey: "firing:fake", Summary: "虚构分页告警"}
		if err := f.db.SaveTransition(ctx, a, rules.Event{AlertID: a.ID, Kind: rules.StateFiring, TS: a.StartedAt}); err != nil {
			t.Fatal(err)
		}
		if d, err := f.db.EvaluateNotification(ctx, a, f.clock.Now()); err != nil || d.Suppressed != "paused" {
			t.Fatalf("pause %d: %+v %v", i, d, err)
		}
	}
	f.clock.millis.Add(time.Minute.Milliseconds())
	q := policyQueue(t, f)
	seen := map[string]bool{}
	for range 32 {
		if err := q.advancePause(ctx); err != nil && err != ErrNotificationQueueFull {
			t.Fatal(err)
		}
		for len(q.jobs) > 0 {
			job := <-q.jobs
			seen[job.input.Alert.ID] = true
			// Keep prior deliveries in flight, like an unavailable destination retrying.
		}
	}
	if len(seen) != 257 {
		t.Fatalf("catch-up stalled behind first page: reached %d of 257 alerts", len(seen))
	}
}
