package store

import (
	"context"
	"path/filepath"
	"testing"
	"time"
)

func TestClockOffsetAuditsThresholdCrossingsAndKeepsRawTime(t *testing.T) {
	databasePath := filepath.Join(t.TempDir(), "clock.db")
	s := openStore(t, Options{Path: databasePath})
	ctx := context.Background()
	if err := s.RegisterCollector(ctx, CollectorRecord{ID: "collector-test", Hostname: "fake-host", FirstSeen: 1, LastSeen: 1}); err != nil {
		t.Fatal(err)
	}
	for _, offset := range []int64{10000, -10000, 40000, 45000, 40000, 10000, 0} {
		if err := s.SetClockOffset(ctx, "collector-test", offset); err != nil {
			t.Fatal(err)
		}
	}
	var bad, restored int
	if err := s.db.QueryRow("SELECT count(*) FROM collector_events WHERE kind='clock_skew'").Scan(&bad); err != nil {
		t.Fatal(err)
	}
	if err := s.db.QueryRow("SELECT count(*) FROM collector_events WHERE kind='clock_synced'").Scan(&restored); err != nil {
		t.Fatal(err)
	}
	if bad != 1 || restored != 1 {
		t.Fatalf("clock event counts=%d/%d", bad, restored)
	}
	if err := s.SetClockOffset(ctx, "collector-test", -300000); err != nil {
		t.Fatal(err)
	}
	raw := time.UnixMilli(2000000000000).Add(5 * time.Minute).UnixMilli()
	if err := s.Write(ctx, sampleMessage(1, raw)); err != nil {
		t.Fatal(err)
	}
	if err := s.Flush(ctx); err != nil {
		t.Fatal(err)
	}
	var stored int64
	if err := s.db.QueryRow("SELECT ts FROM process_samples").Scan(&stored); err != nil || stored != raw {
		t.Fatalf("raw time=%d want=%d %v", stored, raw, err)
	}
	view, err := OpenView(ctx, databasePath)
	if err != nil {
		t.Fatal(err)
	}
	defer view.Close()
	rows, err := view.Collectors(ctx)
	if err != nil || len(rows) != 1 || rows[0].ClockOffsetMS != -300000 {
		t.Fatalf("collector clocks=%+v %v", rows, err)
	}
}

func TestClockOffsetAndAuditRollbackTogether(t *testing.T) {
	s := openStore(t, Options{})
	ctx := context.Background()
	if err := s.RegisterCollector(ctx, CollectorRecord{ID: "collector-test"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec("CREATE TRIGGER fail_clock BEFORE INSERT ON collector_events BEGIN SELECT RAISE(FAIL,'fake failure'); END"); err != nil {
		t.Fatal(err)
	}
	if err := s.SetClockOffset(ctx, "collector-test", 40000); err == nil {
		t.Fatal("failed audit committed offset")
	}
	row, _, err := s.Collector(ctx, "collector-test")
	if err != nil || row.ClockOffsetMS != 0 || count(t, s, "collector_events") != 0 {
		t.Fatal("clock transaction was partially committed")
	}
}
