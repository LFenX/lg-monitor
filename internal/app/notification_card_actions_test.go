package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"testing"
	"time"

	"github.com/LFenX/lg-monitor/internal/collector"
	"github.com/LFenX/lg-monitor/internal/config"
	"github.com/LFenX/lg-monitor/internal/feishu"
	"github.com/LFenX/lg-monitor/internal/notify/cards"
	"github.com/LFenX/lg-monitor/internal/rules"
)

type actionDeliveryClient struct {
	*apiClient
	rejectFirst bool
	updated     json.RawMessage
}

func (c *actionDeliveryClient) SendCard(ctx context.Context, message feishu.CardMessage) (feishu.SendResult, error) {
	if c.rejectFirst {
		c.rejectFirst = false
		c.sent = append(c.sent, message)
		return feishu.SendResult{}, &feishu.Error{Kind: feishu.CardRejected}
	}
	return c.apiClient.SendCard(ctx, message)
}
func (c *actionDeliveryClient) UpdateCard(_ context.Context, _ string, value json.RawMessage) error {
	c.updated = bytes.Clone(value)
	return nil
}

type actionDiagnosticStore struct {
	Persistence
	events []collector.EventLogRecord
}

func (s actionDiagnosticStore) DiagnosticEvents(context.Context, rules.Alert) ([]collector.EventLogRecord, error) {
	return s.events, nil
}

func TestCardActionsActualNotificationFallbackLateUpdateAndKeyFailure(t *testing.T) {
	for _, kind := range []string{"send-and-update", "platform-fallback", "key-failure", "recovery"} {
		t.Run(kind, func(t *testing.T) {
			f, m, alert, event, _ := appCardActionFixture(t)
			cfg := f.manager.Snapshot()
			renderer, err := cards.New(f.manager, cards.Options{Now: func() time.Time { return f.now }})
			if err != nil {
				t.Fatal(err)
			}
			client := &actionDeliveryClient{apiClient: f.client, rejectFirst: kind == "platform-fallback"}
			q := &notificationQueue{active: cfg, secrets: f.vault, factory: func(config.FeishuApp, []byte) (feishu.Client, error) { return client, nil }, store: actionDiagnosticStore{Persistence: f.db, events: []collector.EventLogRecord{{Channel: "Application", RecordID: 1, TS: f.now.UnixMilli(), Message: "虚构迟到诊断"}}}, renderer: renderer, now: func() time.Time { return f.now }, logger: slog.Default(), clients: map[string]cachedClient{}, cardActions: m}
			if kind == "key-failure" {
				clear(m.key)
				m.key = nil
				delete(f.vault.values, cardActionKeyName)
				f.vault.setError = errors.New("fake unavailable protected vault")
			}
			if kind == "recovery" {
				alert.State = rules.StateResolved
			}
			frozen, err := renderer.Freeze("detailed")
			if err != nil {
				t.Fatal(err)
			}
			job := notificationJob{input: cards.Input{Alert: alert, Rule: cfg.Rules[0], Target: cfg.Targets[0]}, rule: config.Rule{Template: "detailed"}, apps: cfg.FeishuApps, frozen: frozen}
			if err := q.send(context.Background(), job, cfg.Notifiers[0]); err != nil {
				t.Fatal(err)
			}
			wantButtons := kind != "key-failure" && kind != "recovery"
			for _, message := range client.sent {
				if bytes.Contains(message.Card, []byte(`"type":"callback"`)) != wantButtons {
					t.Fatal("notification actions differ", kind, string(message.Card))
				}
			}
			if kind == "platform-fallback" {
				if len(client.sent) != 2 {
					t.Fatal("did not exercise rejected-template fallback")
				}
				return
			}
			if kind == "send-and-update" {
				q.updateEventCard(context.Background(), q, eventLogCardUpdate{job: job, notifier: cfg.Notifiers[0], messageID: "om_fake_PRIVATE_RESPONSE", sentAt: f.now, renderAt: f.now})
				if !bytes.Contains(client.updated, []byte(`"type":"callback"`)) || !bytes.Contains(client.updated, []byte("虚构迟到诊断")) {
					t.Fatal("late update lost events or actions", string(client.updated))
				}
				event.MessageID = "om_fake_PRIVATE_RESPONSE"
				if reply := m.Handle(context.Background(), cfg.FeishuApps[0], event); reply.Type != "success" {
					t.Fatal("actual delivery did not bind callback", reply)
				}
			}
		})
	}
}
