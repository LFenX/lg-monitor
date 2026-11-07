package app

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sync"
	"time"

	"github.com/LFenX/lg-monitor/internal/api"
	"github.com/LFenX/lg-monitor/internal/store"
)

const TraySetupHint = "需要管理员运行一次 lgmon tray setup"

type TrayCredentials interface {
	Load() (address string, token []byte, err error)
}

type remoteTray struct {
	source    TrayCredentials
	transport http.RoundTripper
	mu        sync.Mutex
	rejected  [32]byte
}

func NewRemoteTray(source TrayCredentials, transport http.RoundTripper) TrayBackend {
	if transport == nil {
		// A desktop-local session must never be sent through a configured HTTP proxy.
		t := http.DefaultTransport.(*http.Transport).Clone()
		t.Proxy = nil
		transport = t
	}
	return &remoteTray{source: source, transport: transport}
}

type trayHTTPFault int

func (e trayHTTPFault) Error() string { return fmt.Sprintf("监控器返回 HTTP %d", e) }

func (b *remoteTray) request(ctx context.Context, address, method, path, token string, body []byte, value any) (api.Scope, error) {
	base, err := url.Parse(address)
	if err != nil || !trayPanelURL(address) || (base.Path != "" && base.Path != "/") {
		return "", fmt.Errorf("托盘监控器地址无效")
	}
	base.Path, base.RawQuery = path, ""
	if path == "/api/v1/alerts" {
		base.RawQuery = "state=firing"
	}
	call, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(call, method, base.String(), bytes.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("创建托盘请求失败：%w", err)
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	res, err := b.transport.RoundTrip(req)
	if err != nil {
		if res != nil && res.Body != nil {
			res.Body.Close()
		}
		return "", fmt.Errorf("连接监控器失败")
	}
	if res == nil || res.Body == nil {
		return "", fmt.Errorf("监控器响应不完整")
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		return "", trayHTTPFault(res.StatusCode)
	}
	scope := api.Scope(res.Header.Get("X-Lgmon-Scope"))
	if !scope.Valid() {
		return "", fmt.Errorf("监控器未返回有效权限")
	}
	data, err := io.ReadAll(io.LimitReader(res.Body, 16*1024*1024+1))
	if err != nil || len(data) > 16*1024*1024 || json.Unmarshal(data, value) != nil {
		return "", fmt.Errorf("监控器响应格式无效")
	}
	return scope, nil
}

func (b *remoteTray) credentials() (string, []byte) {
	address, token, err := b.source.Load()
	if address == "" {
		address = "http://127.0.0.1:7444"
	}
	if err != nil {
		clear(token)
		token = nil
	}
	b.mu.Lock()
	rejected := len(token) > 0 && sha256.Sum256(token) == b.rejected
	b.mu.Unlock()
	if rejected {
		clear(token)
		token = nil
	}
	return address, token
}

func (b *remoteTray) reject(token []byte) {
	b.mu.Lock()
	b.rejected = sha256.Sum256(token)
	b.mu.Unlock()
}

func (b *remoteTray) Status(ctx context.Context) (TrayStatus, error) {
	address, saved := b.credentials()
	defer clear(saved)
	state := TrayStatus{PanelURL: address, Help: TraySetupHint}
	read := func(token string) error {
		reader, err := NewTrayHTTPReader(address, trayBearer{transport: b.transport, token: token})
		if err != nil {
			return err
		}
		alerts, err := reader.Read(ctx)
		if err != nil {
			return err
		}
		var pause store.NotificationPause
		scope, err := b.request(ctx, address, "GET", "/api/v1/notifications/pause", token, nil, &pause)
		if err != nil {
			return err
		}
		if pause.Paused && (pause.Until == nil || pause.StartedAt == nil) {
			return fmt.Errorf("通知暂停状态无效")
		}
		state.Firing, state.Critical, state.Pause = alerts.Firing, alerts.Critical, pause
		state.CanPause = scope.Allows(api.ScopeConfig) && alerts.Scope.Allows(api.ScopeConfig)
		if state.CanPause {
			state.Help = ""
		}
		return nil
	}
	if len(saved) > 0 {
		// Probe the pause route first so revocation is distinguishable from a network failure.
		var pause store.NotificationPause
		_, err := b.request(ctx, address, "GET", "/api/v1/notifications/pause", string(saved), nil, &pause)
		if err == nil {
			err = read(string(saved))
			return state, err
		}
		if err != trayHTTPFault(401) {
			return state, err
		}
		b.reject(saved)
	}
	var session api.SessionResult
	if _, err := b.request(ctx, address, "GET", "/api/v1/session", "", nil, &session); err != nil {
		state.Help = "请运行 lgmon tray setup"
		return state, err
	}
	if session.Token == "" || session.Scope != api.ScopeRead {
		return state, fmt.Errorf("监控器返回的本机会话权限无效")
	}
	err := read(session.Token)
	return state, err
}

type trayBearer struct {
	transport http.RoundTripper
	token     string
}

func (t trayBearer) RoundTrip(req *http.Request) (*http.Response, error) {
	copy := req.Clone(req.Context())
	copy.Header.Set("Authorization", "Bearer "+t.token)
	return t.transport.RoundTrip(copy)
}

func (b *remoteTray) change(ctx context.Context, method string, body []byte) error {
	address, token := b.credentials()
	defer clear(token)
	if len(token) == 0 {
		return fmt.Errorf("%s", TraySetupHint)
	}
	var pause store.NotificationPause
	_, err := b.request(ctx, address, method, "/api/v1/notifications/pause", string(token), body, &pause)
	if err == trayHTTPFault(401) {
		b.reject(token)
	}
	return err
}
func (b *remoteTray) Pause(ctx context.Context, duration time.Duration) error {
	body, _ := json.Marshal(map[string]string{"for": duration.String()})
	return b.change(ctx, "PUT", body)
}
func (b *remoteTray) Resume(ctx context.Context) error { return b.change(ctx, "DELETE", nil) }
