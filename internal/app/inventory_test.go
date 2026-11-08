package app

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"

	"github.com/LFenX/lg-monitor/internal/collector"
	"github.com/LFenX/lg-monitor/internal/pipeline"
)

type controlledInventory struct {
	requests chan chan []collector.InventorySnapshot
}

func (s controlledInventory) Snapshot(ctx context.Context) ([]collector.InventorySnapshot, error) {
	reply := make(chan []collector.InventorySnapshot, 1)
	select {
	case s.requests <- reply:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	select {
	case rows := <-reply:
		return rows, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}
func TestInventoryWorkerUsesIndependentClockChangesAndPeriodicSnapshot(t *testing.T) {
	f := newFixture(t, "targets: []\n")
	source := controlledInventory{make(chan chan []collector.InventorySnapshot, 5)}
	f.options.Inventory = source
	instance, err := NewCollector(f.options)
	if err != nil {
		t.Fatal(err)
	}
	emitter, err := pipeline.NewEmitter(f.options.ID, f.channel, f.clock.Now)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- instance.runAuxiliary(ctx, emitter) }()
	t.Cleanup(func() {
		cancel()
		select {
		case err := <-done:
			if err != nil {
				t.Error(err)
			}
		case <-time.After(3 * time.Second):
			t.Error("inventory worker did not stop")
		}
	})
	ticker := receive(t, f.clock.created)
	if ticker.interval != time.Minute {
		t.Fatal("inventory cadence changed ordinary collection", ticker.interval)
	}
	snapshot := collector.InventorySnapshot{Kind: "tasks", Scope: "local", Entries: []collector.InventoryEntry{{ID: `\fake`, Name: "虚构", Enabled: true, State: "ready"}}}
	receive(t, source.requests) <- []collector.InventorySnapshot{snapshot}
	message(t, f.channel, 1, pipeline.KindInventory)
	message(t, f.channel, 2, pipeline.KindInventory)
	f.clock.tick(ticker, time.Minute)
	receive(t, source.requests) <- []collector.InventorySnapshot{snapshot}
	f.clock.tick(ticker, 10*time.Minute)
	request := receive(t, source.requests)
	request <- []collector.InventorySnapshot{snapshot}
	row := message(t, f.channel, 3, pipeline.KindInventory)
	if row.Inventory.Scope != "local" {
		t.Fatal("unchanged snapshot was sent before periodic interval")
	}
	message(t, f.channel, 4, pipeline.KindInventory)
	f.clock.tick(ticker, time.Minute)
	request = receive(t, source.requests)
	snapshot.Entries[0].LastResult = 5
	request <- []collector.InventorySnapshot{snapshot}
	row = message(t, f.channel, 5, pipeline.KindInventory)
	if row.Inventory.Entries[0].LastResult != 5 {
		t.Fatal("changed task result missing")
	}
}
func TestInventoryKeyStaysEncryptedLocalAndStableAcrossInitialization(t *testing.T) {
	vault := &apiVault{values: map[string][]byte{}}
	if _, err := inventorySource(context.Background(), vault); err != nil {
		t.Fatal(err)
	}
	first := bytes.Clone(vault.values["lgmon-inventory-hmac"])
	if len(first) != 32 || vault.setCalls != 1 {
		t.Fatal("local random key not saved")
	}
	if _, err := inventorySource(context.Background(), vault); err != nil {
		t.Fatal(err)
	}
	if vault.setCalls != 1 || !bytes.Equal(first, vault.values["lgmon-inventory-hmac"]) {
		t.Fatal("local key rotated on restart")
	}
	if !bytes.Equal(vault.lastRead, make([]byte, 32)) {
		t.Fatal("decrypted key buffer not cleared")
	}
	bad := &apiVault{values: map[string][]byte{"lgmon-inventory-hmac": []byte(strings.Repeat("x", 3))}}
	if _, err := inventorySource(context.Background(), bad); err == nil {
		t.Fatal("invalid saved key accepted")
	}
}
