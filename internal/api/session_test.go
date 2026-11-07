package api

import (
	"encoding/json"
	"net/http/httptest"
	"testing"
)

func TestSessionIsSameReadOnlyLocalSessionAndRejectsNonlocalAccess(t *testing.T) {
	s := serverFixture(t, nil)
	w := apiRequest(s, "GET", "/api/v1/session", "", nil)
	var result SessionResult
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &result) != nil || result.Scope != ScopeRead || result.Token == "" {
		t.Fatalf("session: %d %s", w.Code, w.Body.String())
	}
	request := httptest.NewRequest("GET", "http://127.0.0.1:7444/", nil)
	request.RemoteAddr = "127.0.0.1:1234"
	expected, local, err := s.options.Auth.LocalSession(request)
	if err != nil || !local || result.Token != expected {
		t.Fatal("HTML and tray session differ")
	}
	for _, method := range []string{"PUT", "DELETE"} {
		w := apiRequest(s, method, "/api/v1/notifications/pause", result.Token, []byte(`{"for":"1h"}`))
		if w.Code != 403 {
			t.Fatalf("read session changed global pause: %d %s", w.Code, w.Body.String())
		}
	}
	for _, tc := range []struct{ host, peer, header string }{
		{"127.0.0.1:7444", "192.0.2.9:1234", ""},
		{"fake.invalid:7444", "127.0.0.1:1234", ""},
		{"127.0.0.1:7444", "127.0.0.1:1234", "Forwarded"},
		{"127.0.0.1:7444", "127.0.0.1:1234", "X-Forwarded-For"},
		{"127.0.0.1:7444", "127.0.0.1:1234", "X-Forwarded-Host"},
		{"127.0.0.1:7444", "127.0.0.1:1234", "X-Real-IP"},
	} {
		r := httptest.NewRequest("GET", "http://"+tc.host+"/api/v1/session", nil)
		r.RemoteAddr = tc.peer
		if tc.header != "" {
			r.Header.Set(tc.header, "fake")
		}
		w := httptest.NewRecorder()
		s.ServeHTTP(w, r)
		if w.Code != 403 || containsToken(w.Body.String(), result.Token) {
			t.Fatalf("nonlocal session leaked: %+v %d %s", tc, w.Code, w.Body.String())
		}
	}
}

func containsToken(body, token string) bool {
	var value SessionResult
	return json.Unmarshal([]byte(body), &value) == nil && value.Token == token
}
