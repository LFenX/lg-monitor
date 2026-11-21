//go:build windows

// Copyright (c) 2026 LFen (https://lfen.ink) — lg-monitor

package app

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/LFenX/lg-monitor/internal/desktop"
)

// OpenAppWindow shows lgmon's small start page in lgmon's own window (lgmon-window.exe). When that program is
// missing it falls back to an Edge app window, which has no address bar or tabs, and then to the default browser.
func OpenAppWindow(ctx context.Context, address string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	// The service has no desktop; a window it started ran as SYSTEM and only showed WebView2's error box.
	if desktop.NoDesktop() {
		return fmt.Errorf("服务进程没有桌面，不打开 lgmon 窗口")
	}
	if !trayPanelURL(address) {
		return fmt.Errorf("lgmon 地址必须为不含凭据、查询参数和片段的 HTTP/HTTPS 地址")
	}
	target := StartPageURL(address)
	// LGMON_WINDOW_DATA keeps a development window's browser profile inside its own home.
	if desktop.OpenWindow(target, os.Getenv("LGMON_WINDOW_DATA")) == nil {
		return nil
	}
	if desktop.OpenEdgeApp(target, 460, 760) == nil {
		return nil
	}
	return desktop.ShellOpen(target)
}

// StartPageURL is the small page shown when lgmon is opened.
func StartPageURL(address string) string {
	return strings.TrimRight(address, "/") + "/#/start"
}

func shellOpen(target string) error { return desktop.ShellOpen(target) }

func (NativeTrayOpener) Window(ctx context.Context, address string) error {
	return OpenAppWindow(ctx, address)
}
