package store

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/LFenX/lg-monitor/internal/collector"
	"github.com/LFenX/lg-monitor/internal/pipeline"
)

func startupInventoryMessage(seq uint64, ts int64, entries ...collector.InventoryEntry) pipeline.Message {
	return pipeline.Message{CollectorID: "fake-inventory", Seq: seq, TS: ts, Kind: pipeline.KindInventory, Inventory: &collector.InventorySnapshot{Kind: "startup", Scope: "machine/64/Run", Epoch: "0123456789abcdef", Entries: entries}}
}
func startupEntry(id, digest string) collector.InventoryEntry {
	return collector.InventoryEntry{ID: id, Name: "虚构 " + id, State: "registered", Fingerprint: strings.Repeat(digest, 64)}
}
func writeInventory(t *testing.T, s *Store, message pipeline.Message) {
	t.Helper()
	if err := s.Write(context.Background(), message); err != nil {
		t.Fatal(err)
	}
	if err := s.Flush(context.Background()); err != nil {
		t.Fatal(err)
	}
}
func TestInventoryBaselinesChangesErrorsReplayAndReopen(t *testing.T) {
	ctx := context.Background()
	s := openStore(t, Options{DisableMaintenance: true})
	a, b, c := startupEntry("a", "a"), startupEntry("b", "b"), startupEntry("c", "c")
	writeInventory(t, s, startupInventoryMessage(1, 1000, a, c))
	if count(t, s, "inventory_changes") != 0 {
		t.Fatal("initial baseline reported changes")
	}
	a.Fingerprint = strings.Repeat("d", 64)
	changed := startupInventoryMessage(2, 2000, a, b)
	writeInventory(t, s, changed)
	if count(t, s, "inventory_changes") != 3 || count(t, s, "inventory_current") != 2 {
		t.Fatal("add/change/remove not committed atomically")
	}
	writeInventory(t, s, changed)
	writeInventory(t, s, startupInventoryMessage(1, 9999, c))
	if count(t, s, "inventory_changes") != 3 || count(t, s, "inventory_snapshots") != 2 {
		t.Fatal("replay or older seq rewrote baseline")
	}
	failed := startupInventoryMessage(3, 3000)
	failed.Inventory.ErrorCode = "source_unavailable"
	writeInventory(t, s, failed)
	var last, initialized int64
	if err := s.db.QueryRow(`SELECT last_success,initialized FROM inventory_scopes`).Scan(&last, &initialized); err != nil || last != 2000 || initialized != 1 || count(t, s, "inventory_current") != 2 {
		t.Fatal("failed source erased successful baseline", err)
	}
	path := s.maintenanceView.path
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s = openStore(t, Options{Path: path, DisableMaintenance: true})
	writeInventory(t, s, startupInventoryMessage(4, 4000, a))
	if count(t, s, "inventory_changes") != 4 {
		t.Fatal("persistent baseline lost after monitor restart")
	}
	rotated := startupInventoryMessage(5, 5000, c)
	rotated.Inventory.Epoch = "fedcba9876543210"
	writeInventory(t, s, rotated)
	if count(t, s, "inventory_changes") != 4 || count(t, s, "inventory_current") != 1 {
		t.Fatal("key epoch change generated false changes")
	}
	view, err := OpenView(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer view.Close()
	result, err := view.Query(ctx, `SELECT name,detail FROM inventory_current`)
	if err != nil || len(result.Rows) != 1 {
		t.Fatal("inventory inaccessible through read-only SQL", err)
	}
	data, _ := json.Marshal(result)
	if strings.Contains(string(data), "--password") {
		t.Fatal("command data stored")
	}
	queries, err := OpenRuleQueries(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer queries.Close()
	for _, table := range []string{"inventory_current", "inventory_scopes"} {
		if _, err := queries.QueryRule(ctx, "SELECT * FROM "+table, 6000); err == nil {
			t.Fatal("current-only inventory leaked into replay", table)
		}
	}
	result, err = queries.QueryRule(ctx, `SELECT seq AS key FROM inventory_snapshots ORDER BY ts`, 1500)
	if err != nil || len(result.Rows) != 1 {
		t.Fatal("future snapshots leaked into event-time rule", err, result)
	}
	result, err = queries.QueryRule(ctx, `SELECT id AS key FROM inventory_changes`, 2500)
	if err != nil || len(result.Rows) != 3 {
		t.Fatal("startup rule cannot read changes", err, result)
	}
}

func TestInventoryRawRetentionDoesNotEraseCurrentOrResurrectLateHistory(t *testing.T) {
	s := openStore(t, Options{DisableMaintenance: true})
	ctx := context.Background()
	a := startupEntry("a", "a")
	writeInventory(t, s, startupInventoryMessage(1, 1000, a))
	b := startupEntry("b", "b")
	writeInventory(t, s, startupInventoryMessage(2, 2000, b))
	for _, table := range []string{"inventory_snapshots", "inventory_changes"} {
		if err := s.cleanRawTable(ctx, table, 3000); err != nil {
			t.Fatal(err)
		}
	}
	if count(t, s, "inventory_snapshots") != 0 || count(t, s, "inventory_changes") != 0 || count(t, s, "inventory_current") != 1 || count(t, s, "inventory_scopes") != 1 {
		t.Fatal("retention erased current baseline")
	}
	writeInventory(t, s, startupInventoryMessage(3, 2500, a))
	if count(t, s, "inventory_snapshots") != 0 || count(t, s, "inventory_changes") != 0 {
		t.Fatal("late snapshot resurrected retained history")
	}
	var dropped int
	if err := s.db.QueryRow(`SELECT count(*) FROM collector_events WHERE kind='late_dropped' AND source_table='inventory_snapshots'`).Scan(&dropped); err != nil || dropped != 1 {
		t.Fatal("late inventory gap is invisible", err)
	}
	var id string
	if err := s.db.QueryRow(`SELECT id FROM inventory_current`).Scan(&id); err != nil || id != "a" {
		t.Fatal("late ordered inventory failed to update known current", err)
	}
	writeInventory(t, s, startupInventoryMessage(4, 4000, b))
	if count(t, s, "inventory_snapshots") != 1 || count(t, s, "inventory_changes") != 2 || count(t, s, "rollup_dirty") != 0 {
		t.Fatal("inventory polluted numeric rollups or lost new history")
	}
}
