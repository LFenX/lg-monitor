//go:build windows

// Copyright (c) 2026 LFen (https://lfen.ink) — lg-monitor

package app

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/LFenX/lg-monitor/internal/api"
	"golang.org/x/sys/windows"
)

func diskUsageForPath(target string) (api.DiskVolume, error) {
	probe := filepath.Clean(target)
	for {
		if info, err := os.Stat(probe); err == nil && info.IsDir() {
			break
		}
		parent := filepath.Dir(probe)
		if parent == probe {
			break
		}
		probe = parent
	}
	ptr, err := windows.UTF16PtrFromString(probe)
	if err != nil {
		return api.DiskVolume{}, err
	}
	var freeAvail, totalBytes, totalFree uint64
	if err := windows.GetDiskFreeSpaceEx(ptr, &freeAvail, &totalBytes, &totalFree); err != nil {
		return api.DiskVolume{}, fmt.Errorf("读取磁盘空间失败：%w", err)
	}
	vol := filepath.VolumeName(filepath.Clean(target))
	root := probe
	if vol != "" {
		root = strings.ToUpper(vol) + `\`
	}
	used := uint64(0)
	if totalBytes >= freeAvail {
		used = totalBytes - freeAvail
	}
	return api.DiskVolume{
		Root:       root,
		TotalBytes: totalBytes,
		FreeBytes:  freeAvail,
		UsedBytes:  used,
	}, nil
}

func listLocalDrives(currentHome string) []api.DiskVolume {
	mask, err := windows.GetLogicalDrives()
	if err != nil {
		return []api.DiskVolume{}
	}
	currentVol := strings.ToUpper(filepath.VolumeName(filepath.Clean(currentHome)))
	drives := make([]api.DiskVolume, 0, 4)
	for i := 0; i < 26; i++ {
		if mask&(1<<uint(i)) == 0 {
			continue
		}
		letter := fmt.Sprintf("%c:", 'A'+i)
		root := letter + `\`
		ptr, err := windows.UTF16PtrFromString(root)
		if err != nil {
			continue
		}
		if windows.GetDriveType(ptr) != windows.DRIVE_FIXED {
			continue
		}
		vol, err := diskUsageForPath(root)
		if err != nil || vol.TotalBytes == 0 {
			continue
		}
		vol.Root = root
		vol.SuggestedPath = filepath.Join(root, "lgmon-data")
		vol.Current = strings.EqualFold(letter, currentVol)
		drives = append(drives, vol)
	}
	return drives
}
