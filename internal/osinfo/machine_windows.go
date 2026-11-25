// Copyright (c) 2026 LFen (https://lfen.ink) — lg-monitor

//go:build windows

package osinfo

import "golang.org/x/sys/windows/registry"

func machineID() string {
	k, err := registry.OpenKey(registry.LOCAL_MACHINE, `SOFTWARE\Microsoft\Cryptography`, registry.QUERY_VALUE|registry.WOW64_64KEY)
	if err != nil {
		return ""
	}
	defer k.Close()
	value, _, _ := k.GetStringValue("MachineGuid")
	return value
}
