package pipeline

import (
	"context"
	"errors"
	"io"
	"testing"

	"github.com/LFenX/lg-monitor/internal/collector"
)

func TestBatchChannelOwnsEveryMessageAndDrainsOnClose(t *testing.T) {
	ctx := context.Background()
	channel := NewBatchChannel(1)
	message := Message{CollectorID: "fake", Seq: 1, Kind: KindSystemMetrics, SystemMetrics: &collector.SystemMetrics{Disks: []collector.DiskMetrics{{Path: "fake", Total: 10, Used: 5}}}}
	input := []Message{message}
	if err := channel.WriteBatch(ctx, input); err != nil {
		t.Fatal(err)
	}
	input[0].SystemMetrics.Disks[0].Used = 9
	input[0].CollectorID = "changed"
	if err := channel.Close(); err != nil {
		t.Fatal(err)
	}
	output, err := channel.ReadBatch(ctx)
	if err != nil || output[0].CollectorID != "fake" || output[0].SystemMetrics.Disks[0].Used != 5 {
		t.Fatalf("batch aliases producer memory: %+v %v", output, err)
	}
	if _, err := channel.ReadBatch(ctx); !errors.Is(err, io.EOF) {
		t.Fatal("closed channel did not drain", err)
	}
	if err := channel.WriteBatch(ctx, []Message{message}); !errors.Is(err, io.ErrClosedPipe) {
		t.Fatal("closed channel accepted batch", err)
	}
}
