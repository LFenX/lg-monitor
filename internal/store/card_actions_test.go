package store

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/LFenX/lg-monitor/internal/notify"
	"github.com/LFenX/lg-monitor/internal/rules"
)

func cardActionFixture(t *testing.T) (*controlFixture, CardActionInput, rules.Alert) {
	t.Helper()
	f := newControlFixture(t)
	f.status(false)
	alert := f.sent.rows[0]
	ctx := context.Background()
	if err := f.db.RecordNotification(ctx, notify.Attempt{ID: "attempt-card", AlertID: alert.ID, State: rules.StateFiring, EventKey: alert.NotificationKey, NotifierID: "fake-channel", MessageID: "om_fake_card", OK: true, TS: f.now.UnixMilli()}); err != nil {
		t.Fatal(err)
	}
	binding, err := f.db.CardNotification(ctx, "om_fake_card", alert.ID, alert.NotificationKey)
	if err != nil {
		t.Fatal(err)
	}
	return f, CardActionInput{AppID: "fake-app", EventID: "fake-event", OperatorHash: strings.Repeat("a", 64), Operation: "ack", Notification: binding}, alert
}

func TestCardActionAcknowledgementBindingAndRestartDeduplication(t *testing.T) {
	f, input, alert := cardActionFixture(t)
	ctx := context.Background()
	first, err := f.db.ApplyCardAction(ctx, input)
	if err != nil || first.Duplicate || first.AcknowledgedAt != f.now.UnixMilli() {
		t.Fatal(first, err)
	}
	f.reopen()
	f.now = f.now.Add(time.Minute)
	duplicate, err := f.db.ApplyCardAction(ctx, input)
	if err != nil || !duplicate.Duplicate || duplicate.AcknowledgedAt != first.AcknowledgedAt || duplicate.TS != first.TS {
		t.Fatal("restart repeated effect", duplicate, err)
	}
	for _, field := range []string{"message", "event-key", "channel", "operator", "operation"} {
		bad := input
		switch field {
		case "message":
			bad.Notification.MessageID = "om_other"
		case "event-key":
			bad.Notification.EventKey = "other"
		case "channel":
			bad.Notification.NotifierID = "other"
		case "operator":
			bad.OperatorHash = strings.Repeat("b", 64)
		case "operation":
			bad.Operation = "silence_1h"
		}
		if _, err := f.db.ApplyCardAction(ctx, bad); !errors.Is(err, ErrCardActionBinding) {
			t.Fatal("confused duplicate accepted", field, err)
		}
	}
	if _, err := f.db.CardNotification(ctx, "om_unknown", alert.ID, alert.NotificationKey); !errors.Is(err, ErrCardActionBinding) {
		t.Fatal("unknown message bound", err)
	}
	alert.State = rules.StateResolved
	alert.ResolvedAt = f.now.UnixMilli()
	if err := f.db.SaveTransition(ctx, alert, rules.Event{AlertID: alert.ID, Kind: rules.StateResolved, TS: f.now.UnixMilli()}); err != nil {
		t.Fatal(err)
	}
	input.EventID = "new-after-recovery"
	if _, err := f.db.ApplyCardAction(ctx, input); !errors.Is(err, ErrAlertUnavailable) {
		t.Fatal("recovered card remained actionable", err)
	}
}

func TestCardActionSilenceScopesExpiryAndDistinctClicks(t *testing.T) {
	f, input, alert := cardActionFixture(t)
	ctx := context.Background()
	input.Operation = "silence_1h"
	first, err := f.db.ApplyCardAction(ctx, input)
	if err != nil || first.SilenceUntil != f.now.Add(time.Hour).UnixMilli() {
		t.Fatal(first, err)
	}
	for _, field := range []string{"same", "collector", "target", "rule", "instance"} {
		candidate := alert
		if field != "same" {
			candidate.ID = "other-" + field
			switch field {
			case "collector":
				candidate.CollectorID = "other-host"
			case "target":
				candidate.TargetID = "other-target"
			case "rule":
				candidate.RuleID = "other-rule"
			case "instance":
				candidate.InstanceKey = "other-instance"
			}
			if err := f.db.SaveTransition(ctx, candidate, rules.Event{AlertID: candidate.ID, Kind: rules.StateFiring, TS: f.now.UnixMilli()}); err != nil {
				t.Fatal(err)
			}
		}
		decision, err := f.db.EvaluateNotification(ctx, candidate, f.now)
		if err != nil || (decision.Suppressed == "silenced") != (field == "same") {
			t.Fatal("silence escaped exact group", field, decision, err)
		}
	}
	f.now = f.now.Add(time.Minute)
	duplicate, err := f.db.ApplyCardAction(ctx, input)
	if err != nil || !duplicate.Duplicate || duplicate.SilenceUntil != first.SilenceUntil {
		t.Fatal("replay extended silence", duplicate, err)
	}
	input.EventID = "distinct-click"
	extended, err := f.db.ApplyCardAction(ctx, input)
	if err != nil || extended.Duplicate || extended.SilenceID != first.SilenceID || extended.SilenceUntil != f.now.Add(time.Hour).UnixMilli() {
		t.Fatal("distinct click semantics", extended, err)
	}
	f.now = f.now.Add(time.Hour)
	alert.NotificationAt = f.now.UnixMilli()
	alert.NotificationKey = fmt.Sprintf("firing:%d", f.now.UnixMilli())
	decision, err := f.db.EvaluateNotification(ctx, alert, f.now)
	if err != nil || decision.Suppressed != "" {
		t.Fatal("window never expired", decision, err)
	}
}

