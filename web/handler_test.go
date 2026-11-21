// Copyright (c) 2026 LFen (https://lfen.ink) — lg-monitor

package web

import (
	"compress/gzip"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"
)

func testHandler() *handler {
	return &handler{files: fstest.MapFS{
		"index.html":     {Data: []byte(`<html><body><!--LGMON_BOOTSTRAP--></body></html>`)},
		"assets/fake.js": {Data: []byte("console.log('fixture')")},
		"package.json":   {Data: []byte("PRIVATE_SOURCE")},
	}}
}

func TestIndexSessionIsUncachedJSONAndCannotEscapeScript(t *testing.T) {
	h := testHandler()
	h.session = func(r *http.Request) (string, bool, error) {
		return `fake</script><script>alert(1)</script>`, r.RemoteAddr == "127.0.0.1:1234", nil
	}
	for _, peer := range []string{"127.0.0.1:1234", "192.0.2.1:1234"} {
		r := httptest.NewRequest("GET", "http://127.0.0.1/", nil)
		r.RemoteAddr = peer
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != 200 || w.Header().Get("Cache-Control") != "no-store" || !strings.Contains(w.Header().Get("Content-Security-Policy"), "script-src 'self'") || strings.Contains(w.Body.String(), "<script>alert") {
			t.Fatalf("unsafe index: %d %s", w.Code, w.Body.String())
		}
		if (peer == "127.0.0.1:1234") != strings.Contains(w.Body.String(), `"scope":"read"`) || peer != "127.0.0.1:1234" && strings.Contains(w.Body.String(), "fake") {
			t.Fatal("local session leaked to a remote visitor")
		}
	}
}

func TestAssetsNeverExposeSourcesDirectoriesOrUnsupportedMethods(t *testing.T) {
	h := testHandler()
	for _, tc := range []struct {
		method, path string
		code         int
	}{{"GET", "/assets/fake.js", 200}, {"HEAD", "/", 200}, {"GET", "/assets/", 404}, {"GET", "/package.json", 404}, {"GET", "/assets/../package.json", 404}, {"POST", "/", 405}, {"GET", "/missing", 404}} {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest(tc.method, "http://127.0.0.1"+tc.path, nil))
		if w.Code != tc.code || strings.Contains(w.Body.String(), "PRIVATE_SOURCE") || tc.method == "HEAD" && w.Body.Len() != 0 {
			t.Fatalf("%s %s: %d %s", tc.method, tc.path, w.Code, w.Body.String())
		}
	}
	h.err = errors.New("网页尚未构建")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("GET", "http://127.0.0.1/", nil))
	if w.Code != 503 || !strings.Contains(w.Body.String(), "尚未构建") {
		t.Fatal("missing build was disguised as an available page")
	}
}

func TestStaticAssetsServeCachedGzipWhenAccepted(t *testing.T) {
	payload := strings.Repeat("export const monitor = 'lgmon';\n", 120)
	h := &handler{files: fstest.MapFS{
		"assets/app.js": {Data: []byte(payload)},
	}}
	// Without Accept-Encoding, response stays uncompressed.
	plain := httptest.NewRecorder()
	h.ServeHTTP(plain, httptest.NewRequest("GET", "http://127.0.0.1/assets/app.js", nil))
	if plain.Code != 200 || plain.Header().Get("Content-Encoding") != "" || plain.Body.String() != payload {
		t.Fatalf("plain=%d enc=%q len=%d", plain.Code, plain.Header().Get("Content-Encoding"), plain.Body.Len())
	}
	// With Accept-Encoding: gzip, response is compressed and round-trips identically.
	req := httptest.NewRequest("GET", "http://127.0.0.1/assets/app.js", nil)
	req.Header.Set("Accept-Encoding", "gzip, deflate")
	gzRec := httptest.NewRecorder()
	h.ServeHTTP(gzRec, req)
	if gzRec.Code != 200 || gzRec.Header().Get("Content-Encoding") != "gzip" || gzRec.Body.Len() >= len(payload) {
		t.Fatalf("gzip=%d enc=%q len=%d raw=%d", gzRec.Code, gzRec.Header().Get("Content-Encoding"), gzRec.Body.Len(), len(payload))
	}
	reader, err := gzip.NewReader(gzRec.Body)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := io.ReadAll(reader)
	reader.Close()
	if err != nil || string(decoded) != payload {
		t.Fatalf("roundtrip mismatch: err=%v len=%d", err, len(decoded))
	}
}
