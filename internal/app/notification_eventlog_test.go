package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/LFenX/lg-monitor/internal/collector"
	"github.com/LFenX/lg-monitor/internal/config"
	"github.com/LFenX/lg-monitor/internal/feishu"
	"github.com/LFenX/lg-monitor/internal/notify/cards"
	"github.com/LFenX/lg-monitor/internal/pipeline"
	"github.com/LFenX/lg-monitor/internal/rules"
)

type diagnosticWaitRequest struct {
	delay time.Duration
	wake  chan struct{}
}
type diagnosticWaiter struct{ requests chan diagnosticWaitRequest }

func (w diagnosticWaiter) Wait(ctx context.Context, delay time.Duration) error {
	request := diagnosticWaitRequest{delay: delay, wake: make(chan struct{})}
	select {
	case w.requests <- request:
	case <-ctx.Done():
		return ctx.Err()
	}
	select {
	case <-request.wake:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

type diagnosticPatch struct {
	id   string
	card json.RawMessage
}
type diagnosticClient struct {
	feishu.Client
	patches chan diagnosticPatch
}

type diagnosticStoreSpy struct {
	Persistence
	calls atomic.Int32
}

func (s *diagnosticStoreSpy) DiagnosticEvents(context.Context, rules.Alert) ([]collector.EventLogRecord, error) {
	s.calls.Add(1)
	return nil, nil
}

func (c diagnosticClient) UpdateCard(_ context.Context, id string, card json.RawMessage) error {
	c.patches <- diagnosticPatch{id, append(json.RawMessage(nil), card...)}
	return nil
}

func startDiagnosticQueue(t *testing.T, q *notificationQueue) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- q.run(ctx) }()
	t.Cleanup(func() {
		cancel()
		select {
		case err := <-done:
			if err != nil && !errors.Is(err, context.Canceled) {
				t.Error(err)
			}
		case <-time.After(3 * time.Second):
			t.Error("diagnostic workers did not stop")
		}
	})
}
func diagnosticAlert(t *testing.T, f *runtimeFixture, id, rule string) rules.Alert {
	t.Helper()
	at := f.clock.Now().UnixMilli()
	alert := rules.Alert{ID: id, RuleID: rule, CollectorID: f.opts.ID, TargetID: "demo", State: rules.StateFiring, Severity: "critical", StartedAt: at, SourceTS: at, Summary: "虚构进程故障", NotificationAt: at, NotificationKey: fmt.Sprintf("firing:%d", at)}
	if err := f.db.SaveTransition(context.Background(), alert, rules.Event{AlertID: id, TS: at, Kind: rules.StateFiring, Message: alert.Summary}); err != nil {
		t.Fatal(err)
	}
	return alert
}
func diagnosticRecord(t *testing.T, f *runtimeFixture, q *notificationQueue, id uint64, at int64, message string) {
	t.Helper()
	event := collector.EventLogRecord{Channel: "Application", Provider: "Application Error", EventID: 1000, Level: 2, RecordID: id, TS: at, Subject: "demo.exe", Message: message, Presets: []string{"app_crash"}, Targets: []collector.EventLogTarget{{ID: "demo", Via: "subject"}}}
	row := pipeline.Message{CollectorID: f.opts.ID, Seq: id, TS: at, Kind: pipeline.KindEventLog, EventLog: &event}
	if err := f.db.Store.Write(context.Background(), row); err != nil {
		t.Fatal(err)
	}
	if err := f.db.Flush(context.Background()); err != nil {
		t.Fatal(err)
	}
	q.observe(row)
}

