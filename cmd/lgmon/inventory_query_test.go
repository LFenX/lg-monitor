package main

import (
	"bytes"
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/LFenX/lg-monitor/internal/collector"
	"github.com/LFenX/lg-monitor/internal/pipeline"
	"github.com/LFenX/lg-monitor/internal/store"
)

func TestQueryInventoryThroughExistingLocalCLI(t *testing.T) {
	home := t.TempDir()
	db, err := store.Open(context.Background(), store.Options{Path: filepath.Join(home, "data", "lgmon.db"), DisableMaintenance: true})
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := db.Write(context.Background(), pipeline.Message{CollectorID: "fake", Seq: 1, TS: 1000, Kind: pipeline.KindInventory, Inventory: &collector.InventorySnapshot{Kind: "tasks", Scope: "local", Entries: []collector.InventoryEntry{{ID: `\fake`, Name: "虚构 CLI 计划任务", Enabled: true, State: "ready", LastRun: 900, LastResult: 5}}}}); err != nil {
		t.Fatal(err)
	}
	if err := db.Flush(context.Background()); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	if code := execute([]string{"query", "SELECT name,json_extract(detail,'$.lastResult') AS result FROM inventory_current", "--home", home, "--json"}, &stdout, &stderr); code != 0 || !strings.Contains(stdout.String(), "虚构 CLI 计划任务") {
		t.Fatalf("inventory CLI: %d %s %s", code, stdout.String(), stderr.String())
	}
}
