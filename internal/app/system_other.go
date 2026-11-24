//go:build !windows

// Copyright (c) 2026 LFen (https://lfen.ink) — lg-monitor

package app

import (
	"context"
	"fmt"
	"net/url"
)

func systemProxyURL() *url.URL { return nil }

func launchRestartHelper(string, func()) (string, error) {
	return "", fmt.Errorf("当前平台不支持自动重启服务")
}

func launchUpdateWindow(context.Context, string, string) error {
	return fmt.Errorf("Linux 上请在终端运行 sudo lgmon update -y 完成更新")
}