func TestEventLogDiagnosticImmediateSendAndOneFrozenCardUpdate(t *testing.T) {
	f := newRuntimeFixture(t)
	close(f.client.release)
	q := policyQueue(t, f)
	q.active.Templates = append(q.active.Templates, config.Template{ID: "diagnostic", Extends: "detailed", Title: "原始规则 {{.Alert.Duration}}"})
	q.active.Rules[0].Template = "diagnostic"
	q.renderer, _ = cards.New(apiSnapshot{q.active}, cards.Options{Now: f.clock.Now})
	patches := make(chan diagnosticPatch, 3)
	client := diagnosticClient{Client: f.client, patches: patches}
	q.factory = func(config.FeishuApp, []byte) (feishu.Client, error) { return client, nil }
	waits := make(chan diagnosticWaitRequest, 5)
	q.waiter = diagnosticWaiter{waits}
	alert := diagnosticAlert(t, f, "fake-diagnostic", "down")
	diagnosticRecord(t, f, q, 1, alert.SourceTS-1000, "先于故障的事件")
	startDiagnosticQueue(t, q)
	if err := q.Enqueue(context.Background(), alert); err != nil {
		t.Fatal(err)
	}
	initial := receive(t, f.client.wire)
	if !strings.Contains(string(initial.Card), "先于故障的事件") || !cards.UpdateEnabled(initial.Card) {
		t.Fatalf("initial diagnostic missing: %s", initial.Card)
	}
	wait := receive(t, waits)
	if wait.delay != 60*time.Second {
		t.Fatalf("existing events delayed initial send: %s", wait.delay)
	}
	diagnosticRecord(t, f, q, 2, alert.SourceTS+1000, "发送以后到达的事件")
	q.active.Templates[len(q.active.Templates)-1].Title = "后来修改的标题"
	f.clock.millis.Store(alert.SourceTS + 60000)
	close(wait.wake)
	patch := receive(t, patches)
	if patch.id != "om_fake_firing" || !strings.Contains(string(patch.card), "发送以后到达的事件") || !strings.Contains(string(patch.card), "原始规则 0秒") || strings.Contains(string(patch.card), "后来修改的标题") {
		t.Fatalf("update changed frozen fields or missed events: %s %s", patch.id, patch.card)
	}
	if len(f.client.wire) != 0 || len(patches) != 0 || len(waits) != 0 {
		t.Fatal("card update sent another notification or scheduled another update")
	}
}

func TestEventLogDiagnosticWaitDeadlineDoesNotBlockOtherAlerts(t *testing.T) {
	f := newRuntimeFixture(t)
	close(f.client.release)
	q := policyQueue(t, f)
	q.active.Rules[0].Template = "detailed"
	q.active.Rules = append(q.active.Rules, config.Rule{ID: "other", Type: "process_absent", Target: "demo", Severity: "critical", Notify: []string{"test-channel"}, Template: "standard"})
	waits := make(chan diagnosticWaitRequest, 5)
	q.waiter = diagnosticWaiter{waits}
	first := diagnosticAlert(t, f, "fake-waiting", "down")
	second := diagnosticAlert(t, f, "fake-immediate", "other")
	second.SourceTS = 0
	startDiagnosticQueue(t, q)
	if err := q.Enqueue(context.Background(), first); err != nil {
		t.Fatal(err)
	}
	wait := receive(t, waits)
	if wait.delay != 8*time.Second {
		t.Fatalf("wait limit: %s", wait.delay)
	}
	if err := q.Enqueue(context.Background(), second); err != nil {
		t.Fatal(err)
	}
	if message := receive(t, f.client.wire); message.AlertID != second.ID {
		t.Fatal("waiting alert blocked unrelated notification")
	}
	f.clock.millis.Store(first.SourceTS + 8000)
	close(wait.wake)
	message := receive(t, f.client.wire)
	if message.AlertID != first.ID || f.clock.Now().UnixMilli()-(first.SourceTS-2000) >= 15000 {
		t.Fatal("initial diagnostic exceeded 15 second fault budget")
	}
}

