package store

import (
	"context"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/LFenX/lg-monitor/internal/collector"
	"github.com/LFenX/lg-monitor/internal/config"
	"github.com/LFenX/lg-monitor/internal/pipeline"
	"github.com/LFenX/lg-monitor/internal/rules"
)

func eventLogMessage(seq uint64, at int64) pipeline.Message {
	return pipeline.Message{CollectorID: "fake", Seq: seq, TS: at, Kind: pipeline.KindEventLog, EventLog: &collector.EventLogRecord{Channel: "Application", Provider: "Application Error", EventID: 1000, Level: 2, RecordID: 1, TS: at, Subject: "fake.exe", Message: "fake event", Presets: []string{"app_crash"}, Targets: []collector.EventLogTarget{{ID: "first", Via: "subscription"}, {ID: "second", Via: "subscription"}}}}
}

func TestEventLogStorageDeduplicationOwnershipAndClearedLogIdentity(t *testing.T) {
	ctx := context.Background()
	s := openStore(t, Options{DisableMaintenance: true})
	first := eventLogMessage(1, 1000000)
	if err := s.Write(ctx, first); err != nil {
		t.Fatal(err)
	}
	if err := s.Flush(ctx); err != nil {
		t.Fatal(err)
	}
	duplicate := eventLogMessage(2, first.TS)
	duplicate.EventLog.Message = "changed"
	duplicate.EventLog.Targets = []collector.EventLogTarget{{ID: "later", Via: "both"}}
	if err := s.Write(ctx, duplicate); err != nil {
		t.Fatal(err)
	}
	if err := s.Flush(ctx); err != nil {
		t.Fatal(err)
	}
	if count(t, s, "eventlog_events") != 1 || count(t, s, "eventlog_matches") != 3 {
		t.Fatal("duplicate changed frozen ownership")
	}
	var message string
	if err := s.db.QueryRow(`SELECT message FROM eventlog_events`).Scan(&message); err != nil || message != "fake event" {
		t.Fatalf("duplicate changed message: %q %v", message, err)
	}
	second := eventLogMessage(3, first.TS+1)
	second.EventLog.Targets = []collector.EventLogTarget{{ID: "first", Via: "both"}}
	if err := s.Write(ctx, second); err != nil {
		t.Fatal(err)
	}
	if err := s.Flush(ctx); err != nil {
		t.Fatal(err)
	}
	if count(t, s, "eventlog_events") != 2 || count(t, s, "eventlog_matches") != 6 || count(t, s, "v_eventlog_targets") != 3 || count(t, s, "rollup_dirty") != 0 {
		t.Fatal("event identity, ownership, or aggregate exclusion differs")
	}
	var via string
	if err := s.db.QueryRow(`SELECT via FROM v_eventlog_targets WHERE ts=?`, second.TS).Scan(&via); err != nil || via != "both" {
		t.Fatalf("double attribution lost: %s %v", via, err)
	}
	view, err := OpenView(ctx, sPath(t, s))
	if err != nil {
		t.Fatal(err)
	}
	defer view.Close()
	got, err := view.QueryEventLog(ctx, rules.EventLogWindow{CollectorID: "fake", TargetID: "first", Preset: "app_crash", From: 0, To: second.TS, Threshold: 1})
	if err != nil || got.Count != 1 || got.Latest == nil || got.Latest.TS != second.TS || got.ThresholdTS != second.TS {
		t.Fatalf("subject/preset intersection incorrect: %+v %v", got, err)
	}
	queries, err := OpenRuleQueries(ctx, sPath(t, s))
	if err != nil {
		t.Fatal(err)
	}
	defer queries.Close()
	for _, table := range []string{"eventlog_events", "eventlog_matches", "v_eventlog_targets"} {
		result, err := queries.QueryRule(ctx, "SELECT count(*) FROM "+table, first.TS)
		want := int64(1)
		if table == "eventlog_matches" {
			want = 3
		}
		if table == "v_eventlog_targets" {
			want = 2
		}
		if err != nil || !reflect.DeepEqual(result.Rows, [][]any{{want}}) {
			t.Fatalf("%s leaked future events: %+v %v", table, result, err)
		}
	}
}

type eventLogNotifications struct{ alerts []rules.Alert }

func (n *eventLogNotifications) Enqueue(_ context.Context, alert rules.Alert) error {
	n.alerts = append(n.alerts, alert)
	return nil
}

