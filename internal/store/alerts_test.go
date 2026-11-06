package store

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"

	"github.com/LFenX/lg-monitor/internal/rules"
)

func testAlert() rules.Alert {
	return rules.Alert{ID: "alert-test", RuleID: "down", CollectorID: "collector-test", TargetID: "web", State: rules.StateFiring, Severity: "critical", StartedAt: 1000, LastNotifiedAt: 1100, Summary: "测试触发"}
}
func testEvent(alert rules.Alert) rules.Event {
	return rules.Event{AlertID: alert.ID, TS: 1200, Kind: alert.State, Message: alert.Summary}
}

func TestAlertTransitionsAndNotificationTime(t *testing.T) {
	s := openStore(t, Options{})
	alert := testAlert()
	if err := s.SaveTransition(context.Background(), alert, testEvent(alert)); err != nil {
		t.Fatal(err)
	}
	if err := s.SaveTransition(context.Background(), alert, testEvent(alert)); err != nil || count(t, s, "alert_events") != 1 {
		t.Fatalf("replaying an uncertain commit duplicated the event: %v", err)
	}
	active, err := s.ActiveAlerts(context.Background())
	if err != nil || len(active) != 1 || active[0] != alert {
		t.Fatalf("active alert = %+v, %v", active, err)
	}
	alert.State, alert.ResolvedAt, alert.LastNotifiedAt = rules.StateResolved, 2000, 0
	if err := s.SaveTransition(context.Background(), alert, testEvent(alert)); err != nil {
		t.Fatal(err)
	}
	if active, err := s.ActiveAlerts(context.Background()); err != nil || len(active) != 0 || count(t, s, "alert_events") != 2 {
		t.Fatalf("resolved alert is still active: %+v, %v", active, err)
	}
	var notified int64
	if err := s.db.QueryRow(`SELECT last_notified_at FROM alerts WHERE id=?`, alert.ID).Scan(&notified); err != nil || notified != 1100 {
		t.Fatalf("successful notification timestamp was overwritten: %d, %v", notified, err)
	}
}

func TestActiveAlertUniquenessAndTransitionRollback(t *testing.T) {
	for _, duplicate := range []bool{true, false} {
		s, err := Open(context.Background(), Options{Path: filepath.Join(t.TempDir(), "monitor.db")})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = s.Close() })
		alert := testAlert()
		if duplicate {
			if err := s.SaveTransition(context.Background(), alert, testEvent(alert)); err != nil {
				t.Fatal(err)
			}
			alert.ID = "duplicate-incident"
		} else {
			if err := s.operation(context.Background(), func(ctx context.Context, tx *sql.Tx) error {
				_, err := tx.ExecContext(ctx, `DROP TABLE alert_events`)
				return err
			}); err != nil {
				t.Fatal(err)
			}
		}
		if err := s.SaveTransition(context.Background(), alert, testEvent(alert)); err == nil {
			t.Fatal("invalid transition unexpectedly committed")
		}
		want := 0
		if duplicate {
			want = 1
		}
		if got := count(t, s, "alerts"); got != want {
			t.Fatalf("transition partially committed: %d alerts, want %d", got, want)
		}
		if duplicate && count(t, s, "alert_events") != 1 {
			t.Fatal("duplicate active incident appended an event")
		}
	}
}
