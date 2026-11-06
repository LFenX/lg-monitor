package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/LFenX/lg-monitor/internal/collector"
	"github.com/LFenX/lg-monitor/internal/pipeline"
)

func openStore(t *testing.T, options Options) *Store {
	t.Helper()
	if options.Path == "" {
		options.Path = filepath.Join(t.TempDir(), "monitor.db")
	}
	if options.Now == nil {
		options.Now = func() time.Time { return time.UnixMilli(2000000000000) }
	}
	s, err := Open(context.Background(), options)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := s.Close(); err != nil {
			t.Errorf("close store: %v", err)
		}
	})
	return s
}

func sampleMessage(seq uint64, ts int64) pipeline.Message {
	return pipeline.Message{CollectorID: "collector-test", Seq: seq, TS: ts, Kind: pipeline.KindProcessSample, ProcessSample: &pipeline.ProcessSample{TargetID: "web", PID: 42, CreatedAt: 1, CPUPercent: 25, RSS: 4096}}
}

func count(t *testing.T, s *Store, table string) int {
	t.Helper()
	var got int
	if err := s.db.QueryRow("SELECT count(*) FROM " + table).Scan(&got); err != nil {
		t.Fatal(err)
	}
	return got
}

func TestMigrationReopensWithoutLosingDataAndSetsPragmas(t *testing.T) {
	path := filepath.Join(t.TempDir(), "中文 # % database", "monitor.db")
	for attempt := 0; attempt < 3; attempt++ {
		s := openStore(t, Options{Path: path})
		var journal string
		var busy, foreignKeys int
		if err := s.db.QueryRow("PRAGMA journal_mode").Scan(&journal); err != nil {
			t.Fatal(err)
		}
		if err := s.db.QueryRow("PRAGMA busy_timeout").Scan(&busy); err != nil {
			t.Fatal(err)
		}
		if err := s.db.QueryRow("PRAGMA foreign_keys").Scan(&foreignKeys); err != nil {
			t.Fatal(err)
		}
		if journal != "wal" || busy != 5000 || foreignKeys != 1 || count(t, s, "schema_migrations") != 4 {
			t.Fatalf("database setup: journal=%q busy=%d foreignKeys=%d", journal, busy, foreignKeys)
		}
		if err := s.Write(context.Background(), sampleMessage(uint64(attempt+1), int64(attempt+1))); err != nil {
			t.Fatal(err)
		}
		if err := s.Flush(context.Background()); err != nil {
			t.Fatal(err)
		}
		if got := count(t, s, "process_samples"); got != attempt+1 {
			t.Fatalf("reopen reset the data: %d", got)
		}
		if err := s.Close(); err != nil {
			t.Fatal(err)
		}
		if err := s.Write(context.Background(), sampleMessage(1, 1)); !errors.Is(err, io.ErrClosedPipe) {
			t.Fatalf("write after close = %v", err)
		}
	}
}

type testTicker struct {
	interval time.Duration
	ticks    chan time.Time
	stopped  atomic.Bool
}

func (t *testTicker) Ticks() <-chan time.Time { return t.ticks }
func (t *testTicker) Stop()                   { t.stopped.Store(true) }

type testClock struct{ created chan *testTicker }

func (c *testClock) NewTicker(interval time.Duration) Ticker {
	ticker := &testTicker{interval: interval, ticks: make(chan time.Time, 1)}
	c.created <- ticker
	return ticker
}

