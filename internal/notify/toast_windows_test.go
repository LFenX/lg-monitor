//go:build windows

package notify

import (
	"context"
	"encoding/base64"
	"errors"
	"strings"
	"testing"
)

type fakeToastSystem struct {
	current, runSession uint32
	active              []uint32
	exit                uint32
	currentErr, runErr  error
	activeErr           error
	script              string
	activeCalls         int
	beforeRun           func()
}

func (s *fakeToastSystem) CurrentSession() (uint32, error) { return s.current, s.currentErr }
func (s *fakeToastSystem) ActiveSessions() ([]uint32, error) {
	s.activeCalls++
	return s.active, s.activeErr
}
func (s *fakeToastSystem) Run(_ context.Context, session uint32, script string) (uint32, error) {
	s.runSession, s.script = session, script
	if s.beforeRun != nil {
		s.beforeRun()
	}
	return s.exit, s.runErr
}

func TestToastUsesInteractiveSessionAndNeverDisplaysInSessionZero(t *testing.T) {
	for _, tc := range []struct {
		name    string
		system  fakeToastSystem
		want    uint32
		skipped bool
	}{
		{"interactive", fakeToastSystem{current: 4}, 4, false},
		{"service", fakeToastSystem{active: []uint32{7, 9}}, 7, false},
		{"no users", fakeToastSystem{}, 0, true},
		{"zero is not user", fakeToastSystem{active: []uint32{0}}, 0, true},
		{"session error", fakeToastSystem{currentErr: errors.New("fake")}, 0, true},
		{"enumeration error", fakeToastSystem{activeErr: errors.New("fake")}, 0, true},
		{"permission denied", fakeToastSystem{active: []uint32{7}, runErr: errors.New("fake")}, 7, true},
		{"notifications disabled", fakeToastSystem{current: 4, exit: 2}, 4, true},
		{"native failure", fakeToastSystem{current: 4, exit: 3}, 4, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			platform := windowsToast{system: &tc.system}
			result, err := platform.Deliver(context.Background(), []byte("<toast/>"))
			if err != nil || result.Skipped != tc.skipped || tc.system.runSession != tc.want || result.Skipped && result.Reason == "" {
				t.Fatalf("deliver=%+v,%v session=%d", result, err, tc.system.runSession)
			}
			if tc.system.current > 0 && tc.system.activeCalls != 0 {
				t.Fatal("interactive process searched other user sessions")
			}
		})
	}
}

func TestToastScriptContainsOnlyEncodedAlertText(t *testing.T) {
	content := []byte(`<toast>$(throw 'fake-injected-code') &amp; 中文</toast>`)
	script := toastScript(content)
	if strings.Contains(script, "fake-injected-code") || !strings.Contains(script, base64.StdEncoding.EncodeToString(content)) || !strings.Contains(script, "ToastNotificationManager") || strings.Contains(script, "LocalServer32") || strings.Contains(script, "CurrentVersion\\Run") {
		t.Fatal("toast content became executable code or registered an executable callback")
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	system := &fakeToastSystem{current: 4, runErr: context.Canceled, beforeRun: cancel}
	if _, err := (windowsToast{system: system}).Deliver(ctx, nil); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation lost: %v", err)
	}
	system = &fakeToastSystem{current: 4}
	if _, err := (windowsToast{system: system}).Deliver(ctx, nil); !errors.Is(err, context.Canceled) || system.runSession != 0 {
		t.Fatalf("cancelled delivery started a process: %v", err)
	}
}
