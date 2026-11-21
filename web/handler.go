// Copyright (c) 2026 LFen (https://lfen.ink) — lg-monitor

package web

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"io/fs"
	"mime"
	"net/http"
	"path"
	"strconv"
	"strings"
	"sync"
)

type Session func(*http.Request) (string, bool, error)

// WebSession resolves either the local owner session or a read-only LAN visitor session.
type WebSession func(*http.Request) (token string, local bool, guest bool, err error)

type handler struct {
	files      fs.FS
	err        error
	session    Session
	webSession WebSession
	gzipCache  sync.Map // asset path -> []byte (gzipped)
}

func acceptsGzip(r *http.Request) bool {
	for _, part := range strings.Split(r.Header.Get("Accept-Encoding"), ",") {
		token, params, err := mime.ParseMediaType(strings.TrimSpace(part))
		if err != nil || token != "gzip" {
			continue
		}
		if q, exists := params["q"]; exists {
			quality, err := strconv.ParseFloat(q, 64)
			return err == nil && quality > 0 && quality <= 1
		}
		return true
	}
	return false
}

func compressibleAsset(name string) bool {
	switch strings.ToLower(path.Ext(name)) {
	case ".js", ".mjs", ".css", ".svg", ".json", ".txt", ".html":
		return true
	}
	return false
}

func (h *handler) gzippedAsset(name string, raw []byte) ([]byte, bool) {
	if len(raw) < 1024 || !compressibleAsset(name) {
		return nil, false
	}
	if cached, ok := h.gzipCache.Load(name); ok {
		out := cached.([]byte)
		return out, len(out) > 0
	}
	var buf bytes.Buffer
	gz, err := gzip.NewWriterLevel(&buf, gzip.BestSpeed)
	if err != nil {
		return nil, false
	}
	if _, err := gz.Write(raw); err != nil {
		return nil, false
	}
	if err := gz.Close(); err != nil {
		return nil, false
	}
	out := buf.Bytes()
	if len(out) >= len(raw) {
		h.gzipCache.Store(name, []byte(nil))
		return nil, false
	}
	h.gzipCache.Store(name, out)
	return out, true
}

func NewHandler(session Session) http.Handler {
	files, err := assets()
	return &handler{files: files, err: err, session: session}
}

func NewHandlerWithWebSession(session WebSession) http.Handler {
	files, err := assets()
	return &handler{files: files, err: err, webSession: session}
}

func (h *handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("X-Frame-Options", "DENY")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'; img-src 'self' data:; font-src 'self'; connect-src 'self'; frame-src 'self'; object-src 'none'; base-uri 'none'; frame-ancestors 'none'; form-action 'self'")
	w.Header().Set("Cache-Control", "no-store")
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		http.Error(w, "网页只接受 GET 或 HEAD", http.StatusMethodNotAllowed)
		return
	}
	if serveHTMLFramework(w, r) {
		return
	}
	name := strings.TrimPrefix(r.URL.Path, "/")
	index := name == ""
	if !index && (!strings.HasPrefix(name, "assets/") || !fs.ValidPath(name) || path.Clean(name) != name) {
		http.NotFound(w, r)
		return
	}
	if h.err != nil {
		http.Error(w, h.err.Error(), http.StatusServiceUnavailable)
		return
	}
	if index {
		name = "index.html"
	}
	entry, err := fs.Stat(h.files, name)
	if err != nil || entry.IsDir() {
		http.NotFound(w, r)
		return
	}
	data, err := fs.ReadFile(h.files, name)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	if index {
		bootstrap := struct {
			Token string `json:"token"`
			Scope string `json:"scope"`
			Guest bool   `json:"guest,omitempty"`
		}{}
		if h.webSession != nil {
			token, local, guest, err := h.webSession(r)
			if err != nil {
				http.Error(w, "无法读取本机网页会话", http.StatusForbidden)
				return
			}
			if local {
				bootstrap.Token, bootstrap.Scope = token, "read"
			} else if guest {
				bootstrap.Token, bootstrap.Scope, bootstrap.Guest = token, "read", true
			}
		} else if h.session != nil {
			token, local, err := h.session(r)
			if err != nil {
				http.Error(w, "无法读取本机网页会话", http.StatusForbidden)
				return
			}
			if local {
				bootstrap.Token, bootstrap.Scope = token, "read"
			}
		}
		encoded, _ := json.Marshal(bootstrap)
		data = bytes.Replace(data, []byte("<!--LGMON_BOOTSTRAP-->"), append(append([]byte(`<script id="lgmon-session" type="application/json">`), encoded...), []byte("</script>")...), 1)
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
	} else {
		w.Header().Set("Content-Type", mime.TypeByExtension(path.Ext(name)))
		w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		if compressibleAsset(name) {
			w.Header().Add("Vary", "Accept-Encoding")
			if acceptsGzip(r) {
				if gz, ok := h.gzippedAsset(name, data); ok {
					w.Header().Set("Content-Encoding", "gzip")
					data = gz
				}
			}
		}
	}
	w.Header().Set("Content-Length", strconv.Itoa(len(data)))
	if r.Method != http.MethodHead {
		_, _ = w.Write(data)
	}
}
