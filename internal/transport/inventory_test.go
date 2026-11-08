package transport

import (
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/LFenX/lg-monitor/internal/collector"
	"github.com/LFenX/lg-monitor/internal/pipeline"
	"github.com/LFenX/lg-monitor/internal/store"
	lgmonv1 "github.com/LFenX/lg-monitor/proto/lgmon/v1"
)

func inventoryWireMessage(seq uint64) pipeline.Message {
	return pipeline.Message{CollectorID: "fake-collector", Seq: seq, TS: 1000 + int64(seq), Kind: pipeline.KindInventory, Inventory: &collector.InventorySnapshot{Kind: "tasks", Scope: "local", Entries: []collector.InventoryEntry{{ID: `\fake-task`, Name: "虚构任务", Enabled: true, State: "ready", LastRun: 500, LastResult: 0x80070005}}}}
}
func TestInventoryProtobufRoundTripAndMalformedBodies(t *testing.T) {
	message := inventoryWireMessage(1)
	wire, err := Encode(message)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := Decode(message.CollectorID, wire)
	if err != nil || !reflect.DeepEqual(message, decoded) {
		t.Fatalf("inventory roundtrip: %+v %v", decoded, err)
	}
	for _, body := range []*lgmonv1.Record_Inventory{{}, {Inventory: &lgmonv1.InventorySnapshot{Kind: "tasks", Scope: "local", Entries: []*lgmonv1.InventoryEntry{nil}}}} {
		if _, err := Decode("fake", &lgmonv1.Record{Seq: 1, Ts: 1000, Body: body}); err == nil {
			t.Fatal("malformed inventory accepted")
		}
	}
}

func TestInventoryOfflineReopenBatchCommitAckAndReplay(t *testing.T) {
	ctx, receiver, connection, _ := linkFixture(t)
	now := func() time.Time { return time.UnixMilli(100000) }
	opts := OutboxOptions{Path: filepath.Join(t.TempDir(), "outbox.db"), CollectorID: "fake-collector", Now: now, Locker: fakeCacheLocker{}}
	box, err := OpenOutbox(ctx, opts)
	if err != nil {
		t.Fatal(err)
	}
	first := inventoryWireMessage(1)
	first.Inventory = &collector.InventorySnapshot{Kind: "startup", Scope: "machine/64/Run", Epoch: "0123456789abcdef", Entries: []collector.InventoryEntry{{ID: "fake-startup", Name: "虚构启动项", State: "registered", Fingerprint: strings.Repeat("a", 64)}}}
	if err := box.Write(ctx, first); err != nil {
		t.Fatal(err)
	}
	first.Inventory.Entries[0].Fingerprint = strings.Repeat("b", 64)
	if err := box.Write(ctx, first); err != nil {
		t.Fatal(err)
	}
	if err := box.Write(ctx, inventoryWireMessage(3)); err != nil {
		t.Fatal(err)
	}
	if err := box.Close(); err != nil {
		t.Fatal(err)
	}
	box, err = OpenOutbox(ctx, opts)
	if err != nil {
		t.Fatal(err)
	}
	defer box.Close()
	packet, err := box.NextBatch(ctx)
	if err != nil || len(packet) != 3 || packet[0].Inventory.Entries[0].Fingerprint != strings.Repeat("a", 64) {
		t.Fatal("durable snapshot mutated or lost", err)
	}
	path := filepath.Join(t.TempDir(), "monitor.db")
	backend, err := store.Open(ctx, store.Options{Path: path, Now: now, DisableMaintenance: true})
	if err != nil {
		t.Fatal(err)
	}
	defer backend.Close()
	receiver.resume = backend.LastCommitted
	writer, err := NewWriter(ctx, connection, Hello{CollectorID: "fake-collector", Capabilities: []string{InventoryCapability}})
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Close()
	done := make(chan error, 1)
	go func() { done <- writer.WriteBatch(ctx, packet) }()
	incoming, err := receiver.ReadBatch(ctx)
	if err != nil {
		t.Fatal(err)
	}
	batch, err := backend.IngestBatch(ctx, incoming)
	if err != nil {
		t.Fatal(err)
	}
	if err := backend.CompleteIngressBatch(ctx, "fake-collector", batch.Through); err != nil {
		t.Fatal(err)
	}
	receiver.AcknowledgeBatch(incoming, nil)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if err := box.Acknowledge(ctx, batch.Through); err != nil {
		t.Fatal(err)
	}
	if replay, err := backend.IngestBatch(ctx, incoming); err != nil || len(replay.Messages) != 0 {
		t.Fatal("replayed inbox generated more changes", replay, err)
	}
	view, err := store.OpenView(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer view.Close()
	for _, check := range []struct {
		table string
		count int64
	}{{"inventory_current", 2}, {"inventory_snapshots", 3}, {"inventory_changes", 1}} {
		result, err := view.Query(ctx, "SELECT count(*) FROM "+check.table)
		if err != nil || len(result.Rows) != 1 || result.Rows[0][0] != check.count {
			t.Fatal(check.table, result, err)
		}
	}
	if state, err := box.State(ctx); err != nil || state.Acked != 3 {
		t.Fatal("ACK did not release cache", state, err)
	}
	if seq, err := backend.LastCommitted(ctx, "fake-collector"); err != nil || seq != 3 {
		t.Fatal("monitor commit offset lost", seq, err)
	}
}

func TestInventoryRequiresItsCapabilityWithoutChangingLegacyTraffic(t *testing.T) {
	ctx, _, connection, _ := linkFixture(t)
	writer, err := NewWriter(ctx, connection, Hello{CollectorID: "fake-collector"})
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Close()
	if err := writer.Write(ctx, inventoryWireMessage(1)); err == nil || !strings.Contains(err.Error(), "inventory") {
		t.Fatal("undeclared inventory accepted", err)
	}
}
