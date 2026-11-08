package transport

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/LFenX/lg-monitor/internal/pipeline"
)

func TestEventLogCheckpointAndBatchCommitOrRollbackTogether(t *testing.T) {
	ctx := context.Background()
	options := OutboxOptions{Path: filepath.Join(t.TempDir(), "outbox.db"), CollectorID: "fake-collector", Locker: fakeCacheLocker{}}
	box, err := OpenOutbox(ctx, options)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { box.Close() }()
	if _, found, err := box.EventLogBookmark(ctx, "Application"); err != nil || found {
		t.Fatalf("fresh checkpoint: found=%v err=%v", found, err)
	}
	checkpoint := pipeline.EventLogCheckpoint{Channel: "Application", Bookmark: "fake-bookmark-1"}
	if err := box.WriteEventLogCheckpoint(ctx, []pipeline.Message{wireMessage(1), wireMessage(1)}, checkpoint); err != nil {
		t.Fatal(err)
	}
	if _, err := box.db.ExecContext(ctx, `CREATE TRIGGER fail_bookmark BEFORE UPDATE ON eventlog_checkpoints WHEN NEW.bookmark='fake-failure' BEGIN SELECT RAISE(ABORT,'fake checkpoint failure'); END`); err != nil {
		t.Fatal(err)
	}
	checkpoint.Bookmark = "fake-failure"
	if err := box.WriteEventLogCheckpoint(ctx, []pipeline.Message{wireMessage(1)}, checkpoint); err == nil {
		t.Fatal("failed checkpoint accepted")
	}
	state, err := box.State(ctx)
	if err != nil || state.Allocated != 2 {
		t.Fatalf("failed transaction allocated a record: %+v %v", state, err)
	}
	bookmark, found, err := box.EventLogBookmark(ctx, "Application")
	if err != nil || !found || bookmark != "fake-bookmark-1" {
		t.Fatalf("failed transaction changed bookmark: %q %v %v", bookmark, found, err)
	}
	if _, err := box.db.ExecContext(ctx, `DROP TRIGGER fail_bookmark; CREATE TRIGGER fail_event_record BEFORE INSERT ON outbox_records WHEN NEW.seq=3 BEGIN SELECT RAISE(ABORT,'fake event failure'); END`); err != nil {
		t.Fatal(err)
	}
	checkpoint.Bookmark = "fake-bookmark-2"
	if err := box.WriteEventLogCheckpoint(ctx, []pipeline.Message{wireMessage(1)}, checkpoint); err == nil {
		t.Fatal("failed record accepted")
	}
	bookmark, _, err = box.EventLogBookmark(ctx, "Application")
	if err != nil || bookmark != "fake-bookmark-1" {
		t.Fatalf("failed record advanced bookmark: %q %v", bookmark, err)
	}
	if _, err := box.db.ExecContext(ctx, `DROP TRIGGER fail_event_record`); err != nil {
		t.Fatal(err)
	}
	if err := box.WriteEventLogCheckpoint(ctx, nil, checkpoint); err != nil {
		t.Fatal(err)
	}
	if err := box.Close(); err != nil {
		t.Fatal(err)
	}
	box, err = OpenOutbox(ctx, options)
	if err != nil {
		t.Fatal(err)
	}
	bookmark, found, err = box.EventLogBookmark(ctx, "Application")
	if err != nil || !found || bookmark != checkpoint.Bookmark {
		t.Fatalf("restart lost checkpoint: %q %v %v", bookmark, found, err)
	}
	batch, err := box.NextBatch(ctx)
	if err != nil || len(batch) != 2 || batch[0].Seq != 1 || batch[1].Seq != 2 {
		t.Fatalf("restart lost or duplicated records: %+v %v", batch, err)
	}
	state, err = box.State(ctx)
	if err != nil || state.Allocated != 2 {
		t.Fatalf("bookmark-only commit consumed a sequence: %+v %v", state, err)
	}
}
