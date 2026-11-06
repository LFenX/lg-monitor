package rules

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/LFenX/lg-monitor/internal/config"
	"github.com/LFenX/lg-monitor/internal/pipeline"
)

type replaySourceFunc func(context.Context, config.Rule, int64, int64) (ReplayData, error)

func (f replaySourceFunc) LoadReplay(ctx context.Context, rule config.Rule, from, to int64) (ReplayData, error) {
	return f(ctx, rule, from, to)
}
func replayStatus(ts int64, up bool) pipeline.Message {
	state, process := "down", "missing"
	if up {
		state, process = "up", "found"
	}
	return pipeline.Message{CollectorID: "fake", TS: ts, Kind: pipeline.KindTargetStatus, TargetStatus: &pipeline.TargetStatus{TargetID: "web", State: state, Process: process}}
}

func TestReplayUsesHistoricalClockAndNeverMutatesTheSource(t *testing.T) {
	messages := []pipeline.Message{replayStatus(0, true), replayStatus(2000, false), replayStatus(9000, true)}
	source := replaySourceFunc(func(context.Context, config.Rule, int64, int64) (ReplayData, error) {
		return ReplayData{Messages: messages}, nil
	})
	cfg := &config.Config{Targets: []config.Target{{ID: "web", Match: config.Match{Process: "fake.exe"}}}, Rules: []config.Rule{{ID: "down", Type: "process_absent", Target: "web", For: "3s"}}}
	result, err := Replay(context.Background(), ReplayOptions{Config: cfg, Rule: cfg.Rules[0], Since: time.UnixMilli(1000), Until: time.UnixMilli(12000), Source: source})
	if err != nil || len(result.Events) != 2 || result.Events[0].TS != 5000 || result.Events[0].State != StateFiring || result.Events[1].TS != 9000 || result.Events[1].State != StateResolved {
		t.Fatalf("replay chronology: %+v %v", result, err)
	}
	if messages[0].Seq != 0 {
		t.Fatal("replay mutated source records")
	}
	for _, message := range messages {
		if message.TargetStatus.State != "up" && message.TargetStatus.State != "down" {
			t.Fatal("source snapshot changed")
		}
	}
}

func TestReplayMergesSameMillisecondFactsAndReportsNoData(t *testing.T) {
	cfg := &config.Config{Targets: []config.Target{{ID: "web", Match: config.Match{Process: "fake.exe"}}}, Rules: []config.Rule{{ID: "down", Type: "process_absent", Target: "web"}}}
	data := ReplayData{Messages: []pipeline.Message{replayStatus(2000, false), replayStatus(2000, true)}}
	opts := ReplayOptions{Config: cfg, Rule: cfg.Rules[0], Since: time.UnixMilli(1000), Until: time.UnixMilli(4000), Source: replaySourceFunc(func(context.Context, config.Rule, int64, int64) (ReplayData, error) { return data, nil })}
	result, err := Replay(context.Background(), opts)
	if err != nil || len(result.Events) != 0 {
		t.Fatalf("invented transient order: %+v %v", result, err)
	}
	data = ReplayData{}
	result, err = Replay(context.Background(), opts)
	if err != nil || !result.Complete || len(result.Notes) == 0 || len(result.Events) != 0 {
		t.Fatalf("empty data called healthy: %+v %v", result, err)
	}
	data = ReplayData{Messages: []pipeline.Message{replayStatus(5000, false)}}
	if _, err = Replay(context.Background(), opts); err == nil || !strings.Contains(err.Error(), "未来") {
		t.Fatalf("future source data accepted: %v", err)
	}
}

func TestReplayWindowSeedDoesNotOverwriteNewerProcessFacts(t *testing.T) {
	limit := 80.0
	cfg := &config.Config{Targets: []config.Target{{ID: "web", Match: config.Match{Process: "fake.exe"}}}, Rules: []config.Rule{{ID: "cpu", Type: "metric_threshold", Target: "web", Metric: "process.cpu_percent", Op: "gt", Threshold: &limit}}}
	status := replayStatus(900, true)
	status.TargetStatus.Processes = []pipeline.ProcessRef{{PID: 42, CreatedAt: 1, CPUPercent: 10}}
	older := pipeline.Message{CollectorID: "fake", TS: 500, Kind: pipeline.KindProcessSample, ProcessSample: &pipeline.ProcessSample{TargetID: "web", PID: 42, CreatedAt: 1, CPUPercent: 95}}
	source := replaySourceFunc(func(context.Context, config.Rule, int64, int64) (ReplayData, error) {
		return ReplayData{Messages: []pipeline.Message{status, older}}, nil
	})
	result, err := Replay(context.Background(), ReplayOptions{Config: cfg, Rule: cfg.Rules[0], Since: time.UnixMilli(1000), Until: time.UnixMilli(2000), Source: source})
	if err != nil || len(result.Events) != 0 {
		t.Fatalf("older seed overwrote newer CPU: %+v %v", result, err)
	}
}
