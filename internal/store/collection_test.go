package store

import (
	"context"
	"testing"
	"time"

	"github.com/LFenX/lg-monitor/internal/collector"
	"github.com/LFenX/lg-monitor/internal/pipeline"
)

func collectionMessages(ts int64) []pipeline.Message {
	return []pipeline.Message{
		{CollectorID: "fake-more", Seq: 1, TS: ts, Kind: pipeline.KindSystemMetrics, SystemMetrics: &collector.SystemMetrics{TS: ts, CPUPercent: 75, MemoryTotal: 1000, MemoryUsed: 200, MemoryAvailable: 800, MemoryPercent: 20, UptimeSeconds: 100, Disks: []collector.DiskMetrics{{Path: `C:\`, Total: 100, Used: 50, Free: 50, UsedPercent: 50}}, Networks: []collector.NetworkMetrics{{Name: "fake", BytesSent: 100, BytesReceived: 200, SendBytesPerSec: 25, ReceiveBytesPerSec: 50}}}},
		{CollectorID: "fake-more", Seq: 2, TS: ts, Kind: pipeline.KindServiceStatus, ServiceStatus: &collector.Service{Name: "fake-service", State: "running", StartType: "automatic", PID: 42}},
		{CollectorID: "fake-more", Seq: 3, TS: ts, Kind: pipeline.KindProbeResult, ProbeResult: &pipeline.ProbeResult{TargetID: "backup", Type: "script", TS: ts, OK: true, Message: "备份完成", Metrics: map[string]float64{"backup_age_hours": 2, "size": 1024}}},
	}
}

func TestAdditionalCollectionsPersistImmutableValuesAndPruneHistory(t *testing.T) {
	ctx := context.Background()
	now := time.UnixMilli(2000000000000)
	s := openStore(t, Options{Now: func() time.Time { return now }})
	old := collectionMessages(now.Add(-48 * time.Hour).UnixMilli())
	current := collectionMessages(now.UnixMilli())
	for _, messages := range [][]pipeline.Message{old, current} {
		for _, message := range messages {
			if err := s.Write(ctx, message); err != nil {
				t.Fatal(err)
			}
		}
	}
	current[0].SystemMetrics.Disks[0].Used = 99
	current[0].SystemMetrics.Networks[0].SendBytesPerSec = 999
	current[2].ProbeResult.Metrics["backup_age_hours"] = 999
	if err := s.Flush(ctx); err != nil {
		t.Fatal(err)
	}
	var cpu, used, rate, age float64
	for _, query := range []struct {
		sql  string
		dest *float64
	}{
		{`SELECT cpu FROM system_metrics ORDER BY ts DESC LIMIT 1`, &cpu},
		{`SELECT used FROM disk_metrics ORDER BY ts DESC LIMIT 1`, &used},
		{`SELECT send_bytes_per_sec FROM network_metrics ORDER BY ts DESC LIMIT 1`, &rate},
		{`SELECT value FROM script_metrics WHERE name='backup_age_hours' ORDER BY ts DESC LIMIT 1`, &age},
	} {
		if err := s.db.QueryRow(query.sql).Scan(query.dest); err != nil {
			t.Fatal(err)
		}
	}
	if cpu != 75 || used != 50 || rate != 25 || age != 2 {
		t.Fatalf("stored snapshot mutated: %g %g %g %g", cpu, used, rate, age)
	}
	var probeType, message string
	if err := s.db.QueryRow(`SELECT type,message FROM probe_results LIMIT 1`).Scan(&probeType, &message); err != nil || probeType != "script" || message != "备份完成" {
		t.Fatalf("script details lost: %s %s %v", probeType, message, err)
	}
	old[1].ServiceStatus.Name = "unchanged-old-service"
	if err := s.Write(ctx, old[1]); err != nil {
		t.Fatal(err)
	}
	if err := s.Prune(ctx); err != nil {
		t.Fatal(err)
	}
	for table, want := range map[string]int{"system_metrics": 1, "disk_metrics": 1, "network_metrics": 1, "script_metrics": 2, "probe_results": 1, "service_status": 2} {
		if got := count(t, s, table); got != want {
			t.Fatalf("%s rows after pruning: %d want %d", table, got, want)
		}
	}
}

func TestAdditionalCollectionsUseIngressDedup(t *testing.T) {
	ctx := context.Background()
	s := openStore(t, Options{})
	for _, message := range collectionMessages(123456) {
		if _, needed, err := s.Ingest(ctx, message); err != nil || !needed {
			t.Fatalf("ingest new data: %v %v", needed, err)
		}
		if err := s.CompleteIngress(ctx, message.CollectorID, message.Seq); err != nil {
			t.Fatal(err)
		}
		if _, needed, err := s.Ingest(ctx, message); err != nil || needed {
			t.Fatalf("ingest duplicate: %v %v", needed, err)
		}
	}
	for table, want := range map[string]int{"system_metrics": 1, "disk_metrics": 1, "network_metrics": 1, "service_status": 1, "probe_results": 1, "script_metrics": 2} {
		if got := count(t, s, table); got != want {
			t.Fatalf("%s count after duplicate: %d want %d", table, got, want)
		}
	}
}
