// Copyright (c) 2026 LFen (https://lfen.ink) — lg-monitor

package app

import (
	"context"
	"encoding/json"
	"net/http"
	"path/filepath"
	"testing"
	"testing/fstest"
	"time"

	"github.com/LFenX/lg-monitor/internal/api"
	"github.com/LFenX/lg-monitor/internal/cloud"
	"github.com/LFenX/lg-monitor/internal/config"
	"github.com/LFenX/lg-monitor/internal/feishu"
	"github.com/LFenX/lg-monitor/internal/rules"
	"github.com/LFenX/lg-monitor/internal/store"
)

type stubCloudClient struct{}

func (stubCloudClient) Do(_ context.Context, method, _, path, _, _ string, _ any, _ bool) (cloud.ClientResult, error) {
	if method == http.MethodPost && path == "/v1/auth/otp/request" {
		b, _ := json.Marshal(map[string]any{"ok": true, "cooldownSec": 60, "expiresInSec": 600})
		return cloud.ClientResult{Status: 200, Body: b}, nil
	}
	return cloud.ClientResult{Status: 404, Body: []byte(`{"error":{"code":"not_found","message":"not found"}}`)}, nil
}

func TestCloudOperationsAndGuestIsolation(t *testing.T) {
	home := t.TempDir()
	now := time.UnixMilli(1795350000000)
	clock := func() time.Time { return now }

	manager, err := config.NewManager(config.FSLoader{FS: fstest.MapFS{}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	editor, err := config.NewEditor(&apiDocuments{MapFS: fstest.MapFS{}}, manager)
	if err != nil {
		t.Fatal(err)
	}
	dbPath := filepath.Join(home, "data", "lgmon.db")
	db, err := store.Open(context.Background(), store.Options{Path: dbPath, Now: clock})
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	view, err := store.OpenViewWithClock(context.Background(), dbPath, clock)
	if err != nil {
		t.Fatal(err)
	}
	defer view.Close()

	vault := &apiVault{values: map[string][]byte{}}
	cloudMgr := cloud.NewManager(cloud.Options{
		Home:    home,
		Version: "0.17.0",
		Secrets: vault,
		Client:  stubCloudClient{},
		Now:     clock,
	})

	backend, err := NewAPIBackend(APIBackendOptions{
		Cloud:   cloudMgr,
		Config:  manager,
		Editor:  editor,
		Read:    view,
		SQL:     view,
		Write:   db,
		Secrets: vault,
		Collectors: presenceSourceFunc(func(context.Context) ([]rules.CollectorPresence, error) {
			return nil, nil
		}),
		NewClient: func(config.FeishuApp, []byte) (feishu.Client, error) { return nil, nil },
		Now:       clock,
		Home:      home,
		Version:   "0.17.0",
	})
	if err != nil {
		t.Fatal(err)
	}
	cloudMgr.SetProvider(backend)

	// 1. Default logged-out status works cleanly.
	rawStatus, err := backend.call(context.Background(), api.GetCloudStatus, api.Request{})
	if err != nil {
		t.Fatalf("GetCloudStatus: %v", err)
	}
	status := rawStatus.(api.CloudStatus)
	if status.LoggedIn || status.DeviceID == "" {
		t.Fatalf("unexpected initial cloud status: %+v", status)
	}

	// 2. OTP request proxies through cloud.Manager.
	rawOTP, err := backend.call(context.Background(), api.CloudRequestOTP, api.Request{
		Body: []byte(`{"email":"user@example.com"}`),
	})
	if err != nil {
		t.Fatalf("CloudRequestOTP: %v", err)
	}
	if !rawOTP.(api.CloudOTPResult).OK {
		t.Fatalf("expected OK OTP result")
	}

	// 3. LAN guest context MUST be blocked from accessing private cloud status or devices.
	guestCtx := api.WithPrincipal(context.Background(), api.Principal{Name: "guest", Scope: api.ScopeRead, Guest: true})
	if _, err := backend.call(guestCtx, api.GetCloudStatus, api.Request{}); err == nil {
		t.Fatal("expected LAN guest to be forbidden from GetCloudStatus")
	}
	if _, err := backend.call(guestCtx, api.ListCloudDevices, api.Request{}); err == nil {
		t.Fatal("expected LAN guest to be forbidden from ListCloudDevices")
	}
}
