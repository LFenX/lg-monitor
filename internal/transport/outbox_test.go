package transport

import (
	"bytes"
	"context"
	"io"
	"path/filepath"
	"testing"
	"time"

	"github.com/LFenX/lg-monitor/internal/pipeline"
)

type fakeCacheLocker struct{}

func (fakeCacheLocker) Lock(string) (io.Closer, error) {
	return io.NopCloser(bytes.NewReader(nil)), nil
}

func TestOutboxPersistsAllocationAndOnlyDeletesAcknowledgedPrefix(t *testing.T) {
	ctx := context.Background()
	now := time.UnixMilli(100000)
	options := OutboxOptions{Path: filepath.Join(t.TempDir(), "离线 # %", "outbox.db"), CollectorID: "fake-collector", Now: func() time.Time { return now }, Locker: fakeCacheLocker{}}
	box, err := OpenOutbox(ctx, options)
	if err != nil {
		t.Fatal(err)
	}
	message := wireMessage(1)
	message.TargetStatus.State = "up"
	for range 3 {
		if err := box.Write(ctx, message); err != nil {
			t.Fatal(err)
		}
	}
	message.TargetStatus.State = "down"
	first, ok, err := box.Next(ctx)
	if err != nil || !ok || first.Seq != 1 || first.TargetStatus.State != "up" {
		t.Fatalf("first: %+v %v", first, err)
	}
	if err := box.Acknowledge(ctx, 1); err != nil {
		t.Fatal(err)
	}
	box.Close()
	box, err = OpenOutbox(ctx, options)
	if err != nil {
		t.Fatal(err)
	}
	defer box.Close()
	state, err := box.State(ctx)
	if err != nil || state.Allocated != 3 || state.Acked != 1 {
		t.Fatalf("persistent state: %+v %v", state, err)
	}
	if err := box.SyncResume(ctx, 3); err != nil {
		t.Fatal(err)
	}
	remaining, ok, err := box.Next(ctx)
	if err != nil || !ok || remaining.Seq != 3 {
		t.Fatalf("resume: %+v %v", remaining, err)
	}
	if err := box.Write(ctx, wireMessage(1)); err != nil {
		t.Fatal(err)
	}
	if err := box.Acknowledge(ctx, 3); err != nil {
		t.Fatal(err)
	}
	remaining, ok, err = box.Next(ctx)
	if err != nil || !ok || remaining.Seq != 4 {
		t.Fatalf("allocation reset across restart: %+v %v", remaining, err)
	}
	if err := box.Acknowledge(ctx, 5); err == nil {
		t.Fatal("deleted records beyond allocated sequence")
	}
	if err := box.SyncResume(ctx, 2); err == nil {
		t.Fatal("silently accepted monitor rollback")
	}
	if err := box.Acknowledge(ctx, 4); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := box.Next(ctx); err != nil || ok {
		t.Fatalf("acked queue retained data: %v %v", ok, err)
	}
}

func TestOutboxAgeLimitPersistsGapAndAcknowledgesPartialOverlap(t *testing.T) {
	ctx := context.Background()
	now := time.UnixMilli(100000)
	options := OutboxOptions{Path: filepath.Join(t.TempDir(), "outbox.db"), CollectorID: "fake-collector", Now: func() time.Time { return now }, Locker: fakeCacheLocker{}}
	box, err := OpenOutbox(ctx, options)
	if err != nil {
		t.Fatal(err)
	}
	for range 3 {
		if err := box.Write(ctx, wireMessage(1)); err != nil {
			t.Fatal(err)
		}
	}
	now = now.Add(25 * time.Hour)
	if err := box.Prune(ctx); err != nil {
		t.Fatal(err)
	}
	box.Close()
	box, err = OpenOutbox(ctx, options)
	if err != nil {
		t.Fatal(err)
	}
	defer box.Close()
	if err := box.SyncResume(ctx, 2); err != nil {
		t.Fatal(err)
	}
	gap, ok, err := box.Next(ctx)
	if err != nil || !ok || gap.Kind != pipeline.KindDataGap || gap.Seq != 3 || gap.DataGap.FromSeq != 2 || gap.DataGap.Dropped != 2 {
		t.Fatalf("gap overlap: %+v %v", gap, err)
	}
	if err := gap.Validate(); err != nil {
		t.Fatal(err)
	}
	if err := box.Acknowledge(ctx, 3); err != nil {
		t.Fatal(err)
	}
	if err := box.Write(ctx, wireMessage(1)); err != nil {
		t.Fatal(err)
	}
	next, ok, err := box.Next(ctx)
	if err != nil || !ok || next.Seq != 4 {
		t.Fatalf("after gap: %+v %v", next, err)
	}
}
