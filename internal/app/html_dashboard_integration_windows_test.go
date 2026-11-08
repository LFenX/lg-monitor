//go:build windows && integration && webui

package app

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"testing/fstest"
	"time"

	"github.com/LFenX/lg-monitor/internal/collector"
	"github.com/LFenX/lg-monitor/internal/pipeline"
)

const htmlBrowserFixture = `<!doctype html><html lang="zh-CN"><head><style>
body{margin:20px;background:var(--lgmon-bg);color:var(--lgmon-text)}h1{font-size:24px}.tiles{display:grid;grid-template-columns:repeat(auto-fit,minmax(150px,1fr));gap:18px}.tile{padding:22px;border:1px solid var(--lgmon-border);border-radius:14px}strong{display:block;font-size:38px}svg{max-width:100%;height:auto}.muted{opacity:.65}
</style></head><body><h1>HTML 虚构大屏</h1><p class="muted">独立 HTML / SVG / JavaScript，只读数据与实时更新</p><section class="tiles"><article class="tile">目标状态<strong id="value">读取中</strong><span id="identity"></span></article><article class="tile">实时订阅<strong id="event">等待事件</strong><span id="connection"></span></article></section><svg viewBox="0 0 600 180" role="img" aria-label="虚构监控拓扑"><path d="M100 90H500" stroke="#558dff" stroke-width="4"/><circle cx="100" cy="90" r="45" fill="#447dff"/><circle cx="500" cy="90" r="45" fill="#24a57c"/><text x="100" y="96" text-anchor="middle" fill="white">采集器</text><text x="500" y="96" text-anchor="middle" fill="white">监控器</text></svg><script>
async function load() { const result = await lgmon.query("SELECT state, target_id FROM v_target_current WHERE (:collector IS NULL OR collector_id=:collector) AND ts<=:to ORDER BY target_id"); document.getElementById('value').textContent = result.rows[0] ? result.rows[0][0] : '无目标'; document.getElementById('identity').textContent = result.rows[0] ? result.rows[0][1] : ''; }
lgmon.subscribe('context', () => { load().catch(error => { document.getElementById('value').textContent = error.message; }); });
lgmon.subscribe('target', event => { document.getElementById('event').textContent = '实时：' + event.state; load().catch(error => { document.getElementById('value').textContent = error.message; }); });
lgmon.subscribe('connection', status => { document.getElementById('connection').textContent = status; });
</script></body></html>`

func TestNativeHTMLDashboardBrowser(t *testing.T) {
	f := newAPIFixtureWithHTTPClock(t, time.Date(2026, 11, 9, 4, 0, 0, 0, time.UTC), time.Now)
	f.samples(t)
	f.files.MapFS["dashboards/overview.html"] = &fstest.MapFile{Data: []byte(htmlBrowserFixture)}
	loadAPIDashboard(t, f, "id: overview\ntitle: HTML 虚构大屏\nrefresh: 10s\ndefaultRange: 1h\nhtml: overview.html\n")
	original := bytes.Clone(f.files.MapFS["targets.yaml"].Data)
	var streams atomic.Int32
	var directWrites atomic.Int32
	var streamMu sync.Mutex
	var streamCancel context.CancelFunc
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/api/v1/") && (r.Method == "PUT" || r.Method == "DELETE" || r.Method == "PATCH" || r.Method == "OPTIONS") {
			directWrites.Add(1)
		}
		if r.URL.Path == "/api/v1/stream" && r.Method == "GET" {
			streams.Add(1)
			streamCtx, cancel := context.WithCancel(r.Context())
			defer cancel()
			streamMu.Lock()
			streamCancel = cancel
			streamMu.Unlock()
			r = r.WithContext(streamCtx)
		}
		if strings.HasPrefix(r.URL.Path, "/__test/html/") {
			if r.Header.Get("Authorization") != "Bearer fake-test-control" {
				http.Error(w, "forbidden", 403)
				return
			}
			switch r.URL.Path {
			case "/__test/html/reconnect":
				streamMu.Lock()
				stop := streamCancel
				streamMu.Unlock()
				if stop != nil {
					stop()
				}
			case "/__test/html/stats":
				json.NewEncoder(w).Encode(map[string]int32{"streams": streams.Load()})
				return
			case "/__test/html/target":
				if err := f.db.Write(r.Context(), pipeline.Message{CollectorID: "fake-host", Seq: 7, TS: f.now.UnixMilli(), Kind: pipeline.KindTargetStatus, TargetStatus: &pipeline.TargetStatus{TargetID: "web", State: "down", Process: collector.ProcessMissing}}); err != nil {
					http.Error(w, "fake data write failed", 500)
					return
				}
				if err := f.db.Flush(r.Context()); err != nil {
					http.Error(w, "fake data flush failed", 500)
					return
				}
				if err := f.broker.Publish("target", map[string]any{"collectorId": "fake-host", "targetId": "web", "state": "down"}); err != nil {
					http.Error(w, "fake publish failed", 500)
					return
				}
			case "/__test/html/reload", "/__test/html/bad":
				content := []byte(strings.Replace(htmlBrowserFixture, "<h1>HTML 虚构大屏</h1>", "<h1>HTML 虚构大屏 · 更新</h1>", 1))
				if strings.HasSuffix(r.URL.Path, "/bad") {
					content = []byte{0xff}
				}
				f.files.MapFS["dashboards/overview.html"] = &fstest.MapFile{Data: content}
				if err := f.manager.Reload(); err != nil {
					http.Error(w, "fake reload failed", 500)
					return
				}
			default:
				http.NotFound(w, r)
				return
			}
			w.WriteHeader(204)
			return
		}
		f.server.ServeHTTP(w, r)
	})
	runNativeWebBrowser(t, f, "html.spec.ts", handler)
	if directWrites.Load() != 0 {
		t.Fatal("sandbox attempted direct HTTP mutation or preflight")
	}
	if _, known, err := f.view.Collector(context.Background(), "fake-host"); err != nil || !known {
		t.Fatal("SQL write escaped read-only source", err)
	}
	if f.files.writes != 0 || !bytes.Equal(original, f.files.MapFS["targets.yaml"].Data) || len(f.client.sent) != 0 || f.hookFactories != 0 || len(f.toast.values) != 0 {
		t.Fatal("custom HTML changed configuration or attempted notifications")
	}
}
