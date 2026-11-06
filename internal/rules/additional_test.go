package rules

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/LFenX/lg-monitor/internal/collector"
	"github.com/LFenX/lg-monitor/internal/config"
	"github.com/LFenX/lg-monitor/internal/pipeline"
)

func threshold(value float64) *float64 { return &value }

func newAdditionalEngine(t *testing.T, rule config.Rule, target *config.Target) (*Engine, *memoryRepository, *notificationQueue, *time.Time) {
	t.Helper()
	now := time.UnixMilli(1000000)
	repository := &memoryRepository{alerts: make(map[string]Alert)}
	queue := &notificationQueue{alerts: make(chan Alert, 100)}
	cfg := &config.Config{Rules: []config.Rule{rule}}
	if target != nil {
		cfg.Targets = []config.Target{*target}
	}
	engine, err := New(context.Background(), Options{Config: cfg, Repository: repository, Notifications: queue, Now: func() time.Time { return now }})
	if err != nil {
		t.Fatal(err)
	}
	return engine, repository, queue, &now
}

func TestSystemThresholdDurationAndRenotifyUseInjectedClock(t *testing.T) {
	rule := config.Rule{ID: "cpu", Type: "metric_threshold", Metric: "system.cpu_percent", Op: "gt", Threshold: threshold(90), For: "5m", Renotify: "1h", Severity: "warning"}
	engine, _, queue, now := newAdditionalEngine(t, rule, nil)
	send := func(value float64) {
		t.Helper()
		if err := engine.Handle(context.Background(), pipeline.Message{CollectorID: "fake", Seq: 1, TS: now.UnixMilli(), Kind: pipeline.KindSystemMetrics, SystemMetrics: &collector.SystemMetrics{TS: now.UnixMilli(), CPUPercent: value}}); err != nil {
			t.Fatal(err)
		}
	}
	send(95)
	for range 59 {
		*now = now.Add(5 * time.Second)
		send(95)
	}
	if len(queue.alerts) != 0 {
		t.Fatal("threshold fired before five minutes")
	}
	*now = now.Add(5 * time.Second)
	send(95)
	first := <-queue.alerts
	if first.TargetID != SystemTargetID || first.State != StateFiring {
		t.Fatalf("threshold: %+v", first)
	}
	*now = now.Add(time.Hour - time.Millisecond)
	if err := engine.Evaluate(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(queue.alerts) != 0 {
		t.Fatal("reminder fired early")
	}
	*now = now.Add(time.Millisecond)
	if err := engine.Evaluate(context.Background()); err != nil {
		t.Fatal(err)
	}
	reminder := <-queue.alerts
	if reminder.ID != first.ID || reminder.NotificationKey == first.NotificationKey {
		t.Fatalf("reminder identity: %+v", reminder)
	}
	send(80)
	resolved := <-queue.alerts
	if resolved.ID != first.ID || resolved.State != StateResolved {
		t.Fatalf("threshold recovery: %+v", resolved)
	}
}

func TestAdditionalMetricKindsServiceAndScriptConditions(t *testing.T) {
	target := config.Target{ID: "fake-target", Match: config.Match{Service: "fake-service"}, Probes: []config.Probe{{Type: "script", Run: "fake", Interval: "5m", Timeout: "30s"}}}
	for _, tc := range []struct {
		metric string
		value  float64
		body   pipeline.Message
	}{
		{"system.memory_percent", 30, pipeline.Message{Kind: pipeline.KindSystemMetrics, SystemMetrics: &collector.SystemMetrics{MemoryTotal: 100, MemoryUsed: 70, MemoryPercent: 70}}},
		{"disk.used_percent", 90, pipeline.Message{Kind: pipeline.KindSystemMetrics, SystemMetrics: &collector.SystemMetrics{Disks: []collector.DiskMetrics{{Path: `C:\`, Total: 100, Used: 95, Free: 5, UsedPercent: 95}}}}},
		{"network.send_bytes_per_sec", 10, pipeline.Message{Kind: pipeline.KindSystemMetrics, SystemMetrics: &collector.SystemMetrics{Networks: []collector.NetworkMetrics{{Name: "fake", SendBytesPerSec: 20}}}}},
		{"process.cpu_percent", 10, pipeline.Message{Kind: pipeline.KindTargetStatus, TargetStatus: &pipeline.TargetStatus{TargetID: target.ID, State: "up", Process: "found", Processes: []pipeline.ProcessRef{{PID: 42, CreatedAt: 1, CPUPercent: 25}}}}},
		{"process.rss_bytes", 100, pipeline.Message{Kind: pipeline.KindTargetStatus, TargetStatus: &pipeline.TargetStatus{TargetID: target.ID, State: "up", Process: "found", Processes: []pipeline.ProcessRef{{PID: 42, CreatedAt: 1, RSS: 200}}}}},
		{"script.age", 24, pipeline.Message{Kind: pipeline.KindProbeResult, ProbeResult: &pipeline.ProbeResult{TargetID: target.ID, Type: "script", OK: true, Metrics: map[string]float64{"age": 26}}}},
	} {
		t.Run(tc.metric, func(t *testing.T) {
			rule := config.Rule{ID: "metric", Type: "metric_threshold", Target: target.ID, Metric: tc.metric, Op: "gt", Threshold: threshold(tc.value)}
			engine, _, queue, _ := newAdditionalEngine(t, rule, &target)
			body := tc.body
			body.CollectorID, body.Seq = "fake", 1
			if err := engine.Handle(context.Background(), body); err != nil {
				t.Fatal(err)
			}
			if len(queue.alerts) != 1 {
				t.Fatalf("metric did not fire: %v", engine.Snapshot())
			}
		})
	}
	for _, ruleType := range []string{"service_stopped", "script_failed"} {
		t.Run(ruleType, func(t *testing.T) {
			engine, _, queue, _ := newAdditionalEngine(t, config.Rule{ID: "health", Type: ruleType, Target: target.ID}, &target)
			status := &pipeline.TargetStatus{TargetID: target.ID, State: "unknown", Service: "stopped", ServiceName: "fake-service"}
			if err := engine.Handle(context.Background(), pipeline.Message{CollectorID: "fake", Seq: 1, Kind: pipeline.KindTargetStatus, TargetStatus: status}); err != nil {
				t.Fatal(err)
			}
			if ruleType == "script_failed" {
				if err := engine.Handle(context.Background(), pipeline.Message{CollectorID: "fake", Seq: 2, Kind: pipeline.KindProbeResult, ProbeResult: &pipeline.ProbeResult{TargetID: target.ID, Type: "script", Error: "fake failure"}}); err != nil {
					t.Fatal(err)
				}
			}
			if len(queue.alerts) != 1 {
				t.Fatalf("condition did not fire: %v", engine.Snapshot())
			}
		})
	}
}

type queryFunc func(context.Context, string) (QueryResult, error)

func (f queryFunc) Query(ctx context.Context, text string) (QueryResult, error) { return f(ctx, text) }

func (f queryFunc) QueryRule(ctx context.Context, text string, _ int64) (QueryResult, error) {
	return f(ctx, text)
}

func TestSQLInstancesIntervalFailureAndRecovery(t *testing.T) {
	engine, _, queue, now := newAdditionalEngine(t, config.Rule{ID: "sql-rule", Type: "sql", Query: "SELECT 'fake' AS key", Interval: "10s"}, nil)
	rows := [][]any{{"one", "first"}, {"two", "second"}}
	calls := 0
	var queryErr error
	engine.queries = queryFunc(func(context.Context, string) (QueryResult, error) {
		calls++
		return QueryResult{Columns: []string{"key", "summary"}, Rows: rows}, queryErr
	})
	if err := engine.Evaluate(context.Background()); err != nil {
		t.Fatal(err)
	}
	first, second := <-queue.alerts, <-queue.alerts
	if first.InstanceKey == second.InstanceKey || first.ID == second.ID {
		t.Fatal("SQL keys merged")
	}
	rows = [][]any{{"two", "second"}}
	*now = now.Add(9 * time.Second)
	if err := engine.Evaluate(context.Background()); err != nil || calls != 1 || len(queue.alerts) != 0 {
		t.Fatalf("SQL interval ignored: %d %v", calls, err)
	}
	*now = now.Add(time.Second)
	if err := engine.Evaluate(context.Background()); err != nil {
		t.Fatal(err)
	}
	resolved := <-queue.alerts
	if resolved.InstanceKey != "one" || resolved.State != StateResolved {
		t.Fatalf("wrong SQL key recovered: %+v", resolved)
	}
	queryErr = errors.New("fake query failure")
	rows = nil
	*now = now.Add(10 * time.Second)
	if err := engine.Evaluate(context.Background()); err == nil || len(queue.alerts) != 0 {
		t.Fatal("query failure recovered an instance")
	}
	queryErr = nil
	*now = now.Add(10 * time.Second)
	if err := engine.Evaluate(context.Background()); err != nil {
		t.Fatal(err)
	}
	if resolved = <-queue.alerts; resolved.InstanceKey != "two" || resolved.ID != second.ID {
		t.Fatalf("SQL recovery identity: %+v", resolved)
	}
	for _, invalid := range []QueryResult{{Columns: []string{"summary"}}, {Columns: []string{"key"}, Rows: [][]any{{""}}}, {Columns: []string{"key"}, Truncated: true}} {
		if _, err := sqlInstances(invalid); err == nil {
			t.Fatalf("invalid SQL result accepted: %+v", invalid)
		}
	}
	merged, err := sqlInstances(QueryResult{Columns: []string{"key", "summary"}, Rows: [][]any{{"same", "first"}, {"same", "second"}}})
	if err != nil || len(merged) != 1 || merged["same"] != "first" {
		t.Fatalf("duplicate SQL rows did not keep first summary: %+v %v", merged, err)
	}
}
