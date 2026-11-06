package store

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/LFenX/lg-monitor/internal/config"
	"github.com/LFenX/lg-monitor/internal/pipeline"
)

func TestExpiredSamplesAreGroupedByCollectorAndTableWithExactRange(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 1, 4, 0, 30, 0, 0, time.UTC)
	s := openStore(t, Options{Now: func() time.Time { return now }})
	if err := s.Prune(ctx); err != nil {
		t.Fatal(err)
	}
	cutoff := now.Add(-24 * time.Hour).Truncate(time.Hour).UnixMilli()
	for _, ts := range []int64{cutoff - 2000, cutoff - 1000} {
		if err := s.Write(ctx, sampleMessage(1, ts)); err != nil {
			t.Fatal(err)
		}
	}
	probe := pipeline.Message{CollectorID: "fake-late", Seq: 1, TS: cutoff - 3000, Kind: pipeline.KindProbeResult, ProbeResult: &pipeline.ProbeResult{TargetID: "web", Type: "script", OK: true, Metrics: map[string]float64{"age": 3, "size": 4}}}
	if err := s.Write(ctx, probe); err != nil {
		t.Fatal(err)
	}
	writeMetric(t, s, cutoff-5000, 25)
	if err := s.Flush(ctx); err != nil {
		t.Fatal(err)
	}
	for _, table := range []string{"process_samples", "probe_results", "script_metrics", "system_metrics", "disk_metrics", "network_metrics"} {
		if count(t, s, table) != 0 {
			t.Fatalf("expired samples were stored in %s", table)
		}
	}
	for _, want := range []struct {
		collector, table   string
		count, first, last int64
	}{{"collector-test", "process_samples", 2, cutoff - 2000, cutoff - 1000}, {"fake-late", "probe_results", 1, cutoff - 3000, cutoff - 3000}, {"fake-late", "script_metrics", 2, cutoff - 3000, cutoff - 3000}, {"fake-rollup", "system_metrics", 1, cutoff - 5000, cutoff - 5000}, {"fake-rollup", "disk_metrics", 2, cutoff - 5000, cutoff - 5000}, {"fake-rollup", "network_metrics", 1, cutoff - 5000, cutoff - 5000}} {
		var records int
		var dropped, first, last, ts, seq int64
		if err := s.db.QueryRow(`SELECT count(*),sum(dropped),min(first_ts),max(last_ts),min(ts),min(seq) FROM collector_events WHERE kind='late_dropped' AND collector_id=? AND source_table=?`, want.collector, want.table).Scan(&records, &dropped, &first, &last, &ts, &seq); err != nil || records != 1 || dropped != want.count || first != want.first || last != want.last || ts != now.UnixMilli() || seq >= 0 {
			t.Fatalf("late audit %s/%s: records=%d dropped=%d range=%d..%d ts=%d seq=%d %v", want.collector, want.table, records, dropped, first, last, ts, seq, err)
		}
	}
}

func TestExpiredIngressAcknowledgesOnceAndWatermarksNeverMoveBack(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 1, 4, 0, 30, 0, 0, time.UTC)
	path := filepath.Join(t.TempDir(), "late.db")
	s := openStore(t, Options{Path: path, Now: func() time.Time { return now }})
	if err := s.Prune(ctx); err != nil {
		t.Fatal(err)
	}
	cutoff := now.Add(-24 * time.Hour).Truncate(time.Hour).UnixMilli()
	message := ingressMessage(1)
	message.TS = cutoff - 1
	for range 2 {
		if _, needed, err := s.Ingest(ctx, message); err != nil || needed {
			t.Fatalf("expired ingress requested rule evaluation: %t %v", needed, err)
		}
	}
	if ack, err := s.LastCommitted(ctx, message.CollectorID); err != nil || ack != 1 || count(t, s, "collector_inbox") != 0 || count(t, s, "probe_results") != 0 || count(t, s, "collector_events") != 1 {
		t.Fatalf("drop acknowledgement/dedup: %d %v", ack, err)
	}
	if err := s.SetRetention(config.Retention{Raw: "2d"}); err != nil {
		t.Fatal(err)
	}
	if err := s.Prune(ctx); err != nil {
		t.Fatal(err)
	}
	var watermark int64
	if err := s.db.QueryRow(`SELECT raw_complete_from FROM raw_watermarks WHERE table_name='probe_results'`).Scan(&watermark); err != nil || watermark != cutoff {
		t.Fatalf("increased retention moved completeness back: %d %v", watermark, err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s = openStore(t, Options{Path: path, Now: func() time.Time { return now }, Retention: config.Retention{Raw: "2d"}})
	message.Seq = 2
	if _, needed, err := s.Ingest(ctx, message); err != nil || needed {
		t.Fatalf("reopen forgot drop watermark: %t %v", needed, err)
	}
	message.Seq, message.TS = 3, cutoff
	if _, needed, err := s.Ingest(ctx, message); err != nil || !needed {
		t.Fatalf("complete boundary sample rejected: %t %v", needed, err)
	}
	if err := s.CompleteIngress(ctx, message.CollectorID, message.Seq); err != nil {
		t.Fatal(err)
	}
	if count(t, s, "probe_results") != 1 || count(t, s, "collector_events") != 2 {
		t.Fatal("boundary or persisted late audit count incorrect")
	}
}
