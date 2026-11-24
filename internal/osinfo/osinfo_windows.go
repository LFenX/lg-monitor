// Copyright (c) 2026 LFen (https://lfen.ink) — lg-monitor

//go:build windows

package osinfo

import "golang.org/x/sys/windows/registry"

func detect() string {
	k, err := registry.OpenKey(registry.LOCAL_MACHINE, `SOFTWARE\Microsoft\Windows NT\CurrentVersion`, registry.QUERY_VALUE)
	if err != nil {
		return "Windows"
	}
	defer k.Close()

	product, _, _ := k.GetStringValue("ProductName")
	display, _, _ := k.GetStringValue("DisplayVersion")
	if display == "" {
		display, _, _ = k.GetStringValue("ReleaseId")
	}
	build, _, _ := k.GetStringValue("CurrentBuildNumber")
	if build == "" {
		build, _, _ = k.GetStringValue("CurrentBuild")
	}
	return FormatWindowsVersion(product, display, build)
}
