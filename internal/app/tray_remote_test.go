package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/LFenX/lg-monitor/internal/store"
)

type fakeTrayCredentials struct {
	address string
	token   []byte
	err     error
}

func (s *fakeTrayCredentials) Load() (string, []byte, error) {
	return s.address, append([]byte(nil), s.token...), s.err
}

func TestRemoteTrayReadSessionSetupRevocationAndPauseResume(t *testing.T) {
	var mu sync.Mutex
	revoked, sessionAllowed := false, true
	paused := false
	var methods []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		w.Header().Set("X-Lgmon-Scope", "read")
		token := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		if r.URL.Path == "/api/v1/session" {
			if !sessionAllowed {
				w.WriteHeader(403)
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]string{"token": "fake-read", "scope": "read"})
			return
		}
		if token == "fake-config" && !revoked {
			w.Header().Set("X-Lgmon-Scope", "config")
		} else if token != "fake-read" {
			w.WriteHeader(401)
			return
		}
		switch r.URL.Path {
		case "/api/v1/alerts":
			if r.URL.Query().Get("state") != "firing" {
				t.Error("missing firing filter")
			}
			_, _ = w.Write([]byte(`[{"state":"firing","severity":"critical"},{"state":"firing","severity":"warning"}]`))
		case "/api/v1/notifications/pause":
			if r.Method != "GET" {
				if token != "fake-config" {
					w.WriteHeader(403)
					return
				}
				methods = append(methods, r.Method)
				paused = r.Method == "PUT"
			}
			until := int64(4000000)
			start := int64(1000)
			state := store.NotificationPause{Paused: paused}
			if paused {
				state.Until = &until
				state.StartedAt = &start
			}
			_ = json.NewEncoder(w).Encode(state)
		default:
			w.WriteHeader(404)
		}
	}))
	defer server.Close()
	source := &fakeTrayCredentials{address: server.URL}
	backend := NewRemoteTray(source, server.Client().Transport)
	ctx := context.Background()
	state, err := backend.Status(ctx)
	if err != nil || state.Firing != 2 || state.Critical != 1 || state.CanPause || state.Help != TraySetupHint {
		t.Fatalf("read-only: %+v %v", state, err)
	}
	if err := backend.Pause(ctx, time.Hour); err == nil {
		t.Fatal("missing token changed pause")
	}
	source.err = errors.New("fake decrypt failure")
	source.token = []byte("fake-config")
	state, err = backend.Status(ctx)
	if err != nil || state.CanPause {
		t.Fatalf("decrypt failure did not fall back: %+v %v", state, err)
	}
	source.err = nil
	source.token = []byte("fake-config")
	state, err = backend.Status(ctx)
	if err != nil || !state.CanPause || state.Help != "" {
		t.Fatalf("setup: %+v %v", state, err)
	}
	if err := backend.Pause(ctx, time.Hour); err != nil {
		t.Fatal(err)
	}
	state, err = backend.Status(ctx)
	if err != nil || !state.Pause.Paused {
		t.Fatalf("pause: %+v %v", state, err)
	}
	if err := backend.Resume(ctx); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	revoked = true
	mu.Unlock()
	state, err = backend.Status(ctx)
	if err != nil || state.CanPause || state.Pause.Paused || state.Help != TraySetupHint {
		t.Fatalf("revocation: %+v %v", state, err)
	}
	mu.Lock()
	sessionAllowed = false
	mu.Unlock()
	state, err = backend.Status(ctx)
	if err == nil || state.Help != "请运行 lgmon tray setup" {
		t.Fatalf("non-loopback fallback: %+v %v", state, err)
	}
	mu.Lock()
	defer mu.Unlock()
	if strings.Join(methods, ",") != "PUT,DELETE" {
		t.Fatalf("unexpected mutation: %v", methods)
	}
}

type accountTrayProtector string

func (p accountTrayProtector) Encrypt(v []byte) ([]byte, error) {
	return append([]byte(string(p)+":"), v...), nil
}
func (p accountTrayProtector) Decrypt(v []byte) ([]byte, error) {
	prefix := []byte(string(p) + ":")
	if !bytes.HasPrefix(v, prefix) {
		return nil, errors.New("different account")
	}
	return append([]byte(nil), v[len(prefix):]...), nil
}

func TestTrayProfileAccountIsolationRotationAndRemoval(t *testing.T) {
	dir := t.TempDir()
	p := TrayProfile{Directory: dir, Protector: accountTrayProtector("account-a")}
	reg := TrayRegistration{URL: "http://127.0.0.1:7444", TokenID: "fake-id", Name: "tray-fake"}
	if err := p.Save(reg, []byte("fake-token")); err != nil {
		t.Fatal(err)
	}
	address, token, err := p.Load()
	if err != nil || address != reg.URL || string(token) != "fake-token" {
		t.Fatalf("round trip: %s %v", address, err)
	}
	other := p
	other.Protector = accountTrayProtector("account-b")
	if _, token, err := other.Load(); err == nil || len(token) != 0 {
		t.Fatal("another account decrypted token")
	}
	reg.TokenID = "replacement"
	if err := p.Save(reg, []byte("replacement-token")); err != nil {
		t.Fatal(err)
	}
	saved, err := p.Registration()
	if err != nil || saved.TokenID != "replacement" {
		t.Fatalf("rotation: %+v %v", saved, err)
	}
	if err := p.Remove(); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"tray.json", "tray-token"} {
		if _, err := os.Stat(filepath.Join(dir, name)); !os.IsNotExist(err) {
			t.Fatalf("profile retained %s: %v", name, err)
		}
	}
}
