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

func launchUpdateWindow(context.Context, string, bool) error {
	return fmt.Errorf("当前平台不支持应用内自动更新")
}
