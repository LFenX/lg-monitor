package app

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/LFenX/lg-monitor/internal/notify/cards"
	"github.com/LFenX/lg-monitor/internal/rules"
	"github.com/LFenX/lg-monitor/internal/store"
)

type failedDigestStore struct{ Persistence }

func (failedDigestStore) DigestAlerts(context.Context, rules.Alert, int64) (store.AlertDigest, error) {
	return store.AlertDigest{}, fmt.Errorf("FAKE_PRIVATE_DATABASE_DETAIL")
}

func TestSelectedDigestSendsRealOccurrencesBeforeFlappingThreshold(t *testing.T) {
	f := newRuntimeFixture(t)
	close(f.client.release)
	var alert rules.Alert
	for i := range 2 {
		at := f.clock.Now().UnixMilli()
		alert = rules.Alert{ID: fmt.Sprintf("fake-digest-selected-%d", i), RuleID: "down", CollectorID: f.opts.ID, TargetID: "demo", State: "firing", Severity: "critical", StartedAt: at, NotificationAt: at, NotificationKey: fmt.Sprintf("firing:%d", at)}
		if err := f.db.SaveTransition(context.Background(), alert, rules.Event{AlertID: alert.ID, TS: at, Kind: "firing"}); err != nil {
			t.Fatal(err)
		}
		if i == 0 {
			f.clock.millis.Add(time.Second.Milliseconds())
			alert.State, alert.ResolvedAt = "resolved", f.clock.Now().UnixMilli()
			if err := f.db.SaveTransition(context.Background(), alert, rules.Event{AlertID: alert.ID, TS: alert.ResolvedAt, Kind: "resolved"}); err != nil {
				t.Fatal(err)
			}
			f.clock.millis.Add(time.Second.Milliseconds())
		}
	}
	q := policyQueue(t, f)
	q.active.Rules[0].Template = ""
	q.active.Notifiers[0].Template = "digest"
	if err := q.Enqueue(context.Background(), alert); err != nil {
		t.Fatal(err)
	}
	close(q.jobs)
	if err := q.run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(f.client.wire) != 1 || len(f.db.attempts) != 1 {
		t.Fatal("digest changed event notification identity")
	}
	message := <-f.client.wire
	if strings.Count(string(message.Card), "已恢复") != 1 || strings.Count(string(message.Card), "已触发") != 1 || message.AlertID != alert.ID || message.EventKey != alert.NotificationKey {
		t.Fatal("selected digest lost real occurrences or identity")
	}
}

func TestDigestReadFailureKeepsCurrentRealAlertWithoutPrivateError(t *testing.T) {
	f := newRuntimeFixture(t)
	q := policyQueue(t, f)
	q.store = failedDigestStore{q.store}
	var logs bytes.Buffer
	q.logger = slog.New(slog.NewTextHandler(&logs, nil))
	input := cards.Input{Alert: rules.Alert{ID: "fake-current", State: "firing", StartedAt: 1}}
	q.fillDigest(context.Background(), &input)
	if len(input.Alerts) != 1 || input.Alerts[0].Alert.ID != "fake-current" || input.AlertsTotal != 1 || len(input.Alerts[0].Alerts) != 0 || strings.Contains(logs.String(), "FAKE_PRIVATE") {
		t.Fatal("failed digest fabricated history or lost current alert")
	}
}
