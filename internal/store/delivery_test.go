package store

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/LFenX/lg-monitor/internal/notify"
)

func TestDeliverySeparatesAlertStateAndChannel(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "monitor.db")
	s := openStore(t, Options{Path: path})
	alert := testAlert()
	if err := s.SaveTransition(ctx, alert, testEvent(alert)); err != nil {
		t.Fatal(err)
	}
	for _, attempt := range []notify.Attempt{
		{ID: "fire", AlertID: alert.ID, State: "firing", NotifierID: "first", TS: 1000, OK: true, MessageID: "om_fake_fire"},
		{ID: "other-channel", AlertID: alert.ID, State: "resolved", NotifierID: "second", TS: 2000, OK: true, MessageID: "om_fake_other"},
		{ID: "failed-recovery", AlertID: alert.ID, State: "resolved", NotifierID: "first", TS: 3000, Error: "虚构失败"},
	} {
		if err := s.RecordNotification(ctx, attempt); err != nil {
			t.Fatal(err)
		}
	}
	view, err := OpenView(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer view.Close()
	for _, query := range []struct {
		alert, state, channel string
		ok                    bool
	}{
		{alert.ID, "firing", "first", true},
		{alert.ID, "resolved", "first", false},
		{alert.ID, "resolved", "second", true},
		{"missing", "firing", "first", false},
	} {
		receipt, err := view.Delivery(ctx, query.alert, query.state, query.channel)
		if err != nil || receipt.OK != query.ok {
			t.Fatalf("delivery %+v: %+v %v", query, receipt, err)
		}
	}
	if _, err := view.Delivery(ctx, alert.ID, "closed", "first"); err == nil {
		t.Fatal("invalid state accepted")
	}
}
