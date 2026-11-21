package api

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestLANWebVisitorSessionIsReadOnlyAndTracked(t *testing.T) {
	now := time.UnixMilli(2_000_000_000_000)
	policy, err := NewHostPolicy("0.0.0.0:7444", "", []string{"192.168.1.10"})
	if err != nil {
		t.Fatal(err)
	}
	auth, err := NewAuthenticator(AuthOptions{
		Tokens:  TokenLookupFunc(func(context.Context, [32]byte) (Principal, bool, error) { return Principal{}, false, nil }),
		Entropy: rand.Reader,
		Hosts:   policy,
		Now:     func() time.Time { return now },
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := auth.SetLocalScope(ScopeConfig); err != nil {
		t.Fatal(err)
	}
	// Disabled by default: LAN peer gets no guest session.
	lanReq := httptest.NewRequest(http.MethodGet, "http://192.168.1.10:7444/", nil)
	lanReq.RemoteAddr = "192.168.1.42:54321"
	if token, local, guest, err := auth.WebSession(lanReq); err != nil || token != "" || local || guest {
		t.Fatalf("disabled LAN web returned token=%q local=%v guest=%v err=%v", token, local, guest, err)
	}

	auth.SetLANWeb(true)
	guestToken, local, guest, err := auth.WebSession(lanReq)
	if err != nil || guestToken == "" || local || !guest {
		t.Fatalf("enabled LAN web returned token=%q local=%v guest=%v err=%v", guestToken, local, guest, err)
	}

	// Public IPs and proxied requests must never receive a guest session.
	pubReq := httptest.NewRequest(http.MethodGet, "http://192.168.1.10:7444/", nil)
	pubReq.RemoteAddr = "203.0.113.5:12345"
	if token, _, guest, err := auth.WebSession(pubReq); err != nil || token != "" || guest {
		t.Fatalf("public IP got guest session: token=%q guest=%v err=%v", token, guest, err)
	}
	proxyReq := httptest.NewRequest(http.MethodGet, "http://192.168.1.10:7444/", nil)
	proxyReq.RemoteAddr = "192.168.1.42:54321"
	proxyReq.Header.Set("X-Forwarded-For", "192.168.1.99")
	if token, _, guest, err := auth.WebSession(proxyReq); err != nil || token != "" || guest {
		t.Fatalf("proxied request got guest session: token=%q guest=%v err=%v", token, guest, err)
	}

	// Read route succeeds with X-Lgmon-Guest: 1 and strictly ScopeRead.
	readHandler, err := auth.Protect(ScopeRead, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		principal, _ := CurrentPrincipal(r.Context())
		writeJSON(w, 200, principal)
	}))
	if err != nil {
		t.Fatal(err)
	}
	apiRead := httptest.NewRequest(http.MethodGet, "http://192.168.1.10:7444/api/v1/boards", nil)
	apiRead.RemoteAddr = "192.168.1.42:54322"
	apiRead.Header.Set("Authorization", "Bearer "+guestToken)
	rec := httptest.NewRecorder()
	readHandler.ServeHTTP(rec, apiRead)
	if rec.Code != 200 || rec.Header().Get("X-Lgmon-Scope") != "read" || rec.Header().Get("X-Lgmon-Guest") != "1" {
		t.Fatalf("guest read: code=%d scope=%q guest=%q body=%s", rec.Code, rec.Header().Get("X-Lgmon-Scope"), rec.Header().Get("X-Lgmon-Guest"), rec.Body.String())
	}
	var got Principal
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil || got.Scope != ScopeRead || !got.Guest || got.Local {
		t.Fatalf("guest principal=%+v err=%v", got, err)
	}

	// Config route must reject guest token with 403 even when localScope is config.
	cfgHandler, err := auth.Protect(ScopeConfig, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(200)
	}))
	if err != nil {
		t.Fatal(err)
	}
	apiWrite := httptest.NewRequest(http.MethodPut, "http://192.168.1.10:7444/api/v1/profile", nil)
	apiWrite.RemoteAddr = "192.168.1.42:54323"
	apiWrite.Header.Set("Authorization", "Bearer "+guestToken)
	recWrite := httptest.NewRecorder()
	cfgHandler.ServeHTTP(recWrite, apiWrite)
	if recWrite.Code != 403 {
		t.Fatalf("guest write must be forbidden, got %d %s", recWrite.Code, recWrite.Body.String())
	}

	// Visitor tracker lists active visitor with deterministic avatar, and expires after 90s idle.
	visitors := auth.Visitors()
	if len(visitors) != 1 || visitors[0].IP != "192.168.1.42" || visitors[0].Avatar == "" {
		t.Fatalf("visitors=%+v", visitors)
	}
	leave := auth.EnterGuestStream("192.168.1.42")
	now = now.Add(2 * time.Minute)
	if live := auth.Visitors(); len(live) != 1 || live[0].Connections != 1 {
		t.Fatalf("live SSE visitor should remain active: %+v", live)
	}
	leave()
	now = now.Add(2 * time.Minute)
	if expired := auth.Visitors(); len(expired) != 0 {
		t.Fatalf("idle visitor should expire: %+v", expired)
	}
}
