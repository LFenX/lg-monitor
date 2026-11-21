// Copyright (c) 2026 LFen (https://lfen.ink) — lg-monitor

package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestHomeRedirectRoundTripAndFallback(t *testing.T) {
	base := t.TempDir()
	target := filepath.Join(t.TempDir(), "lgmon-data")

	// Before target exists, ResolveHome falls back safely to base.
	if err := WriteHomeRedirect(base, target, base, time.UnixMilli(1700000000000)); err != nil {
		t.Fatalf("WriteHomeRedirect: %v", err)
	}
	if resolved, redirected := ResolveHome(base); redirected || !SamePath(resolved, base) {
		t.Fatalf("missing target should fall back: resolved=%q redirected=%v", resolved, redirected)
	}

	// Once target exists as a directory, ResolveHome follows it.
	if err := os.MkdirAll(target, 0o700); err != nil {
		t.Fatal(err)
	}
	resolved, redirected := ResolveHome(base)
	if !redirected || !SamePath(resolved, target) {
		t.Fatalf("ResolveHome = (%q, %v), want (%q, true)", resolved, redirected, target)
	}
}

func TestHTTPWarningSuppressesIntentionalLANWebOnly(t *testing.T) {
	lanWebDefault := Settings{LAN: LANSettings{Web: true}}
	if got := lanWebDefault.HTTPWarning(); got != "" {
		t.Fatalf("lan.web with default httpListen should not warn, got %q", got)
	}

	lanWebLoopback := Settings{HTTPListen: "127.0.0.1:7444", LAN: LANSettings{Web: true}}
	if got := lanWebLoopback.HTTPWarning(); got != "" {
		t.Fatalf("lan.web with loopback httpListen should not warn, got %q", got)
	}

	explicitExposed := Settings{HTTPListen: "0.0.0.0:7444"}
	if got := explicitExposed.HTTPWarning(); got != HTTPWarningText {
		t.Fatalf("explicit 0.0.0.0 without lan.web must still warn, got %q", got)
	}
}
