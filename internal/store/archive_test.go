// Copyright (c) 2026 LFen (https://lfen.ink) — lg-monitor

package store

import (
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/LFenX/lg-monitor/internal/collector"
	"github.com/LFenX/lg-monitor/internal/config"
	"github.com/LFenX/lg-monitor/internal/pipeline"
)

func dirTotalBytes(t *testing.T, dir string) int64 {
	t.Helper()
	var total int64
	_ = filepath.WalkDir(dir, func(_ string, d fs.DirEntry, err error) error {
		if err != nil || d == nil || d.IsDir() {
			return nil
		}
		if info, statErr := d.Info(); statErr == nil {
			total += info.Size()
		}
		return nil
	})
	return total
}

func TestStorageCleanupArchiveNetSavingsAndExactRoundTrip(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "lgmon.db")
	start := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
	// Current time is 20 days after start: 20d > 14d (minute retention) and > 24h (raw retention).
	now := start.Add(20 * 24 * time.Hour)
	nowFn := func() time.Time { return now }

	s, err := Open(ctx, Options{
		Path:               dbPath,
		Now:                nowFn,
		DisableMaintenance: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	// Seed realistic multi-day expired data across archivable tables, plus ephemeral tables
	// (network_metrics with idle 0/0 interfaces, inventory_snapshots, system_metrics).
	targetDay := start.Add(2 * 24 * time.Hour) // 18 days ago -> both raw and minute expire
	type expectedTargetRow struct {
		TS    int64
		State string
	}
	expectedTargets := make([]expectedTargetRow, 0, 120)

	for i := 0; i < 120; i++ {
		ts := targetDay.Add(time.Duration(i) * time.Minute).UnixMilli()
		state := "up"
		if i%15 == 0 {
			state = "down"
		}
		expectedTargets = append(expectedTargets, expectedTargetRow{TS: ts, State: state})
		if err := s.Write(ctx, pipeline.Message{
			CollectorID: "c1", Seq: uint64(i + 1), TS: ts, Kind: pipeline.KindTargetStatus,
			TargetStatus: &pipeline.TargetStatus{TargetID: "web", State: state, Reason: "status-" + fmt.Sprint(i)},
		}); err != nil {
			t.Fatal(err)
		}
		if err := s.Write(ctx, pipeline.Message{
			CollectorID: "c1", Seq: uint64(1000 + i), TS: ts, Kind: pipeline.KindProbeResult,
			ProbeResult: &pipeline.ProbeResult{TargetID: "web", Probe: 0, Type: "http", OK: state == "up", LatencyMS: int64(12 + i%5)},
		}); err != nil {
			t.Fatal(err)
		}
		// Include 1 active + 10 permanently idle 0/0 adapters.
		nets := []collector.NetworkMetrics{{Name: "WLAN", BytesSent: uint64(1000 + i*100), BytesReceived: uint64(2000 + i*150), SendBytesPerSec: 50, ReceiveBytesPerSec: 80}}
		for n := 0; n < 10; n++ {
			nets = append(nets, collector.NetworkMetrics{Name: fmt.Sprintf("IdleAdapter-%d", n), BytesSent: 0, BytesReceived: 0})
		}
		if err := s.Write(ctx, pipeline.Message{
			CollectorID: "c1", Seq: uint64(2000 + i), TS: ts, Kind: pipeline.KindSystemMetrics,
			SystemMetrics: &collector.SystemMetrics{
				TS: ts, CPUPercent: 15.5, MemoryTotal: 16 << 30, MemoryUsed: 8 << 30, MemoryAvailable: 8 << 30, MemoryPercent: 50, UptimeSeconds: 3600,
				Disks:    []collector.DiskMetrics{{Path: `C:\`, Total: 100 << 30, Used: 40 << 30, Free: 60 << 30, UsedPercent: 40}},
				Networks: nets,
			},
		}); err != nil {
			t.Fatal(err)
		}
		// Add bulk service_status + inventory_snapshots + task_status + eventlog_events.
		if err := s.Write(ctx, pipeline.Message{
			CollectorID: "c1", Seq: uint64(3000 + i), TS: ts, Kind: pipeline.KindServiceStatus,
			ServiceStatus: &collector.Service{Name: "svc-demo", State: "running", StartType: "automatic", PID: 1234},
		}); err != nil {
			t.Fatal(err)
		}
	}
	// Add 1 eventlog event with match and 1 inventory change + 1 collector gap + 1 task_status.
	evTS := targetDay.Add(30 * time.Minute).UnixMilli()
	if err := s.Write(ctx, pipeline.Message{
		CollectorID: "c1", Seq: 5000, TS: evTS, Kind: pipeline.KindEventLog,
		EventLog: &collector.EventLogRecord{
			Channel: "Application", RecordID: 77, TS: evTS, Provider: "DemoApp", EventID: 1000, Level: 2,
			Subject: "demo.exe", Message: "crash happened",
			Targets: []collector.EventLogTarget{{ID: "web", Via: "both"}},
		},
	}); err != nil {
		t.Fatal(err)
	}
	if err := s.Write(ctx, pipeline.Message{
		CollectorID: "c1", Seq: 5001, TS: evTS, Kind: pipeline.KindDataGap,
		DataGap: &pipeline.DataGap{FromSeq: 5001, ToSeq: 5001, Dropped: 1, Reason: "gap-test"},
	}); err != nil {
		t.Fatal(err)
	}
	// Keep 1 recent point inside raw window so service_status keeps its latest row.
	recentTS := now.Add(-2 * time.Hour).UnixMilli()
	if err := s.Write(ctx, pipeline.Message{
		CollectorID: "c1", Seq: 6000, TS: recentTS, Kind: pipeline.KindServiceStatus,
		ServiceStatus: &collector.Service{Name: "svc-demo", State: "running", StartType: "automatic", PID: 1234},
	}); err != nil {
		t.Fatal(err)
	}
	if err := s.Flush(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.ExecContext(ctx, `INSERT INTO task_status(collector_id,target_id,ts,state,detail) VALUES('c1','nightly',?,'up','{"reason":"ok"}')`, evTS); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.ExecContext(ctx, `INSERT INTO inventory_changes(collector_id,kind,scope,seq,ts,id,name,change,old_fingerprint,new_fingerprint) VALUES('c1','startup','run',90,?,'demo','Demo','added','','abc')`, evTS); err != nil {
		t.Fatal(err)
	}
	// Checkpoint before measuring "before" total disk usage.
	if err := s.Checkpoint(ctx, "TRUNCATE"); err != nil {
		t.Fatal(err)
	}
	beforeTotal := dirTotalBytes(t, dir)

	// Run full Prune + Compact (aggregates, archives expired raw+minute, deletes online rows, VACUUMs, truncates WAL).
	if err := s.Compact(ctx); err != nil {
		t.Fatalf("Compact: %v", err)
	}
	afterTotal := dirTotalBytes(t, dir)
	t.Logf("Disk usage before=%d bytes, after=%d bytes (including archives), net freed=%d bytes", beforeTotal, afterTotal, beforeTotal-afterTotal)
	if afterTotal >= beforeTotal {
		t.Fatalf("expected net disk reduction including archives, before=%d after=%d", beforeTotal, afterTotal)
	}

	// Verify online expired rows were cleaned (only recent service_status remains).
	if got := count(t, s, "target_status"); got != 0 {
		t.Fatalf("expired target_status not deleted: %d", got)
	}
	if got := count(t, s, "probe_results"); got != 0 {
		t.Fatalf("expired probe_results not deleted: %d", got)
	}
	if got := count(t, s, "metric_minute"); got != 0 {
		t.Fatalf("expired metric_minute not deleted: %d", got)
	}
	if got := count(t, s, "service_status"); got != 1 {
		t.Fatalf("expected only latest service_status retained, got %d", got)
	}

	// Verify archive summary & chunks, and confirm idle 0/0 adapters were excluded from metric_minute archive.
	view, err := OpenViewWithClock(ctx, dbPath, nowFn)
	if err != nil {
		t.Fatal(err)
	}
	defer view.Close()
	summary, chunks, err := view.ListArchives(ctx, 0, 0, "")
	if err != nil || summary.ChunkCount == 0 || len(chunks) == 0 {
		t.Fatalf("expected verified archive chunks: summary=%+v err=%v", summary, err)
	}
	for _, c := range chunks {
		if err := view.VerifyArchiveChunk(ctx, c.ID); err != nil {
			t.Fatalf("chunk %s failed verification: %v", c.ID, err)
		}
	}

	// Extract a specific 30-minute archived window [10..40) and verify exact count and contents.
	winFrom := targetDay.Add(10 * time.Minute).UnixMilli()
	winTo := targetDay.Add(39 * time.Minute).UnixMilli()
	extracted, err := view.ExtractArchive(ctx, ArchiveExtractQuery{
		From: winFrom, To: winTo, Tables: []string{"target_status"}, CollectorID: "c1", TargetID: "web", Limit: 500,
	})
	if err != nil {
		t.Fatalf("ExtractArchive target_status: %v", err)
	}
	if extracted.Total != 30 || len(extracted.Records) != 30 {
		t.Fatalf("expected 30 extracted target_status rows, got total=%d len=%d", extracted.Total, len(extracted.Records))
	}
	for i, rec := range extracted.Records {
		want := expectedTargets[10+i]
		if rec.TS != want.TS || rec.Row["state"] != want.State {
			t.Fatalf("row %d mismatch: got ts=%d state=%v, want ts=%d state=%s", i, rec.TS, rec.Row["state"], want.TS, want.State)
		}
		detailStr, _ := rec.Row["detail"].(string)
		if !strings.Contains(detailStr, fmt.Sprintf("status-%d", 10+i)) {
			t.Fatalf("row %d detail mismatch: %s", i, detailStr)
		}
	}

	// Extract eventlog_events and verify nested matches preserved.
	evExtracted, err := view.ExtractArchive(ctx, ArchiveExtractQuery{
		From: evTS, To: evTS, Tables: []string{"eventlog_events"},
	})
	if err != nil || evExtracted.Total != 1 {
		t.Fatalf("expected 1 extracted eventlog row, got %+v err=%v", evExtracted, err)
	}
	rawJSON, _ := json.Marshal(evExtracted.Records[0].Row)
	if !strings.Contains(string(rawJSON), `"subject"`) || !strings.Contains(string(rawJSON), `"subscription"`) {
		t.Fatalf("eventlog matches not preserved: %s", string(rawJSON))
	}

	// Ensure idle 0/0 network adapters were not aggregated/archived into metric_minute.
	netExtracted, err := view.ExtractArchive(ctx, ArchiveExtractQuery{
		From: targetDay.UnixMilli(), To: targetDay.Add(2 * time.Hour).UnixMilli(), Tables: []string{"metric_minute"}, Limit: 2000,
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, rec := range netExtracted.Records {
		if res, _ := rec.Row["resource"].(string); strings.HasPrefix(res, "IdleAdapter-") {
			t.Fatalf("idle 0/0 network adapter leaked into metric_minute archive: %+v", rec)
		}
	}
}

func TestArchiveVerificationFailurePreservesOnlineRows(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "lgmon.db")
	// Point archiveDir to an existing regular file so archive writing must fail.
	badArchiveDir := filepath.Join(dir, "not-a-dir")
	if err := os.WriteFile(badArchiveDir, []byte("file"), 0o600); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 11, 20, 0, 0, 0, 0, time.UTC)
	s, err := Open(ctx, Options{
		Path:               dbPath,
		ArchiveDir:         badArchiveDir,
		Now:                func() time.Time { return now },
		DisableMaintenance: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	oldTS := now.Add(-48 * time.Hour).UnixMilli()
	if err := s.Write(ctx, pipeline.Message{
		CollectorID: "c1", Seq: 1, TS: oldTS, Kind: pipeline.KindTargetStatus,
		TargetStatus: &pipeline.TargetStatus{TargetID: "web", State: "down", Reason: "must-keep"},
	}); err != nil {
		t.Fatal(err)
	}
	if err := s.Flush(ctx); err != nil {
		t.Fatal(err)
	}
	if err := s.Prune(ctx); err == nil {
		t.Fatal("expected archive error when archiveDir is a regular file")
	}
	if got := count(t, s, "target_status"); got != 1 {
		t.Fatalf("online row was deleted despite archive failure: %d", got)
	}
}

func TestArchiveTTLExpiresOldChunksAndPaginatesExtraction(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "lgmon.db")
	now := time.Date(2026, 11, 28, 0, 0, 0, 0, time.UTC)
	s, err := Open(ctx, Options{
		Path:               dbPath,
		Now:                func() time.Time { return now },
		Retention:          config.Retention{Raw: "24h", Minute: "48h", Hour: "168h", Archive: "72h"},
		DisableMaintenance: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	// Write one very old batch (10 days ago -> > 72h archive TTL) and one recent expired batch (36h ago -> < 72h archive TTL).
	tooOldTS := now.Add(-10 * 24 * time.Hour).UnixMilli()
	keepArchiveTS := now.Add(-36 * time.Hour).UnixMilli()
	for i := 0; i < 5; i++ {
		if err := s.Write(ctx, pipeline.Message{
			CollectorID: "c1", Seq: uint64(i + 1), TS: tooOldTS + int64(i*1000), Kind: pipeline.KindTargetStatus,
			TargetStatus: &pipeline.TargetStatus{TargetID: "old-svc", State: "up", Reason: "old"},
		}); err != nil {
			t.Fatal(err)
		}
		if err := s.Write(ctx, pipeline.Message{
			CollectorID: "c1", Seq: uint64(100 + i), TS: keepArchiveTS + int64(i*1000), Kind: pipeline.KindTargetStatus,
			TargetStatus: &pipeline.TargetStatus{TargetID: "mid-svc", State: "down", Reason: "mid"},
		}); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.Flush(ctx); err != nil {
		t.Fatal(err)
	}
	if err := s.Prune(ctx); err != nil {
		t.Fatal(err)
	}

	view, err := OpenViewWithClock(ctx, dbPath, func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	defer view.Close()
	summary, chunks, err := view.ListArchives(ctx, 0, 0, "")
	if err != nil {
		t.Fatal(err)
	}
	if summary.ChunkCount != 1 || len(chunks) != 1 || summary.TotalRecords != 5 {
		t.Fatalf("expected only 1 non-expired archive chunk with 5 records, got summary=%+v chunks=%+v", summary, chunks)
	}

	page1, err := view.ExtractArchive(ctx, ArchiveExtractQuery{Limit: 2, Offset: 0})
	if err != nil || page1.Total != 5 || page1.Returned != 2 || !page1.Truncated {
		t.Fatalf("unexpected page1: %+v err=%v", page1, err)
	}
	page2, err := view.ExtractArchive(ctx, ArchiveExtractQuery{Limit: 2, Offset: 4})
	if err != nil || page2.Total != 5 || page2.Returned != 1 || page2.Truncated {
		t.Fatalf("unexpected page2: %+v err=%v", page2, err)
	}
}