func TestEventLogRulesCountWindowRecoveryAndRestartWithoutRepeatedTriggers(t *testing.T) {
	ctx := context.Background()
	var millis atomic.Int64
	millis.Store(1000000)
	now := func() time.Time { return time.UnixMilli(millis.Load()) }
	s := openStore(t, Options{Now: now, DisableMaintenance: true})
	n := 2
	cfg := &config.Config{Targets: []config.Target{{ID: "first", EventLog: &config.EventLogSubscription{Channel: "Application", Provider: "Application Error"}}, {ID: "second", EventLog: &config.EventLogSubscription{Channel: "Application", Provider: "Application Error"}}}, Rules: []config.Rule{{ID: "preset", Type: "eventlog", Preset: "app_crash", Window: "10s", Count: &n}, {ID: "first", Type: "eventlog", Target: "first", Window: "10s", Count: &n}, {ID: "second", Type: "eventlog", Target: "second", Window: "10s", Count: &n}}}
	queue := &eventLogNotifications{}
	engine, err := rules.New(ctx, rules.Options{Config: cfg, Repository: s, Notifications: queue, Now: now})
	if err != nil {
		t.Fatal(err)
	}
	write := func(message pipeline.Message) {
		t.Helper()
		if err := s.Write(ctx, message); err != nil {
			t.Fatal(err)
		}
		if err := s.Flush(ctx); err != nil {
			t.Fatal(err)
		}
		if err := engine.Handle(ctx, message); err != nil {
			t.Fatal(err)
		}
	}
	write(eventLogMessage(1, 1000000))
	if len(queue.alerts) != 0 {
		t.Fatal("rule fired before count")
	}
	millis.Store(1001000)
	write(eventLogMessage(2, 1001000))
	if len(queue.alerts) != 3 {
		t.Fatalf("three independent rules: %+v", queue.alerts)
	}
	for _, alert := range queue.alerts {
		if alert.State != rules.StateFiring || alert.EventLog == nil || alert.EventLog.Count != 2 || alert.StartedAt != 1001000 {
			t.Fatalf("event rule snapshot: %+v", alert)
		}
	}
	engine, err = rules.New(ctx, rules.Options{Config: cfg, Repository: s, Notifications: queue, Now: now})
	if err != nil {
		t.Fatal(err)
	}
	if err := engine.Evaluate(ctx); err != nil {
		t.Fatal(err)
	}
	if len(queue.alerts) != 3 {
		t.Fatal("restart repeated trigger")
	}
	millis.Store(1010000)
	if err := engine.Evaluate(ctx); err != nil {
		t.Fatal(err)
	}
	active, err := s.ActiveAlerts(ctx)
	if err != nil || len(active) != 3 || active[0].EventLog == nil || active[0].EventLog.Count != 1 {
		t.Fatalf("firing did not survive count falling below threshold: %+v %v", active, err)
	}
	if count(t, s, "alert_events") != 3 || len(queue.alerts) != 3 {
		t.Fatal("count update created another trigger")
	}
	backfill, err := s.PendingNotifications(ctx, 0, 2000000, 0, 10)
	if err != nil || len(backfill) != 3 || backfill[0].Alert.EventLog == nil || backfill[0].Alert.EventLog.Count != 2 {
		t.Fatalf("notification snapshot changed with live count: %+v %v", backfill, err)
	}
	millis.Store(1011000)
	if err := engine.Evaluate(ctx); err != nil {
		t.Fatal(err)
	}
	if len(queue.alerts) != 6 || queue.alerts[3].State != rules.StateResolved {
		t.Fatal("window did not recover at its exact end")
	}
	millis.Store(1030000)
	write(eventLogMessage(3, 1002000))
	if len(queue.alerts) != 6 {
		t.Fatal("expired backfill sent a notification")
	}
	view, err := OpenViewWithClock(ctx, sPath(t, s), now)
	if err != nil {
		t.Fatal(err)
	}
	defer view.Close()
	replay, err := rules.Replay(ctx, rules.ReplayOptions{Config: cfg, Rule: cfg.Rules[0], Since: time.UnixMilli(1000000), Until: time.UnixMilli(1013000), Now: now, Source: view})
	if err != nil || !replay.Complete || replay.Totals.Fired != 1 || replay.Totals.Resolved != 1 {
		t.Fatalf("event rule replay: %+v %v", replay, err)
	}
}

