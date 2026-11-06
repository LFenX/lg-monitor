package store

import (
	"context"
	"testing"
	"time"

	"github.com/LFenX/lg-monitor/internal/config"
	"github.com/LFenX/lg-monitor/internal/pipeline"
	"github.com/LFenX/lg-monitor/internal/rules"
)

func TestHistoryReplayReadsPrefixAndLeavesDatabaseUnchanged(t *testing.T) {
	s := openStore(t, Options{})
	for _, row := range []struct {
		ts int64
		up bool
	}{{1000, true}, {3000, false}, {9000, true}, {20000, false}} {
		state, process := "down", "missing"
		if row.up {
			state, process = "up", "found"
		}
		if err := s.Write(context.Background(), pipeline.Message{CollectorID: "fake", Seq: 1, TS: row.ts, Kind: pipeline.KindTargetStatus, TargetStatus: &pipeline.TargetStatus{TargetID: "web", State: state, Process: process}}); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.Flush(context.Background()); err != nil {
		t.Fatal(err)
	}
	view, err := OpenView(context.Background(), sPath(t, s))
	if err != nil {
		t.Fatal(err)
	}
	defer view.Close()
	rule := config.Rule{ID: "down", Type: "process_absent", Target: "web", For: "2s"}
	data, err := view.LoadReplay(context.Background(), rule, 2000, 12000)
	if err != nil || len(data.Messages) != 3 {
		t.Fatalf("historical prefix/future boundary: %+v %v", data, err)
	}
	cfg := &config.Config{Targets: []config.Target{{ID: "web", Match: config.Match{Process: "fake.exe"}}}, Rules: []config.Rule{rule}}
	result, err := rules.Replay(context.Background(), rules.ReplayOptions{Config: cfg, Rule: cfg.Rules[0], Since: time.UnixMilli(2000), Until: time.UnixMilli(12000), Source: view})
	if err != nil || len(result.Events) != 2 || result.Events[0].TS != 5000 || result.Events[1].TS != 9000 {
		t.Fatalf("stored history replay: %+v %v", result, err)
	}
	if count(t, s, "alerts") != 0 || count(t, s, "notifications") != 0 || count(t, s, "target_status") != 4 {
		t.Fatal("replay wrote to the source database")
	}
}

func TestScriptReplayRestoresMetricValues(t *testing.T) {
	s := openStore(t, Options{})
	for _, row := range []struct {
		ts  int64
		age float64
	}{{1000, 2}, {2000, 26}, {6000, 1}} {
		message := pipeline.Message{CollectorID: "fake", Seq: 1, TS: row.ts, Kind: pipeline.KindProbeResult, ProbeResult: &pipeline.ProbeResult{TargetID: "backup", Type: "script", OK: true, Metrics: map[string]float64{"age": row.age}}}
		if err := s.Write(context.Background(), message); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.Flush(context.Background()); err != nil {
		t.Fatal(err)
	}
	view, err := OpenView(context.Background(), sPath(t, s))
	if err != nil {
		t.Fatal(err)
	}
	defer view.Close()
	limit := 24.0
	cfg := &config.Config{Targets: []config.Target{{ID: "backup", Match: config.Match{Service: "fake"}, Probes: []config.Probe{{Type: "script", Script: "fake.ps1", Interval: "1s", Timeout: "1s"}}}}, Rules: []config.Rule{{ID: "age", Type: "metric_threshold", Target: "backup", Metric: "script.age", Op: "gt", Threshold: &limit, For: "1s"}}}
	result, err := rules.Replay(context.Background(), rules.ReplayOptions{Config: cfg, Rule: cfg.Rules[0], Since: time.UnixMilli(1000), Until: time.UnixMilli(8000), Source: view})
	if err != nil || len(result.Events) != 2 || result.Events[0].TS != 3000 || result.Events[1].TS != 6000 {
		t.Fatalf("script history values: %+v %v", result, err)
	}
}
