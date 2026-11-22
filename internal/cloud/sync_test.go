// Copyright (c) 2026 LFen (https://lfen.ink) — lg-monitor

package cloud

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/LFenX/lg-monitor/internal/api"
)

type scriptedCall struct {
	Method, Path, Token, IfNoneMatch string
	Body                             []byte
}

// scriptedClient answers each request through handle and records it.
type scriptedClient struct {
	mu     sync.Mutex
	calls  []scriptedCall
	handle func(call scriptedCall) (int, any, string)
}

func (c *scriptedClient) Do(_ context.Context, method, _, path, token, ifNoneMatch string, body any, _ bool) (ClientResult, error) {
	var raw []byte
	if body != nil {
		raw, _ = json.Marshal(body)
	}
	call := scriptedCall{Method: method, Path: path, Token: token, IfNoneMatch: ifNoneMatch, Body: raw}
	c.mu.Lock()
	c.calls = append(c.calls, call)
	c.mu.Unlock()
	status, value, etag := c.handle(call)
	var out []byte
	if value != nil {
		out, _ = json.Marshal(value)
	}
	return ClientResult{Status: status, Body: out, ETag: etag, BytesSent: int64(len(raw)), BytesReceived: int64(len(out))}, nil
}

func (c *scriptedClient) count() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.calls)
}

func (c *scriptedClient) last() scriptedCall {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.calls[len(c.calls)-1]
}

func signedInReply(call scriptedCall) (int, any, string, bool) {
	if call.Path != "/v1/auth/otp/verify" {
		return 0, nil, "", false
	}
	return 200, map[string]any{"token": "lgs_fake", "user": map[string]any{"id": "usr_1", "email": "me@example.com"}}, "", true
}

func newSignedIn(t *testing.T, home string, secrets SecretStore, client Client, clock func() time.Time, provider SnapshotProvider) *Manager {
	t.Helper()
	mgr := NewManager(Options{Home: home, Version: "0.17.0", Secrets: secrets, Client: client, Now: clock})
	mgr.SetProvider(provider)
	if _, err := mgr.VerifyOTP(context.Background(), "me@example.com", "123456", "公司主机"); err != nil {
		t.Fatalf("VerifyOTP: %v", err)
	}
	return mgr
}

func oneMonitor(latency float64) *fakeProvider {
	up := 100.0
	return &fakeProvider{
		name:     "公司主机",
		avatar:   "fiora-3",
		boards:   []api.BoardInfo{{ID: "main", Name: "核心", Monitors: []string{"api"}}},
		monitors: []api.Monitor{{ID: "api", Name: "接口", State: "up", Uptime: &up, LatencyMS: &latency, Kind: "port"}},
	}
}

func TestLatencyDriftWaitsButStateFlipsGoOutAtOnce(t *testing.T) {
	now := time.UnixMilli(1795350000000)
	clock := func() time.Time { return now }
	var pushes []SyncPushRequest
	client := &scriptedClient{handle: func(call scriptedCall) (int, any, string) {
		if status, body, etag, ok := signedInReply(call); ok {
			return status, body, etag
		}
		if call.Path == "/v1/sync/push" {
			var req SyncPushRequest
			_ = json.Unmarshal(call.Body, &req)
			pushes = append(pushes, req)
			return 200, SyncPushResponse{OK: true}, ""
		}
		return 404, nil, ""
	}}
	provider := oneMonitor(10)
	mgr := newSignedIn(t, t.TempDir(), &fakeSecrets{}, client, clock, provider)
	ctx := context.Background()

	if err := mgr.PushOnce(ctx, false); err != nil || len(pushes) != 1 || pushes[0].Meta == nil || pushes[0].State == nil {
		t.Fatalf("first push should carry meta and state: %+v err=%v", pushes, err)
	}
	firstStateHash := pushes[0].StateHash

	provider.mu.Lock()
	*provider.monitors[0].LatencyMS = 37
	provider.mu.Unlock()
	now = now.Add(10 * time.Second)
	if err := mgr.PushOnce(ctx, false); err != nil || len(pushes) != 1 {
		t.Fatalf("latency drift alone must not push within the heartbeat window: %d pushes, err=%v", len(pushes), err)
	}

	now = now.Add(55 * time.Second)
	if err := mgr.PushOnce(ctx, false); err != nil || len(pushes) != 2 {
		t.Fatalf("heartbeat expected after 60s: %d pushes, err=%v", len(pushes), err)
	}
	if beat := pushes[1]; beat.State != nil || beat.Meta != nil || beat.StateHash != firstStateHash {
		t.Fatalf("heartbeat must carry only the hashes the server holds: %+v", beat)
	}

	now = now.Add(stateRefreshEvery)
	if err := mgr.PushOnce(ctx, false); err != nil || len(pushes) != 3 || pushes[2].State == nil {
		t.Fatalf("drift should ride along after %v: %+v err=%v", stateRefreshEvery, pushes, err)
	}
	if got := pushes[2].State.Monitors["api"].Latency; got != 37 {
		t.Fatalf("refreshed frame latency = %d, want 37", got)
	}

	provider.mu.Lock()
	provider.monitors[0].State = "down"
	provider.monitors[0].Issue = "端口无响应"
	provider.mu.Unlock()
	now = now.Add(time.Second)
	if err := mgr.PushOnce(ctx, false); err != nil || len(pushes) != 4 || pushes[3].State == nil {
		t.Fatalf("a state flip must go out at once: %+v err=%v", pushes, err)
	}
	if st := pushes[3].State.Monitors["api"]; st.State != 2 || st.Issue != "端口无响应" {
		t.Fatalf("unexpected flipped frame: %+v", st)
	}
}

