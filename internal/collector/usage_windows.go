//go:build windows

// Copyright (c) 2026 LFen (https://lfen.ink) — lg-monitor

package collector

import (
	"os/exec"
	"syscall"

	"golang.org/x/sys/windows"
)

// hideWindow keeps a console helper such as docker from flashing a window on the desktop.
func hideWindow(command *exec.Cmd) {
	command.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: windows.CREATE_NO_WINDOW}
}
