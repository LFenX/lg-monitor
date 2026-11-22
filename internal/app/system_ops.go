// Copyright (c) 2026 LFen (https://lfen.ink) — lg-monitor

package app

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/LFenX/lg-monitor/internal/api"
	"github.com/LFenX/lg-monitor/internal/update"
)

const webUpdateRecheck = 20 * time.Minute

type systemUpdateManager struct {
	mu      sync.Mutex
	state   string
	during  string
	message string
	release update.Release
	checked time.Time
	path    string
	done    int64
	total   int64
}

func (b *APIBackend) systemOperation(ctx context.Context, op api.Operation, r api.Request) (any, error) {
	if p, ok := api.CurrentPrincipal(ctx); ok && p.Guest {
		return nil, &api.Fault{Status: 403, Code: "guest_forbidden", Message: "内网访客模式不能执行服务重启或版本更新"}
	}
	switch op {
	case api.RestartSystem:
		return b.handleSystemRestart(ctx)
	case api.GetSystemUpdate:
		return b.systemUpdateSnapshot(), nil
	case api.ActionSystemUpdate:
		var input api.SystemUpdateActionInput
		if err := api.DecodeBody(r.Body, &input); err != nil {
			return nil, err
		}
		return b.handleSystemUpdateAction(ctx, input.Action)
	default:
		return nil, invalidAPI("不支持的系统操作")
	}
}

func (b *APIBackend) handleSystemRestart(ctx context.Context) (api.SystemRestartResult, error) {
	if b.options.Restart == nil {
		return api.SystemRestartResult{}, &api.Fault{Status: 503, Code: "restart_unavailable", Message: "当前运行模式不支持网页一键重启"}
	}
	mode, err := b.options.Restart(ctx)
	if err != nil {
		return api.SystemRestartResult{}, &api.Fault{Status: 500, Code: "restart_failed", Message: err.Error(), Cause: err}
	}
	message := "正在重启 lgmon，请稍候几秒…"
	if mode == "service" {
		message = "正在重启 lgmon Windows 服务，请稍候几秒…"
	}
	return api.SystemRestartResult{OK: true, Mode: mode, Message: message}, nil
}

func (b *APIBackend) updateHTTPClient() *http.Client {
	if b.options.UpdateClient != nil {
		return b.options.UpdateClient
	}
	fallback := systemProxyURL()
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = func(request *http.Request) (*url.URL, error) {
		if address, err := http.ProxyFromEnvironment(request); address != nil || err != nil {
			return address, err
		}
		host := request.URL.Hostname()
		if ip := net.ParseIP(host); host == "localhost" || (ip != nil && ip.IsLoopback()) || !strings.Contains(host, ".") {
			return nil, nil
		}
		return fallback, nil
	}
	transport.ResponseHeaderTimeout = 30 * time.Second
	return &http.Client{Transport: transport}
}

func (b *APIBackend) updateFeedURL() string {
	if b.options.UpdateFeed != "" {
		return b.options.UpdateFeed
	}
	if feed := os.Getenv("LGMON_UPDATE_FEED"); feed != "" {
		return feed
	}
	return update.DefaultFeed
}

func (b *APIBackend) updateDownloadDir() string {
	if dir := os.Getenv("LGMON_UPDATE_DIR"); dir != "" {
		return dir
	}
	if b.options.Home != "" {
		return filepath.Join(b.options.Home, "updates")
	}
	if base, err := os.UserCacheDir(); err == nil {
		return filepath.Join(base, "lg-monitor", "updates")
	}
	return filepath.Join(os.TempDir(), "lgmon-updates")
}

func (b *APIBackend) systemUpdateSnapshot() api.SystemUpdateState {
	current := strings.TrimPrefix(strings.TrimSpace(b.options.Version), "v")
	if current == "" || current == "dev" {
		return api.SystemUpdateState{State: "dev", Current: "dev"}
	}
	u := &b.updater
	u.mu.Lock()
	defer u.mu.Unlock()
	state := u.state
	if state == "" {
		state = "idle"
	}
	checked := int64(0)
	if !u.checked.IsZero() {
		checked = u.checked.UnixMilli()
	}
	return api.SystemUpdateState{
		State:   state,
		During:  u.during,
		Message: u.message,
		Current: current,
		Version: u.release.Version,
		Notes:   u.release.Notes,
		Page:    u.release.Page,
		Size:    u.release.Size,
		Done:    u.done,
		Total:   u.total,
		Checked: checked,
	}
}

