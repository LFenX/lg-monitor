package store

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/LFenX/lg-monitor/internal/notify"
	"github.com/LFenX/lg-monitor/internal/rules"
)

func TestPauseBoundsModificationAndNullState(t *testing.T) {
	f := newControlFixture(t)
	ctx := context.Background()
	for _, d := range []time.Duration{0, time.Minute - 1, 24*time.Hour + 1} {
		if _, err := f.db.SetNotificationPause(ctx, d, "", "local"); err == nil {
			t.Fatalf("accepted duration %s", d)
		}
	}
	if _, err := f.db.SetNotificationPause(ctx, time.Hour, strings.Repeat("会", 201), "local"); err == nil {
		t.Fatal("accepted long reason")
	}
	p, err := f.db.SetNotificationPause(ctx, time.Minute, strings.Repeat("会", 200), "tray-fake")
	if err != nil || !p.Paused || *p.By != "tray-fake" {
		t.Fatalf("minimum: %+v %v", p, err)
	}
	start := *p.StartedAt
	f.now = f.now.Add(10 * time.Second)
	p, err = f.db.SetNotificationPause(ctx, 24*time.Hour, "延长", "another")
	if err != nil || *p.StartedAt != start || *p.Until != f.now.Add(24*time.Hour).UnixMilli() {
		t.Fatalf("extend: %+v %v", p, err)
	}
	p, err = f.db.SetNotificationPause(ctx, time.Minute, "缩短", "local")
	if err != nil || *p.Until != f.now.Add(time.Minute).UnixMilli() {
		t.Fatalf("shorten: %+v %v", p, err)
	}
	if _, err := f.db.ResumeNotifications(ctx); err != nil {
		t.Fatal(err)
	}
	p, err = f.db.NotificationPause(ctx)
	data, _ := json.Marshal(p)
	if err != nil || string(data) != `{"paused":false,"startedAt":null,"until":null,"reason":null,"by":null,"suppressed":null}` {
		t.Fatalf("inactive: %s %v", data, err)
	}
}

func TestPauseCatchupSelectionAndRestart(t *testing.T) {
	for _, tc := range []struct {
		name                  string
		before, recover, want bool
	}{
		{"ongoing", false, false, true},
		{"recovery of notified alert", true, true, true},
		{"transient incident", false, true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newControlFixture(t)
			ctx := context.Background()
			if tc.before {
				f.status(false)
				a := f.sent.rows[len(f.sent.rows)-1]
				if err := f.db.RecordNotification(ctx, notify.Attempt{ID: "prior", AlertID: a.ID, NotifierID: "channel-a", State: a.State, EventKey: a.NotificationKey, TS: f.now.UnixMilli(), OK: true}); err != nil {
					t.Fatal(err)
				}
				f.now = f.now.Add(time.Second)
			}
			if _, err := f.db.SetNotificationPause(ctx, time.Minute, "meeting", "local"); err != nil {
				t.Fatal(err)
			}
			if !tc.before {
				f.status(false)
			}
			if tc.recover {
				f.now = f.now.Add(time.Second)
				f.status(true)
			}
			p, err := f.db.NotificationPause(ctx)
			wantCount := int64(1)
			if !tc.before && tc.recover {
				wantCount = 2
			}
			if err != nil || *p.Suppressed != wantCount {
				t.Fatalf("suppression count: %+v %v", p, err)
			}
			// The process was offline at expiry; startup must reconstruct catch-up.
			f.now = f.now.Add(2 * time.Minute)
			f.reopen()
			if err := f.db.PreparePauseResumes(ctx); err != nil {
				t.Fatal(err)
			}
			items, err := f.db.PendingPauseNotifications(ctx)
			if err != nil || (len(items) == 1) != tc.want {
				t.Fatalf("pending: %+v %v", items, err)
			}
			if !tc.want {
				return
			}
			item := items[0]
			if !strings.HasPrefix(item.Alert.NotificationKey, "resume:") {
				t.Fatal("original event reused")
			}
			wantState := rules.StateFiring
			if tc.recover {
				wantState = rules.StateResolved
			}
			if item.Alert.State != wantState || (tc.recover && len(item.Allowed) != 1) {
				t.Fatalf("catch-up: %+v", item)
			}
			if err := f.db.RecordNotification(ctx, notify.Attempt{ID: "catch-up", AlertID: item.Alert.ID, NotifierID: "channel-a", State: item.Alert.State, EventKey: item.Alert.NotificationKey, TS: f.now.UnixMilli(), OK: true}); err != nil {
				t.Fatal(err)
			}
			f.reopen()
			if err := f.db.PreparePauseResumes(ctx); err != nil {
				t.Fatal(err)
			}
			items, err = f.db.PendingPauseNotifications(ctx)
			if err != nil || len(items) != 1 || len(items[0].Successful) != 1 || items[0].Alert.NotificationKey != item.Alert.NotificationKey {
				t.Fatalf("restart lost progress: %+v %v", items, err)
			}
			if err := f.db.CompletePauseNotification(ctx, item.Alert.ID, item.Alert.NotificationKey); err != nil {
				t.Fatal(err)
			}
			if err := f.db.PreparePauseResumes(ctx); err != nil {
				t.Fatal(err)
			}
			items, err = f.db.PendingPauseNotifications(ctx)
			if err != nil || len(items) != 0 {
				t.Fatalf("completed event repeated: %+v %v", items, err)
			}
			var done int
			if err := f.db.db.QueryRow("SELECT count(*) FROM notification_pauses WHERE resumed_at IS NOT NULL").Scan(&done); err != nil || done != 1 {
				t.Fatalf("completion missing: %d %v", done, err)
			}
		})
	}
}

