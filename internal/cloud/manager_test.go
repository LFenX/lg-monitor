// Copyright (c) 2026 LFen (https://lfen.ink) — lg-monitor

package cloud

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/LFenX/lg-monitor/internal/api"
)

type fakeSecrets struct {
	mu   sync.Mutex
	data map[string][]byte
}

func (f *fakeSecrets) Get(_ context.Context, key string) ([]byte, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	v := f.data[key]
	if len(v) == 0 {
		return nil, fmt.Errorf("missing")
	}
	return append([]byte{}, v...), nil
}

func (f *fakeSecrets) Set(_ context.Context, key string, val []byte) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	// Like secrets.FileStore, which is what signing out used to trip over.
	if len(val) == 0 {
		return fmt.Errorf("密钥名称和内容不能为空")
	}
	if f.data == nil {
		f.data = map[string][]byte{}
	}
	f.data[key] = append([]byte{}, val...)
	return nil
}

type fakeProvider struct {
	mu       sync.Mutex
	name     string
	avatar   string
	boards   []api.BoardInfo
	monitors []api.Monitor
	docs     map[string]string
}

func (p *fakeProvider) CloudIdentity(_ context.Context) (string, string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.name, p.avatar
}

func (p *fakeProvider) CloudSnapshot(_ context.Context) ([]api.BoardInfo, []api.Monitor, map[string]string, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	hashes := map[string]string{}
	for id, md := range p.docs {
		if h := HashDoc(md); h != "" {
			hashes[id] = h
		}
	}
	return p.boards, p.monitors, hashes, nil
}

func (p *fakeProvider) CloudMonitorDoc(_ context.Context, monitorID string) (string, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.docs[monitorID], nil
}

type fakeCloudServer struct {
	mu           sync.Mutex
	otpCode      string
	token        string
	email        string
	hasPassword  bool
	pushes       []SyncPushRequest
	docsUploaded map[string]SyncDocItem
	docFetches   int
	peerDevice   RemoteDevicePayload
	peerDocs     map[string]string
	seq          int64
	etag         string
}

