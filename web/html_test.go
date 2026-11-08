package web

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestHTMLFrameworkNeverBootstrapsASessionAndKeepsItsOwnSandbox(t *testing.T) {
	h := testHandler()
	h.session = func(*http.Request) (string, bool, error) {
		t.Fatal("framework requested a session token")
		return "", false, nil
	}
	for _, name := range []string{"/lgmon-frame.html", "/lgmon.js"} {
		for _, method := range []string{"GET", "HEAD"} {
			response := httptest.NewRecorder()
			h.ServeHTTP(response, httptest.NewRequest(method, "http://127.0.0.1"+name, nil))
			if response.Code != 200 || response.Header().Get("Cache-Control") != "no-store" || strings.Contains(response.Body.String(), "lgmon-session") || strings.Contains(response.Body.String(), "PRIVATE_SOURCE") || (method == "HEAD" && response.Body.Len() != 0) {
				t.Fatalf("%s %s => %d %s", method, name, response.Code, response.Body.String())
			}
			if name == "/lgmon-frame.html" {
				policy := response.Header().Get("Content-Security-Policy")
				if !strings.Contains(policy, "sandbox allow-scripts;") || strings.Contains(policy, "allow-same-origin") || !strings.Contains(policy, "connect-src 'none'") || !strings.Contains(policy, "frame-ancestors 'self'") || response.Header().Get("X-Frame-Options") != "" {
					t.Fatal("unsafe frame policy", policy)
				}
			}
		}
		for _, suffix := range []string{"?html=untrusted", "?token=fake-private"} {
			response := httptest.NewRecorder()
			h.ServeHTTP(response, httptest.NewRequest("GET", "http://127.0.0.1"+name+suffix, nil))
			if response.Code != 400 || strings.Contains(response.Body.String(), "untrusted") || strings.Contains(response.Body.String(), "fake-private") {
				t.Fatal("framework accepted source/token query")
			}
		}
		response := httptest.NewRecorder()
		h.ServeHTTP(response, httptest.NewRequest("POST", "http://127.0.0.1"+name, nil))
		if response.Code != 405 {
			t.Fatal("framework accepted write method")
		}
	}
	if strings.Contains(strings.ToLower(htmlSDK), "</script") {
		t.Fatal("embedded SDK can terminate its bootstrap script")
	}
}