func TestPauseCatchupRechecksAcknowledgementAndSilence(t *testing.T) {
	for _, silence := range []bool{false, true} {
		f := newControlFixture(t)
		ctx := context.Background()
		if _, err := f.db.SetNotificationPause(ctx, time.Hour, "", "local"); err != nil {
			t.Fatal(err)
		}
		f.status(false)
		active, err := f.db.ActiveAlerts(ctx)
		if err != nil || len(active) != 1 {
			t.Fatalf("pause lost active state: %+v %v", active, err)
		}
		a := active[0]
		// Repeated destination policy checks count the event only once.
		for range 3 {
			if d, err := f.db.EvaluateNotification(ctx, a, f.now); err != nil || d.Suppressed != "paused" {
				t.Fatalf("decision: %+v %v", d, err)
			}
		}
		p, _ := f.db.NotificationPause(ctx)
		if *p.Suppressed != 1 {
			t.Fatalf("duplicate count: %d", *p.Suppressed)
		}
		if silence {
			_, err = f.db.AddSilence(ctx, "down", "", time.Hour, "maintenance")
		} else {
			err = f.db.AcknowledgeAlert(ctx, a.ID)
		}
		if err != nil {
			t.Fatal(err)
		}
		f.now = f.now.Add(time.Second)
		if _, err := f.db.ResumeNotifications(ctx); err != nil {
			t.Fatal(err)
		}
		if err := f.db.PreparePauseResumes(ctx); err != nil {
			t.Fatal(err)
		}
		items, err := f.db.PendingPauseNotifications(ctx)
		if err != nil || len(items) != 1 {
			t.Fatalf("catch-up: %+v %v", items, err)
		}
		d, err := f.db.EvaluateNotification(ctx, items[0].Alert, f.now)
		want := "acknowledged"
		if silence {
			want = "silenced"
		}
		if err != nil || d.Suppressed != want {
			t.Fatalf("current control ignored: %+v %v", d, err)
		}
	}
}

func TestPauseCatchupUsesCurrentSilenceAndPerChannelProgress(t *testing.T) {
	f := newControlFixture(t)
	ctx := context.Background()
	if _, err := f.db.SetNotificationPause(ctx, time.Minute, "", "local"); err != nil {
		t.Fatal(err)
	}
	f.status(false)
	f.now = f.now.Add(time.Minute)
	// Success on one channel must not discard catch-up for other channels.
	active, err := f.db.ActiveAlerts(ctx)
	if err != nil || len(active) != 1 {
		t.Fatal("missing alert")
	}
	a := active[0]
	if err := f.db.RecordNotification(ctx, notify.Attempt{ID: "late-success", AlertID: a.ID, State: a.State, NotifierID: "first", TS: f.now.UnixMilli(), OK: true}); err != nil {
		t.Fatal(err)
	}
	if err := f.db.PreparePauseResumes(ctx); err != nil {
		t.Fatal(err)
	}
	items, err := f.db.PendingPauseNotifications(ctx)
	if err != nil || len(items) != 1 {
		t.Fatalf("one channel swallowed other catch-up: %+v %v", items, err)
	}
	if _, err := f.db.AddSilence(ctx, "down", "", time.Second, "expired before delivery"); err != nil {
		t.Fatal(err)
	}
	f.now = f.now.Add(2 * time.Second)
	decision, err := f.db.EvaluateNotification(ctx, items[0].Alert, f.now)
	if err != nil || decision.Suppressed != "" {
		t.Fatalf("expired silence suppressed current catch-up: %+v %v", decision, err)
	}
}
