// Copyright (c) 2026 LFen (https://lfen.ink) — lg-monitor

//go:build !windows && !linux

package osinfo

import "runtime"

func detect() string {
	if runtime.GOOS == "darwin" {
		return "macOS"
	}
	return runtime.GOOS
}
