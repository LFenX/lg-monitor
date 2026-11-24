//go:build !windows

// Copyright (c) 2026 LFen (https://lfen.ink) — lg-monitor

package collector

import "os/exec"

func hideWindow(*exec.Cmd) {}
