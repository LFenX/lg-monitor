//go:build !windows

// Copyright (c) 2026 LFen (https://lfen.ink) — lg-monitor

package collector

import (
	"context"
	"fmt"
)

func (nativeServices) Open(context.Context, bool) (serviceSession, error) {
	return nil, fmt.Errorf("Windows 服务采集仅支持 Windows")
}
