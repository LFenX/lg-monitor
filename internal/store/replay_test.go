package store

import (
	"context"
	"reflect"
	"testing"

	"github.com/LFenX/lg-monitor/internal/notify"
	"github.com/LFenX/lg-monitor/internal/rules"
)

func TestPendingNotificationsSeparateStatesChannelsAndClosure(t *testing.T) {
	ctx := context.Background()
	s := openStore(t, Options{})
	alert := testAlert()
	if err := s.SaveTransition(ctx, alert, testEvent(alert)); err != nil {
		t.Fatal(err)
	}
	alert.State, alert.ResolvedAt, alert.Summary = rules.StateResolved, 1200, "虚构恢复"
	if err := s.SaveTransition(ctx, alert, testEvent(alert)); err != nil {
		t.Fatal(err)
	}
	if err := s.RecordNotification(ctx, notify.Attempt{ID: "fake-attempt", AlertID: alert.ID, State: "firing", NotifierID: "first", TS: 1200, OK: true, MessageID: "om_fake"}); err != nil {
		t.Fatal(err)
	}
	first, err := s.PendingNotifications(ctx, 1000, 2000, 0, 1)
	if err != nil || len(first) != 1 || first[0].Alert.State != "firing" || first[0].Alert.ResolvedAt != 0 || !reflect.DeepEqual(first[0].Successful, []string{"first"}) {
		t.Fatalf("firing delivery lookup: %+v %v", first, err)
	}
	second, err := s.PendingNotifications(ctx, 1000, 2000, first[0].EventID, 1)
	if err != nil || len(second) != 1 || second[0].Alert.State != "resolved" || len(second[0].Successful) != 0 {
		t.Fatalf("same-millisecond firing success hid recovery: %+v %v", second, err)
	}
	if outside, err := s.PendingNotifications(ctx, 1201, 2000, 0, 10); err != nil || len(outside) != 0 {
		t.Fatalf("outside-window transition selected: %+v %v", outside, err)
	}
	event := rules.Event{AlertID: alert.ID, TS: 1300, Kind: "closed", Message: "规则已删除或修改，告警已关闭"}
	if err := s.SaveTransition(ctx, alert, event); err != nil {
		t.Fatal(err)
	}
	if retired, err := s.PendingNotifications(ctx, 1000, 2000, 0, 10); err != nil || len(retired) != 0 {
		t.Fatalf("closed alert was selected for replay: %+v %v", retired, err)
	}
}
