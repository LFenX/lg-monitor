//go:build !windows

// Copyright (c) 2026 LFen (https://lfen.ink) — lg-monitor

package secrets

import (
	"fmt"
	"os"
)

func nativeAdapters() (Protector, AccessPolicy, error) {
	return nil, nil, fmt.Errorf("DPAPI 密钥库当前仅支持 Windows")
}
func replaceFile(temporary, destination string) error { return os.Rename(temporary, destination) }
