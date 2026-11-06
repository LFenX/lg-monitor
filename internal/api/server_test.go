package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func serverFixture(t *testing.T, configure func(*ServerOptions)) *Server {
	t.Helper()
	auth, _ := authFixture(t)
	opts := ServerOptions{Auth: auth, Broker: NewBroker(), Now: time.Now, Actions: map[Operation]Action{}}
	for _, route := range Routes() {
		opts.Actions[route.ID] = func(context.Context, Request) (any, error) { return Confirmation{true, "测试完成"}, nil }
	}
	if configure != nil {
		configure(&opts)
	}
	server, err := NewServer(opts)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(opts.Broker.Close)
	return server
}

func routePath(route Route) string {
	return strings.NewReplacer("{collector}", "fake-host", "{target}", "fake-target", "{id}", "fake-id").Replace(route.Path)
}

func apiRequest(s *Server, method, path, token string, body []byte) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, "http://127.0.0.1:7444"+path, bytes.NewReader(body))
	r.RemoteAddr = "127.0.0.1:1234"
	if token != "" {
		r.Header.Set("Authorization", "Bearer "+token)
	}
	ctx, cancel := context.WithCancel(r.Context())
	if path == "/api/v1/stream" {
		cancel()
	} else {
		defer cancel()
	}
	w := httptest.NewRecorder()
	s.ServeHTTP(w, r.WithContext(ctx))
	return w
}

