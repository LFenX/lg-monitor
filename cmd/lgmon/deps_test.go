package main

import (
	"os/exec"
	"strings"
	"testing"
)

// The WebView2 library initialises COM and pins the main thread in any process that links it, so only the
// launcher and the installer may; the monitor itself must not, even indirectly.
func TestLgmonDoesNotLinkTheWebViewLibrary(t *testing.T) {
	output, err := exec.Command("go", "list", "-deps", ".").CombinedOutput()
	if err != nil {
		t.Skipf("go list unavailable: %v %s", err, output)
	}
	for _, line := range strings.Split(string(output), "\n") {
		if strings.Contains(line, "go-webview2") || strings.HasSuffix(strings.TrimSpace(line), "/internal/webwindow") {
			t.Fatalf("lgmon links %s", strings.TrimSpace(line))
		}
	}
}