func TestEventLogDiagnosticArrivingDuringWaitUnblocksAndWebhookNeverUpdates(t *testing.T) {
	f := newRuntimeFixture(t)
	close(f.client.release)
	q := policyQueue(t, f)
	q.active.Rules[0].Template = "detailed"
	waits := make(chan diagnosticWaitRequest, 5)
	q.waiter = diagnosticWaiter{waits}
	patches := make(chan diagnosticPatch, 2)
	client := diagnosticClient{Client: f.client, patches: patches}
	q.active.Notifiers[0].Type = "feishu_webhook"
	q.active.Notifiers[0].URLRef = "fake-url"
	q.secrets = notificationSecrets{"fake-url": "https://open.feishu.cn/open-apis/bot/v2/hook/fake-token"}
	q.webhookFactory = func([]byte, []byte) (feishu.WebhookClient, error) { return client, nil }
	alert := diagnosticAlert(t, f, "fake-webhook-diagnostic", "down")
	startDiagnosticQueue(t, q)
	if err := q.Enqueue(context.Background(), alert); err != nil {
		t.Fatal(err)
	}
	if wait := receive(t, waits); wait.delay != 8*time.Second {
		t.Fatal("webhook wait deadline differs")
	}
	f.clock.millis.Store(alert.SourceTS + 3000)
	diagnosticRecord(t, f, q, 1, alert.SourceTS+2000, "等待中到达的事件")
	message := receive(t, f.client.wire)
	if !strings.Contains(string(message.Card), "等待中到达的事件") || len(waits) != 0 || len(patches) != 0 {
		t.Fatal("event did not unblock delivery or webhook scheduled a card update")
	}
}

func TestEventLogDiagnosticUnusedTemplateDoesNotQueryOrWait(t *testing.T) {
	f := newRuntimeFixture(t)
	close(f.client.release)
	q := policyQueue(t, f)
	q.active.Rules[0].Template = "standard"
	spy := &diagnosticStoreSpy{Persistence: q.store}
	q.store = spy
	waits := make(chan diagnosticWaitRequest, 2)
	q.waiter = diagnosticWaiter{waits}
	alert := diagnosticAlert(t, f, "fake-no-events", "down")
	startDiagnosticQueue(t, q)
	if err := q.Enqueue(context.Background(), alert); err != nil {
		t.Fatal(err)
	}
	if message := receive(t, f.client.wire); message.AlertID != alert.ID || spy.calls.Load() != 0 || len(waits) != 0 {
		t.Fatal("unused Events triggered a query or delay")
	}
}

func TestEventLogToastIncludesLatestAvailableEventOnly(t *testing.T) {
	input := cards.Input{Alert: rules.Alert{State: rules.StateFiring, Summary: "fake down"}, Events: []collector.EventLogRecord{{TS: 1000, Provider: "older", Message: "older event"}, {TS: 2000, Provider: "latest", EventID: 1000, Subject: "fake.exe", Message: strings.Repeat("测", 201)}}}
	toast := toastContent(input)
	if !strings.Contains(toast.Message, "关联事件（UTC 1970-01-01 00:00:02）：latest #1000 fake.exe") || strings.Contains(toast.Message, "older event") || strings.Count(toast.Message, "测") != 200 {
		t.Fatalf("toast diagnostic content: %+v", toast)
	}
}

func TestEventLogLateCardUpdateRespectsGlobalPause(t *testing.T) {
	f := newRuntimeFixture(t)
	close(f.client.release)
	q := policyQueue(t, f)
	q.active.Rules[0].Template = "detailed"
	patches := make(chan diagnosticPatch, 1)
	q.factory = func(config.FeishuApp, []byte) (feishu.Client, error) {
		return diagnosticClient{Client: f.client, patches: patches}, nil
	}
	alert := diagnosticAlert(t, f, "fake-paused-update", "down")
	diagnosticRecord(t, f, q, 1, alert.SourceTS+1000, "new event")
	job, err := q.job(alert)
	if err != nil {
		t.Fatal(err)
	}
	job.frozen, err = q.renderer.Freeze("detailed")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.SetNotificationPause(context.Background(), time.Hour, "fake pause", "fake"); err != nil {
		t.Fatal(err)
	}
	f.clock.millis.Add(60000)
	q.updateEventCard(context.Background(), q, eventLogCardUpdate{job: job, notifier: job.notifiers[0], messageID: "om_fake", sentAt: time.UnixMilli(alert.SourceTS), renderAt: time.UnixMilli(alert.SourceTS)})
	if len(patches) != 0 {
		t.Fatal("paused notifications still updated an external card")
	}
	for _, cached := range q.clients {
		cached.client.Close()
	}
}
