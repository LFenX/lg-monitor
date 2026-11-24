//go:build !windows

// Copyright (c) 2026 LFen (https://lfen.ink) — lg-monitor

package collector

import (
	"context"
	"fmt"
	"os/exec"
)

func (nativeServices) Open(ctx context.Context, _ bool) (serviceSession, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if _, err := exec.LookPath("systemctl"); err != nil {
		return nil, fmt.Errorf("未找到 systemctl，当前系统不支持 systemd 服务采集")
	}
	return &systemdSession{run: execSystemctl}, nil
}