func TestTenThousandRecordsCommitWithinTwoSeconds(t *testing.T) {
	clock := &testClock{created: make(chan *testTicker, 2)}
	s := openStore(t, Options{Clock: clock})
	started := time.Now()
	for i := 0; i < 10000; i++ {
		if err := s.Write(context.Background(), sampleMessage(uint64(i+1), 2000000000000+int64(i))); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.Flush(context.Background()); err != nil {
		t.Fatal(err)
	}
	elapsed := time.Since(started)
	t.Logf("10000 records accepted and committed in %s", elapsed)
	if elapsed >= 2*time.Second || count(t, s, "process_samples") != 10000 {
		t.Fatalf("10000 records took %s or were not fully committed", elapsed)
	}
}

func waitCount(t *testing.T, s *Store, table string, want int, tick func()) {
	t.Helper()
	deadline := time.After(5 * time.Second)
	for {
		if tick != nil {
			tick()
		}
		if got := count(t, s, table); got == want {
			return
		}
		select {
		case <-deadline:
			t.Fatalf("%s did not reach %d rows", table, want)
		case <-time.After(5 * time.Millisecond):
		}
	}
}

func TestBatchLimitAndPeriodicFlushAndPrune(t *testing.T) {
	clock := &testClock{created: make(chan *testTicker, 2)}
	s := openStore(t, Options{Clock: clock})
	flush, prune := <-clock.created, <-clock.created
	if flush.interval != 500*time.Millisecond || prune.interval != time.Hour {
		t.Fatalf("timer intervals = %s, %s", flush.interval, prune.interval)
	}
	for i := 0; i < 500; i++ {
		if err := s.Write(context.Background(), sampleMessage(uint64(i+1), 1000+int64(i))); err != nil {
			t.Fatal(err)
		}
	}
	waitCount(t, s, "process_samples", 500, nil)
	if err := s.Write(context.Background(), sampleMessage(501, 2000000000000)); err != nil {
		t.Fatal(err)
	}
	waitCount(t, s, "process_samples", 501, func() {
		select {
		case flush.ticks <- time.Time{}:
		default:
		}
	})
	waitCount(t, s, "process_samples", 1, func() {
		select {
		case prune.ticks <- time.Time{}:
		default:
		}
	})
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	if !flush.stopped.Load() || !prune.stopped.Load() {
		t.Fatal("storage timers were not stopped")
	}
}

func TestRetentionBoundaryAndLatestStatus(t *testing.T) {
	s := openStore(t, Options{})
	cutoff := s.now().Add(-24 * time.Hour).UnixMilli()
	for i, ts := range []int64{cutoff - 1, cutoff, cutoff + 1} {
		state := []string{collector.StateDown, collector.StateUp, collector.StateUnknown}[i]
		messages := []pipeline.Message{
			{CollectorID: "collector-test", Seq: uint64(i + 1), TS: ts, Kind: pipeline.KindTargetStatus, TargetStatus: &pipeline.TargetStatus{TargetID: "web", State: state, Reason: "测试状态"}},
			{CollectorID: "collector-test", Seq: uint64(i + 1), TS: ts, Kind: pipeline.KindProbeResult, ProbeResult: &pipeline.ProbeResult{TargetID: "web", Probe: 1, OK: false, Error: "连接失败"}},
			sampleMessage(uint64(i+1), ts),
		}
		for _, message := range messages {
			if err := s.Write(context.Background(), message); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := s.RegisterCollector(context.Background(), CollectorRecord{ID: "collector-test", Hostname: "test-host", FirstSeen: cutoff - 10, LastSeen: cutoff + 10, Version: "dev"}); err != nil {
		t.Fatal(err)
	}
	if err := s.operation(context.Background(), func(ctx context.Context, tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `INSERT INTO alerts(id,state,started_at) VALUES('audit','firing',?)`, cutoff-100)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if err := s.Prune(context.Background()); err != nil {
		t.Fatal(err)
	}
	for _, table := range []string{"target_status", "probe_results", "process_samples"} {
		if got := count(t, s, table); got != 2 {
			t.Fatalf("%s count = %d, want boundary and newer row", table, got)
		}
	}
	if count(t, s, "collectors") != 1 || count(t, s, "alerts") != 1 {
		t.Fatal("raw retention removed metadata or active alerts")
	}
	rows, err := s.CurrentTargets(context.Background())
	if err != nil || len(rows) != 1 || rows[0].State != collector.StateUnknown || rows[0].TS != cutoff+1 || !json.Valid(rows[0].Detail) {
		t.Fatalf("latest target = %+v, %v", rows, err)
	}
}

func TestWriteOwnsStateAndCloseFlushesTail(t *testing.T) {
	path := filepath.Join(t.TempDir(), "monitor.db")
	s := openStore(t, Options{Path: path})
	status := &pipeline.TargetStatus{TargetID: "web", State: collector.StateUp, Processes: []pipeline.ProcessRef{{PID: 42, CreatedAt: 1}}}
	if err := s.Write(context.Background(), pipeline.Message{CollectorID: "collector-test", Seq: 1, TS: 1234, Kind: pipeline.KindTargetStatus, TargetStatus: status}); err != nil {
		t.Fatal(err)
	}
	status.State, status.Processes[0].PID = collector.StateDown, 99
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	reopened := openStore(t, Options{Path: path})
	rows, err := reopened.CurrentTargets(context.Background())
	if err != nil || len(rows) != 1 || rows[0].State != collector.StateUp || !strings.Contains(string(rows[0].Detail), `"pid":42`) {
		t.Fatalf("queued state was mutated or lost on close: %+v, %v", rows, err)
	}
}

func TestWriterFailureRetainsDataAndKeepsAcceptingWrites(t *testing.T) {
	s, err := Open(context.Background(), Options{Path: filepath.Join(t.TempDir(), "monitor.db")})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	if err := s.operation(context.Background(), func(ctx context.Context, tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `DROP TABLE process_samples`)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if err := s.Write(context.Background(), sampleMessage(1, 1000)); err != nil {
		t.Fatal(err)
	}
	if err := s.Flush(context.Background()); err == nil {
		t.Fatal("failed database write was reported as committed")
	}
	select {
	case <-s.done:
		t.Fatal("one failed write stopped the writer")
	default:
	}
	if s.Err() == nil {
		t.Fatal("background writer failure was lost")
	}
	if err := s.Write(context.Background(), sampleMessage(2, 1001)); err != nil {
		t.Fatalf("writer rejected data after a temporary failure: %v", err)
	}
	if err := s.Close(); err == nil {
		t.Fatal("close hid the writer failure")
	}
}
