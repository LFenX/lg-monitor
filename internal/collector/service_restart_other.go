//go:build !windows

// Copyright (c) 2026 LFen (https://lfen.ink) — lg-monitor

package collector

import (
	"context"
	"fmt"
)

type NativeRestartServices struct{}

func (NativeRestartServices) Open(context.Context, string) (RestartServiceHandle, error) {
	return nil, fmt.Errorf("服务控制只支持 Windows")
}
