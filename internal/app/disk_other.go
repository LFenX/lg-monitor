//go:build !windows

// Copyright (c) 2026 LFen (https://lfen.ink) — lg-monitor

package app

import (
	"path/filepath"

	"github.com/LFenX/lg-monitor/internal/api"
)

func diskUsageForPath(target string) (api.DiskVolume, error) {
	return api.DiskVolume{Root: filepath.VolumeName(filepath.Clean(target))}, nil
}

func listLocalDrives(string) []api.DiskVolume {
	return []api.DiskVolume{}
}
