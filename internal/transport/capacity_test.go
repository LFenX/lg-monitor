package transport

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/LFenX/lg-monitor/examples"
	"github.com/LFenX/lg-monitor/internal/pipeline"
)

func TestDefaultOutboxCapacityForFiftyTargetsWithHTTPProbes(t *testing.T) {
	// Byte budgets must not depend on durable write speed on a shared CI disk.
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Minute)
	defer cancel()
	const duration = 10 * time.Minute
	start := time.UnixMilli(2000000000000)
	now := start
	workload := examples.Workload{Collector: "fake-capacity", StartedAt: start}
	box, err := OpenOutbox(ctx, OutboxOptions{Path: filepath.Join(t.TempDir(), "outbox.db"), CollectorID: workload.Collector, Now: func() time.Time { return now }, Locker: fakeCacheLocker{}})
	if err != nil {
		t.Fatal(err)
	}
	defer box.Close()
	counts := map[pipeline.Kind]int{}
	for elapsed := time.Duration(0); elapsed < duration; elapsed += time.Second {
		now = start.Add(elapsed)
		for _, message := range workload.Messages(elapsed, now, nil) {
			if err := box.Write(ctx, message); err != nil {
				t.Fatal(err)
			}
			counts[message.Kind]++
		}
	}
	for kind, want := range map[pipeline.Kind]int{
		pipeline.KindProcessSample: 15000, pipeline.KindTargetStatus: 3000, pipeline.KindSystemMetrics: 120, pipeline.KindProbeResult: 1000,
	} {
		if counts[kind] != want {
			t.Fatalf("workload shape changed for %s: %d want %d", kind, counts[kind], want)
		}
	}
	state, err := box.State(ctx)
	if err != nil || state.Allocated != 19120 || state.GapFrom != 0 || state.GapTo != 0 {
		t.Fatalf("capacity lost records: %+v %v", state, err)
	}
	if _, err := box.db.ExecContext(ctx, `PRAGMA wal_checkpoint(TRUNCATE)`); err != nil {
		t.Fatal(err)
	}
	var pages, pageSize int64
	if err := box.db.QueryRowContext(ctx, `PRAGMA page_count`).Scan(&pages); err != nil {
		t.Fatal(err)
	}
	if err := box.db.QueryRowContext(ctx, `PRAGMA page_size`).Scan(&pageSize); err != nil {
		t.Fatal(err)
	}
	databaseBytes := pages * pageSize
	usage, err := box.Usage(ctx)
	if err != nil {
		t.Fatal(err)
	}
	charged := box.writes.total
	if charged < usage.Bytes || charged*100 > usage.Bytes*110 {
		t.Fatalf("payload+24 differs from physical used pages by more than 10%% or underestimates: charged=%d used=%d", charged, usage.Bytes)
	}
	projected := databaseBytes*int64(24*time.Hour/duration) + cacheWALReserve
	t.Logf("targets=%d records=%d protobuf_bytes=%d database_bytes=%d mean_database_bytes=%.1f projected_24h_with_wal=%d default_limit=%d fraction=%.3f",
		examples.WorkloadTargets, state.Allocated, state.Bytes, databaseBytes, float64(databaseBytes)/float64(state.Allocated), projected, DefaultCacheBytes, float64(projected)/float64(DefaultCacheBytes))
	if projected > DefaultCacheBytes*70/100 {
		t.Fatalf("24h cache projection exceeds 70%% of default: %d > %d", projected, DefaultCacheBytes*70/100)
	}
}
