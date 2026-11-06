package store

import (
	"context"
	"database/sql"
	"errors"
	"math"
	"testing"
	"time"

	"github.com/LFenX/lg-monitor/internal/pipeline"
)

func TestAPIHistoryUsesRawAndWeightedRollupsWithoutMergingResources(t *testing.T) {
	ctx := context.Background()
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	now := start.Add(2 * time.Hour)
	s := openStore(t, Options{Now: func() time.Time { return now }, DisableMaintenance: true})
	for i, value := range []float64{10, 30, 80} {
		ts := start.Add([]time.Duration{10 * time.Second, 20 * time.Second, 70 * time.Second}[i]).UnixMilli()
		writeMetric(t, s, ts, value)
		for _, sample := range []pipeline.ProcessSample{{TargetID: "web", PID: 1, CreatedAt: 1, CPUPercent: value, RSS: 100}, {TargetID: "web", PID: 2, CreatedAt: 1, CPUPercent: 5, RSS: 200}} {
			if err := s.Write(ctx, pipeline.Message{CollectorID: "fake-rollup", Seq: uint64(i + 1), TS: ts, Kind: pipeline.KindProcessSample, ProcessSample: &sample}); err != nil {
				t.Fatal(err)
			}
		}
		if err := s.Write(ctx, pipeline.Message{CollectorID: "fake-rollup", Seq: uint64(i + 1), TS: ts, Kind: pipeline.KindProbeResult, ProbeResult: &pipeline.ProbeResult{TargetID: "web", Probe: 0, Type: "script", OK: i != 1, LatencyMS: int64(value), Error: "fake diagnostic", Message: "fake message", Metrics: map[string]float64{"age": value + 3}}}); err != nil {
			t.Fatal(err)
		}
		state := "up"
		if i == 1 {
			state = "down"
		}
		if err := s.Write(ctx, pipeline.Message{CollectorID: "fake-rollup", Seq: uint64(i + 1), TS: ts, Kind: pipeline.KindTargetStatus, TargetStatus: &pipeline.TargetStatus{TargetID: "web", State: state}}); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.Flush(ctx); err != nil {
		t.Fatal(err)
	}
	v, err := OpenViewWithClock(ctx, s.maintenanceView.path, func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	defer v.Close()
	period := HistoryRange{From: start.UnixMilli(), To: now.UnixMilli(), Step: "raw"}
	metrics, err := v.MetricHistory(ctx, "fake-rollup", "system.cpu_percent", period)
	if err != nil || len(metrics.Metrics) != 3 || metrics.Metrics[1].Average != 30 || metrics.Metrics[1].Samples != 1 {
		t.Fatalf("raw metrics: %+v %v", metrics, err)
	}
	history, err := v.TargetHistory(ctx, "fake-rollup", "web", period)
	if err != nil || len(history.States) != 3 || len(history.Probes) != 3 || len(history.Processes) != 6 {
		t.Fatalf("raw history: %+v %v", history, err)
	}
	if history.Processes[0].Name != "process.cpu_percent" || history.Processes[0].Average != 15 || history.Processes[1].Average != 300 || history.Probes[1].SuccessRate != 0 || history.Probes[1].Error != "fake diagnostic" {
		t.Fatalf("target grouping or probe diagnostic: %+v", history)
	}
	if err := s.Prune(ctx); err != nil {
		t.Fatal(err)
	}
	for _, step := range []string{"1m", "1h"} {
		period.Step = step
		metrics, err = v.MetricHistory(ctx, "fake-rollup", "system.cpu_percent", period)
		if err != nil {
			t.Fatal(err)
		}
		if step == "1m" {
			if len(metrics.Metrics) != 2 || metrics.Metrics[0].Samples != 2 || metrics.Metrics[0].Sum != 40 || metrics.Metrics[0].Average != 20 {
				t.Fatalf("minute metrics: %+v", metrics)
			}
		} else if len(metrics.Metrics) != 1 || metrics.Metrics[0].Samples != 3 || metrics.Metrics[0].Average != 40 || metrics.Metrics[0].Max != 80 {
			t.Fatalf("hour mean was not weighted: %+v", metrics)
		}
		disks, err := v.MetricHistory(ctx, "fake-rollup", "disk.used_percent", period)
		if err != nil {
			t.Fatal(err)
		}
		want := 2
		if step == "1m" {
			want = 4
		}
		if len(disks.Metrics) != want || disks.Metrics[0].Resource == disks.Metrics[1].Resource {
			t.Fatalf("disk resources merged: %+v", disks)
		}
		history, err = v.TargetHistory(ctx, "fake-rollup", "web", period)
		if err != nil {
			t.Fatal(err)
		}
		if step == "1h" {
			if len(history.Probes) != 1 || history.Probes[0].Samples != 3 || history.Probes[0].Successes != 2 || math.Abs(history.Probes[0].SuccessRate-2.0/3) > 1e-12 || history.Probes[0].AverageLatencyMS != 40 {
				t.Fatalf("probe weighted history: %+v", history)
			}
			if len(history.Processes) != 2 || history.Processes[0].Average != 45 || history.Processes[1].Average != 300 {
				t.Fatalf("process weighted history: %+v", history)
			}
		}
	}
	period.Step = "raw"
	for _, filter := range []struct{ collector, name string }{{"other", ""}, {"fake-rollup", "' OR 1=1 --"}} {
		result, err := v.MetricHistory(ctx, filter.collector, filter.name, period)
		if err != nil || len(result.Metrics) != 0 {
			t.Fatalf("filter escaped scope: %+v %v", result, err)
		}
	}
	period.To = start.Add(20 * time.Second).UnixMilli()
	result, err := v.MetricHistory(ctx, "fake-rollup", "script.age", period)
	if err != nil || len(result.Metrics) != 2 || result.Metrics[1].Average != 33 {
		t.Fatalf("inclusive time boundary or script mapping: %+v %v", result, err)
	}
}

func TestAPIHistoryRejectsUnboundedResultsAndInvalidRanges(t *testing.T) {
	ctx := context.Background()
	s := openStore(t, Options{DisableMaintenance: true})
	_, err := s.db.ExecContext(ctx, `WITH RECURSIVE ticks(n) AS(SELECT 1 UNION ALL SELECT n+1 FROM ticks WHERE n<10001)
INSERT INTO system_metrics SELECT 'fake',n,1,100,10,90,10,1,'{}' FROM ticks`)
	if err != nil {
		t.Fatal(err)
	}
	v, err := OpenView(ctx, s.maintenanceView.path)
	if err != nil {
		t.Fatal(err)
	}
	defer v.Close()
	period := HistoryRange{From: 0, To: 10002, Step: "raw"}
	if _, err := v.MetricHistory(ctx, "fake", "system.cpu_percent", period); !errors.Is(err, ErrHistoryTooLarge) {
		t.Fatalf("history was truncated silently: %v", err)
	}
	for _, period := range []HistoryRange{{From: -1, To: 1, Step: "raw"}, {From: 2, To: 1, Step: "raw"}, {From: 0, To: 1, Step: "2m"}} {
		if _, err := v.MetricHistory(ctx, "fake", "", period); err == nil {
			t.Fatal("invalid history range accepted")
		}
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := v.MetricHistory(cancelled, "fake", "system.cpu_percent", period); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled read continued: %v", err)
	}
}

func TestAPIDeleteSilenceRemovesOnlySpecifiedEntry(t *testing.T) {
	ctx := context.Background()
	s := openStore(t, Options{DisableMaintenance: true})
	one, err := s.AddSilence(ctx, "fake-one", "", time.Hour, "fake reason")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.AddSilence(ctx, "fake-two", "", time.Hour, "fake reason"); err != nil {
		t.Fatal(err)
	}
	if err := s.RemoveSilence(ctx, one.ID); err != nil {
		t.Fatal(err)
	}
	var remaining string
	if err := s.db.QueryRowContext(ctx, `SELECT rule_id FROM silences`).Scan(&remaining); err != nil || remaining != "fake-two" {
		t.Fatal("silence deletion changed another entry")
	}
	if err := s.RemoveSilence(ctx, one.ID); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("missing silence not reported: %v", err)
	}
}
