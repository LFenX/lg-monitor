//go:build integration && windows

package transport

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/LFenX/lg-monitor/internal/pipeline"
)

func TestNativeOutboxBoundsPhysicalFilesAndReleasesKernelLock(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "outbox.db")
	opts := OutboxOptions{Path: path, CollectorID: "fake-collector", MaxBytes: MinimumCacheBytes, Now: time.Now}
	box, err := OpenOutbox(ctx, opts)
	if err != nil {
		t.Fatal(err)
	}
	defer box.Close()
	if other, err := OpenOutbox(ctx, opts); err == nil {
		other.Close()
		t.Fatal("second process handle acquired the same cache lock")
	}
	for range 24 {
		message := wireMessage(1)
		message.TargetStatus.Reason = strings.Repeat("x", 1024*1024)
		if err := box.Write(ctx, message); err != nil {
			t.Fatal(err)
		}
		var diskBytes int64
		for _, suffix := range []string{"", "-wal", "-shm", ".lock"} {
			info, err := os.Stat(path + suffix)
			if os.IsNotExist(err) {
				continue
			}
			if err != nil {
				t.Fatal(err)
			}
			diskBytes += info.Size()
		}
		if diskBytes > opts.MaxBytes {
			t.Fatalf("physical SQLite files exceed cache quota: %d > %d", diskBytes, opts.MaxBytes)
		}
	}
	gap, exists, err := box.Next(ctx)
	if err != nil || !exists || gap.Kind != pipeline.KindDataGap || gap.DataGap.FromSeq != 1 || gap.Seq >= 24 {
		t.Fatalf("capacity gap or newest-data retention: %+v %v", gap, err)
	}
	if err := box.Acknowledge(ctx, gap.Seq); err != nil {
		t.Fatal(err)
	}
	last := gap.Seq
	for {
		message, exists, err := box.Next(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if !exists {
			break
		}
		if message.Seq != last+1 {
			t.Fatal("newest cached records are not contiguous")
		}
		last = message.Seq
		if err := box.Acknowledge(ctx, message.Seq); err != nil {
			t.Fatal(err)
		}
	}
	if last != 24 {
		t.Fatalf("latest record lost: %d", last)
	}
	box.Close()
	reopened, err := OpenOutbox(ctx, opts)
	if err != nil {
		t.Fatalf("kernel lock was not released: %v", err)
	}
	defer reopened.Close()
	state, err := reopened.State(ctx)
	if err != nil || state.Acked != 24 || state.Allocated != 24 {
		t.Fatalf("durable capacity acknowledgement: %+v %v", state, err)
	}
}
