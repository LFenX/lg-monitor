// Copyright (c) 2026 LFen (https://lfen.ink) — lg-monitor

//go:build linux

package osinfo

import "os"

func machineID() string {
	for _, path := range []string{"/etc/machine-id", "/var/lib/dbus/machine-id"} {
		if raw, err := os.ReadFile(path); err == nil && len(raw) > 0 {
			return string(raw)
		}
	}
	return ""
}
