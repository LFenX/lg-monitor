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
	// The author's pages in the header open too, exactly as written and nothing near them.
	for _, credit := range []string{"https://github.com/LFenX/lg-monitor", "https://lfen.ink/", "https://margin.lfen.ink/"} {
		if target, ok := external(credit, start); !ok || target != credit {
			t.Fatalf("credit %q: %q %v", credit, target, ok)
		}
	}
	// 留白's entry opens with a ticket in its fragment, and nothing else rides along.
	ticket := strings.Repeat("e", 120) + "." + strings.Repeat("s", 86)
	if target, ok := external("https://margin.lfen.ink/lgmon#ticket="+ticket, start); !ok || target != "https://margin.lfen.ink/lgmon#ticket="+ticket {
		t.Fatalf("留白 entry: %q %v", target, ok)
	}
	for _, other := range []string{"https://example.invalid/", "http://127.0.0.1:9999/", "file:///C:/Windows/", "http://user@127.0.0.1:7444/", "ms-settings:", "https://lfen.ink.evil.test/", "https://github.com/LFenX/lg-monitor/../../evil", "https://margin.lfen.ink/?next=https://evil.test",
		"https://margin.lfen.ink/lgmon#ticket=", "https://margin.lfen.ink/lgmon?x=1#ticket=" + ticket, "https://margin.lfen.ink.evil.test/lgmon#ticket=" + ticket, "https://margin.lfen.ink/lgmon#ticket=" + ticket + "&next=https://evil.test", "http://margin.lfen.ink/lgmon#ticket=" + ticket} {
		if _, ok := external(other, start); ok {
			t.Fatalf("opened %q", other)
		}
	}
	for _, part := range []string{"postMessage", "target === '_blank'", "dataset.launcher", "window.open", "data-lgmon-opens"} {
		if !strings.Contains(bridge, part) {
			t.Fatalf("bridge lacks %q", part)
		}
	}
	// The script runs before <html> exists: a bare document.documentElement access would throw and leave the
	// page without the flag and the link handler, which is how U7 shipped.
	if strings.Contains(bridge, "document.documentElement.dataset") && !strings.Contains(bridge, "if (document.documentElement)") ||
		strings.Index(bridge, "window.lgmonLauncher = true") > strings.Index(bridge, "documentElement") {
		t.Fatal("the bridge touches documentElement before it can exist")
	}
}