func (s *fakeCloudServer) Do(_ context.Context, method, _, path, token, ifNoneMatch string, body any, _ bool) (ClientResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	var rawReq []byte
	if body != nil {
		rawReq, _ = json.Marshal(body)
	}
	sent := int64(len(rawReq))

	reply := func(status int, v any, etag string) (ClientResult, error) {
		var b []byte
		if v != nil {
			b, _ = json.Marshal(v)
		}
		return ClientResult{Status: status, Body: b, ETag: etag, BytesSent: sent, BytesReceived: int64(len(b))}, nil
	}

	switch {
	case method == http.MethodPost && path == "/v1/auth/otp/request":
		s.otpCode = "654321"
		return reply(200, map[string]any{"ok": true, "cooldownSec": 60, "expiresInSec": 600}, "")
	case method == http.MethodPost && path == "/v1/auth/otp/verify":
		var req struct {
			Email  string          `json:"email"`
			Code   string          `json:"code"`
			Device DeviceMetaInput `json:"device"`
		}
		_ = json.Unmarshal(rawReq, &req)
		if req.Code != s.otpCode {
			return reply(401, map[string]any{"error": map[string]string{"code": "otp_mismatch", "message": "验证码不正确"}}, "")
		}
		s.email = req.Email
		s.token = "lgs_fake_token"
		return reply(200, map[string]any{
			"token": s.token,
			"user":  map[string]any{"id": "usr_1", "email": s.email, "hasPassword": s.hasPassword},
			"device": map[string]any{
				"id":   req.Device.ID,
				"name": req.Device.Name,
			},
		}, "")
	case method == http.MethodPost && path == "/v1/auth/password":
		if token != s.token {
			return reply(401, map[string]any{"error": map[string]string{"code": "unauthorized", "message": "未授权"}}, "")
		}
		s.hasPassword = true
		return reply(200, map[string]any{"ok": true, "hasPassword": true}, "")
	case method == http.MethodPost && path == "/v1/sync/push":
		if token != s.token {
			return reply(401, map[string]any{"error": map[string]string{"code": "unauthorized", "message": "未授权"}}, "")
		}
		var req SyncPushRequest
		_ = json.Unmarshal(rawReq, &req)
		s.pushes = append(s.pushes, req)
		needDocs := []string{}
		if req.Meta != nil {
			for _, m := range req.Meta.Monitors {
				if m.DocHash != "" && s.docsUploaded[m.ID].Hash != m.DocHash {
					needDocs = append(needDocs, m.ID)
				}
			}
		}
		if req.Meta != nil || req.State != nil {
			s.seq++
		}
		return reply(200, SyncPushResponse{OK: true, Seq: s.seq, NeedDocs: needDocs, ServerTime: 1795350000000}, "")
	case method == http.MethodPut && path == "/v1/sync/docs":
		var req SyncDocsRequest
		_ = json.Unmarshal(rawReq, &req)
		if s.docsUploaded == nil {
			s.docsUploaded = map[string]SyncDocItem{}
		}
		for _, item := range req.Docs {
			s.docsUploaded[item.MonitorID] = item
		}
		return reply(200, map[string]any{"ok": true, "saved": len(req.Docs)}, "")
	case method == http.MethodGet && strings.HasPrefix(path, "/v1/sync/snapshot"):
		etag := fmt.Sprintf(`"sync-%d"`, s.seq)
		if ifNoneMatch == etag {
			return ClientResult{Status: 304, ETag: etag}, nil
		}
		return reply(200, SyncSnapshotResponse{
			Seq:        s.seq,
			ServerTime: 1795350000000,
			Devices:    []RemoteDevicePayload{s.peerDevice},
		}, etag)
	case method == http.MethodGet && strings.HasPrefix(path, "/v1/sync/devices/"):
		s.docFetches++
		parts := strings.Split(strings.TrimPrefix(path, "/v1/sync/devices/"), "/")
		if len(parts) == 3 && parts[1] == "docs" {
			md := s.peerDocs[parts[2]]
			if md != "" {
				return reply(200, SyncDocResponse{
					DeviceID:  parts[0],
					MonitorID: parts[2],
					Hash:      HashDoc(md),
					Markdown:  md,
					UpdatedAt: 1795350000000,
				}, `"`+HashDoc(md)+`"`)
			}
		}
		return reply(404, map[string]any{"error": map[string]string{"code": "not_found", "message": "无文档"}}, "")
	case method == http.MethodPost && path == "/v1/auth/logout":
		s.token = ""
		return reply(200, map[string]any{"ok": true}, "")
	}
	return reply(404, map[string]any{"error": map[string]string{"code": "not_found", "message": "not found"}}, "")
}

func TestCompactPayloadBudgetAndBeatsCodec(t *testing.T) {
	up := 99.95
	lat := 12.4
	beats := make([]string, 48)
	for i := range beats {
		beats[i] = "up"
	}
	beats[47] = "down"

	encoded := EncodeBeats(beats)
	if len(encoded) != 48 {
		t.Fatalf("expected 48-byte beat string, got %d", len(encoded))
	}
	decoded := DecodeBeats(encoded)
	if decoded[0] != "up" || decoded[47] != "down" {
		t.Fatalf("beat roundtrip mismatch: first=%q last=%q", decoded[0], decoded[47])
	}

	// Build 20 monitors and verify gzipped state frame stays well under 1.2 KB!
	monitors := make([]api.Monitor, 0, 20)
	ids := make([]string, 0, 20)
	for i := 0; i < 20; i++ {
		id := fmt.Sprintf("svc-%02d", i)
		ids = append(ids, id)
		monitors = append(monitors, api.Monitor{
			ID:        id,
			Name:      fmt.Sprintf("核心服务 %02d", i),
			State:     "up",
			Uptime:    &up,
			LatencyMS: &lat,
			Kind:      "port",
			Beats:     beats,
		})
	}
	boards := []api.BoardInfo{{ID: "main", Name: "生产服务", Monitors: ids}}
	_, _, state, stateHash1 := BuildCompactSnapshot(boards, monitors, nil, 1795350000000)
	_, _, _, stateHash2 := BuildCompactSnapshot(boards, monitors, nil, 1795350060000)
	if stateHash1 != stateHash2 {
		t.Fatalf("stateHash must ignore timestamp changes so idle heartbeats stay empty")
	}

	raw, err := json.Marshal(state)
	if err != nil {
		t.Fatal(err)
	}
	var gzBuf bytes.Buffer
	gz := gzip.NewWriter(&gzBuf)
	_, _ = gz.Write(raw)
	_ = gz.Close()
	if gzBuf.Len() > 1200 {
		t.Fatalf("20-monitor gzipped state frame too large: %d bytes (want <= 1200)", gzBuf.Len())
	}
}

