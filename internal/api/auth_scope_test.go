package api

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestBrowserScopeHintIsAuthenticatedAndCannotGrantPermissions(t *testing.T) {
	auth, _ := authFixture(t)
	handler, err := auth.Protect(ScopeConfig, http.HandlerFunc(func(http.ResponseWriter, *http.Request) { t.Error("read credential gained config authority") }))
	if err != nil {
		t.Fatal(err)
	}
	for _, token := range []string{"fake-read", "fake-invalid"} {
		r := httptest.NewRequest("POST", "http://127.0.0.1:7444/api/v1/fake", nil)
		r.RemoteAddr = "127.0.0.1:1234"
		r.Header.Set("Authorization", "Bearer "+token)
		r.Header.Set("X-Lgmon-Scope", "admin")
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		status, hint := 403, "read"
		if token == "fake-invalid" {
			status, hint = 401, ""
		}
		if w.Code != status || w.Header().Get("X-Lgmon-Scope") != hint {
			t.Fatalf("status=%d hint=%q", w.Code, w.Header().Get("X-Lgmon-Scope"))
		}
	}
}
