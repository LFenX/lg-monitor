// Copyright (c) 2026 LFen (https://lfen.ink) — lg-monitor

package collector

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
)

func TestSystemdServiceParsingAndEnumeration(t *testing.T) {
	session := &systemdSession{
		run: func(_ context.Context, args ...string) (string, error) {
			switch args[0] {
			case "list-unit-files":
				return "nginx.service enabled enabled\ngetty@.service static -\npostgresql.service disabled enabled\n", nil
			case "list-units":
				return "  nginx.service loaded active running A high performance web server\n● failed-app.service loaded failed failed Crashed App\n", nil
			case "show":
				switch args[1] {
				case "nginx.service":
					return "LoadState=loaded\nActiveState=active\nUnitFileState=enabled\nMainPID=1234\nDescription=Nginx Web Server\n", nil
				case "postgresql.service":
					return "LoadState=loaded\nActiveState=inactive\nUnitFileState=disabled\nMainPID=0\nDescription=PostgreSQL\n", nil
				case "missing.service":
					return "LoadState=not-found\nActiveState=inactive\nUnitFileState=\nMainPID=0\nDescription=\n", nil
				}
			}
			return "", errors.New("unexpected command: " + strings.Join(args, " "))
		},
	}

	names, err := session.Names(context.Background())
	if err != nil {
		t.Fatalf("Names: %v", err)
	}
	slices.Sort(names)
	wantNames := []string{"failed-app", "nginx", "postgresql"}
	if !slices.Equal(names, wantNames) {
		t.Fatalf("names = %v, want %v", names, wantNames)
	}

	nginx, err := session.Read(context.Background(), "nginx")
	if err != nil {
		t.Fatalf("Read nginx: %v", err)
	}
	if nginx.State != ServiceRunning || nginx.StartType != "automatic" || nginx.PID != 1234 || nginx.DisplayName != "Nginx Web Server" {
		t.Fatalf("nginx = %+v", nginx)
	}
	if err := nginx.Validate(); err != nil {
		t.Fatalf("Validate nginx: %v", err)
	}

	pg, err := session.Read(context.Background(), "postgresql")
	if err != nil || pg.State != ServiceStopped || pg.StartType != "disabled" {
		t.Fatalf("postgresql = %+v err=%v", pg, err)
	}

	if _, err := session.Read(context.Background(), "missing"); !errors.Is(err, errServiceMissing) {
		t.Fatalf("missing err = %v, want errServiceMissing", err)
	}
}

func TestSystemdRestartHandleLifecycle(t *testing.T) {
	state := "active"
	var calls []string
	handle := &systemdRestartHandle{
		ctx:  context.Background(),
		unit: systemdUnitName("nginx"),
		run: func(_ context.Context, args ...string) (string, error) {
			calls = append(calls, strings.Join(args, " "))
			switch args[0] {
			case "show":
				return state, nil
			case "stop":
				state = "inactive"
				return "", nil
			case "start":
				state = "active"
				return "", nil
			}
			return "", nil
		},
	}
	if s, err := handle.State(); err != nil || s != "running" {
		t.Fatalf("initial state = %q err=%v", s, err)
	}
	if err := handle.Stop(); err != nil {
		t.Fatal(err)
	}
	if s, err := handle.State(); err != nil || s != "stopped" {
		t.Fatalf("stopped state = %q err=%v", s, err)
	}
	if err := handle.Start(); err != nil {
		t.Fatal(err)
	}
	if s, err := handle.State(); err != nil || s != "running" {
		t.Fatalf("restarted state = %q err=%v", s, err)
	}
	if len(calls) != 5 {
		t.Fatalf("calls = %v", calls)
	}
}
