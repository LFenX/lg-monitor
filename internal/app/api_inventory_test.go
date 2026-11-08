package app

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/LFenX/lg-monitor/internal/collector"
	"github.com/LFenX/lg-monitor/internal/pipeline"
)

func seedAPIInventory(t *testing.T, f *apiFixture) {
	t.Helper()
	put := func(seq uint64, snapshot collector.InventorySnapshot) {
		if err := f.db.Write(context.Background(), pipeline.Message{CollectorID: "fake-host", Seq: seq, TS: f.now.UnixMilli(), Kind: pipeline.KindInventory, Inventory: &snapshot}); err != nil {
			t.Fatal(err)
		}
	}
	startup := collector.InventorySnapshot{Kind: "startup", Scope: "machine/64/Run", Epoch: "0123456789abcdef", Entries: []collector.InventoryEntry{{ID: "fake-startup", Name: "虚构启动项", State: "registered", Fingerprint: strings.Repeat("a", 64)}}}
	put(200, startup)
	startup.Entries[0].Fingerprint = strings.Repeat("b", 64)
	put(201, startup)
	put(202, collector.InventorySnapshot{Kind: "tasks", Scope: "local", Entries: []collector.InventoryEntry{
		{ID: `\fake\daily-backup`, Name: "虚构备份任务", Enabled: true, State: "ready", LastRun: f.now.Add(-time.Minute).UnixMilli(), NextRun: f.now.Add(time.Hour).UnixMilli(), LastResult: 0x80070005, MissedRuns: 2},
		{ID: `\fake\never-ran`, Name: "虚构未运行任务", Enabled: false, State: "disabled", LastResult: 0x41303},
	}})
	put(203, collector.InventorySnapshot{Kind: "startup", Scope: "user/fake/Run", Entries: []collector.InventoryEntry{}, ErrorCode: "source_unavailable"})
	if err := f.db.Flush(context.Background()); err != nil {
		t.Fatal(err)
	}
}
func TestInventoryReadHTTPAndMCPQueryUseSameExistingReadPermission(t *testing.T) {
	f := newAPIFixture(t)
	seedAPIInventory(t, f)
	response := f.request(t, "POST", "/api/v1/query", "read", `{"sql":"SELECT name FROM inventory_current ORDER BY name"}`, 200)
	if !strings.Contains(response.Body.String(), "虚构备份任务") || !strings.Contains(response.Body.String(), "虚构启动项") {
		t.Fatal("inventory not readable", response.Body.String())
	}
	server := httptest.NewServer(f.server)
	defer server.Close()
	session := mcpSession(t, server.URL, "fake-read", false)
	result := mcpCall(t, session, "query_sql", map[string]any{"sql": "SELECT name FROM inventory_current ORDER BY name"}, false)
	if !strings.Contains(result, "虚构备份任务") {
		t.Fatal("MCP differs", result)
	}
	f.request(t, "POST", "/api/v1/query", "read", `{"sql":"DELETE FROM inventory_current"}`, 400)
	f.request(t, "POST", "/api/v1/query", "invalid", `{"sql":"SELECT * FROM inventory_current"}`, 401)
}
