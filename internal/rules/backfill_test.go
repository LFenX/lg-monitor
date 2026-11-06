package rules

import (
	"context"
	"testing"
	"time"

	"github.com/LFenX/lg-monitor/internal/collector"
	"github.com/LFenX/lg-monitor/internal/config"
	"github.com/LFenX/lg-monitor/internal/pipeline"
)

func backfillStatus(t *testing.T, f *fixture, id string, at time.Time, down bool) {
	t.Helper()
	f.seq++
	state, process := collector.StateUp, collector.ProcessFound
	if down {
		state, process = collector.StateDown, collector.ProcessMissing
	}
	if err := f.engine.Handle(context.Background(), pipeline.Message{CollectorID: id, Seq: f.seq, TS: at.UnixMilli(), Kind: pipeline.KindTargetStatus, TargetStatus: &pipeline.TargetStatus{TargetID: "web", State: state, Process: process}}); err != nil {
		t.Fatal(err)
	}
}

func TestCompletedBackfillIncidentNeverFiresEvenAcrossTicks(t *testing.T) {
	f := newFixture(t, "0s", "process_absent", 0)
	backfillStatus(t, f, "host-test", f.now.Add(-5*time.Minute), true)
	f.evaluate(t, time.Minute)
	backfillStatus(t, f, "host-test", f.now.Add(-2*time.Minute), false)
	f.evaluate(t, time.Minute)
	backfillStatus(t, f, "host-test", f.now.Add(-time.Second), false)
	noNotification(t, f.notifications)
	if len(f.repository.events) != 0 {
		t.Fatal("completed historical incident created alert events")
	}
}

func TestOngoingBackfillIncidentUsesOriginalDataStart(t *testing.T) {
	f := newFixture(t, "2m", "process_absent", 0)
	start := f.now.Add(-10 * time.Minute)
	backfillStatus(t, f, "host-test", start, true)
	backfillStatus(t, f, "host-test", f.now.Add(-5*time.Minute), true)
	noNotification(t, f.notifications)
	backfillStatus(t, f, "host-test", f.now.Add(-time.Second), true)
	alert := takeAlert(t, f.notifications, StateFiring)
	if alert.StartedAt != start.UnixMilli() || len(f.repository.events) != 1 || f.repository.events[0].TS != f.now.UnixMilli() {
		t.Fatalf("backfill incident time lost: %+v %+v", alert, f.repository.events)
	}
	f.evaluate(t, time.Second)
	noNotification(t, f.notifications)
}

func TestExistingFiringIncidentIgnoresIntermediateBackfillRecovery(t *testing.T) {
	f := newFixture(t, "0s", "process_absent", 0)
	f.status(t, collector.StateDown)
	first := takeAlert(t, f.notifications, StateFiring)
	f.now = f.now.Add(10 * time.Minute)
	backfillStatus(t, f, "host-test", f.now.Add(-8*time.Minute), false)
	backfillStatus(t, f, "host-test", f.now.Add(-6*time.Minute), true)
	f.evaluate(t, time.Second)
	noNotification(t, f.notifications)
	backfillStatus(t, f, "host-test", f.now.Add(-time.Second), true)
	noNotification(t, f.notifications)
	if len(f.repository.events) != 1 {
		t.Fatal("historical recovery cycled an existing incident")
	}
	backfillStatus(t, f, "host-test", f.now, false)
	resolved := takeAlert(t, f.notifications, StateResolved)
	if resolved.ID != first.ID || len(f.repository.events) != 2 {
		t.Fatal("latest recovery did not resolve the original incident")
	}
}

func TestThirtySecondBoundaryAndOtherCollectorsRemainLive(t *testing.T) {
	f := newFixture(t, "5s", "process_absent", 0)
	started := f.now
	backfillStatus(t, f, "host-test", f.now.Add(-30*time.Second), true)
	f.evaluate(t, 4*time.Second)
	noNotification(t, f.notifications)
	f.evaluate(t, time.Second)
	if alert := takeAlert(t, f.notifications, StateFiring); alert.StartedAt != started.UnixMilli() {
		t.Fatal("live behavior changed at exact 30-second boundary")
	}
	f = newFixture(t, "0s", "process_absent", 0)
	backfillStatus(t, f, "backlog-host", f.now.Add(-time.Minute), true)
	backfillStatus(t, f, "live-host", f.now, true)
	if alert := takeAlert(t, f.notifications, StateFiring); alert.CollectorID != "live-host" {
		t.Fatal("one collector's backlog suppressed another collector")
	}
	noNotification(t, f.notifications)
}

