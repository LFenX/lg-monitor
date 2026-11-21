// Copyright (c) 2026 LFen (https://lfen.ink) — lg-monitor

package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"golang.org/x/text/encoding/unicode"
)

func TestSelfRunHomeTellsMonitorsFromAgentsAndCommands(t *testing.T) {
	for _, c := range []struct {
		args    []string
		home    string
		monitor bool
	}{
		// The Start menu's lgmon, opened while the service was missing, as found on a colleague's computer.
		{[]string{"--home", `C:\ProgramData\lg-monitor`}, `C:\ProgramData\lg-monitor`, true},
		{nil, "", true},
		{[]string{"--log-level", "debug", "--home=D:\\data"}, `D:\data`, true},
		{[]string{"run", "--role", "both", "--home", `C:\home`, "--listen", "127.0.0.1:7443", "--no-tray"}, `C:\home`, true},
		{[]string{"--home", `C:\home`, "run"}, `C:\home`, true},
		{[]string{"mcp"}, "", false},
		{[]string{"--home", `C:\home`, "mcp"}, "", false},
		{[]string{"setup", "install", "--home", `C:\home`, "--confirm"}, "", false},
		{[]string{"alerts", "--json"}, "", false},
		{[]string{"--unknown", "run"}, "", false},
	} {
		home, monitor := selfRunHome(c.args)
		if monitor != c.monitor || (monitor && home != c.home) {
			t.Errorf("%q: home=%q monitor=%v, want %q %v", c.args, home, monitor, c.home, c.monitor)
		}
	}
}

func TestOnlyCopiesRunningTheMonitorForThisHomeAreEnded(t *testing.T) {
	root := t.TempDir()
	program, home := filepath.Join(root, "program"), filepath.Join(root, "home")
	if err := os.MkdirAll(program, 0o700); err != nil {
		t.Fatal(err)
	}
	ini := "[Setup]\r\nProduct=lg-monitor\r\nHome=" + home + "\r\nRole=both\r\n"
	data, err := unicode.UTF16(unicode.LittleEndian, unicode.UseBOM).NewEncoder().Bytes([]byte(ini))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(program, "setup.ini"), data, 0o600); err != nil {
		t.Fatal(err)
	}
	image := filepath.Join(program, "lgmon.exe")
	copies := []setupLocalCopy{
		{PID: 1, Executable: image, Args: []string{"--home", home}},
		{PID: 2, Executable: image, Args: nil}, // names no home: runs the one setup.ini records
		{PID: 3, Executable: filepath.Join(program, "lgmon.exe.previous-20261120023614-0"), Args: []string{"run", "--home", home}},
		{PID: 4, Executable: image, Args: []string{"mcp"}},
		{PID: 5, Executable: image, Args: []string{"setup", "install", "--home", home, "--confirm"}},
		{PID: 6, Executable: image, Args: []string{"--home", filepath.Join(root, "other-home")}},
		{PID: 7, Executable: filepath.Join(root, "elsewhere", "lgmon.exe"), Args: []string{"--home", home}},
		{PID: 8, Executable: filepath.Join(program, "lgmon-window.exe"), Args: nil},
	}
	var ended []int32
	stopped, err := stopSelfRunCopies(context.Background(), program, home, copies, func(_ context.Context, pid int32) error {
		ended = append(ended, pid)
		return nil
	})
	if err != nil || stopped != 3 || !slices.Equal(ended, []int32{1, 2, 3}) {
		t.Fatalf("stopped=%d ended=%v err=%v", stopped, ended, err)
	}
	if _, err := stopSelfRunCopies(context.Background(), program, home, copies[:1], func(context.Context, int32) error {
		return errors.New("fake access denied")
	}); err == nil {
		t.Fatal("a copy that could not be ended was ignored; the service would then fail to start")
	}
}
