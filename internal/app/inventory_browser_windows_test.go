//go:build windows && integration && webui

package app

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/LFenX/lg-monitor/internal/collector"
	"github.com/LFenX/lg-monitor/internal/pipeline"
)

func TestNativeInventoryBrowser(t *testing.T) {
	f := newAPIFixtureWithHTTPClock(t, time.Date(2026, 11, 9, 5, 0, 0, 0, time.UTC), time.Now)
	f.samples(t)
	seedAPIInventory(t, f)
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/__test/inventory/update" {
			f.server.ServeHTTP(w, r)
			return
		}
		if r.Method != "POST" || r.Header.Get("Authorization") != "Bearer fake-test-control" {
			http.Error(w, "forbidden", 403)
			return
		}
		put := func(seq uint64, snapshot collector.InventorySnapshot) bool {
			if err := f.db.Write(r.Context(), pipeline.Message{CollectorID: "fake-host", Seq: seq, TS: f.now.UnixMilli(), Kind: pipeline.KindInventory, Inventory: &snapshot}); err != nil {
				http.Error(w, "fake inventory write failed", 500)
				return false
			}
			return true
		}
		if !put(210, collector.InventorySnapshot{Kind: "tasks", Scope: "local", Entries: []collector.InventoryEntry{{ID: `\fake\daily-backup`, Name: "虚构备份任务", Enabled: true, State: "ready", LastRun: f.now.UnixMilli(), NextRun: f.now.Add(time.Hour).UnixMilli()}, {ID: `\fake\never-ran`, Name: "虚构未运行任务", State: "disabled", LastResult: 0x41303}}}) {
			return
		}
		if !put(211, collector.InventorySnapshot{Kind: "startup", Scope: "user/fake/Run", Epoch: "0123456789abcdef", Entries: []collector.InventoryEntry{}}) {
			return
		}
		if err := f.db.Flush(r.Context()); err != nil {
			http.Error(w, "fake flush failed", 500)
			return
		}
		if err := f.broker.Publish("sample", map[string]string{"kind": "inventory"}); err != nil {
			http.Error(w, "fake event failed", 500)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
	runNativeWebBrowser(t, f, "inventory.spec.ts", handler)
	if f.files.writes != 0 || len(f.client.sent) != 0 || len(f.toast.values) != 0 {
		t.Fatal("read-only inventory UI changed configuration or sent notifications")
	}
	if _, found, err := f.db.Collector(context.Background(), "fake-host"); err != nil || !found {
		t.Fatal("inventory UI removed collector", err)
	}
}
