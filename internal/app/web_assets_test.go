//go:build webui

package app

import (
	"net/http/httptest"
	"strings"
	"testing"
)

func TestEmbeddedPageUsesRealLoopbackSessionPolicyAndPreservesAPI404(t *testing.T) {
	f := newAPIFixture(t)
	for _, tc := range []struct {
		host, peer, path, proxy string
		status                  int
		local                   bool
	}{
		{"127.0.0.1:7444", "127.0.0.1:1234", "/", "", 200, true},
		{"127.0.0.1:7444", "192.0.2.1:1234", "/", "", 200, false},
		{"127.0.0.1:7444", "127.0.0.1:1234", "/", "192.0.2.1", 200, false},
		{"evil.example.test", "127.0.0.1:1234", "/", "", 403, false},
		{"127.0.0.1:7444", "127.0.0.1:1234", "/api/v1/missing", "", 404, false},
		{"127.0.0.1:7444", "127.0.0.1:1234", "/package.json", "", 404, false},
	} {
		r := httptest.NewRequest("GET", "http://"+tc.host+tc.path, nil)
		r.RemoteAddr = tc.peer
		if tc.proxy != "" {
			r.Header.Set("X-Forwarded-For", tc.proxy)
		}
		w := httptest.NewRecorder()
		f.server.ServeHTTP(w, r)
		if w.Code != tc.status || strings.Contains(w.Body.String(), `"scope":"read"`) != tc.local {
			t.Fatalf("%s %s: %d local=%v", tc.host, tc.path, w.Code, tc.local)
		}
		if tc.path == "/api/v1/missing" && !strings.Contains(w.Header().Get("Content-Type"), "application/json") {
			t.Fatal("unknown API was replaced with HTML")
		}
	}
}
