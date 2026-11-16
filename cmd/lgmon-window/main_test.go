package main

import (
	"io"
	"strings"
	"testing"
)

func TestOnlyLgmonsOwnPageOpensInTheWindow(t *testing.T) {
	for _, bad := range []string{"", "file:///C:/fake.html", "javascript:alert(1)", "http://user:pass@127.0.0.1:7444/#/start", "http://127.0.0.1:7444/?token=fake", "127.0.0.1:7444"} {
		if _, err := parseOptions([]string{"--url", bad}, io.Discard); err == nil {
			t.Fatalf("accepted %q", bad)
		}
	}
	opts, err := parseOptions([]string{"--url", "http://127.0.0.1:7444/#/start", "--data", `C:\fake-home\window`}, io.Discard)
	if err != nil || opts.start.String() != "http://127.0.0.1:7444/#/start" || opts.data != `C:\fake-home\window` {
		t.Fatalf("opts=%+v err=%v", opts, err)
	}
	if fallback := dataDir(""); !strings.Contains(strings.ToLower(fallback), "lg-monitor") {
		t.Fatalf("default data dir %q", fallback)
	}
}

func TestOnlyLgmonsOwnLinksGoToTheBrowser(t *testing.T) {
	start, _ := startURL("http://127.0.0.1:7444/#/start")
	if target, ok := external("http://127.0.0.1:7444/#/boards/main", start); !ok || target != "http://127.0.0.1:7444/#/boards/main" {
		t.Fatalf("board link: %q %v", target, ok)
	}
	for _, other := range []string{"https://example.invalid/", "http://127.0.0.1:9999/", "file:///C:/Windows/", "http://user@127.0.0.1:7444/", "ms-settings:"} {
		if _, ok := external(other, start); ok {
			t.Fatalf("opened %q", other)
		}
	}
	for _, part := range []string{"postMessage", "target === '_blank'", "dataset.launcher", "window.open"} {
		if !strings.Contains(bridge, part) {
			t.Fatalf("bridge lacks %q", part)
		}
	}
}
