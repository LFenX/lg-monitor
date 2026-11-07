//go:build webui

package web

import (
	"io/fs"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestProductionBundleContainsEntryAndCanBeServedWithoutDisk(t *testing.T) {
	files, err := assets()
	if err != nil {
		t.Fatal(err)
	}
	data, err := fs.ReadFile(files, "index.html")
	if err != nil || !strings.Contains(string(data), "<!--LGMON_BOOTSTRAP-->") {
		t.Fatalf("missing production bootstrap entry: %v", err)
	}
	entries, err := fs.Glob(files, "assets/*.js")
	if err != nil || len(entries) == 0 {
		t.Fatal("production JavaScript is missing")
	}
	h := NewHandler(nil)
	for _, name := range append([]string{""}, entries...) {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest("GET", "http://127.0.0.1/"+name, nil))
		if w.Code != 200 || w.Body.Len() == 0 {
			t.Fatalf("embedded asset %s unavailable: %d", name, w.Code)
		}
	}
}
