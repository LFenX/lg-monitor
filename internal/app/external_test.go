// Copyright (c) 2026 LFen (https://lfen.ink) — lg-monitor

package app

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/LFenX/lg-monitor/internal/api"
	"github.com/LFenX/lg-monitor/internal/cloud"
)

// homeDeviceClient signs in and serves one other computer of the account, "dev-home", whose NAS is down.
type homeDeviceClient struct{ online bool }

func (c homeDeviceClient) Do(_ context.Context, method, _, path, _, _ string, _ any, _ bool) (cloud.ClientResult, error) {
	reply := func(status int, value any) (cloud.ClientResult, error) {
		body, _ := json.Marshal(value)
		return cloud.ClientResult{Status: status, Body: body}, nil
	}
	switch {
	case method == http.MethodPost && path == "/v1/auth/otp/verify":
		return reply(200, map[string]any{"token": "lgs_fake", "user": map[string]any{"id": "usr_1", "email": "me@example.com"}})
	case strings.HasPrefix(path, "/v1/sync/snapshot"):
		return reply(200, cloud.SyncSnapshotResponse{Seq: 2, Devices: []cloud.RemoteDevicePayload{{
			ID: "dev-home", Name: "家里电脑", Online: c.online, Seq: 2, MetaHash: "m1", StateHash: "s1",
			Meta: &cloud.CompactMeta{
				Boards:   []cloud.CompactBoard{{ID: "home", Name: "家庭服务", Monitors: []string{"nas", "site"}}},
				Monitors: []cloud.CompactMonitorMeta{{ID: "nas", Name: "虚构 NAS", Role: "storage"}, {ID: "site", Name: "家里同名的网站"}},
			},
			State: &cloud.CompactState{TS: 1795350000000, Monitors: map[string]cloud.CompactMonitorState{
				"nas":  {State: 2, Uptime: 9000, Latency: -1, Issue: "虚构的超时"},
				"site": {State: 1, Uptime: 10000, Latency: 3},
			}},
		}}})
	}
	return reply(404, map[string]any{"error": map[string]string{"code": "not_found", "message": "not found"}})
}

func signedInCloud(t *testing.T, f *apiFixture, online bool) *cloud.Manager {
	t.Helper()
	mgr := cloud.NewManager(cloud.Options{Home: t.TempDir(), Version: "0.18.0", Secrets: f.vault, Client: homeDeviceClient{online: online}, Now: func() time.Time { return f.now }})
	if _, err := mgr.VerifyOTP(context.Background(), "me@example.com", "123456", "公司主机", true); err != nil {
		t.Fatal(err)
	}
	if err := mgr.PullOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	f.backend.options.Cloud = mgr
	return mgr
}

