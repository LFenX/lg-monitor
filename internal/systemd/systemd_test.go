// Copyright (c) 2026 LFen (https://lfen.ink) — lg-monitor

package systemd

import (
	"context"
	"strings"
	"testing"

	"github.com/LFenX/lg-monitor/internal/winsvc"
)

type fakeFiles struct{ written map[string]string }

func (f *fakeFiles) Write(path string, data []byte) error { f.written[path] = string(data); return nil }
func (f *fakeFiles) Remove(path string) error             { delete(f.written, path); return nil }

func TestUnitQuotesArgumentsAndRestartsOnFailure(t *testing.T) {
	unit := UnitFile(winsvc.Definition{Executable: "/usr/local/bin/lgmon", Home: "/var/lib/lg monitor", Role: "both", Listen: ":7443", LogLevel: "info"})
	for _, want := range []string{
		`ExecStart="/usr/local/bin/lgmon" "run" "--home" "/var/lib/lg monitor" "--role" "both" "--log-level" "info" "--listen" ":7443"`,
		"Restart=on-failure", "WantedBy=multi-user.target", "After=network-online.target",
	} {
		if !strings.Contains(unit, want) {
			t.Fatalf("unit lacks %q:\n%s", want, unit)
		}
	}
	if got := quote(`a"b\c%d`); got != `"a\"b\\c%%d"` {
		t.Fatal(got)
	}
}

func TestControllerDrivesSystemctlAndReadsState(t *testing.T) {
	var calls []string
	state := "LoadState=loaded\nActiveState=active\nMainPID=4242"
	files := &fakeFiles{written: map[string]string{}}
	root := true
	c := &Controller{Files: files, Root: func() bool { return root }, Run: func(_ context.Context, args ...string) (string, error) {
		calls = append(calls, strings.Join(args, " "))
		if args[0] == "show" {
			return state, nil
		}
		return "", nil
	}}
	ctx := context.Background()
	if err := c.Install(ctx, winsvc.Definition{Executable: "/usr/local/bin/lgmon", Home: t.TempDir(), Role: "both", LogLevel: "info"}); err != nil {
		t.Fatal(err)
	}
	if files.written[UnitPath] == "" || strings.Join(calls, ";") != "daemon-reload;enable lgmon.service" {
		t.Fatalf("calls=%v files=%v", calls, files.written)
	}
	status, err := c.Status(ctx)
	if err != nil || !status.Installed || status.State != winsvc.Running || status.PID != 4242 {
		t.Fatalf("status=%+v err=%v", status, err)
	}
	calls = nil
	if err := c.Restart(ctx); err != nil || calls[0] != "restart lgmon.service" {
		t.Fatalf("restart calls=%v err=%v", calls, err)
	}
	calls = nil
	if err := c.Uninstall(ctx); err != nil || files.written[UnitPath] != "" || !strings.Contains(strings.Join(calls, ";"), "disable --now lgmon.service") {
		t.Fatalf("uninstall calls=%v err=%v", calls, err)
	}
	state = "LoadState=not-found\nActiveState=inactive\nMainPID=0"
	if status, _ := c.Status(ctx); status.Installed || status.State != winsvc.Stopped {
		t.Fatalf("after uninstall=%+v", status)
	}
	// Without root nothing changes, and the message says what to do.
	root = false
	if err := c.Start(ctx); err == nil || !strings.Contains(err.Error(), "sudo") {
		t.Fatal(err)
	}
}
