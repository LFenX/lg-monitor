package transport

import (
	"context"
	"database/sql"
	"errors"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestCacheUsageWarmupWindowAcknowledgementAndRestart(t *testing.T) {
	ctx := context.Background()
	now := time.UnixMilli(2000000000000)
	options := OutboxOptions{Path: filepath.Join(t.TempDir(), "outbox.db"), CollectorID: "fake-collector", Now: func() time.Time { return now }, Locker: fakeCacheLocker{}}
	box, err := OpenOutbox(ctx, options)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { box.Close() }()
	if err := box.Write(ctx, wireMessage(1)); err != nil {
		t.Fatal(err)
	}
	charged := box.writes.total
	state, _ := box.State(ctx)
	if charged != state.Bytes+cacheRecordOverhead {
		t.Fatal("write accounting omitted fixed row overhead")
	}
	now = now.Add(5*time.Minute - time.Millisecond)
	cold, err := box.Usage(ctx)
	if err != nil || cold.BytesPerHour != nil || cold.ProjectedHours != nil {
		t.Fatalf("warmup reported rate: %+v %v", cold, err)
	}
	now = now.Add(time.Millisecond)
	usage, err := box.Usage(ctx)
	if err != nil || usage.BytesPerHour == nil || math.Abs(*usage.BytesPerHour-float64(charged)*12) > 1e-6 {
		t.Fatalf("five-minute rate: %+v %v", usage, err)
	}
	if err := box.Acknowledge(ctx, state.Allocated); err != nil {
		t.Fatal(err)
	}
	online, err := box.Usage(ctx)
	if err != nil || *online.BytesPerHour != *usage.BytesPerHour || online.Bytes <= 0 {
		t.Fatalf("acknowledgement erased write rate: %+v %v", online, err)
	}
	saved, _, err := ReadCacheUsage(ctx, options.Path)
	if err != nil || saved.BytesPerHour == nil || *saved.BytesPerHour != *usage.BytesPerHour {
		t.Fatalf("last reported estimate: %+v %v", saved, err)
	}
	now = now.Add(time.Hour)
	idle, err := box.Usage(ctx)
	if err != nil || idle.BytesPerHour != nil || idle.ProjectedHours != nil {
		t.Fatalf("expired window retained bytes: %+v %v", idle, err)
	}
	if err := box.Write(ctx, wireMessage(2)); err != nil {
		t.Fatal(err)
	}
	renewed, err := box.Usage(ctx)
	if err != nil || renewed.BytesPerHour == nil || *renewed.BytesPerHour != float64(box.writes.total-charged) {
		t.Fatalf("hourly rolling rate: %+v %v", renewed, err)
	}
	if err := box.Close(); err != nil {
		t.Fatal(err)
	}
	box, err = OpenOutbox(ctx, options)
	if err != nil {
		t.Fatal(err)
	}
	restarted, _, err := ReadCacheUsage(ctx, options.Path)
	if err != nil || restarted.BytesPerHour != nil || box.writes.total != 0 {
		t.Fatalf("restart restored old rate counters: %+v %v", restarted, err)
	}
}

type controlledCompactor struct {
	fail  bool
	calls int
}

func (c *controlledCompactor) Compact(ctx context.Context, db *sql.DB) error {
	c.calls++
	if c.fail {
		return errors.New("fake compaction space shortage")
	}
	return (nativeCacheCompactor{}).Compact(ctx, db)
}

func TestCacheLimitGrowthAndShrinkKeepUnacknowledgedRecords(t *testing.T) {
	for _, mode := range []string{"grow", "fits", "fits-pending", "pending", "vacuum-fails"} {
		t.Run(mode, func(t *testing.T) {
			ctx := context.Background()
			now := time.UnixMilli(2000000000000)
			path := filepath.Join(t.TempDir(), "outbox.db")
			oldMax, newMax := int64(32<<20), int64(16<<20)
			if mode == "grow" {
				oldMax, newMax = newMax, oldMax
			}
			options := OutboxOptions{Path: path, CollectorID: "fake-collector", MaxBytes: oldMax, Now: func() time.Time { return now }, Locker: fakeCacheLocker{}}
			box, err := OpenOutbox(ctx, options)
			if err != nil {
				t.Fatal(err)
			}
			message := wireMessage(1)
			count := 12
			if mode == "grow" {
				count = 1
			}
			message.TargetStatus.Reason = strings.Repeat("x", 1<<20)
			for index := 0; index < count; index++ {
				if err := box.Write(ctx, message); err != nil {
					t.Fatal(err)
				}
			}
			before, _ := box.State(ctx)
			if mode == "fits" || mode == "vacuum-fails" {
				if err := box.Acknowledge(ctx, before.Allocated); err != nil {
					t.Fatal(err)
				}
			}
			if mode == "fits-pending" {
				if err := box.Acknowledge(ctx, before.Allocated/2); err != nil {
					t.Fatal(err)
				}
			}
			if err := box.Close(); err != nil {
				t.Fatal(err)
			}
			sizeBefore, err := os.Stat(path)
			if err != nil {
				t.Fatal(err)
			}
			compactor := &controlledCompactor{fail: mode == "vacuum-fails"}
			options.MaxBytes, options.Compactor = newMax, compactor
			box, err = OpenOutbox(ctx, options)
			if err != nil {
				t.Fatalf("size change refused startup: %v", err)
			}
			defer box.Close()
			current, err := box.State(ctx)
			if err != nil || current.Allocated != before.Allocated || current.GapFrom != 0 || current.GapTo != 0 {
				t.Fatalf("resize discarded records: %+v %v", current, err)
			}
			usage, limit, err := ReadCacheUsage(ctx, path)
			if err != nil || limit.RequestedMaxBytes != newMax {
				t.Fatalf("effective size metadata: %+v %+v %v", usage, limit, err)
			}
			switch mode {
			case "grow":
				if usage.MaxBytes != newMax || current.Acked != 0 || compactor.calls != 0 {
					t.Fatalf("growth: %+v %+v", usage, current)
				}
			case "fits", "fits-pending":
				sizeAfter, err := os.Stat(path)
				if err != nil || sizeAfter.Size() >= sizeBefore.Size() || usage.MaxBytes != newMax || compactor.calls != 1 {
					t.Fatalf("file did not shrink: before=%d after=%+v usage=%+v calls=%d %v", sizeBefore.Size(), sizeAfter, usage, compactor.calls, err)
				}
				if mode == "fits-pending" && (current.Acked != before.Allocated/2 || current.Bytes <= 0) {
					t.Fatal("compaction lost unacknowledged data")
				}
			case "pending", "vacuum-fails":
				if usage.MaxBytes <= newMax || limit.Message == "" {
					t.Fatalf("deferred limit not explained: %+v %+v", usage, limit)
				}
				if mode == "pending" {
					if current.Bytes != before.Bytes || current.Acked != 0 || compactor.calls != 0 {
						t.Fatal("new limit removed pending data")
					}
					if err := box.Acknowledge(ctx, before.Allocated/2); err != nil {
						t.Fatal(err)
					}
					partial, err := box.Usage(ctx)
					if err != nil || partial.MaxBytes <= newMax {
						t.Fatal("partial acknowledgement applied the smaller limit")
					}
				} else {
					if !strings.Contains(limit.Message, "缓存整理失败") {
						t.Fatal("VACUUM failure reason omitted")
					}
					if err := box.Write(ctx, wireMessage(1)); err != nil {
						t.Fatalf("VACUUM failure stopped ingestion: %v", err)
					}
					current, _ = box.State(ctx)
					compactor.fail = false
					now = now.Add(time.Minute)
				}
				if err := box.Acknowledge(ctx, current.Allocated); err != nil {
					t.Fatal(err)
				}
				drained, limit, err := ReadCacheUsage(ctx, path)
				if err != nil || drained.MaxBytes != newMax || limit.Message != "" {
					t.Fatalf("drained cache did not adopt limit: %+v %+v %v", drained, limit, err)
				}
				final, _ := box.State(ctx)
				if final.GapFrom != 0 || final.GapTo != 0 || final.Acked != final.Allocated {
					t.Fatalf("resize lost sequence state: %+v", final)
				}
			}
		})
	}
}

func TestCacheWriteRateIncludesExpiredAndCapacityDroppedRecords(t *testing.T) {
	ctx := context.Background()
	for _, mode := range []string{"age", "capacity"} {
		t.Run(mode, func(t *testing.T) {
			now := time.UnixMilli(2000000000000)
			box, err := OpenOutbox(ctx, OutboxOptions{Path: filepath.Join(t.TempDir(), "outbox.db"), CollectorID: "fake-collector", MaxBytes: MinimumCacheBytes, MaxAge: 10 * time.Minute, Now: func() time.Time { return now }, Locker: fakeCacheLocker{}})
			if err != nil {
				t.Fatal(err)
			}
			defer box.Close()
			message := wireMessage(1)
			count := 1
			if mode == "capacity" {
				count = 12
				message.TargetStatus.Reason = strings.Repeat("x", 1<<20)
			}
			for index := 0; index < count; index++ {
				if err := box.Write(ctx, message); err != nil {
					t.Fatal(err)
				}
			}
			total := box.writes.total
			now = now.Add(5 * time.Minute)
			if mode == "age" {
				now = now.Add(6 * time.Minute)
				if err := box.Prune(ctx); err != nil {
					t.Fatal(err)
				}
			}
			state, err := box.State(ctx)
			if err != nil || state.GapFrom == 0 || state.Bytes >= total {
				t.Fatalf("fixture did not discard records: %+v %v", state, err)
			}
			usage, err := box.Usage(ctx)
			if err != nil || usage.BytesPerHour == nil || *usage.BytesPerHour <= float64(state.Bytes)/now.Sub(box.writes.started).Hours() ||
				math.Abs(*usage.BytesPerHour-float64(total)/now.Sub(box.writes.started).Hours()) > 1e-5 {
				t.Fatalf("discarded writes vanished from rate: %+v %v", usage, err)
			}
		})
	}
}