func TestProfileChangeResendsMeta(t *testing.T) {
	now := time.UnixMilli(1795350000000)
	clock := func() time.Time { return now }
	var pushes []SyncPushRequest
	client := &scriptedClient{handle: func(call scriptedCall) (int, any, string) {
		if status, body, etag, ok := signedInReply(call); ok {
			return status, body, etag
		}
		var req SyncPushRequest
		_ = json.Unmarshal(call.Body, &req)
		pushes = append(pushes, req)
		return 200, SyncPushResponse{OK: true}, ""
	}}
	provider := oneMonitor(10)
	mgr := newSignedIn(t, t.TempDir(), &fakeSecrets{}, client, clock, provider)
	ctx := context.Background()
	_ = mgr.PushOnce(ctx, false)

	provider.mu.Lock()
	provider.avatar = "lg-cat-2"
	provider.mu.Unlock()
	now = now.Add(time.Second)
	if err := mgr.PushOnce(ctx, false); err != nil || len(pushes) != 2 || pushes[1].Meta == nil || pushes[1].Device == nil || pushes[1].Device.Avatar != "lg-cat-2" {
		t.Fatalf("a new avatar should re-send meta with the device: %+v err=%v", pushes, err)
	}
}

func TestSignOutDoesNotLeaveAUsableToken(t *testing.T) {
	now := time.UnixMilli(1795350000000)
	clock := func() time.Time { return now }
	client := &scriptedClient{handle: func(call scriptedCall) (int, any, string) {
		if status, body, etag, ok := signedInReply(call); ok {
			return status, body, etag
		}
		return 200, map[string]any{"ok": true}, ""
	}}
	home, secrets := t.TempDir(), &fakeSecrets{}
	mgr := newSignedIn(t, home, secrets, client, clock, oneMonitor(10))
	ctx := context.Background()
	if _, err := mgr.Logout(ctx, false); err != nil {
		t.Fatalf("Logout: %v", err)
	}
	if raw, _ := secrets.Get(ctx, sessionSecretKey); string(raw) != signedOutMarker {
		t.Fatalf("stored token after sign-out = %q, want the signed-out marker", raw)
	}

	restarted := NewManager(Options{Home: home, Secrets: secrets, Client: client, Now: clock})
	restarted.SetProvider(oneMonitor(10))
	before := client.count()
	if err := restarted.PushOnce(ctx, true); err != nil || client.count() != before {
		t.Fatalf("a restarted, signed-out manager must stay offline: calls %d -> %d, err=%v", before, client.count(), err)
	}
	if restarted.Status(ctx).LoggedIn {
		t.Fatal("restarted manager reports signed in")
	}
}

func TestRejectedSessionSignsOutInsteadOfRetrying(t *testing.T) {
	now := time.UnixMilli(1795350000000)
	clock := func() time.Time { return now }
	client := &scriptedClient{handle: func(call scriptedCall) (int, any, string) {
		if status, body, etag, ok := signedInReply(call); ok {
			return status, body, etag
		}
		return 401, map[string]any{"error": map[string]string{"code": "unauthorized", "message": "登录状态已失效，请重新登录"}}, ""
	}}
	home, secrets := t.TempDir(), &fakeSecrets{}
	mgr := newSignedIn(t, home, secrets, client, clock, oneMonitor(10))
	ctx := context.Background()
	if err := mgr.PushOnce(ctx, true); err == nil {
		t.Fatal("expected the rejected push to fail")
	}
	st := mgr.Status(ctx)
	if st.LoggedIn || !strings.Contains(st.LastError, "重新登录") {
		t.Fatalf("expected a local sign-out with a reason: %+v", st)
	}
	before := client.count()
	_ = mgr.PushOnce(ctx, true)
	_ = mgr.PullOnce(ctx)
	if client.count() != before {
		t.Fatalf("no request should follow a rejected session: %d -> %d", before, client.count())
	}
	if NewManager(Options{Home: home, Secrets: secrets, Client: client, Now: clock}).Status(ctx).LoggedIn {
		t.Fatal("the rejected session came back after a restart")
	}
}

