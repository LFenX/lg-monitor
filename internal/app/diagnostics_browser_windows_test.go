//go:build windows && integration && webui

package app

import (
	"testing"
	"time"
)

func TestNativeDiagnosticBrowser(t *testing.T) {
	f := newAPIFixtureWithHTTPClock(t, time.Date(2026, 11, 9, 6, 0, 0, 0, time.UTC), time.Now)
	seedAPIDiagnostic(t, f)
	runNativeWebBrowser(t, f, "diagnostics.spec.ts", f.server)
	if f.files.writes != 0 || len(f.client.sent) != 0 || len(f.toast.values) != 0 {
		t.Fatal("read-only diagnostic UI changed configuration or sent notifications")
	}
}
