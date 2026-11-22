// Copyright (c) 2026 LFen (https://lfen.ink) — lg-monitor

package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"testing/fstest"
	"time"

	"github.com/LFenX/lg-monitor/internal/api"
	"github.com/LFenX/lg-monitor/internal/config"
	"github.com/LFenX/lg-monitor/internal/feishu"
	"github.com/LFenX/lg-monitor/internal/rules"
	"github.com/LFenX/lg-monitor/internal/store"
)

func TestSystemUpdateAndRestartOperations(t *testing.T) {
	ctx := context.Background()
	installerBytes := []byte("FAKE_LGMON_SETUP_BINARY")
	sum := sha256.Sum256(installerBytes)

	feedServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		base := "http://" + r.Host
		switch r.URL.Path {
		case "/latest":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"tag_name": "v0.16.2",
				"body":     "Bug fixes",
				"html_url": base + "/notes",
				"assets": []map[string]any{
					{"name": "lgmon-setup-0.16.2.exe", "browser_download_url": base + "/setup", "size": len(installerBytes)},
					{"name": "SHA256SUMS", "browser_download_url": base + "/sums"},
				},
			})
		case "/sums":
			_, _ = w.Write([]byte(hex.EncodeToString(sum[:]) + "  lgmon-setup-0.16.2.exe\n"))
		case "/setup":
			_, _ = w.Write(installerBytes)
		}
	}))
	t.Cleanup(feedServer.Close)

	home := t.TempDir()
	dbPath := filepath.Join(home, "data", "lgmon.db")
	db, err := store.Open(ctx, store.Options{Path: dbPath, Now: time.Now})
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	view, err := store.OpenViewWithClock(ctx, dbPath, time.Now)
	if err != nil {
		t.Fatal(err)
	}
	defer view.Close()
	manager, err := config.NewManager(config.FSLoader{FS: fstest.MapFS{}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	editor, err := config.NewEditor(&apiDocuments{MapFS: fstest.MapFS{}}, manager)
	if err != nil {
		t.Fatal(err)
	}

	var appliedPath string
	restarted := false
	backend, err := NewAPIBackend(APIBackendOptions{
		Config: manager,
		Editor: editor,
		Read:   view,
		SQL:    view,
		Write:  db,
		Collectors: presenceSourceFunc(func(context.Context) ([]rules.CollectorPresence, error) {
			return nil, nil
		}),
		Secrets:      &apiVault{values: map[string][]byte{}},
		NewClient:    func(config.FeishuApp, []byte) (feishu.Client, error) { return nil, nil },
		Now:          time.Now,
		Version:      "0.16.1",
		Home:         home,
		UpdateFeed:   feedServer.URL + "/latest",
		UpdateClient: feedServer.Client(),
		UpdateApply: func(path string) error {
			appliedPath = path
			return nil
		},
		Restart: func(context.Context) (string, error) {
			restarted = true
			return "service", nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	// 1. Check for updates -> available v0.16.2
	checkBody, _ := json.Marshal(api.SystemUpdateActionInput{Action: "check"})
	if _, err := backend.systemOperation(ctx, api.ActionSystemUpdate, api.Request{Body: checkBody}); err != nil {
		t.Fatalf("check action: %v", err)
	}
	backend.background.Wait()
	st := backend.systemUpdateSnapshot()
	if st.State != "available" || st.Version != "0.16.2" {
		t.Fatalf("after check: %+v", st)
	}

	// 2. Download verified installer -> ready
	dlBody, _ := json.Marshal(api.SystemUpdateActionInput{Action: "download"})
	if _, err := backend.systemOperation(ctx, api.ActionSystemUpdate, api.Request{Body: dlBody}); err != nil {
		t.Fatalf("download action: %v", err)
	}
	backend.background.Wait()
	st = backend.systemUpdateSnapshot()
	if st.State != "ready" || st.Done != int64(len(installerBytes)) {
		t.Fatalf("after download: %+v", st)
	}

	// 3. Apply verified installer -> applying
	applyBody, _ := json.Marshal(api.SystemUpdateActionInput{Action: "apply"})
	if _, err := backend.systemOperation(ctx, api.ActionSystemUpdate, api.Request{Body: applyBody}); err != nil {
		t.Fatalf("apply action: %v", err)
	}
	if appliedPath == "" || backend.systemUpdateSnapshot().State != "applying" {
		t.Fatalf("after apply: path=%q state=%+v", appliedPath, backend.systemUpdateSnapshot())
	}

	// 4. Trigger graceful restart
	resAny, err := backend.systemOperation(ctx, api.RestartSystem, api.Request{})
	if err != nil || !restarted || resAny.(api.SystemRestartResult).Mode != "service" {
		t.Fatalf("restart: restarted=%v res=%+v err=%v", restarted, resAny, err)
	}
}
