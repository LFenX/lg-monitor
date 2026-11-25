//go:build windows

// Copyright (c) 2026 LFen (https://lfen.ink) — lg-monitor

package winsvc

import (
	"bytes"
	"context"
	"os/exec"
	"path/filepath"
	"syscall"
	"time"

	"golang.org/x/sys/windows"
	"golang.org/x/text/encoding/unicode"
)

// LastFailure is why the service failed within the last two minutes, or "". The service log is private, but the
// event log entry the service leaves is readable to everyone.
func LastFailure(parent context.Context) string {
	ctx, cancel := context.WithTimeout(parent, 5*time.Second)
	defer cancel()
	program := "wevtutil.exe"
	if directory, err := windows.GetSystemDirectory(); err == nil {
		program = filepath.Join(directory, program)
	}
	query := "*[System[Provider[@Name='" + Name + "'] and Level=2 and TimeCreated[timediff(@SystemTime) <= 120000]]]"
	command := exec.CommandContext(ctx, program, "qe", "Application", "/q:"+query, "/c:1", "/rd:true", "/f:xml", "/uni:true")
	command.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: windows.CREATE_NO_WINDOW}
	output, err := command.Output()
	if err != nil {
		return ""
	}
	// Redirected output is UTF-16; the mark at its start decides the byte order.
	text, err := unicode.UTF16(unicode.LittleEndian, unicode.UseBOM).NewDecoder().Bytes(output)
	if err != nil {
		return ""
	}
	return failureMessage(bytes.TrimSpace(text))
}
