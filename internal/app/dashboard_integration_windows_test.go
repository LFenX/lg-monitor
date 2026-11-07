//go:build windows && integration && webui

package app

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/LFenX/lg-monitor/internal/rules"
)

func TestNativeDashboardBrowser(t *testing.T) {
	f := newAPIFixtureWithHTTPClock(t, time.Date(2026, 11, 7, 8, 30, 0, 0, time.UTC), time.Now)
	f.samples(t)
	alert := rules.Alert{ID: "fake-dashboard-alert", RuleID: "down", CollectorID: "fake-host", TargetID: "web", State: "firing", Severity: "critical", Summary: "虚构面板告警", StartedAt: f.now.Add(-5 * time.Second).UnixMilli()}
	if err := f.db.SaveTransition(context.Background(), alert, rules.Event{AlertID: alert.ID, TS: alert.StartedAt, Kind: "firing", Message: "虚构日志内容"}); err != nil {
		t.Fatal(err)
	}
	var overview string
	for _, id := range []string{"overview", "hosts", "diagnostics"} {
		data, err := os.ReadFile(filepath.Join("..", "..", "examples", "dashboards", id+".yaml"))
		if err != nil {
			t.Fatal(err)
		}
		f.files.MapFS["dashboards/"+id+".yaml"] = &fstest.MapFile{Data: data}
		if id == "overview" {
			overview = string(data)
		}
	}
	if err := f.manager.Reload(); err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	mux.Handle("/", f.server)
	// This control exists only on the test-owned listener and changes only its in-memory files.
	mux.HandleFunc("POST /__test/dashboards/{action}", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer fake-test-control" {
			http.Error(w, "forbidden", 403)
			return
		}
		source := overview
		switch r.PathValue("action") {
		case "reload":
			source = strings.Replace(source, "title: 运行概览", "title: 运行概览热更新", 1)
			source = strings.Replace(source, "count(*) AS value", "count(*)+1 AS value", 1)
		case "bad-columns":
			source = strings.Replace(source, "latency_ms AS value", "latency_ms AS wrong_value", 1)
		case "bad-file":
			source = "id: ["
		case "restore":
		case "delete":
			delete(f.files.MapFS, "dashboards/overview.yaml")
		default:
			http.Error(w, "unknown fake action", 400)
			return
		}
		if r.PathValue("action") != "delete" {
			f.files.MapFS["dashboards/overview.yaml"] = &fstest.MapFile{Data: []byte(source)}
		}
		if err := f.manager.Reload(); err != nil {
			http.Error(w, "fake reload failed", 500)
			return
		}
		w.WriteHeader(204)
	})
	runNativeWebBrowser(t, f, "dashboards.spec.ts", mux)
	if len(f.client.sent) != 0 || f.hookFactories != 0 || len(f.toast.values) != 0 {
		t.Fatal("dashboard browser attempted notifications")
	}
}
