//go:build !windows

// Copyright (c) 2026 LFen (https://lfen.ink) — lg-monitor

package main

import (
	"context"
	"fmt"
)

func openBrowser(context.Context, string) error {
	return fmt.Errorf("当前系统请手动打开飞书权限链接")
}