func (b *APIBackend) handleSystemUpdateAction(ctx context.Context, action string) (api.SystemUpdateState, error) {
	current := strings.TrimPrefix(strings.TrimSpace(b.options.Version), "v")
	if current == "" || current == "dev" {
		return api.SystemUpdateState{State: "dev", Current: "dev"}, nil
	}
	switch action {
	case "check", "refresh":
		b.startSystemUpdateCheck(action == "refresh")
		return b.systemUpdateSnapshot(), nil
	case "download":
		if err := b.startSystemUpdateDownload(); err != nil {
			return api.SystemUpdateState{}, invalidAPI(err.Error())
		}
		return b.systemUpdateSnapshot(), nil
	case "apply":
		if err := b.applySystemUpdate(); err != nil {
			return api.SystemUpdateState{}, &api.Fault{Status: 400, Code: "update_apply_failed", Message: err.Error(), Cause: err}
		}
		return b.systemUpdateSnapshot(), nil
	default:
		return api.SystemUpdateState{}, invalidAPI("不支持的更新操作，请使用 check、refresh、download 或 apply")
	}
}

func (b *APIBackend) startSystemUpdateCheck(force bool) {
	u := &b.updater
	now := b.options.Now()
	u.mu.Lock()
	recent := !force && u.state != "" && u.state != "idle" && u.state != "error" && now.Sub(u.checked) < webUpdateRecheck
	if recent || u.state == "checking" || u.state == "downloading" || u.state == "ready" || u.state == "applying" {
		u.mu.Unlock()
		return
	}
	u.state, u.during, u.message = "checking", "", ""
	u.mu.Unlock()

	current := strings.TrimPrefix(strings.TrimSpace(b.options.Version), "v")
	b.background.Add(1)
	go func() {
		defer b.background.Done()
		checkCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		release, err := update.Latest(checkCtx, b.updateHTTPClient(), b.updateFeedURL())
		u.mu.Lock()
		u.checked = b.options.Now()
		u.release = update.Release{}
		switch {
		case err != nil:
			u.state, u.during, u.message = "error", "check", err.Error()
		case release.Version == "" || !update.Newer(release.Version, current):
			u.state, u.during, u.message = "none", "", ""
		default:
			u.release = release
			u.state, u.during, u.message = "available", "", ""
		}
		u.mu.Unlock()
	}()
}

func (b *APIBackend) startSystemUpdateDownload() error {
	u := &b.updater
	u.mu.Lock()
	if u.state == "downloading" || u.state == "ready" || u.state == "applying" {
		u.mu.Unlock()
		return nil
	}
	if u.release.Version == "" || (u.state != "available" && !(u.state == "error" && u.during == "download")) {
		u.mu.Unlock()
		return fmt.Errorf("当前没有可下载的新版本，请先检查更新")
	}
	release := u.release
	u.state, u.during, u.message = "downloading", "", ""
	u.done, u.total = 0, release.Size
	u.mu.Unlock()

	b.background.Add(1)
	go func() {
		defer b.background.Done()
		dlCtx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
		defer cancel()
		path, err := update.Download(dlCtx, b.updateHTTPClient(), release, b.updateDownloadDir(), func(done, total int64) {
			u.mu.Lock()
			u.done, u.total = done, total
			u.mu.Unlock()
		})
		u.mu.Lock()
		if err != nil {
			u.state, u.during, u.message = "error", "download", err.Error()
		} else {
			u.path = path
			u.state, u.during, u.message = "ready", "", ""
		}
		u.mu.Unlock()
	}()
	return nil
}

func (b *APIBackend) applySystemUpdate() error {
	u := &b.updater
	u.mu.Lock()
	if u.state != "ready" || u.path == "" {
		u.mu.Unlock()
		return fmt.Errorf("新版本安装包尚未下载完成")
	}
	installerPath := u.path
	u.state, u.during, u.message = "applying", "", ""
	u.mu.Unlock()

	applyFn := b.options.UpdateApply
	if applyFn == nil {
		applyFn = func(path string) error {
			return launchUpdateInstaller(path, b.options.ServiceIdentity)
		}
	}
	if err := applyFn(installerPath); err != nil {
		u.mu.Lock()
		u.state, u.during, u.message = "error", "apply", err.Error()
		u.mu.Unlock()
		return err
	}
	return nil
}
