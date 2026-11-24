// Copyright (c) 2026 LFen (https://lfen.ink) — lg-monitor

package app

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"path/filepath"
	"slices"
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
	if method == http.MethodPost && path == "/v1/auth/otp/verify" {
		return cloud.ClientResult{Status: 401, Body: []byte(`{"error":{"code":"otp_mismatch","message":"验证码不正确"}}`)}, nil
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

	// A wrong code is the sync service's 401; passed on unchanged, the panel would drop its own local session.
	_, err = backend.call(context.Background(), api.CloudVerifyOTP, api.Request{
		Body: []byte(`{"email":"user@example.com","code":"000000"}`),
	})
	var fault *api.Fault
	if !errors.As(err, &fault) || fault.Status != 400 || fault.Code != "otp_mismatch" {
		t.Fatalf("wrong code should surface as 400 otp_mismatch, got %#v", err)
	}

	// 3. LAN guest context MUST be blocked from accessing private cloud status or devices.
	guestCtx := api.WithPrincipal(context.Background(), api.Principal{Name: "guest", Scope: api.ScopeRead, Guest: true})
	if _, err := backend.call(guestCtx, api.GetCloudStatus, api.Request{}); err == nil {
		t.Fatal("expected LAN guest to be forbidden from GetCloudStatus")
	}
	// Other devices are listed to guests only for boards the owner shared; none are shared here.
	rawDevices, err := backend.call(guestCtx, api.ListCloudDevices, api.Request{})
	if err != nil || len(rawDevices.([]api.CloudDeviceSummary)) != 0 {
		t.Fatalf("guest device list: %v %v", rawDevices, err)
	}
	for _, op := range []api.Operation{api.GetCloudDeviceBoard, api.GetCloudDeviceMonitor, api.GetCloudDeviceDoc, api.TriggerCloudSync} {
		if _, err := backend.call(guestCtx, op, api.Request{Params: map[string]string{"deviceId": "dev-home", "boardId": "main", "monitorId": "web"}}); !errors.As(err, &fault) || fault.Status != 403 {
			t.Fatalf("%s should be forbidden to guests, got %v", op, err)
		}
	}
}

type fakeCloudBoards struct {
	devices []cloud.DeviceSummary
	boards  map[string]api.BoardView
}

func (f fakeCloudBoards) Devices() []cloud.DeviceSummary { return f.devices }

func (f fakeCloudBoards) DeviceBoard(deviceID, boardID string) (cloud.SyncedBoardView, error) {
	board, ok := f.boards[deviceID+"/"+boardID]
	if !ok {
		return cloud.SyncedBoardView{}, errors.New("missing board")
	}
	return cloud.SyncedBoardView{DeviceID: deviceID, Board: board}, nil
}

func TestGuestSeesOnlySharedBoardsOfOtherDevices(t *testing.T) {
	info := func(id string, services, down int) api.BoardInfo {
		return api.BoardInfo{ID: id, Name: id, Summary: api.BoardSummary{Services: services, Down: down}}
	}
	src := fakeCloudBoards{
		devices: []cloud.DeviceSummary{
			{ID: "dev-self", Name: "Office", IsCurrent: true, Boards: []api.BoardInfo{info("main", 2, 0)}},
			{ID: "dev-home", Name: "Home", Avatar: "fiora-3", Online: true, Version: "0.17.3", TotalCount: 5, DownCount: 2, Boards: []api.BoardInfo{info("public", 1, 0), info("private", 2, 2)}},
			{ID: "dev-lab", Name: "Lab", Boards: []api.BoardInfo{info("lab", 1, 1)}},
		},
		boards: map[string]api.BoardView{
			"dev-home/public":  {BoardInfo: info("public", 1, 0), Items: []api.Monitor{{ID: "site", Name: "Site", Boards: []string{"public", "private"}, Depends: []api.MonitorDependency{{ID: "db", Relation: "reads", Note: "secret note"}}}}},
			"dev-home/private": {BoardInfo: info("private", 2, 2), Items: []api.Monitor{{ID: "db", Name: "Billing DB"}, {ID: "site", Name: "Site"}}},
		},
	}
	cfg := &config.Config{}
	cfg.Settings.LAN.WebDeviceBoards = map[string][]string{"dev-self": {"main"}, "dev-home": {"public"}}

	devices := guestCloudDevices(cfg, src.devices)
	if len(devices) != 1 || devices[0].ID != "dev-home" || len(devices[0].Boards) != 1 || devices[0].Boards[0].ID != "public" {
		t.Fatalf("guest devices: %+v", devices)
	}
	if d := devices[0]; d.TotalCount != 1 || d.DownCount != 0 || d.Avatar != "fiora-3" || d.Version != "" {
		t.Fatalf("counts should cover shared boards only: %+v", d)
	}

	scope := newCloudGuestScope(src, "dev-home", cfg.Settings.LAN.WebDeviceBoards["dev-home"], nil)
	if !scope.visible["site"] || scope.visible["db"] {
		t.Fatalf("visible: %v", scope.visible)
	}
	site := scope.monitor(src.boards["dev-home/public"].Items[0])
	if len(site.Boards) != 1 || site.Depends[0].RestrictedName != "Billing DB" || site.Depends[0].Note != "" {
		t.Fatalf("restricted dependency leaked: %+v", site)
	}
}

