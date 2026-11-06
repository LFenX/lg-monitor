package api

import (
	"bytes"
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func authFixture(t *testing.T) (*Authenticator, *bytes.Buffer) {
	t.Helper()
	hosts, err := NewHostPolicy("127.0.0.1:7444", "http://monitor.example.test", []string{"100.64.0.4"})
	if err != nil {
		t.Fatal(err)
	}
	values := map[[32]byte]Principal{}
	for _, scope := range []Scope{ScopeRead, ScopeConfig, ScopeAdmin} {
		values[TokenHash("fake-"+string(scope))] = Principal{Name: "fake-" + string(scope), Scope: scope}
	}
	var logs bytes.Buffer
	auth, err := NewAuthenticator(AuthOptions{Tokens: TokenLookupFunc(func(_ context.Context, hash [32]byte) (Principal, bool, error) {
		value, ok := values[hash]
		return value, ok, nil
	}), Entropy: bytes.NewReader(make([]byte, 32)), Hosts: hosts, Logger: slog.New(slog.NewJSONHandler(&logs, nil))})
	if err != nil {
		t.Fatal(err)
	}
	return auth, &logs
}

func TestDeclaredScopeHierarchyAndHeaderOnlyAuthentication(t *testing.T) {
	auth, logs := authFixture(t)
	for _, minimum := range []Scope{ScopeRead, ScopeConfig, ScopeAdmin} {
		handler, err := auth.Protect(minimum, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if _, ok := CurrentPrincipal(r.Context()); !ok {
				t.Fatal("missing principal")
			}
			w.WriteHeader(204)
		}))
		if err != nil {
			t.Fatal(err)
		}
		for _, provided := range []string{"", "unknown", "read", "config", "admin"} {
			r := httptest.NewRequest("POST", "http://monitor.example.test/api/v1/fake?access_token=fake-admin", strings.NewReader("SECRET_BODY_MUST_NOT_BE_LOGGED"))
			r.RemoteAddr = "192.0.2.1:1234"
			if provided != "" {
				r.Header.Set("Authorization", "Bearer fake-"+provided)
			}
			w := httptest.NewRecorder()
			handler.ServeHTTP(w, r)
			want := 204
			if provided == "" || provided == "unknown" {
				want = 401
			} else if !Scope(provided).Allows(minimum) {
				want = 403
			}
			if w.Code != want {
				t.Fatalf("minimum=%s provided=%s got=%d body=%s", minimum, provided, w.Code, w.Body.String())
			}
			if want == 401 && !strings.Contains(w.Body.String(), "unauthorized") {
				t.Fatal("wrong authentication error")
			}
			if want == 403 && (!strings.Contains(w.Body.String(), "insufficient_scope") || !strings.Contains(w.Body.String(), "lgmon token create --scope")) {
				t.Fatal("permission error has no guidance")
			}
			if w.Header().Get("Access-Control-Allow-Origin") != "" {
				t.Fatal("CORS was enabled")
			}
		}
	}
	if strings.Contains(logs.String(), "SECRET_BODY_MUST_NOT_BE_LOGGED") || strings.Contains(logs.String(), "access_token") {
		t.Fatal("audit leaked request body or query")
	}
	if !strings.Contains(logs.String(), `"tokenName":"fake-config"`) || !strings.Contains(logs.String(), `"status":403`) {
		t.Fatal("privileged requests were not audited")
	}
	if _, err := auth.Protect("", http.NotFoundHandler()); err == nil {
		t.Fatal("undeclared permission accepted")
	}
}

func TestLocalSessionRequiresDirectLoopbackAndRemainsReadOnly(t *testing.T) {
	auth, _ := authFixture(t)
	local := httptest.NewRequest("GET", "http://127.0.0.1:7444/", nil)
	local.RemoteAddr = "127.0.0.1:1234"
	value, ok, err := auth.LocalSession(local)
	if err != nil || !ok || value == "" {
		t.Fatalf("local session not issued: %t %v", ok, err)
	}
	for _, scope := range []Scope{ScopeRead, ScopeConfig, ScopeAdmin} {
		handler, err := auth.Protect(scope, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(204) }))
		if err != nil {
			t.Fatal(err)
		}
		r := local.Clone(context.Background())
		r.Header.Set("Authorization", "Bearer "+value)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		want := 403
		if scope == ScopeRead {
			want = 204
		}
		if w.Code != want {
			t.Fatalf("local read session gained %s: %d", scope, w.Code)
		}
	}
	for _, proxy := range []string{"Forwarded", "X-Forwarded-For", "X-Forwarded-Host", "CF-Connecting-IP", "Via"} {
		r := local.Clone(context.Background())
		r.Header.Set(proxy, "fake-proxy")
		if _, ok, err := auth.LocalSession(r); err != nil || ok {
			t.Fatalf("proxied session issued: %s %v", proxy, err)
		}
	}
	public := local.Clone(context.Background())
	public.Host = "monitor.example.test"
	if _, ok, err := auth.LocalSession(public); err != nil || ok {
		t.Fatal("public hostname received loopback session")
	}
	remote := local.Clone(context.Background())
	remote.RemoteAddr = "192.0.2.1:1234"
	remote.Header.Set("Authorization", "Bearer "+value)
	handler, _ := auth.Protect(ScopeRead, http.NotFoundHandler())
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, remote)
	if w.Code != 401 {
		t.Fatal("local token replayed remotely")
	}
	cookie := local.Clone(context.Background())
	cookie.AddCookie(&http.Cookie{Name: "lgmon_local_session", Value: value})
	w = httptest.NewRecorder()
	handler.ServeHTTP(w, cookie)
	if w.Code != 401 || len(w.Result().Cookies()) != 0 {
		t.Fatal("Cookie authentication accepted")
	}
}

func TestHostAndOriginWhitelistRejectsDNSRebinding(t *testing.T) {
	auth, _ := authFixture(t)
	handler, _ := auth.Protect(ScopeRead, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(204) }))
	for _, tc := range []struct {
		host, origin string
		want         int
	}{{"evil.invalid:7444", "", 403}, {"127.0.0.1:7444", "http://evil.invalid", 403}, {"100.64.0.4:7444", "http://100.64.0.4:7444", 204}, {"[::1]:7444", "http://localhost:7444", 204}, {"monitor.example.test", "null", 403}} {
		r := httptest.NewRequest("GET", "http://127.0.0.1:7444/api/v1/targets", nil)
		r.Host = tc.host
		r.RemoteAddr = "127.0.0.1:1234"
		r.Header.Set("Authorization", "Bearer fake-read")
		if tc.origin != "" {
			r.Header.Set("Origin", tc.origin)
		}
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		if w.Code != tc.want || (tc.want == 403 && !strings.Contains(w.Body.String(), "forbidden_host")) {
			t.Fatalf("host/origin decision: %+v %d %s", tc, w.Code, w.Body.String())
		}
	}
}

func TestLocalSessionOnlyUsesTheThreeExplicitLoopbackHosts(t *testing.T) {
	auth, _ := authFixture(t)
	policy, err := NewHostPolicy("127.0.0.2:7444", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := auth.SetHosts(policy); err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest("GET", "http://127.0.0.2:7444/", nil)
	request.RemoteAddr = "127.0.0.1:1234"
	if _, ok, err := auth.LocalSession(request); err != nil || ok {
		t.Fatalf("unlisted loopback alias received local session: %t %v", ok, err)
	}
}