func TestPullRefetchesEverythingWhenADeltaRefersToMissingData(t *testing.T) {
	now := time.UnixMilli(1795350000000)
	clock := func() time.Time { return now }
	avatar := "data:image/png;base64,iVBORw0KGgo="
	full := func(metaHash, boardName string) RemoteDevicePayload {
		return RemoteDevicePayload{
			ID: "dev-home", Name: "家里电脑", Avatar: avatar, Online: true, Seq: 3,
			MetaHash: metaHash, StateHash: "s1",
			Meta: &CompactMeta{
				Boards:   []CompactBoard{{ID: "home", Name: boardName, Monitors: []string{"nas"}}},
				Monitors: []CompactMonitorMeta{{ID: "nas", Name: "NAS"}},
			},
			State: &CompactState{TS: now.UnixMilli(), Monitors: map[string]CompactMonitorState{"nas": {State: 1, Uptime: 10000, Latency: 3}}},
		}
	}
	var snapshots []string
	step := 0
	client := &scriptedClient{handle: func(call scriptedCall) (int, any, string) {
		if status, body, etag, ok := signedInReply(call); ok {
			return status, body, etag
		}
		if !strings.HasPrefix(call.Path, "/v1/sync/snapshot") {
			return 404, nil, ""
		}
		snapshots = append(snapshots, call.Path)
		switch {
		case step == 0:
			return 200, SyncSnapshotResponse{Seq: 3, Devices: []RemoteDevicePayload{full("m1", "家庭服务")}}, `"e1"`
		case step == 1:
			// Only the state changed: meta and avatar stay with the copy already held.
			dev := full("m1", "家庭服务")
			dev.Meta, dev.Avatar, dev.StateHash, dev.Seq = nil, "", "s2", 4
			dev.State = &CompactState{TS: now.UnixMilli(), Monitors: map[string]CompactMonitorState{"nas": {State: 2, Uptime: 9000, Latency: -1, Issue: "超时"}}}
			return 200, SyncSnapshotResponse{Seq: 4, Devices: []RemoteDevicePayload{dev}}, `"e2"`
		case strings.HasSuffix(call.Path, "since=0"):
			return 200, SyncSnapshotResponse{Seq: 9, Devices: []RemoteDevicePayload{full("m2", "家庭服务（新）")}}, `"e9"`
		default:
			// A meta hash this copy never saw, without the meta itself.
			dev := full("m2", "")
			dev.Meta, dev.State, dev.Avatar, dev.Seq = nil, nil, "", 9
			return 200, SyncSnapshotResponse{Seq: 9, Devices: []RemoteDevicePayload{dev}}, `"e9"`
		}
	}}
	mgr := newSignedIn(t, t.TempDir(), &fakeSecrets{}, client, clock, oneMonitor(10))
	ctx := context.Background()

	if err := mgr.PullOnce(ctx); err != nil {
		t.Fatalf("first pull: %v", err)
	}
	step = 1
	if err := mgr.PullOnce(ctx); err != nil {
		t.Fatalf("delta pull: %v", err)
	}
	devices := mgr.Devices()
	if len(devices) != 1 || devices[0].Avatar != avatar || devices[0].DownCount != 1 || len(devices[0].Boards) != 1 {
		t.Fatalf("a state-only delta should keep meta and avatar: %+v", devices)
	}

	step = 2
	if err := mgr.PullOnce(ctx); err != nil {
		t.Fatalf("pull with a missing meta: %v", err)
	}
	if got := snapshots[len(snapshots)-1]; !strings.HasSuffix(got, "since=0") {
		t.Fatalf("expected a full refetch, last request was %s (all: %v)", got, snapshots)
	}
	board, err := mgr.DeviceBoard("dev-home", "home")
	if err != nil || board.Board.Name != "家庭服务（新）" {
		t.Fatalf("refetched board = %+v err=%v", board.Board.BoardInfo, err)
	}
}