func TestExternalDependenciesShowApartAndNeverCountAsLocal(t *testing.T) {
	f := newAPIFixture(t)
	f.request(t, "POST", "/api/v1/monitors", "config", `{"name":"虚构网站","id":"site","port":3000}`, 200)
	f.request(t, "POST", "/api/v1/monitors", "config", `{"name":"虚构备份","id":"backup","port":3001}`, 200)

	// Without an account there is nothing to point at.
	f.request(t, "PUT", "/api/v1/monitors/site", "config", `{"depends":[{"target":"nas","device":"dev-home"}]}`, 400)
	mgr := signedInCloud(t, f, true)

	candidates := decodeAs[[]api.ExternalCandidate](t, f.request(t, "GET", "/api/v1/cloud/services", "read", "", 200).Body.Bytes())
	if len(candidates) != 2 || candidates[0].Device != "dev-home" || candidates[0].ID != "nas" || candidates[0].State != "down" || len(candidates[0].Boards) != 1 || candidates[0].Boards[0] != "家庭服务" {
		t.Fatalf("candidates=%+v", candidates)
	}

	for body, want := range map[string]string{
		`{"depends":[{"target":"nas","device":"dev-ghost"}]}`:                                     "dev-ghost",
		`{"depends":[{"target":"ghost","device":"dev-home"}]}`:                                    "家里电脑",
		`{"depends":[{"target":"nas","device":"bad device"}]}`:                                    "device",
		`{"depends":[{"target":"nas","device":"dev-home"},{"target":"nas","device":"dev-home"}]}`: "两次",
	} {
		if response := f.request(t, "PUT", "/api/v1/monitors/site", "config", body, 400); !strings.Contains(response.Body.String(), want) {
			t.Fatalf("%s: %s", body, response.Body.String())
		}
	}

	// A local service and a service on the other computer may share an ID; both links are kept, apart.
	edited := decodeAs[api.Monitor](t, f.request(t, "PUT", "/api/v1/monitors/backup", "config",
		`{"depends":[{"target":"site"},{"target":"site","device":"dev-home","relation":"calls"},{"target":"nas","device":"dev-home","relation":"backup"}]}`, 200).Body.Bytes())
	if len(edited.Depends) != 1 || edited.Depends[0].ID != "site" || edited.Depends[0].Device != "" ||
		len(edited.External) != 2 || edited.External[1] != (api.MonitorDependency{ID: "nas", Device: "dev-home", DeviceName: "家里电脑", Relation: "backup"}) {
		t.Fatalf("edited=%+v", edited)
	}
	// A device ID that is this computer's own is an ordinary local link.
	self := mgr.CurrentDevice()
	f.request(t, "PUT", "/api/v1/monitors/site", "config", `{"depends":[{"target":"backup","device":"`+self+`"}]}`, 200)
	if site := targetIndex(f.manager.Snapshot())["site"]; len(site.Depends) != 1 || site.Depends[0].Device != "" {
		t.Fatalf("site=%+v", site.Depends)
	}
	list := decodeAs[[]api.Monitor](t, f.request(t, "GET", "/api/v1/monitors", "read", "", 200).Body.Bytes())
	for _, item := range list {
		// The external "site" on the other computer never shows up as using or being used by the local "site".
		if item.ID == "site" && (len(item.Dependents) != 1 || item.Dependents[0].ID != "backup") {
			t.Fatalf("local site dependents=%+v", item.Dependents)
		}
	}

	boards := decodeAs[[]api.BoardInfo](t, f.request(t, "GET", "/api/v1/boards", "read", "", 200).Body.Bytes())
	if len(boards) != 1 {
		t.Fatalf("boards=%+v", boards)
	}
	view := decodeAs[api.BoardView](t, f.request(t, "GET", "/api/v1/boards/"+boards[0].ID, "read", "", 200).Body.Bytes())
	if len(view.External) != 2 || view.Summary.External != 2 || view.Summary.ExternalDown != 1 || view.Summary.Down != 0 {
		t.Fatalf("external=%+v summary=%+v", view.External, view.Summary)
	}
	nas := view.External[1]
	if nas.ID != "nas" || nas.State != "down" || nas.Issue != "虚构的超时" || nas.Name != "虚构 NAS" || nas.DeviceName != "家里电脑" || len(nas.Users) != 1 || nas.Users[0] != "backup" {
		t.Fatalf("nas=%+v", nas)
	}
	// The board's own state ignores the failing NAS: alerts and state only come from this computer's checks.
	if view.Summary.State == "down" {
		t.Fatalf("an external fault changed the board state: %+v", view.Summary)
	}

	// Removing the local "site" keeps the link to the other computer's "site".
	f.request(t, "DELETE", "/api/v1/monitors/site", "config", "", 200)
	backup := targetIndex(f.manager.Snapshot())["backup"]
	if len(backup.Depends) != 2 || backup.Depends[0].Device != "dev-home" || backup.Depends[1].Target != "nas" {
		t.Fatalf("backup=%+v", backup.Depends)
	}
}

func TestExternalDependencyOfAnOfflineDeviceIsUnknownNotDown(t *testing.T) {
	f := newAPIFixture(t)
	f.request(t, "POST", "/api/v1/monitors", "config", `{"name":"虚构备份","id":"backup","port":3001}`, 200)
	signedInCloud(t, f, false)
	f.request(t, "PUT", "/api/v1/monitors/backup", "config", `{"depends":[{"target":"nas","device":"dev-home"}]}`, 200)
	boards := decodeAs[[]api.BoardInfo](t, f.request(t, "GET", "/api/v1/boards", "read", "", 200).Body.Bytes())
	view := decodeAs[api.BoardView](t, f.request(t, "GET", "/api/v1/boards/"+boards[0].ID, "read", "", 200).Body.Bytes())
	if len(view.External) != 1 || view.External[0].State != "unknown" || !strings.Contains(view.External[0].Issue, "离线") || view.Summary.ExternalDown != 0 {
		t.Fatalf("external=%+v summary=%+v", view.External, view.Summary)
	}
	// Signed out, the link stays and says why its state is missing.
	if _, err := f.backend.options.Cloud.Logout(context.Background(), false); err != nil {
		t.Fatal(err)
	}
	view = decodeAs[api.BoardView](t, f.request(t, "GET", "/api/v1/boards/"+boards[0].ID, "read", "", 200).Body.Bytes())
	if len(view.External) != 1 || view.External[0].State != "unknown" || !strings.Contains(view.External[0].Issue, "登录") {
		t.Fatalf("signed out external=%+v", view.External)
	}
}

