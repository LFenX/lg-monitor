package store

import (
	"context"
	"math"
	"testing"
	"time"

	"github.com/LFenX/lg-monitor/internal/collector"
	"github.com/LFenX/lg-monitor/internal/config"
	"github.com/LFenX/lg-monitor/internal/pipeline"
)

func writeMetric(t *testing.T, s *Store, ts int64, cpu float64) {
	t.Helper()
	if err := s.Write(context.Background(), pipeline.Message{CollectorID: "fake-rollup", Seq: 1, TS: ts, Kind: pipeline.KindSystemMetrics, SystemMetrics: &collector.SystemMetrics{TS: ts, CPUPercent: cpu, MemoryTotal: 1000, MemoryUsed: 400, MemoryAvailable: 600, MemoryPercent: 40, UptimeSeconds: 100,
		Disks:    []collector.DiskMetrics{{Path: `C:\`, Total: 100, Used: 60, Free: 40, UsedPercent: 60}, {Path: `D:\`, Total: 200, Used: 60, Free: 140, UsedPercent: 30}},
		Networks: []collector.NetworkMetrics{{Name: "fake-net", SendBytesPerSec: cpu * 2, ReceiveBytesPerSec: cpu * 3}}}}); err != nil {
		t.Fatal(err)
	}
}

func rollupValue(t *testing.T, s *Store, table, name string, ts int64) (int64, float64, float64) {
	t.Helper()
	var samples int64
	var average, maximum float64
	if err := s.db.QueryRow(`SELECT samples,sum_value/samples,max_value FROM `+table+` WHERE name=? AND ts=?`, name, ts).Scan(&samples, &average, &maximum); err != nil {
		t.Fatal(err)
	}
	return samples, average, maximum
}

func TestThreeDaysAggregateWeightedValuesAndRetention(t *testing.T) {
	ctx := context.Background()
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	now := start.Add(72 * time.Hour)
	s := openStore(t, Options{Now: func() time.Time { return now }})
	for day := 0; day < 3; day++ {
		base := start.Add(time.Duration(day) * 24 * time.Hour)
		for index, value := range []float64{10, 30, 80} {
			ts := base.Add([]time.Duration{10 * time.Second, 20 * time.Second, 70 * time.Second}[index]).UnixMilli()
			writeMetric(t, s, ts, value)
			if err := s.Write(ctx, pipeline.Message{CollectorID: "fake-rollup", Seq: 1, TS: ts, Kind: pipeline.KindProbeResult, ProbeResult: &pipeline.ProbeResult{TargetID: "web", Type: "script", OK: index != 1, LatencyMS: int64(value), Metrics: map[string]float64{"age": value + 3}}}); err != nil {
				t.Fatal(err)
			}
			cpu := []float64{10, 15, 60}[index]
			message := pipeline.Message{CollectorID: "fake-rollup", Seq: 1, TS: ts, Kind: pipeline.KindProcessSample, ProcessSample: &pipeline.ProcessSample{TargetID: "web", PID: 42, CreatedAt: 1, CPUPercent: cpu, RSS: 100}}
			if err := s.Write(ctx, message); err != nil {
				t.Fatal(err)
			}
			if index == 0 {
				message.ProcessSample = &pipeline.ProcessSample{TargetID: "web", PID: 43, CreatedAt: 2, CPUPercent: 20, RSS: 200}
				if err := s.Write(ctx, message); err != nil {
					t.Fatal(err)
				}
			}
		}
	}
	if err := s.Prune(ctx); err != nil {
		t.Fatal(err)
	}
	for day := 0; day < 3; day++ {
		ts := start.Add(time.Duration(day) * 24 * time.Hour).UnixMilli()
		if count, average, maximum := rollupValue(t, s, "metric_minute", "system.cpu_percent", ts); count != 2 || average != 20 || maximum != 30 {
			t.Fatalf("minute mean/max: %d %g %g", count, average, maximum)
		}
		for _, value := range []struct {
			name string
			avg  float64
			max  float64
		}{{"system.cpu_percent", 40, 80}, {"network.send_bytes_per_sec", 80, 160}, {"process.cpu_percent", 35, 60}, {"script.age", 43, 83}} {
			if count, average, maximum := rollupValue(t, s, "metric_hour", value.name, ts); count != 3 || average != value.avg || maximum != value.max {
				t.Fatalf("hour weighted %s: %d %g %g", value.name, count, average, maximum)
			}
		}
		for _, disk := range []struct {
			path string
			used float64
		}{{`C:\`, 60}, {`D:\`, 30}} {
			var average float64
			if err := s.db.QueryRow(`SELECT sum_value/samples FROM metric_hour WHERE name='disk.used_percent' AND resource=? AND ts=?`, disk.path, ts).Scan(&average); err != nil || average != disk.used {
				t.Fatalf("disk resource groups merged: %s %g %v", disk.path, average, err)
			}
		}
		var count, successes int
		var rate, latency float64
		if err := s.db.QueryRow(`SELECT samples,successes,1.0*successes/samples,latency_sum/samples FROM probe_hour WHERE ts=?`, ts).Scan(&count, &successes, &rate, &latency); err != nil || count != 3 || successes != 2 || math.Abs(rate-2.0/3) > 1e-12 || latency != 40 {
			t.Fatalf("probe weighted aggregates: %d %d %g %g %v", count, successes, rate, latency, err)
		}
	}
	for table, want := range map[string]int{"system_metrics": 3, "disk_metrics": 6, "network_metrics": 3, "probe_results": 3, "script_metrics": 3, "process_samples": 4} {
		if got := count(t, s, table); got != want {
			t.Fatalf("raw 24h retention %s: %d want %d", table, got, want)
		}
	}
	if err := s.SetRetention(config.Retention{Raw: "24h", Minute: "48h", Hour: "72h"}); err != nil {
		t.Fatal(err)
	}
	now = now.Add(2 * time.Hour)
	if err := s.Prune(ctx); err != nil {
		t.Fatal(err)
	}
	var minutes, hours int
	if err := s.db.QueryRow(`SELECT count(*) FROM metric_minute WHERE name='system.cpu_percent'`).Scan(&minutes); err != nil {
		t.Fatal(err)
	}
	if err := s.db.QueryRow(`SELECT count(*) FROM metric_hour WHERE name='system.cpu_percent'`).Scan(&hours); err != nil || minutes != 2 || hours != 2 || count(t, s, "system_metrics") != 0 {
		t.Fatalf("custom minute/hour retention: minutes=%d hours=%d raw=%d %v", minutes, hours, count(t, s, "system_metrics"), err)
	}
}

func TestLateSamplesRebuildCompleteBucketsAndStaleSnapshotCannotPublish(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 1, 3, 0, 0, 0, 0, time.UTC)
	start := now.Add(-2 * time.Hour).UnixMilli()
	s := openStore(t, Options{Now: func() time.Time { return now }})
	writeMetric(t, s, start, 10)
	writeMetric(t, s, start+minuteMS, 30)
	if err := s.Prune(ctx); err != nil {
		t.Fatal(err)
	}
	writeMetric(t, s, start, 90)
	if err := s.Flush(ctx); err != nil {
		t.Fatal(err)
	}
	stale, found, err := s.aggregates.Next(ctx, "minute", now.UnixMilli())
	for err == nil && found && stale.source != "system_metrics" {
		if err = s.commitAggregate(ctx, stale); err != nil {
			break
		}
		stale, found, err = s.aggregates.Next(ctx, "minute", now.UnixMilli())
	}
	if err != nil || !found {
		t.Fatalf("missing dirty bucket: %+v %v", stale, err)
	}
	writeMetric(t, s, start+1000, 30)
	if err := s.Flush(ctx); err != nil {
		t.Fatal(err)
	}
	if err := s.commitAggregate(ctx, stale); err != nil {
		t.Fatal(err)
	}
	if _, average, _ := rollupValue(t, s, "metric_minute", "system.cpu_percent", start); average != 10 {
		t.Fatal("stale snapshot was published after a concurrent write")
	}
	if err := s.Prune(ctx); err != nil {
		t.Fatal(err)
	}
	if count, average, maximum := rollupValue(t, s, "metric_hour", "system.cpu_percent", start); count != 3 || average != 50 || maximum != 90 {
		t.Fatalf("late overwrite/insert not rebuilt: %d %g %g", count, average, maximum)
	}
	writeMetric(t, s, start, 90)
	if err := s.Prune(ctx); err != nil {
		t.Fatal(err)
	}
	if count, average, maximum := rollupValue(t, s, "metric_hour", "system.cpu_percent", start); count != 3 || average != 50 || maximum != 90 {
		t.Fatal("rebuilding the same bucket changed its totals")
	}
	now = now.Add(48 * time.Hour)
	if err := s.Prune(ctx); err != nil {
		t.Fatal(err)
	}
	writeMetric(t, s, start, 1)
	if err := s.Prune(ctx); err != nil {
		t.Fatal(err)
	}
	if count, average, maximum := rollupValue(t, s, "metric_hour", "system.cpu_percent", start); count != 3 || average != 50 || maximum != 90 {
		t.Fatal("expired-source fragment replaced a complete historical aggregate")
	}
}
