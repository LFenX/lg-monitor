//go:build !windows

// Copyright (c) 2026 LFen (https://lfen.ink) — lg-monitor

package collector

import (
	"context"
	"fmt"
	"os/exec"
)

type NativeRestartServices struct{}

func (NativeRestartServices) Open(ctx context.Context, name string) (RestartServiceHandle, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if _, err := exec.LookPath("systemctl"); err != nil {
		return nil, fmt.Errorf("未找到 systemctl，当前系统不支持服务重启")
	}
	unit := systemdUnitName(name)
	if unit == "" {
		return nil, fmt.Errorf("服务名称无效")
	}
	return &systemdRestartHandle{ctx: ctx, unit: unit, run: execSystemctl}, nil
}