func TestEveryRouteHasCorrectPermissionAndOpenAPIOperation(t *testing.T) {
	s := serverFixture(t, nil)
	for _, route := range Routes() {
		for _, role := range []string{"", "invalid", "read", "config", "admin"} {
			t.Run(string(route.ID)+"/"+role, func(t *testing.T) {
				token := ""
				if role != "" {
					token = "fake-" + role
				}
				w := apiRequest(s, route.Method, routePath(route), token, []byte(`{}`))
				want := 200
				if route.ID == Query {
					want = 501
				}
				if role == "" || role == "invalid" {
					want = 401
				} else if !Scope(role).Allows(route.Scope) {
					want = 403
				}
				if w.Code != want {
					t.Fatalf("status=%d want=%d body=%s", w.Code, want, w.Body.String())
				}
				if w.Header().Get("Access-Control-Allow-Origin") != "" {
					t.Fatal("CORS is enabled")
				}
				if want != 200 && !json.Valid(w.Body.Bytes()) {
					t.Fatal("non-JSON failure")
				}
				if route.ID == TestNotifier && want == 200 {
					s.notifierTests = map[string]time.Time{}
				}
			})
		}
	}
	w := apiRequest(s, "GET", "/api/v1/openapi.json", "fake-read", nil)
	var spec struct {
		Version string `json:"openapi"`
		Paths   map[string]map[string]struct {
			ID          Operation       `json:"operationId"`
			Scope       Scope           `json:"x-lgmon-scope"`
			RequestBody json.RawMessage `json:"requestBody"`
		} `json:"paths"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &spec); err != nil {
		t.Fatal(err)
	}
	if spec.Version != "3.1.0" {
		t.Fatal("wrong OpenAPI version")
	}
	count := 0
	for _, methods := range spec.Paths {
		count += len(methods)
	}
	if count != len(Routes()) {
		t.Fatalf("documented operations=%d registered=%d", count, len(Routes()))
	}
	for _, route := range Routes() {
		op := spec.Paths[route.Path][strings.ToLower(route.Method)]
		if op.ID != route.ID || op.Scope != route.Scope {
			t.Fatalf("OpenAPI diverges: %+v", route)
		}
		if route.Request != nil && len(op.RequestBody) == 0 {
			t.Fatalf("request model missing: %s", route.ID)
		}
	}
	if !bytes.Contains(w.Body.Bytes(), []byte(`"writeOnly":true`)) {
		t.Fatal("credential model is not write-only")
	}
}

func TestLocalSessionCannotMutateAnyRoute(t *testing.T) {
	s := serverFixture(t, nil)
	r := httptest.NewRequest("GET", "http://localhost:7444/", nil)
	r.RemoteAddr = "127.0.0.1:1234"
	token, ok, err := s.options.Auth.LocalSession(r)
	if err != nil || !ok {
		t.Fatal(err)
	}
	for _, route := range Routes() {
		if route.Scope == ScopeRead {
			continue
		}
		w := apiRequest(s, route.Method, routePath(route), token, []byte(`{}`))
		if w.Code != 403 || !strings.Contains(w.Body.String(), "insufficient_scope") {
			t.Fatalf("local read session mutated %s: %d", route.ID, w.Code)
		}
	}
}

func TestRouteRegistrationRejectsMissingPermissionsAndHandlers(t *testing.T) {
	s := serverFixture(t, nil)
	bad := Routes()
	bad[0].Scope = ""
	if _, err := newServer(s.options, bad); err == nil {
		t.Fatal("route without scope started")
	}
	delete(s.options.Actions, ListCollectors)
	if _, err := NewServer(s.options); err == nil {
		t.Fatal("route without handler started")
	}
}

func TestJSONErrorsBodyBoundAndPrivilegedFailuresAreAudited(t *testing.T) {
	auth, logs := authFixture(t)
	s := serverFixture(t, func(opts *ServerOptions) {
		opts.Auth = auth
		opts.Actions[PutSettingsConfig] = func(context.Context, Request) (any, error) { return nil, fmt.Errorf("PRIVATE_BACKEND_RESPONSE") }
		opts.Actions[PutTemplate] = func(context.Context, Request) (any, error) { panic("PRIVATE_PANIC") }
	})
	for _, tc := range []struct {
		method, path string
		status       int
		body         []byte
	}{
		{"GET", "/api/v1/tokens", 404, nil}, {"HEAD", "/api/v1/stream", 405, nil}, {"POST", "/api/v1/collectors", 405, nil},
		{"GET", "/api//v1/collectors", 400, nil}, {"PUT", "/api/v1/config/settings", 500, []byte(`{}`)},
		{"PUT", "/api/v1/templates/fake", 500, []byte(`{}`)}, {"PUT", "/api/v1/config/targets", 413, bytes.Repeat([]byte("x"), MaxRequestBody+1)},
	} {
		w := apiRequest(s, tc.method, tc.path, "fake-admin", tc.body)
		if w.Code != tc.status || !json.Valid(w.Body.Bytes()) || strings.Contains(w.Body.String(), "PRIVATE_") {
			t.Fatalf("error response %s: %d %s", tc.path, w.Code, w.Body.String())
		}
	}
	r := httptest.NewRequest("PUT", "http://attacker.example.test/api/v1/config/settings", strings.NewReader("PRIVATE_BODY"))
	r.Header.Set("Authorization", "Bearer fake-admin")
	w := httptest.NewRecorder()
	s.ServeHTTP(w, r)
	if w.Code != 403 || !strings.Contains(w.Body.String(), "forbidden_host") {
		t.Fatal("DNS rebinding accepted")
	}
	if !strings.Contains(logs.String(), `"status":500`) || !strings.Contains(logs.String(), `"status":403`) || strings.Contains(logs.String(), "PRIVATE_") {
		t.Fatalf("audit missing or contains body: %s", logs.String())
	}
}

func TestNotifierTestIsLimitedPerChannelForTenSeconds(t *testing.T) {
	now := time.UnixMilli(2000000000000)
	calls := 0
	s := serverFixture(t, func(opts *ServerOptions) {
		opts.Now = func() time.Time { return now }
		opts.Actions[TestNotifier] = func(context.Context, Request) (any, error) { calls++; return Confirmation{true, "已发送"}, nil }
	})
	for _, id := range []string{"one", "two"} {
		path := "/api/v1/notifiers/" + id + "/test"
		if got := apiRequest(s, "POST", path, "fake-config", nil); got.Code != 200 {
			t.Fatal(got.Body.String())
		}
		if got := apiRequest(s, "POST", path, "fake-config", nil); got.Code != 429 || got.Header().Get("Retry-After") != "10" {
			t.Fatal("second test escaped rate limit")
		}
	}
	now = now.Add(10 * time.Second)
	if got := apiRequest(s, "POST", "/api/v1/notifiers/one/test", "fake-config", nil); got.Code != 200 || calls != 3 {
		t.Fatal("channel was not released after ten seconds")
	}
}

func TestOnlyOneRuleReplayCanRunAndCancellationReleasesSlot(t *testing.T) {
	started := make(chan struct{}, 1)
	var count atomic.Int32
	s := serverFixture(t, func(opts *ServerOptions) {
		opts.Actions[TestRule] = func(ctx context.Context, _ Request) (any, error) {
			if count.Add(1) == 1 {
				started <- struct{}{}
				<-ctx.Done()
				return nil, ctx.Err()
			}
			return Confirmation{true, "完成"}, nil
		}
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	r := httptest.NewRequest("POST", "http://127.0.0.1:7444/api/v1/rules/one/test", nil).WithContext(ctx)
	r.Header.Set("Authorization", "Bearer fake-read")
	done := make(chan struct{})
	go func() { defer close(done); s.ServeHTTP(httptest.NewRecorder(), r) }()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("replay did not start")
	}
	if got := apiRequest(s, "POST", "/api/v1/rules/two/test", "fake-read", nil); got.Code != 429 {
		t.Fatal("concurrent replay was not rejected")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("cancelled replay held its slot")
	}
	if got := apiRequest(s, "POST", "/api/v1/rules/two/test", "fake-read", nil); got.Code != 200 || count.Load() != 2 {
		t.Fatal("replay slot was not reusable")
	}
}

func TestDecodeBodyRejectsNonObjectsUnknownFieldsAndTrailingData(t *testing.T) {
	for _, input := range []string{"", "null", "[]", `{"unexpected":true}`, `{} {}`} {
		var result Confirmation
		if err := DecodeBody([]byte(input), &result); err == nil {
			t.Fatalf("invalid input accepted: %q", input)
		}
	}
}