func TestEventLogRetentionDeletesMatchesAndDropsLateEventOnce(t *testing.T) {
	ctx := context.Background()
	s := openStore(t, Options{DisableMaintenance: true})
	old := eventLogMessage(1, 1000)
	if err := s.Write(ctx, old); err != nil {
		t.Fatal(err)
	}
	if err := s.Flush(ctx); err != nil {
		t.Fatal(err)
	}
	if err := s.cleanRawTable(ctx, "eventlog_events", 3600000); err != nil {
		t.Fatal(err)
	}
	if count(t, s, "eventlog_events") != 0 || count(t, s, "eventlog_matches") != 0 {
		t.Fatal("event retention orphaned matches")
	}
	if err := s.Write(ctx, eventLogMessage(2, 1001)); err != nil {
		t.Fatal(err)
	}
	if err := s.Flush(ctx); err != nil {
		t.Fatal(err)
	}
	var dropped int
	var table, message string
	if err := s.db.QueryRow(`SELECT dropped,source_table,message FROM collector_events WHERE kind='late_dropped'`).Scan(&dropped, &table, &message); err != nil || dropped != 1 || table != "eventlog_events" || !strings.Contains(message, "1 条") {
		t.Fatalf("late event counted more than once: %d %s %s %v", dropped, table, message, err)
	}
	if count(t, s, "eventlog_events") != 0 || count(t, s, "eventlog_matches") != 0 || count(t, s, "rollup_dirty") != 0 {
		t.Fatal("late event resurrected expired facts")
	}
}

func TestEventLogFieldsDoNotBreakReadOnlyAlertsOnBaselineSchema(t *testing.T) {
	ctx := context.Background()
	s := openStore(t, Options{DisableMaintenance: true})
	alert := testAlert()
	if err := s.SaveTransition(ctx, alert, testEvent(alert)); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.ExecContext(ctx, `ALTER TABLE alerts DROP COLUMN source_ts; ALTER TABLE alerts DROP COLUMN eventlog`); err != nil {
		t.Fatal(err)
	}
	view, err := OpenView(ctx, sPath(t, s))
	if err != nil {
		t.Fatal(err)
	}
	defer view.Close()
	alerts, err := view.Alerts(ctx, true)
	if err != nil || len(alerts) != 1 || alerts[0].ID != alert.ID || alerts[0].SourceTS != 0 || alerts[0].EventLog != nil {
		t.Fatalf("baseline alert read failed: %+v %v", alerts, err)
	}
	page, err := view.AlertEventLogs(ctx, alert.ID, 200, "")
	if err != nil || page.From != nil || len(page.Events) != 0 {
		t.Fatalf("baseline event window should be unavailable: %+v %v", page, err)
	}
}

func TestEventLogIngressPersistsFactsAndRateAuditWithOneWatermark(t *testing.T) {
	ctx := context.Background()
	s := openStore(t, Options{DisableMaintenance: true})
	input := []pipeline.Message{eventLogMessage(1, 1000000), {CollectorID: "fake", Seq: 2, TS: 1000001, Kind: pipeline.KindEventLogNotice, EventLogNotice: &collector.EventLogNotice{Kind: "eventlog_dropped", Channel: "Application", Count: 150, Message: "fake rate limit"}}}
	batch, err := s.IngestBatch(ctx, input)
	if err != nil || batch.Through != 2 || len(batch.Messages) != 2 || batch.Messages[0].EventLog.RecordID != 1 || batch.Messages[1].EventLogNotice.Count != 150 {
		t.Fatalf("event ingress or JSON canonical copy failed: %+v %v", batch, err)
	}
	var dropped, from, to int64
	if err := s.db.QueryRow(`SELECT dropped,gap_from,gap_to FROM collector_events WHERE collector_id='fake' AND seq=2 AND kind='eventlog_dropped'`).Scan(&dropped, &from, &to); err != nil || dropped != 150 || from != 0 || to != 0 {
		t.Fatalf("rate audit not persisted: %d %d %d %v", dropped, from, to, err)
	}
	if watermark, err := s.LastCommitted(ctx, "fake"); err != nil || watermark != 0 {
		t.Fatal("event batch acknowledged before application")
	}
	if err := s.CompleteIngressBatch(ctx, "fake", batch.Through); err != nil {
		t.Fatal(err)
	}
	if watermark, err := s.LastCommitted(ctx, "fake"); err != nil || watermark != 2 || count(t, s, "eventlog_events") != 1 || count(t, s, "eventlog_matches") != 3 {
		t.Fatalf("facts or transport watermark missing: %d %v", watermark, err)
	}
	if duplicate, err := s.IngestBatch(ctx, input); err != nil || len(duplicate.Messages) != 0 || count(t, s, "collector_events") != 1 {
		t.Fatal("acknowledged event batch duplicated facts or rate audit", err)
	}
}