func TestMetricBackfillReusesDurationAndUnknownBreaksContinuity(t *testing.T) {
	target := config.Target{ID: "scope", Collector: "fake", Match: config.Match{Process: "unused"}}
	rule := config.Rule{ID: "cpu", Type: "metric_threshold", Target: "scope", Metric: "system.cpu_percent", Op: "gt", Threshold: threshold(90), For: "5m"}
	engine, _, queue, now := newAdditionalEngine(t, rule, &target)
	start := now.Add(-10 * time.Minute)
	send := func(at time.Time) {
		t.Helper()
		if err := engine.Handle(context.Background(), pipeline.Message{CollectorID: "fake", Seq: 1, TS: at.UnixMilli(), Kind: pipeline.KindSystemMetrics, SystemMetrics: &collector.SystemMetrics{CPUPercent: 95}}); err != nil {
			t.Fatal(err)
		}
	}
	send(start)
	if len(queue.alerts) != 0 {
		t.Fatal("historical threshold fired")
	}
	send(*now)
	if len(queue.alerts) != 1 {
		t.Fatal("completed threshold duration restarted after catchup")
	}
	if alert := <-queue.alerts; alert.StartedAt != start.UnixMilli() {
		t.Fatal("target-scoped system metric lost data start")
	}
	f := newFixture(t, "2m", "process_absent", 0)
	backfillStatus(t, f, "host-test", f.now.Add(-5*time.Minute), true)
	f.seq++
	if err := f.engine.Handle(context.Background(), pipeline.Message{CollectorID: "host-test", Seq: f.seq, TS: f.now.Add(-time.Minute).UnixMilli(), Kind: pipeline.KindTargetStatus, TargetStatus: &pipeline.TargetStatus{TargetID: "web", State: collector.StateUnknown, Process: collector.ProcessUnknown}}); err != nil {
		t.Fatal(err)
	}
	backfillStatus(t, f, "host-test", f.now.Add(-time.Second), true)
	noNotification(t, f.notifications)
	f.evaluate(t, 119*time.Second)
	alert := takeAlert(t, f.notifications, StateFiring)
	if alert.StartedAt != f.now.Add(-120*time.Second).UnixMilli() {
		t.Fatal("unknown did not reset the historical duration")
	}
}

func TestBackfillDoesNotDelaySQLOrCollectorOffline(t *testing.T) {
	f, source := offlineFixture(t, "0s")
	source.items[0].Connected = false
	backfillStatus(t, f, "paired", f.now.Add(-time.Minute), true)
	if alert := takeAlert(t, f.notifications, StateFiring); alert.TargetID != CollectorTargetID {
		t.Fatal("backfill suppressed or replaced collector offline")
	}
	source.items[0].Connected, source.items[0].LastSeen = true, f.now.UnixMilli()
	f.evaluate(t, 0)
	takeAlert(t, f.notifications, StateResolved)
	noNotification(t, f.notifications)

	engine, _, queue, now := newAdditionalEngine(t, config.Rule{ID: "sql", Type: "sql", Query: "SELECT 1 AS key", Interval: "10s"}, nil)
	engine.queries = ruleQueryFunc(func(_ context.Context, _ string, at int64) (QueryResult, error) {
		if at != now.UnixMilli() {
			t.Fatal("backfill changed SQL evaluation time")
		}
		return QueryResult{Columns: []string{"key"}, Rows: [][]any{{"fake"}}}, nil
	})
	if err := engine.Handle(context.Background(), pipeline.Message{CollectorID: "fake", Seq: 1, TS: now.Add(-time.Minute).UnixMilli(), Kind: pipeline.KindSystemMetrics, SystemMetrics: &collector.SystemMetrics{}}); err != nil {
		t.Fatal(err)
	}
	if alert := takeAlert(t, queue, StateFiring); alert.RuleID != "sql" || alert.StartedAt != now.UnixMilli() {
		t.Fatal("backfill changed SQL lifecycle")
	}
}

func TestRetiredCollectorAssignmentCannotReuseBackfillDuration(t *testing.T) {
	f := newFixture(t, "2m", "process_absent", 0)
	f.cfg.Targets[0].Collector = "assigned-host"
	hostname := "assigned-host"
	opts := f.options()
	opts.CollectorHostname = func(context.Context, string) (string, error) { return hostname, nil }
	var err error
	f.engine, err = New(context.Background(), opts)
	if err != nil {
		t.Fatal(err)
	}
	backfillStatus(t, f, "host-test", f.now.Add(-5*time.Minute), true)
	hostname = "another-host"
	backfillStatus(t, f, "host-test", f.now, true)
	hostname = "assigned-host"
	backfillStatus(t, f, "host-test", f.now, true)
	noNotification(t, f.notifications)
	f.evaluate(t, 2*time.Minute)
	if alert := takeAlert(t, f.notifications, StateFiring); alert.StartedAt != f.now.Add(-2*time.Minute).UnixMilli() {
		t.Fatal("retired assignment reused historical condition time")
	}
}
