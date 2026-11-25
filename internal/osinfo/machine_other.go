// Copyright (c) 2026 LFen (https://lfen.ink) — lg-monitor

//go:build !windows && !linux

package osinfo

func machineID() string { return "" }