func TestCardActionAuditFailureRollsBackEffectAndConcurrentRetryIsSingle(t *testing.T) {
	f, input, _ := cardActionFixture(t)
	ctx := context.Background()
	input.Operation = "silence_1h"
	if _, err := f.db.db.Exec(`CREATE TRIGGER reject_action_audit BEFORE INSERT ON card_action_events BEGIN SELECT RAISE(ABORT,'fake audit failure'); END`); err != nil {
		t.Fatal(err)
	}
	for _, operation := range []string{"ack", "silence_1h"} {
		input.Operation = operation
		if _, err := f.db.ApplyCardAction(ctx, input); err == nil {
			t.Fatal("failed audit reported success", operation)
		}
	}
	input.Operation = "silence_1h"
	var acknowledged int64
	if err := f.db.db.QueryRow(`SELECT acknowledged_at FROM alerts WHERE id=?`, input.Notification.AlertID).Scan(&acknowledged); err != nil || acknowledged != 0 {
		t.Fatal("failed audit committed acknowledgement", acknowledged, err)
	}
	for _, table := range []string{"silences", "card_action_events"} {
		var count int
		if err := f.db.db.QueryRow(`SELECT count(*) FROM ` + table).Scan(&count); err != nil || count != 0 {
			t.Fatal("partial action committed", table, count, err)
		}
	}
	if _, err := f.db.db.Exec(`DROP TRIGGER reject_action_audit`); err != nil {
		t.Fatal(err)
	}
	type outcome struct {
		result CardActionResult
		err    error
	}
	results := make(chan outcome, 6)
	start := make(chan struct{})
	var workers sync.WaitGroup
	for range 6 {
		workers.Add(1)
		go func() {
			defer workers.Done()
			<-start
			result, err := f.db.ApplyCardAction(ctx, input)
			results <- outcome{result, err}
		}()
	}
	close(start)
	workers.Wait()
	close(results)
	first := 0
	for result := range results {
		if result.err != nil {
			t.Fatal(result.err)
		}
		if !result.result.Duplicate {
			first++
		}
	}
	if first != 1 {
		t.Fatal("concurrent retry repeated effect", first)
	}
}

func TestCardActionAuditReplayScopeAndRetentionKeepActiveReceipts(t *testing.T) {
	f, input, alert := cardActionFixture(t)
	ctx := context.Background()
	input.Operation = "silence_1h"
	first, err := f.db.ApplyCardAction(ctx, input)
	if err != nil {
		t.Fatal(err)
	}
	query, err := OpenRuleQueries(ctx, f.path)
	if err != nil {
		t.Fatal(err)
	}
	defer query.Close()
	for _, at := range []int64{first.TS - 1, first.TS} {
		result, err := query.QueryRule(ctx, `SELECT event_id AS key FROM card_action_events`, at)
		want := 0
		if at == first.TS {
			want = 1
		}
		if err != nil || len(result.Rows) != want {
			t.Fatal("audit leaked future callback", at, result, err)
		}
	}
	view, err := OpenViewWithClock(ctx, f.path, func() time.Time { return f.now })
	if err != nil {
		t.Fatal(err)
	}
	defer view.Close()
	for _, host := range []string{alert.CollectorID, "other-host"} {
		result, err := view.QueryShare(ctx, `SELECT event_id FROM card_action_events`, ShareScope{Collectors: []string{host}, Targets: []string{alert.TargetID}}, nil)
		want := 0
		if host == alert.CollectorID {
			want = 1
		}
		if err != nil || len(result.Rows) != want {
			t.Fatal("audit escaped shared group", host, result, err)
		}
	}
	f.now = f.now.Add(48 * time.Hour)
	if err := f.db.cleanExpired(ctx, f.now.UnixMilli()); err != nil {
		t.Fatal(err)
	}
	duplicate, err := f.db.ApplyCardAction(ctx, input)
	if err != nil || !duplicate.Duplicate || duplicate.SilenceUntil != first.SilenceUntil {
		t.Fatal("active receipt purged and replayed", duplicate, err)
	}
	alert.State = rules.StateResolved
	alert.ResolvedAt = f.now.UnixMilli()
	if err := f.db.SaveTransition(ctx, alert, rules.Event{AlertID: alert.ID, Kind: rules.StateResolved, TS: f.now.UnixMilli()}); err != nil {
		t.Fatal(err)
	}
	if err := f.db.cleanExpired(ctx, f.now.UnixMilli()); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := f.db.db.QueryRow(`SELECT count(*) FROM card_action_events`).Scan(&count); err != nil || count != 0 {
		t.Fatal("resolved audit outlived raw retention", count, err)
	}
	if _, err := f.db.ApplyCardAction(ctx, input); !errors.Is(err, ErrAlertUnavailable) {
		t.Fatal("expired receipt revived recovered alert", err)
	}
}
