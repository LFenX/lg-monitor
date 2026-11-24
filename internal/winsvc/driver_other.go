//go:build !windows

// Copyright (c) 2026 LFen (https://lfen.ink) — lg-monitor

package winsvc

import (
	"fmt"
	"os"
)

func NativeController() (*Controller, error) {
	return nil, fmt.Errorf("Windows 服务管理仅支持 Windows")
}

// IsAdministrator means root outside Windows: the account that owns the service home.
func IsAdministrator() (bool, error) { return os.Geteuid() == 0, nil }
