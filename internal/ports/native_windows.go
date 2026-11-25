// Copyright (c) 2026 LFen (https://lfen.ink) — lg-monitor

package ports

import (
	"context"
	"errors"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"golang.org/x/sys/windows"
)

// Reserved reports whether binding was refused because Windows holds the port back, rather than because a
// program listens on it.
func Reserved(err error) bool { return errors.Is(err, windows.WSAEACCES) }

func netsh(parent context.Context, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(parent, 5*time.Second)
	defer cancel()
	program := "netsh.exe"
	if directory, err := windows.GetSystemDirectory(); err == nil {
		program = filepath.Join(directory, program)
	}
	command := exec.CommandContext(ctx, program, args...)
	command.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: windows.CREATE_NO_WINDOW}
	output, err := command.CombinedOutput()
	return strings.TrimSpace(string(output)), err
}
