package store

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/LFenX/lg-monitor/internal/rules"
)

func TestDigestContainsOnlyRealRecentGroupOccurrencesAndExactTotal(t *testing.T) {
	ctx := context.Background()
	through := int64(2000000000000)
	s := openStore(t, Options{Now: func() time.Time { return time.UnixMilli(through) }})
	var current rules.Alert
	for i := range 25 {
		at := through - 2500 + int64(i)*100
		current = rules.Alert{ID: fmt.Sprintf("fake-digest-%02d", i), CollectorID: "fake-host", TargetID: "fake-target", RuleID: "fake-rule", InstanceKey: "fake-key", State: "firing", Severity: "warning", StartedAt: at, Summary: "真实虚构夹具记录"}
		if err := s.SaveTransition(ctx, current, rules.Event{AlertID: current.ID, TS: at, Kind: "firing"}); err != nil {
			t.Fatal(err)
		}
		if i < 24 {
			current.State, current.ResolvedAt = "resolved", at+1
			if err := s.SaveTransition(ctx, current, rules.Event{AlertID: current.ID, TS: at + 1, Kind: "resolved"}); err != nil {
				t.Fatal(err)
			}
		}
	}
	for i := range 5 {
		other := current
		other.ID, other.State, other.ResolvedAt = fmt.Sprintf("fake-other-%d", i), "resolved", through
		switch i {
		case 0:
			other.CollectorID = "private-host"
		case 1:
			other.TargetID = "private-target"
		case 2:
			other.RuleID = "private-rule"
		case 3:
			other.InstanceKey = "private-key"
		case 4:
			other.StartedAt = through - (10 * time.Minute).Milliseconds() - 1
		}
		if err := s.SaveTransition(ctx, other, rules.Event{AlertID: other.ID, TS: through, Kind: "resolved"}); err != nil {
			t.Fatal(err)
		}
	}
	got, err := s.DigestAlerts(ctx, current, through)
	if err != nil || got.Total != 25 || len(got.Alerts) != 20 || got.Alerts[0].ID != "fake-digest-05" || got.Alerts[19].ID != "fake-digest-24" {
		t.Fatalf("digest=%+v err=%v", got, err)
	}
	for i, alert := range got.Alerts {
		wantState := "resolved"
		if i == 19 {
			wantState = "firing"
		}
		if alert.State != wantState || alert.CollectorID != current.CollectorID || alert.InstanceKey != current.InstanceKey {
			t.Fatal("digest leaked unrelated group or lost state", alert)
		}
	}
	if _, err := s.DigestAlerts(ctx, current, 0); err == nil {
		t.Fatal("accepted undefined history clock")
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := s.DigestAlerts(canceled, current, through); err == nil {
		t.Fatal("ignored cancellation")
	}
}