// relationClient is homeDeviceClient whose NAS also uses a service on this computer and on a third device.
type relationClient struct{ self func() string }

func (c relationClient) Do(ctx context.Context, method, server, path, token, etag string, body any, compress bool) (cloud.ClientResult, error) {
	result, err := homeDeviceClient{online: true}.Do(ctx, method, server, path, token, etag, body, compress)
	if err != nil || !strings.HasPrefix(path, "/v1/sync/snapshot") {
		return result, err
	}
	var snapshot cloud.SyncSnapshotResponse
	if err := json.Unmarshal(result.Body, &snapshot); err != nil {
		return result, err
	}
	snapshot.Devices[0].Meta.Monitors[0].Ext = [][3]string{{c.self(), "backup", "stores"}, {"dev-gone", "db", "reads"}}
	result.Body, _ = json.Marshal(snapshot)
	return result, nil
}

func TestCrossDeviceRelationsJoinEveryDevicesLinksOnce(t *testing.T) {
	f := newAPIFixture(t)
	f.request(t, "POST", "/api/v1/monitors", "config", `{"name":"虚构备份","id":"backup","port":3001}`, 200)
	if empty := decodeAs[api.CrossDeviceRelations](t, f.request(t, "GET", "/api/v1/cloud/relations", "read", "", 200).Body.Bytes()); len(empty.Relations) != 0 {
		t.Fatalf("relations without an account: %+v", empty)
	}
	var mgr *cloud.Manager
	mgr = cloud.NewManager(cloud.Options{Home: t.TempDir(), Version: "0.18.0", Secrets: f.vault, Client: relationClient{self: func() string { return mgr.CurrentDevice() }}, Now: func() time.Time { return f.now }})
	if _, err := mgr.VerifyOTP(context.Background(), "me@example.com", "123456", "公司主机", true); err != nil {
		t.Fatal(err)
	}
	if err := mgr.PullOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	f.backend.options.Cloud = mgr
	f.request(t, "PUT", "/api/v1/monitors/backup", "config", `{"depends":[{"target":"nas","device":"dev-home","relation":"backup"}]}`, 200)

	got := decodeAs[api.CrossDeviceRelations](t, f.request(t, "GET", "/api/v1/cloud/relations", "read", "", 200).Body.Bytes())
	if len(got.Relations) != 3 || got.Devices != 3 || got.Problems != 3 {
		t.Fatalf("relations=%+v", got)
	}
	pairs := map[string]api.CrossDeviceRelation{}
	for _, relation := range got.Relations {
		pairs[relation.From.ID+">"+relation.To.ID] = relation
	}
	mine := pairs["backup>nas"]
	if !mine.From.Current || mine.To.Name != "虚构 NAS" || mine.To.State != "down" || mine.To.DeviceName != "家里电脑" || mine.Relation != "backup" {
		t.Fatalf("this computer's link=%+v", mine)
	}
	// The other device's link to this computer is resolved against this computer's own services.
	back := pairs["nas>backup"]
	if back.From.Device != "dev-home" || !back.To.Current || back.To.Name != "虚构备份" || back.Relation != "stores" {
		t.Fatalf("synced link=%+v", back)
	}
	if gone := pairs["nas>db"]; gone.To.State != "unknown" || gone.To.Issue == "" {
		t.Fatalf("link to a device that left=%+v", gone)
	}
	guest := api.WithPrincipal(context.Background(), api.Principal{Name: "guest", Scope: api.ScopeRead, Guest: true})
	if _, err := f.backend.call(guest, api.ListCloudRelations, api.Request{}); err == nil {
		t.Fatal("a LAN guest read the account's relations")
	}
}

func TestCloudDeviceSystemIsServed(t *testing.T) {
	f := newAPIFixture(t)
	signedInCloud(t, f, true)
	system := decodeAs[api.CloudDeviceSystem](t, f.request(t, "GET", "/api/v1/cloud/devices/dev-home/system", "read", "", 200).Body.Bytes())
	// The fake device predates resource sync, so it has a name and no samples yet.
	if system.DeviceName != "家里电脑" || system.Devices == nil || len(system.Devices) != 0 {
		t.Fatalf("system=%+v", system)
	}
	f.request(t, "GET", "/api/v1/cloud/devices/dev-ghost/system", "read", "", 404)
}
