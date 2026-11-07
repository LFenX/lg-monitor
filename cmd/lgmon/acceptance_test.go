package main

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/LFenX/lg-monitor/internal/config"
)

func TestM1AcceptanceExamplesMatchActualConfigurationAndServiceArguments(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", "docs", "acceptance-m1.md"))
	if err != nil {
		t.Fatal(err)
	}
	text := strings.ReplaceAll(string(data), "\r\n", "\n")
	blocks := regexp.MustCompile("(?s)```yaml\n(.*?)\n```").FindAllStringSubmatch(text, -1)
	if len(blocks) != 3 {
		t.Fatalf("expected demo, rule and self-monitor examples, got %d", len(blocks))
	}
	replace := strings.NewReplacer("FAKE_REPLACE_WITH_A_COLLECTOR", "fake-a", "FAKE_REPLACE_WITH_CONFIRMED_NOTIFIER", "fake-channel")
	files := fstest.MapFS{
		"targets.yaml":   {Data: []byte("targets:\n" + replace.Replace(blocks[0][1]+"\n"+blocks[2][1]))},
		"rules.yaml":     {Data: []byte("rules:\n" + replace.Replace(blocks[1][1]))},
		"notifiers.yaml": {Data: []byte("notifiers:\n- id: fake-channel\n  type: toast\n")},
	}
	cfg, err := config.Load(files)
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Targets) != 2 || len(cfg.Rules) != 1 {
		t.Fatal("examples did not load as a single configuration")
	}
	for _, test := range []struct {
		target, command string
		want            bool
	}{
		{"acceptance-demo", "ping.exe -t 127.0.0.42 -l 2057", true},
		{"acceptance-demo", "ping.exe -t 127.0.0.42 -l 20570", false},
		{"acceptance-collector-self", `"C:\Program Files\lg-monitor\lgmon.exe" run --home "C:\ProgramData\lg-monitor" --role collector --service`, true},
		{"acceptance-collector-self", `lgmon.exe run --home "C:\ProgramData\lg-monitor" --role monitor`, false},
		{"acceptance-collector-self", "lgmon.exe tray", false},
	} {
		var pattern string
		for _, target := range cfg.Targets {
			if target.ID == test.target {
				pattern = target.Match.Cmdline
			}
		}
		match, err := regexp.MatchString("(?i)"+pattern, test.command)
		if err != nil || pattern == "" || match != test.want {
			t.Fatalf("example matches the wrong process: %+v, %v", test, err)
		}
	}
	for _, match := range regexp.MustCompile(`\]\(([^)]+\.md)(?:#[^)]*)?\)`).FindAllStringSubmatch(text, -1) {
		if _, err := os.Stat(filepath.Join("..", "..", "docs", match[1])); err != nil {
			t.Fatalf("broken acceptance reference %s: %v", match[1], err)
		}
	}
}
