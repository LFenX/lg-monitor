package app

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"testing/fstest"
	"time"

	"github.com/LFenX/lg-monitor/internal/config"
	"github.com/LFenX/lg-monitor/internal/feishu"
	"github.com/LFenX/lg-monitor/internal/notify"
	"github.com/LFenX/lg-monitor/internal/rules"
	"github.com/LFenX/lg-monitor/internal/store"
)

func appCardActionFixture(t *testing.T) (*apiFixture, *CardActions, rules.Alert, feishu.CardAction, *[]store.CardActionResult) {
	t.Helper()
	f := newAPIFixture(t)
	data, err := config.CardActionsConfig(f.files.MapFS["notifiers.yaml"].Data, "fake-channel", true)
	if err != nil {
		t.Fatal(err)
	}
	f.files.MapFS["notifiers.yaml"] = &fstest.MapFile{Data: data}
	if err := f.manager.Reload(); err != nil {
		t.Fatal(err)
	}
	alert := rules.Alert{ID: "fake-alert", RuleID: "down", CollectorID: "fake-host", TargetID: "web", InstanceKey: "fake-instance", State: rules.StateFiring, Severity: "critical", StartedAt: f.now.Add(-time.Minute).UnixMilli(), NotificationKey: "firing:fake"}
	if err := f.db.SaveTransition(context.Background(), alert, rules.Event{AlertID: alert.ID, Kind: rules.StateFiring, TS: alert.StartedAt}); err != nil {
		t.Fatal(err)
	}
	var effects []store.CardActionResult
	m, err := NewCardActions(CardActionOptions{Config: f.manager, Store: f.db, Keys: f.vault, Now: func() time.Time { return f.now }, OnAction: func(result store.CardActionResult) { effects = append(effects, result) }})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(m.closeSigningKey)
	cfg := f.manager.Snapshot()
	actions, err := m.CardsFor(context.Background(), cfg.FeishuApps[0], cfg.Notifiers[0], alert)
	if err != nil || len(actions) != 2 {
		t.Fatal(actions, err)
	}
	if err := f.db.RecordNotification(context.Background(), notify.Attempt{ID: "fake-attempt", AlertID: alert.ID, State: alert.State, EventKey: alert.NotificationKey, NotifierID: "fake-channel", MessageID: "om_fake_card", OK: true, TS: f.now.UnixMilli()}); err != nil {
		t.Fatal(err)
	}
	return f, m, alert, feishu.CardAction{EventID: "fake-event", AppID: cfg.FeishuApps[0].AppID, OperatorID: "ou_fake_operator", ChatID: cfg.Notifiers[0].ChatID, MessageID: "om_fake_card", Value: actions[0].Value}, &effects
}

func TestCardActionsBoundEffectsReplayAndRevocation(t *testing.T) {
	f, m, alert, event, effects := appCardActionFixture(t)
	app := f.manager.Snapshot().FeishuApps[0]
	if reply := m.Handle(context.Background(), app, event); reply.Type != "success" {
		t.Fatal(reply)
	}
	f.now = f.now.Add(time.Minute)
	if reply := m.Handle(context.Background(), app, event); reply.Type != "info" || len(*effects) != 1 {
		t.Fatal(reply, *effects)
	}
	stored, err := f.db.CardActionAlert(context.Background(), alert.ID)
	if err != nil || stored.AcknowledgedAt != f.now.Add(-time.Minute).UnixMilli() {
		t.Fatal(stored, err)
	}
	actions, err := m.CardsFor(context.Background(), app, f.manager.Snapshot().Notifiers[0], alert)
	if err != nil {
		t.Fatal(err)
	}
	event.EventID = "fake-silence"
	event.Value = actions[1].Value
	if reply := m.Handle(context.Background(), app, event); reply.Type != "success" || len(*effects) != 2 {
		t.Fatal(reply, *effects)
	}
	if (*effects)[1].SilenceUntil != f.now.Add(time.Hour).UnixMilli() {
		t.Fatal(*effects)
	}
	data, err := config.CardActionsConfig(f.files.MapFS["notifiers.yaml"].Data, "fake-channel", false)
	if err != nil {
		t.Fatal(err)
	}
	f.files.MapFS["notifiers.yaml"] = &fstest.MapFile{Data: data}
	if err := f.manager.Reload(); err != nil {
		t.Fatal(err)
	}
	if reply := m.Handle(context.Background(), app, event); reply.Type != "error" || len(*effects) != 2 {
		t.Fatal(reply, *effects)
	}
}