func TestManagerOTPAuthDeltaPushDocSyncAndRemotePull(t *testing.T) {
	now := time.UnixMilli(1795350000000)
	clock := func() time.Time { return now }
	up := 100.0
	lat := 9.0
	beats := strings.Repeat("1", 48)
	peerDoc := "# 家里 NAS\n\n部署在群晖 Docker 上。"

	srv := &fakeCloudServer{
		seq: 5,
		peerDevice: RemoteDevicePayload{
			ID:       "dev-peer-home",
			Name:     "家里电脑",
			Avatar:   "lg-cat-1",
			OS:       "windows",
			Version:  "0.17.0",
			Online:   true,
			LastSeen: now.UnixMilli(),
			Seq:      5,
			Meta: &CompactMeta{
				Boards: []CompactBoard{{ID: "home-main", Name: "家庭服务", Monitors: []string{"nas", "db"}}},
				Monitors: []CompactMonitorMeta{
					{ID: "nas", Name: "群晖 NAS", Kind: "url", Desc: "家庭存储", DocHash: HashDoc(peerDoc), Deps: []CompactDep{{"db", "data", 0, ""}}},
					{ID: "db", Name: "PostgreSQL", Parent: "nas", Kind: "service"},
				},
			},
			State: &CompactState{
				TS: now.UnixMilli(),
				Monitors: map[string]CompactMonitorState{
					"nas": {State: 1, Uptime: 10000, Latency: 15, Beats: beats},
					"db":  {State: 2, Uptime: 9850, Latency: -1, Firing: 1, Issue: "服务已停止", Beats: strings.Repeat("1", 47) + "2"},
				},
			},
		},
		peerDocs: map[string]string{"nas": peerDoc},
	}

	provider := &fakeProvider{
		name:   "公司台式机",
		avatar: "fiora-3",
		boards: []api.BoardInfo{{ID: "main", Name: "核心业务", Monitors: []string{"api"}}},
		monitors: []api.Monitor{
			{ID: "api", Name: "业务接口", State: "up", Uptime: &up, LatencyMS: &lat, Kind: "port", Beats: DecodeBeats(beats)},
		},
		docs: map[string]string{"api": "# 业务接口\n\n运行于 8080 端口"},
	}

	mgr := NewManager(Options{
		Home:    t.TempDir(),
		Version: "0.17.0",
		Secrets: &fakeSecrets{},
		Client:  srv,
		Now:     clock,
	})
	mgr.SetProvider(provider)

	ctx := context.Background()
	if mgr.Status(ctx).LoggedIn {
		t.Fatal("fresh manager should not be logged in")
	}

	// 1. OTP Login
	if _, err := mgr.RequestOTP(ctx, "user@example.com", "login", "zh"); err != nil {
		t.Fatalf("RequestOTP: %v", err)
	}
	st, err := mgr.VerifyOTP(ctx, "user@example.com", "654321", "公司主机", true)
	if err != nil {
		t.Fatalf("VerifyOTP: %v", err)
	}
	if !st.LoggedIn || st.Email != "user@example.com" || st.DeviceName != "公司主机" {
		t.Fatalf("unexpected status after OTP verify: %+v", st)
	}

	// 2. First push uploads meta + state + requested doc via needDocs.
	if err := mgr.PushOnce(ctx, true); err != nil {
		t.Fatalf("first PushOnce: %v", err)
	}
	if len(srv.pushes) != 1 || srv.pushes[0].Meta == nil || srv.pushes[0].State == nil {
		t.Fatalf("expected initial push to include meta and state: %+v", srv.pushes)
	}
	if srv.docsUploaded["api"].Markdown != provider.docs["api"] {
		t.Fatalf("expected server-requested doc upload for api")
	}

	// 3. Second push after 61s with no changes sends heartbeat ONLY (nil meta & nil state).
	now = now.Add(61 * time.Second)
	if err := mgr.PushOnce(ctx, false); err != nil {
		t.Fatalf("heartbeat PushOnce: %v", err)
	}
	if len(srv.pushes) != 2 || srv.pushes[1].Meta != nil || srv.pushes[1].State != nil {
		t.Fatalf("expected idle heartbeat to omit meta and state: %+v", srv.pushes[1])
	}

	// 4. Status change pushes state ONLY (omits unchanged meta).
	provider.mu.Lock()
	provider.monitors[0].State = "down"
	provider.monitors[0].Issue = "端口超时"
	provider.mu.Unlock()
	if err := mgr.PushOnce(ctx, false); err != nil {
		t.Fatalf("state delta PushOnce: %v", err)
	}
	if len(srv.pushes) != 3 || srv.pushes[2].Meta != nil || srv.pushes[2].State == nil {
		t.Fatalf("expected state-only push when only health changed: %+v", srv.pushes[2])
	}

	// 5. Pull multi-device snapshot & verify board + detail + cached on-demand doc fetch.
	if err := mgr.PullOnce(ctx); err != nil {
		t.Fatalf("PullOnce: %v", err)
	}
	devices := mgr.Devices()
	if len(devices) != 1 || devices[0].ID != "dev-peer-home" || devices[0].DownCount != 1 {
		t.Fatalf("unexpected synced devices: %+v", devices)
	}
	bv, err := mgr.DeviceBoard("dev-peer-home", "home-main")
	if err != nil || len(bv.Board.Items) != 2 || bv.Board.Summary.Down != 1 {
		t.Fatalf("unexpected DeviceBoard: %+v err=%v", bv, err)
	}
	detail, err := mgr.DeviceMonitor("dev-peer-home", "nas")
	if err != nil || !detail.Monitor.HasDoc || len(detail.Slots) != 48 {
		t.Fatalf("unexpected DeviceMonitor: %+v err=%v", detail, err)
	}

	doc1, err := mgr.DeviceMonitorDoc(ctx, "dev-peer-home", "nas")
	if err != nil || doc1 != peerDoc {
		t.Fatalf("first DeviceMonitorDoc: %q err=%v", doc1, err)
	}
	doc2, err := mgr.DeviceMonitorDoc(ctx, "dev-peer-home", "nas")
	if err != nil || doc2 != peerDoc || srv.docFetches != 1 {
		t.Fatalf("second DeviceMonitorDoc should hit cache (fetches=%d): %q err=%v", srv.docFetches, doc2, err)
	}

	// 6. Second pull gets 304 Not Modified cleanly.
	if err := mgr.PullOnce(ctx); err != nil {
		t.Fatalf("304 PullOnce: %v", err)
	}

	// 7. Set password & logout clears session.
	if st, err = mgr.SetPassword(ctx, "", "StrongPass123!"); err != nil || !st.HasPassword {
		t.Fatalf("SetPassword failed: %+v err=%v", st, err)
	}
	if st, err = mgr.Logout(ctx, false); err != nil || st.LoggedIn {
		t.Fatalf("Logout failed: %+v err=%v", st, err)
	}
}
