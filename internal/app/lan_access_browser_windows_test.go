//go:build windows && integration && webui

// Copyright (c) 2026 LFen (https://lfen.ink) — lg-monitor

package app

import (
	"context"
	"crypto/rand"
	"net/http"
	"net/http/httptest"
	"testing"
	"testing/fstest"
	"time"

	"github.com/LFenX/lg-monitor/internal/api"
	"github.com/LFenX/lg-monitor/web"
)

func TestNativeLANAccessBrowser(t *testing.T) {
	f := newAPIFixtureWithHTTPClock(t, time.Now(), time.Now)
	newTeamFixture(t, f)
	f.files.MapFS["boards.yaml"] = &fstest.MapFile{Data: []byte("boards: [{id: main, name: 发布面板, monitors: [web, db]}]")}
	f.files.MapFS["targets.yaml"] = &fstest.MapFile{Data: []byte("targets: [{id: web, name: 示例站点, match: {process: fake.exe}, depends: [{target: db, relation: reads}]}, {id: db, name: 示例数据库, match: {process: fake-db.exe}}]")}
	f.files.MapFS["settings.yaml"] = &fstest.MapFile{Data: []byte("teamListen: 127.0.0.1:0\nlan: {web: true, webBoards: [main]}")}
	if err := f.manager.Reload(); err != nil {
		t.Fatal(err)
	}
	hosts, err := api.NewHostPolicy("127.0.0.1:0", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	auth, err := api.NewAuthenticator(api.AuthOptions{Hosts: hosts, Entropy: rand.Reader, LANKeys: f.authDB, Now: time.Now, Tokens: api.TokenLookupFunc(func(ctx context.Context, hash [32]byte) (api.Principal, bool, error) {
		row, found, err := f.authDB.Authenticate(ctx, hash[:])
		return api.Principal{Name: row.Name, Scope: api.Scope(row.Scope)}, found, err
	})})
	if err != nil {
		t.Fatal(err)
	}
	auth.SetLANWeb(true)
	actions := f.backend.Actions()
	actions[api.DiscoverLAN] = func(context.Context, api.Request) (any, error) { return []map[string]any{}, nil }
	h, err := api.NewServer(api.ServerOptions{Auth: auth, Now: time.Now, Actions: actions, Broker: f.broker, Frontend: web.NewHandlerWithWebSession(auth.WebSession)})
	if err != nil {
		t.Fatal(err)
	}
	owner := httptest.NewServer(h)
	defer owner.Close()
	t.Setenv("LGMON_BROWSER_OWNER_URL", owner.URL)
	runNativeWebBrowser(t, f, "lan-access.spec.ts", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Both listeners stay on loopback; only the peer seen by the guest handler is synthetic.
		r.RemoteAddr = "192.168.20.8:12345"
		if r.URL.Path == "/api/v1/download/stats" {
			w.WriteHeader(503)
			return
		}
		h.ServeHTTP(w, r)
	}))
}