func TestCardActionsRejectForwardTamperAndReroute(t *testing.T) {
	for _, kind := range []string{"app", "group", "message", "value", "removed-route", "filter", "resolved"} {
		t.Run(kind, func(t *testing.T) {
			f, m, alert, event, effects := appCardActionFixture(t)
			app := f.manager.Snapshot().FeishuApps[0]
			switch kind {
			case "app":
				app.AppID = "cli_other"
			case "group":
				event.ChatID = "oc_other"
			case "message":
				event.MessageID = "om_forwarded"
			case "value":
				var value map[string]any
				if err := json.Unmarshal(event.Value, &value); err != nil {
					t.Fatal(err)
				}
				value["operation"] = "silence_1h"
				event.Value, _ = json.Marshal(value)
			case "removed-route":
				f.files.MapFS["rules.yaml"] = &fstest.MapFile{Data: []byte("rules: []\n")}
			case "filter":
				f.files.MapFS["notifiers.yaml"] = &fstest.MapFile{Data: []byte(strings.Replace(string(f.files.MapFS["notifiers.yaml"].Data), "cardActions: true", "filter: {collectors: [other-host]}, cardActions: true", 1))}
			case "resolved":
				alert.State = rules.StateResolved
				alert.ResolvedAt = f.now.UnixMilli()
				if err := f.db.SaveTransition(context.Background(), alert, rules.Event{AlertID: alert.ID, Kind: rules.StateResolved, TS: alert.ResolvedAt}); err != nil {
					t.Fatal(err)
				}
			}
			if err := f.manager.Reload(); err != nil {
				t.Fatal(err)
			}
			reply := m.Handle(context.Background(), app, event)
			if (reply.Type != "error" && reply.Type != "info") || len(*effects) != 0 {
				t.Fatal(reply, *effects)
			}
		})
	}
}

func TestCardActionsDisabledDoesNotCreateKey(t *testing.T) {
	f := newAPIFixture(t)
	m, err := NewCardActions(CardActionOptions{Config: f.manager, Store: f.db, Keys: f.vault, Now: func() time.Time { return f.now }})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(m.closeSigningKey)
	cfg := f.manager.Snapshot()
	buttons, err := m.CardsFor(context.Background(), cfg.FeishuApps[0], cfg.Notifiers[0], rules.Alert{State: rules.StateFiring})
	if err != nil || len(buttons) != 0 {
		t.Fatal(buttons, err)
	}
	if _, found := f.vault.values[cardActionKeyName]; found {
		t.Fatal("disabled feature created a signing key")
	}
}

func TestCardActionsAuthenticatedApplicationRoutesOtherLocalAlias(t *testing.T) {
	f, m, alert, event, effects := appCardActionFixture(t)
	cfg := f.manager.Snapshot()
	source := cfg.FeishuApps[0]
	alias := source
	alias.ID = "another-local-alias"
	file := config.NotifiersFile{FeishuApps: append(cfg.FeishuApps, alias), Notifiers: cfg.Notifiers}
	file.Notifiers[0].App = alias.ID
	data, err := json.Marshal(file)
	if err != nil {
		t.Fatal(err)
	}
	f.files.MapFS["notifiers.yaml"] = &fstest.MapFile{Data: data}
	if err := f.manager.Reload(); err != nil {
		t.Fatal(err)
	}
	buttons, err := m.CardsFor(context.Background(), alias, file.Notifiers[0], alert)
	if err != nil || len(buttons) != 2 {
		t.Fatal(buttons, err)
	}
	event.Value = buttons[0].Value
	if reply := m.Handle(context.Background(), source, event); reply.Type != "success" || len(*effects) != 1 {
		t.Fatal("shared authenticated app did not route alias binding", reply, *effects)
	}
}

type blockedCardActionRepository struct {
	CardActionRepository
	entered chan time.Duration
}

func (r blockedCardActionRepository) CardNotification(ctx context.Context, _ string, _ string, _ string) (store.CardNotification, error) {
	deadline, _ := ctx.Deadline()
	r.entered <- time.Until(deadline)
	<-ctx.Done()
	return store.CardNotification{}, ctx.Err()
}

func TestCardActionsBoundCallbackBudgetAndConcurrency(t *testing.T) {
	f, m, _, event, effects := appCardActionFixture(t)
	entered := make(chan time.Duration, 16)
	m.options.Store = blockedCardActionRepository{CardActionRepository: f.db, entered: entered}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var workers sync.WaitGroup
	for range 16 {
		workers.Go(func() { m.Handle(ctx, f.manager.Snapshot().FeishuApps[0], event) })
	}
	for range 16 {
		select {
		case budget := <-entered:
			if budget <= 0 || budget > 2*time.Second {
				t.Fatal("unbounded callback", budget)
			}
		case <-time.After(time.Second):
			t.Fatal("callbacks did not reach repository")
		}
	}
	if reply := m.Handle(context.Background(), f.manager.Snapshot().FeishuApps[0], event); reply.Type != "warning" {
		t.Fatal("callback admission was not bounded", reply)
	}
	cancel()
	done := make(chan struct{})
	go func() { workers.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("canceled callbacks did not finish")
	}
	if len(*effects) != 0 {
		t.Fatal("canceled callbacks changed state")
	}
}
