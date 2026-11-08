package web

import (
	_ "embed"
	"net/http"
)

//go:embed lgmon.js
var htmlSDK string

const htmlFramePolicy = "sandbox allow-scripts; default-src 'none'; script-src 'self' 'unsafe-inline'; style-src 'unsafe-inline'; img-src data:; connect-src 'none'; frame-src 'none'; worker-src 'none'; object-src 'none'; base-uri 'none'; form-action 'none'; frame-ancestors 'self'"

func serveHTMLFramework(w http.ResponseWriter, r *http.Request) bool {
	if r.URL.Path != "/lgmon.js" && r.URL.Path != "/lgmon-frame.html" {
		return false
	}
	if r.URL.RawQuery != "" {
		http.Error(w, "HTML 框架资源不接受查询参数", http.StatusBadRequest)
		return true
	}
	w.Header().Set("Cache-Control", "no-store")
	content := htmlSDK
	if r.URL.Path == "/lgmon.js" {
		w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
	} else {
		w.Header().Del("X-Frame-Options")
		w.Header().Set("Content-Security-Policy", htmlFramePolicy)
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		content = `<!doctype html><html lang="zh-CN"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><title>HTML 面板</title><style>:root{color-scheme:light;--lgmon-bg:#fff;--lgmon-text:#17223b;--lgmon-border:#dde3eb}html[data-theme=dark]{color-scheme:dark;--lgmon-bg:#121a29;--lgmon-text:#e0e8f4;--lgmon-border:#354052}body{margin:16px;background:var(--lgmon-bg);color:var(--lgmon-text);font:14px/1.6 system-ui,sans-serif;overflow-wrap:anywhere}*{box-sizing:border-box}</style></head><body><main id="lgmon-content"></main><script>` + htmlSDK + `</script></body></html>`
	}
	if r.Method != http.MethodHead {
		_, _ = w.Write([]byte(content))
	}
	return true
}
