package store

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

type blockedAggregates struct {
	entered chan struct{}
	release chan struct{}
	source  aggregateSource
	blocked bool
}

func (s *blockedAggregates) Next(ctx context.Context, level string, end int64) (aggregateBucket, bool, error) {
	if !s.blocked {
		s.blocked = true
		close(s.entered)
		select {
		case <-ctx.Done():
			return aggregateBucket{}, false, ctx.Err()
		case <-s.release:
		}
	}
	return s.source.Next(ctx, level, end)
}

func TestSlowBackgroundReadDoesNotBlockWritesAndWaitingJobCancels(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	clock := &testClock{created: make(chan *testTicker, 2)}
	s := openStore(t, Options{Clock: clock})
	writeMetric(t, s, s.now().Add(-2*time.Hour).UnixMilli(), 10)
	source := &blockedAggregates{entered: make(chan struct{}), release: make(chan struct{}), source: s.aggregates}
	done := make(chan error, 1)
	go func() { done <- s.maintain(ctx, source) }()
	select {
	case <-source.entered:
	case <-time.After(time.Second):
		t.Fatal("background read did not start")
	}
	writeCtx, stopWrite := context.WithTimeout(ctx, time.Second)
	defer stopWrite()
	if err := s.Write(writeCtx, sampleMessage(1, s.now().UnixMilli())); err != nil {
		t.Fatal(err)
	}
	if err := s.Flush(writeCtx); err != nil {
		t.Fatal("slow aggregation held up normal commits", err)
	}
	waiting, stopWaiting := context.WithCancel(ctx)
	stopWaiting()
	if err := s.Prune(waiting); !errors.Is(err, context.Canceled) {
		t.Fatalf("queued maintenance ignored cancellation: %v", err)
	}
	close(source.release)
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("background aggregation did not resume")
	}
}

func TestWALReadSnapshotAllowsNormalCommits(t *testing.T) {
	ctx := context.Background()
	s := openStore(t, Options{DisableMaintenance: true})
	reader, err := s.maintenanceView.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Rollback()
	var initial int
	if err := reader.QueryRow(`SELECT count(*) FROM process_samples`).Scan(&initial); err != nil {
		t.Fatal(err)
	}
	writeCtx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	if err := s.Write(writeCtx, sampleMessage(1, s.now().UnixMilli())); err != nil {
		t.Fatal(err)
	}
	if err := s.Flush(writeCtx); err != nil {
		t.Fatal("read-only WAL snapshot blocked writes", err)
	}
	var snapshot int
	if err := reader.QueryRow(`SELECT count(*) FROM process_samples`).Scan(&snapshot); err != nil || initial != 0 || snapshot != 0 || count(t, s, "process_samples") != 1 {
		t.Fatalf("WAL snapshot/commit mismatch: initial=%d snapshot=%d %v", initial, snapshot, err)
	}
}

func TestRuleQueriesOnlySeeCompletedRollupBuckets(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 1, 3, 0, 0, 0, 0, time.UTC)
	start := now.Add(-2 * time.Hour).UnixMilli()
	s := openStore(t, Options{Now: func() time.Time { return now }})
	writeMetric(t, s, start+1000, 20)
	if err := s.Prune(ctx); err != nil {
		t.Fatal(err)
	}
	queries, err := OpenRuleQueries(ctx, sPath(t, s))
	if err != nil {
		t.Fatal(err)
	}
	defer queries.Close()
	for _, level := range []struct {
		name  string
		width int64
	}{{"minute", minuteMS}, {"hour", hourMS}} {
		for _, offset := range []int64{-1, 0} {
			result, err := queries.QueryRule(ctx, `SELECT count(*) AS key FROM metric_`+level.name+` WHERE name='system.cpu_percent'`, start+level.width+offset)
			want := int64(0)
			if offset == 0 {
				want = 1
			}
			if err != nil || len(result.Rows) != 1 || result.Rows[0][0] != want {
				t.Fatalf("%s bucket end visibility: %+v %v", level.name, result, err)
			}
		}
	}
	for _, table := range []string{"rollup_dirty", "rollup_state"} {
		if _, err := queries.QueryRule(ctx, "SELECT count(*) AS key FROM "+table, now.UnixMilli()); err == nil || !strings.Contains(err.Error(), "原始采样表") {
			t.Fatalf("maintenance table readable: %s %v", table, err)
		}
	}
}

func TestTenThousandWritesDuringAggregationCommitWithinTwoSeconds(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	now := time.Date(2026, 1, 4, 0, 0, 0, 0, time.UTC)
	s := openStore(t, Options{Now: func() time.Time { return now }, DisableMaintenance: true})
	for i := 0; i < 50; i++ {
		writeMetric(t, s, now.Add(-48*time.Hour+time.Duration(i)*time.Minute).UnixMilli(), 25)
	}
	source := &blockedAggregates{entered: make(chan struct{}), release: make(chan struct{}), source: s.aggregates}
	done := make(chan error, 1)
	go func() { done <- s.maintain(ctx, source) }()
	select {
	case <-source.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("concurrent aggregation did not start")
	}
	started := time.Now()
	close(source.release)
	for i := 0; i < 10000; i++ {
		if err := s.Write(ctx, sampleMessage(uint64(i+1), now.UnixMilli()+int64(i))); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.Flush(ctx); err != nil {
		t.Fatal(err)
	}
	elapsed := time.Since(started)
	t.Logf("10000 records committed during aggregation in %s", elapsed)
	if elapsed >= 2*time.Second || count(t, s, "process_samples") != 10000 {
		t.Fatalf("concurrent writes exceeded limit: %s", elapsed)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("concurrent aggregation did not finish")
	}
}
