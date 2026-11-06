package config

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"testing"
	"testing/fstest"
)

func TestPushedLegacyScriptCommandExplainsMigration(t *testing.T) {
	data := []byte(`{"targets":[{"id":"fake","match":{"service":"fake"},"probes":[{"type":"script","run":"","interval":"1m","timeout":"1s"}]}]}`)
	sum := sha256.Sum256(data)
	if _, err := ParseTargetConfig(hex.EncodeToString(sum[:]), data); err == nil || !strings.Contains(err.Error(), "请改用 script") {
		t.Fatalf("legacy pushed command accepted or unexplained: %v", err)
	}
}

func TestScriptArgumentsDoNotAliasConfigurationSnapshot(t *testing.T) {
	cfg := &Config{Targets: []Target{{Probes: []Probe{{Type: "script", Script: "check.ps1", Args: []string{"original"}}}}}}
	copy := cfg.clone()
	copy.Targets[0].Probes[0].Args[0] = "changed"
	if cfg.Targets[0].Probes[0].Args[0] != "original" {
		t.Fatal("snapshot mutation changed active script arguments")
	}
}

func TestScriptProbeConfiguration(t *testing.T) {
	for _, tc := range []struct{ name, script, extra, want string }{
		{"valid", "script: backup-age.ps1", "args: ['--max-hours', '24']", ""},
		{"cmd", "script: check.cmd", "", ""},
		{"bat", "script: check.BAT", "", ""},
		{"exe", "script: check.exe", "", ""},
		{"Unicode filename", "script: 备份检查.ps1", "", ""},
		{"missing", "", "", "script"},
		{"empty", "script: ' '", "", "script"},
		{"path", "script: '../check.ps1'", "", "文件名"},
		{"backslash", `script: 'sub\check.ps1'`, "", "文件名"},
		{"drive", `script: 'C:\check.ps1'`, "", "文件名"},
		{"dots", "script: 'check..ps1'", "", "文件名"},
		{"unsupported", "script: check.py", "", "文件名"},
		{"device", "script: con.ps1", "", "文件名"},
		{"name expansion", "script: '%evil%.cmd'", "", "文件名"},
		{"argument shell", "script: check.cmd", "args: ['safe & evil']", "参数"},
		{"argument expansion", "script: check.cmd", "args: ['%PATH%']", "参数"},
		{"argument escape", "script: check.cmd", "args: ['a^b']", "参数"},
		{"argument quote", "script: check.cmd", "args: ['\"a\"']", "参数"},
		{"legacy", "run: fake", "", "旧 run"},
		{"legacy empty", "run: ''", "", "旧 run"},
		{"mixed HTTP", "script: check.ps1", "url: http://example.test", "不接受"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			files := fstest.MapFS{"targets.yaml": {Data: []byte("targets:\n  - id: fake\n    match: {service: fake-service}\n    probes:\n      - type: script\n        " + tc.script + "\n        " + tc.extra + "\n        interval: 5m\n        timeout: 30s\n")}}
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
