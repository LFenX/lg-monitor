// Copyright (c) 2026 LFen (https://lfen.ink) — lg-monitor

package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/LFenX/lg-monitor/internal/store"
)

type fixtureProtector struct{}

func (fixtureProtector) Encrypt(value []byte) ([]byte, error) {
	copy := bytes.Clone(value)
	for i := range copy {
		copy[i] ^= 0xa5
	}
	return copy, nil
}
func (p fixtureProtector) Decrypt(value []byte) ([]byte, error) { return p.Encrypt(value) }

func testLANKeyDB(t *testing.T, now func() time.Time) *store.AuthDB {
	t.Helper()
	db, err := store.OpenAuthDB(context.Background(), filepath.Join(t.TempDir(), "auth.db"), now)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func TestLANFullAccessLifecycle(t *testing.T) {
	at := time.Unix(2000, 0)
	now := func() time.Time { return at }
	db := testLANKeyDB(t, now)
	s := serverFixture(t, func(opts *ServerOptions) { opts.Auth.lanKeys = db; opts.Auth.now = now })
	auth := s.options.Auth
	const host = "127.0.0.1:5555"
	const peer = "192.168.1.3:5555"
	const route = "/api/v1/lan/access-key"
	const unlockRoute = "/api/v1/lan/unlock"
	const adminRoute = "/api/v1/config/settings"
	call := func(method, path, remote, token, body string, proxied bool, want int) *httptest.ResponseRecorder {
		t.Helper()
		urlHost := "100.64.0.4:7444"
		if remote == host {
			urlHost = "localhost:7444"
		}
		r := httptest.NewRequest(method, "http://"+urlHost+path, strings.NewReader(body))
		r.RemoteAddr = remote
		r.Header.Set("Authorization", "Bearer "+token)
		if proxied {
			r.Header.Set("Via", "proxy")
		}
		w := httptest.NewRecorder()
		s.ServeHTTP(w, r)
		if w.Code != want {
			t.Fatalf("%s %s from %s got %d, want %d", method, path, remote, w.Code, want)
		}
		if w.Header().Get("Cache-Control") != "no-store" {
			t.Fatal("credential response may be cached")
		}
		return w
	}
	auth.SetLANWeb(true)
	guest := auth.guest.Value
	unlock := func(key string, want int) string {
		t.Helper()
		body, _ := json.Marshal(LANUnlockInput{Key: key})
		w := call("POST", unlockRoute, peer, guest, string(body), false, want)
		if want != 200 {
			return ""
		}
		var result SessionResult
		if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil || result.Scope != ScopeAdmin || !strings.HasPrefix(result.Token, lanSessionPrefix) {
			t.Fatal("invalid session response", err)
		}
		return result.Token
	}
	generate := func(body string) LANKeyResult {
		t.Helper()
		w := call("POST", route, host, "fake-admin", body, false, 200)
		var result LANKeyResult
		if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil || result.Key == "" || !result.Active {
			t.Fatal("invalid generation response", err)
		}
		record, err := db.LANWebKey(context.Background())
		digest := TokenHash(result.Key)
		if err != nil || record.Protected == "" || strings.Contains(record.Protected, result.Key) || !bytes.Equal(record.Hash, digest[:]) {
			t.Fatal("stored key must be a digest", err)
		}
		if strings.Contains(call("GET", route, host, "fake-read", "", false, 200).Body.String(), result.Key) {
			t.Fatal("status leaked the key")
		}
		return result
	}
	unlock("unused", 403)
	call("POST", route, host, "fake-config", `{"expiresAt":0}`, false, 403)
	call("POST", route, peer, "fake-admin", `{"expiresAt":0}`, false, 403)
	call("DELETE", route, host, "fake-admin", "", true, 403)
	for _, invalid := range []string{`{}`, `{"expiresAt":null}`, `{"expiresAt":-1}`, `{"expiresAt":2000000}`} {
		call("POST", route, host, "fake-admin", invalid, false, 400)
	}
	first := generate(`{"expiresAt":0}`)
	if !strings.Contains(call("POST", route+"/reveal", host, "fake-admin", "", false, 200).Body.String(), first.Key) {
		t.Fatal("host could not retrieve the current key")
	}
	call("POST", route+"/reveal", host, "fake-read", "", false, 403)
	call("POST", route+"/reveal", peer, "fake-admin", "", false, 403)
	call("POST", route+"/reveal", host, "fake-admin", "", true, 403)
	unlock("incorrect", 403)
	session := unlock(first.Key, 200)
	call("PUT", adminRoute, peer, guest, "{}", false, 403)
	call("PUT", adminRoute, peer, first.Key, "{}", false, 401)
	w := call("PUT", adminRoute, peer, session, "{}", false, 200)
	if w.Header().Get("X-Lgmon-Scope") != "admin" || w.Header().Get("X-Lgmon-Guest") != "" {
		t.Fatal("unlocked principal is not an admin")
	}
	for _, addr := range []string{host, "203.0.113.1:1234", "192.168.1.9:9999"} {
		call("PUT", adminRoute, addr, session, "{}", false, 401)
	}
	call("PUT", adminRoute, peer, session, "{}", true, 401)
	call("DELETE", route, peer, session, "", false, 403)
	auth.SetLANWeb(false)
	call("PUT", adminRoute, peer, session, "{}", false, 401)
	auth.SetLANWeb(true)
	next := generate(`{"expiresAt":2020000}`)
	unlock(first.Key, 403)
	call("PUT", adminRoute, peer, session, "{}", false, 401)
	session = unlock(next.Key, 200)
	call("PUT", route, host, "fake-admin", `{"expiresAt":2010000}`, false, 200)
	call("PUT", adminRoute, peer, session, "{}", false, 200)
	at = at.Add(10 * time.Second)
	call("PUT", route, host, "fake-admin", `{"expiresAt":0}`, false, 409)
	unlock(next.Key, 403)
	call("POST", route+"/reveal", host, "fake-admin", "", false, 409)
	call("PUT", adminRoute, peer, session, "{}", false, 401)
	key := generate(`{"expiresAt":0}`).Key
	session = unlock(key, 200)
	// A restarted HTTP node uses the same local database but grants new browser sessions.
	s = serverFixture(t, func(opts *ServerOptions) { opts.Auth.lanKeys = db; opts.Auth.now = now })
	s.options.Auth.SetLANWeb(true)
	guest = s.options.Auth.guest.Value
	call("PUT", adminRoute, peer, session, "{}", false, 401)
	session = unlock(key, 200)
	call("DELETE", route, host, "fake-admin", "", false, 200)
	unlock(key, 403)
	call("PUT", adminRoute, peer, session, "{}", false, 401)
	call("POST", route+"/reveal", host, "fake-admin", "", false, 409)
	old := TokenHash("fake-previous-version-key")
	if err := db.SetLANWebKey(context.Background(), store.LANWebKey{Hash: old[:]}); err != nil {
		t.Fatal(err)
	}
	call("POST", route+"/reveal", host, "fake-admin", "", false, 409)
	unlock("fake-previous-version-key", 200)
	call("GET", "/api/v1/collectors", peer, guest, "", false, 200)
}
