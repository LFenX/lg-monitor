package api

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"
)

func TestUILangSyncAndGuestReadOnly(t *testing.T) {
	langFile := filepath.Join(t.TempDir(), "data", "ui-lang")
	broker := NewBroker()
	defer broker.Close()

	policy, err := NewHostPolicy("0.0.0.0:7444", "", []string{"192.168.1.10"})
	if err != nil {
		t.Fatalf("NewHostPolicy: %v", err)
	}
	auth, err := NewAuthenticator(AuthOptions{
		Tokens:  TokenLookupFunc(func(context.Context, [32]byte) (Principal, bool, error) { return Principal{}, false, nil }),
		Entropy: rand.Reader,
		Hosts:   policy,
		Now:     time.Now,
	})
	if err != nil {
		t.Fatalf("NewAuthenticator: %v", err)
	}
	auth.SetLANWeb(true)

	actions := map[Operation]Action{}
	for _, r := range Routes() {
		actions[r.ID] = func(context.Context, Request) (any, error) { return map[string]bool{"ok": true}, nil }
	}

	srv, err := NewServer(ServerOptions{
		Auth:     auth,
		Broker:   broker,
		Now:      time.Now,
		Actions:  actions,
		LangFile: langFile,
	})
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}

	// Obtain local session token.
	localReq := httptest.NewRequest(http.MethodGet, "http://127.0.0.1:7444/api/v1/session", nil)
	localReq.RemoteAddr = "127.0.0.1:50000"
	localToken, localOK, err := auth.LocalSession(localReq)
	if err != nil || !localOK {
		t.Fatalf("LocalSession failed: %v %v", localOK, err)
	}

	// Default language is zh.
	getReq := httptest.NewRequest(http.MethodGet, "http://127.0.0.1:7444/api/v1/ui/lang", nil)
	getReq.RemoteAddr = "127.0.0.1:50000"
	getReq.Header.Set("Authorization", "Bearer "+localToken)
	getRec := httptest.NewRecorder()
	srv.ServeHTTP(getRec, getReq)
	if getRec.Code != 200 {
		t.Fatalf("GET /api/v1/ui/lang = %d: %s", getRec.Code, getRec.Body.String())
	}
	var got uiLangPayload
	if err := json.Unmarshal(getRec.Body.Bytes(), &got); err != nil || got.Lang != "zh" {
		t.Fatalf("expected default lang zh, got %+v (err=%v)", got, err)
	}

	// Subscribe to broker to verify SSE broadcast.
	ch, unsub := broker.subscribe(true, time.Now())
	defer unsub()

	// Local PUT switches to en, persists to file, and broadcasts SSE event.
	putReq := httptest.NewRequest(http.MethodPut, "http://127.0.0.1:7444/api/v1/ui/lang", bytes.NewBufferString(`{"lang":"en"}`))
	putReq.RemoteAddr = "127.0.0.1:50000"
	putReq.Header.Set("Authorization", "Bearer "+localToken)
	putRec := httptest.NewRecorder()
	srv.ServeHTTP(putRec, putReq)
	if putRec.Code != 200 {
		t.Fatalf("PUT /api/v1/ui/lang = %d: %s", putRec.Code, putRec.Body.String())
	}

	select {
	case ev := <-ch:
		if ev.Name != "lang" {
			t.Fatalf("expected SSE event name lang, got %q", ev.Name)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for SSE lang broadcast")
	}

	// Persisted across reloads.
	reloaded := newUILangStore(langFile)
	if reloaded.get() != "en" {
		t.Fatalf("expected persisted lang en, got %q", reloaded.get())
	}

	// LAN guest cannot mutate host UI language.
	guestReq := httptest.NewRequest(http.MethodGet, "http://192.168.1.10:7444/", nil)
	guestReq.RemoteAddr = "192.168.1.55:51234"
	guestToken, _, guest, err := auth.WebSession(guestReq)
	if err != nil || guestToken == "" || !guest {
		t.Fatalf("expected LAN guest session, got token=%q guest=%v err=%v", guestToken, guest, err)
	}
	guestPut := httptest.NewRequest(http.MethodPut, "http://192.168.1.10:7444/api/v1/ui/lang", bytes.NewBufferString(`{"lang":"zh"}`))
	guestPut.RemoteAddr = "192.168.1.55:51234"
	guestPut.Header.Set("Authorization", "Bearer "+guestToken)
	guestRec := httptest.NewRecorder()
	srv.ServeHTTP(guestRec, guestPut)
	if guestRec.Code != 403 {
		t.Fatalf("expected 403 for LAN guest PUT /api/v1/ui/lang, got %d: %s", guestRec.Code, guestRec.Body.String())
	}
}