func TestDeviceBoardNarrowedToSomeServicesForLANViewers(t *testing.T) {
	info := func(id string, services int) api.BoardInfo {
		return api.BoardInfo{ID: id, Name: id, Summary: api.BoardSummary{Services: services}}
	}
	src := fakeCloudBoards{
		devices: []cloud.DeviceSummary{{ID: "dev-home", Name: "Home", Boards: []api.BoardInfo{info("ops", 2)}}},
		boards: map[string]api.BoardView{"dev-home/ops": {BoardInfo: info("ops", 2), Items: []api.Monitor{
			{ID: "api", Name: "API", State: "up", Children: []string{"worker"}},
			{ID: "worker", Name: "Worker", Parent: "api", State: "down"},
			{ID: "billing", Name: "Billing", State: "up"},
		}}},
	}
	scope := newCloudGuestScope(src, "dev-home", []string{"ops"}, map[string][]string{"ops": {"api", "billing"}})
	view := scope.board(src.boards["dev-home/ops"])
	ids := []string{}
	for _, item := range view.Items {
		ids = append(ids, item.ID)
	}
	if !slices.Equal(ids, []string{"api", "billing"}) || len(view.Items[0].Children) != 0 || view.Summary.Down != 0 {
		t.Fatalf("narrowed board leaked a service: %v %+v", ids, view.Summary)
	}
	if scope.visible["worker"] {
		t.Fatal("a sub-service left out of the scope is still visible")
	}

	if id := lanDeviceBoardID("dev-home", "ops"); id != "cloud~dev-home~ops" {
		t.Fatalf("id=%s", id)
	}
	if device, board, ok := splitLANDeviceBoard("cloud~dev-home~ops"); !ok || device != "dev-home" || board != "ops" {
		t.Fatalf("split=%s %s %v", device, board, ok)
	}
	for _, local := range []string{"main", "cloud~", "cloud~dev-home", "cloud~~ops"} {
		if _, _, ok := splitLANDeviceBoard(local); ok {
			t.Fatalf("%q read as another device's board", local)
		}
	}

	// Closing a board of another device drops its narrowing, so opening it again starts from the whole board.
	settings := config.LANSettings{WebDeviceBoards: map[string][]string{"dev-home": {"ops", "misc"}}}
	narrow := map[string]map[string][]string{"dev-home": {"ops": {"api"}, "misc": {}}}
	settings = applyLANInput(settings, api.LANConfigInput{WebDeviceServices: &narrow})
	if !slices.Equal(settings.WebDeviceServices["dev-home"]["ops"], []string{"api"}) {
		t.Fatalf("services=%v", settings.WebDeviceServices)
	}
	closed := map[string][]string{"dev-home": {"misc"}}
	settings = applyLANInput(settings, api.LANConfigInput{WebDeviceBoards: &closed})
	if _, kept := settings.WebDeviceServices["dev-home"]["ops"]; kept {
		t.Fatalf("closed board kept its narrowing: %v", settings.WebDeviceServices)
	}
	whole := map[string]map[string][]string{"dev-home": {"misc": nil}}
	if settings = applyLANInput(settings, api.LANConfigInput{WebDeviceServices: &whole}); settings.WebDeviceServices != nil {
		t.Fatalf("null did not restore the whole board: %v", settings.WebDeviceServices)
	}
}
