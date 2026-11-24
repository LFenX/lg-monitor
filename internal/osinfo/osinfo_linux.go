// Copyright (c) 2026 LFen (https://lfen.ink) — lg-monitor

//go:build linux

package osinfo

import "os"

func detect() string {
	for _, path := range []string{"/etc/os-release", "/usr/lib/os-release"} {
		if raw, err := os.ReadFile(path); err == nil && len(raw) > 0 {
			return ParseOSRelease(string(raw))
		}
	}
	return "Linux"
}
