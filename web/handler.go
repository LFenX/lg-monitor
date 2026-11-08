package web

import (
	"bytes"
	"encoding/json"
	"io/fs"
	"mime"
	"net/http"
	"path"
	"strings"
)

type Session func(*http.Request) (string, bool, error)

type handler struct {
	files   fs.FS
	err     error
	session Session
}

func NewHandler(session Session) http.Handler {
	files, err := assets()
	return &handler{files: files, err: err, session: session}
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
		}{}
		if h.session != nil {
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
	}
	if r.Method != http.MethodHead {
		_, _ = w.Write(data)
	}
}
