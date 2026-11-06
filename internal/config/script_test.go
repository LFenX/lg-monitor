package config

import (
	"strings"
	"testing"
	"testing/fstest"
)

func TestScriptProbeConfiguration(t *testing.T) {
	for _, tc := range []struct{ name, run, extra, want string }{
		{"valid", "run: 'powershell -NoProfile -File C:\\checks\\backup-age.ps1'", "", ""},
		{"no command", "", "", "run"},
		{"empty command", "run: ' '", "", "run"},
		{"mixed HTTP", "run: fake", "url: http://example.test", "不接受"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			files := fstest.MapFS{"targets.yaml": {Data: []byte("targets:\n  - id: fake\n    match: {service: fake-service}\n    probes:\n      - type: script\n        " + tc.run + "\n        " + tc.extra + "\n        interval: 5m\n        timeout: 30s\n")}}
			cfg, err := Load(files)
			if tc.want == "" {
				if err != nil || len(cfg.Targets[0].Probes) != 1 {
					t.Fatalf("script rejected: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("invalid script configuration: %v", err)
			}
		})
	}
}