func TestRegisterAsksForARegisterCodeAndKeepsThePasswordOptional(t *testing.T) {
	now := time.UnixMilli(1795350000000)
	clock := func() time.Time { return now }
	bodies := map[string]map[string]any{}
	client := &scriptedClient{handle: func(call scriptedCall) (int, any, string) {
		var body map[string]any
		_ = json.Unmarshal(call.Body, &body)
		bodies[call.Path] = body
		switch call.Path {
		case "/v1/auth/otp/request":
			return 200, map[string]any{"ok": true, "cooldownSec": 60, "expiresInSec": 600}, ""
		case "/v1/auth/register":
			return 200, map[string]any{"token": "lgs_new", "user": map[string]any{"id": "usr_1", "email": "new@example.com", "hasPassword": body["password"] != nil}}, ""
		}
		return 404, nil, ""
	}}
	mgr := NewManager(Options{Home: t.TempDir(), Secrets: &fakeSecrets{}, Client: client, Now: clock})
	mgr.SetProvider(oneMonitor(10))
	ctx := context.Background()

	if _, err := mgr.RequestOTP(ctx, " New@Example.com ", "register", "en"); err != nil {
		t.Fatalf("RequestOTP: %v", err)
	}
	sent := bodies["/v1/auth/otp/request"]
	if sent["email"] != "new@example.com" || sent["purpose"] != "register" || sent["lang"] != "en" || sent["deviceName"] != "公司主机" || sent["os"] == "" {
		t.Fatalf("unexpected code request: %v", sent)
	}
	if _, err := mgr.RequestOTP(ctx, "new@example.com", "anything", "fr"); err != nil {
		t.Fatal(err)
	}
	if got := bodies["/v1/auth/otp/request"]; got["purpose"] != "login" || got["lang"] != "zh" {
		t.Fatalf("unknown purpose and language should fall back to login and zh: %v", got)
	}

	st, err := mgr.Register(ctx, "new@example.com", "123456", "", "公司主机")
	if err != nil || !st.LoggedIn || st.HasPassword {
		t.Fatalf("Register without password: %+v err=%v", st, err)
	}
	if _, has := bodies["/v1/auth/register"]["password"]; has {
		t.Fatal("an empty password must not be sent")
	}
	if st, err = mgr.Register(ctx, "new@example.com", "123456", "Correct-Horse-1", ""); err != nil || !st.HasPassword {
		t.Fatalf("Register with password: %+v err=%v", st, err)
	}
	if device, _ := bodies["/v1/auth/register"]["device"].(map[string]any); device["id"] == "" {
		t.Fatalf("register must bind this device: %v", bodies["/v1/auth/register"])
	}
}

func TestLoopbackEntriesStayOnTheirOwnDevice(t *testing.T) {
	for entry, want := range map[string]string{
		"http://127.0.0.1:8080":      "",
		"http://localhost:3000/app":  "",
		"http://[::1]:9000":          "",
		"http://192.168.1.20:8080":   "http://192.168.1.20:8080",
		"https://nas.example.com/ui": "https://nas.example.com/ui",
		"":                           "",
	} {
		meta, _, _, _ := BuildCompactSnapshot(nil, []api.Monitor{{ID: "x", Name: "x", Entry: entry}}, nil, 0)
		if got := meta.Monitors[0].Entry; got != want {
			t.Errorf("entry %q synced as %q, want %q", entry, got, want)
		}
	}
}

func TestServerCannotChangeUnderASignedInSession(t *testing.T) {
	now := time.UnixMilli(1795350000000)
	clock := func() time.Time { return now }
	client := &scriptedClient{handle: func(call scriptedCall) (int, any, string) {
		if status, body, etag, ok := signedInReply(call); ok {
			return status, body, etag
		}
		return 200, map[string]any{"ok": true}, ""
	}}
	mgr := newSignedIn(t, t.TempDir(), &fakeSecrets{}, client, clock, oneMonitor(10))
	ctx := context.Background()
	other := "https://sync.example.org"
	if _, err := mgr.SetSettings(ctx, &other, nil, nil); err == nil {
		t.Fatal("expected the server change to be refused while signed in")
	}
	same := DefaultServer()
	if _, err := mgr.SetSettings(ctx, &same, nil, nil); err != nil {
		t.Fatalf("saving the current server again should be fine: %v", err)
	}
	if _, err := mgr.Logout(ctx, false); err != nil {
		t.Fatal(err)
	}
	if st, err := mgr.SetSettings(ctx, &other, nil, nil); err != nil || st.ServerURL != other {
		t.Fatalf("signed out, the server can change: %+v err=%v", st, err)
	}
}
