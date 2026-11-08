package config

import (
	"reflect"
	"strings"
	"testing"
	"testing/fstest"
	"time"
)

func httpChecksFiles(extraProbe, extraRule string) fstest.MapFS {
	files := validFiles()
	files["targets.yaml"] = &fstest.MapFile{Data: []byte("targets:\n- id: web\n  match: {process: node.exe}\n  probes:\n  - {type: http, url: 'https://fake.invalid/', interval: 30s, timeout: 2s" + extraProbe + "}\n  - {type: http, url: 'http://fake.invalid/', interval: 30s, timeout: 2s}\n  - {type: http, url: 'https://fake.invalid/two', interval: 30s, timeout: 2s}\n")}
	files["rules.yaml"] = &fstest.MapFile{Data: []byte("rules:\n- id: expiry\n  type: cert_expiry\n  target: web\n  severity: warning\n  notify: [alerts]\n" + extraRule)}
	return files
}

func TestHTTPCheckConfigurationAndCertificateRuleBounds(t *testing.T) {
	for _, test := range []struct{ name, probe, rule, want string }{
		{"defaults", "", "", ""},
		{"one byte", ", keyword: x", "", ""},
		{"256 bytes", ", keyword: '" + strings.Repeat("x", 256) + "'", "", ""},
		{"255 UTF8 bytes", ", keyword: '" + strings.Repeat("字", 85) + "'", "", ""},
		{"empty", ", keyword: ''", "", "1～256"},
		{"257 bytes", ", keyword: '" + strings.Repeat("x", 257) + "'", "", "1～256"},
		{"258 UTF8 bytes", ", keyword: '" + strings.Repeat("字", 86) + "'", "", "1～256"},
		{"inverse", ", keyword: fatal, keywordAbsent: true", "", ""},
		{"explicit false", ", keyword: fatal, keywordAbsent: false", "", ""},
		{"orphan inverse", ", keywordAbsent: true", "", "同时配置 keyword"},
		{"orphan false", ", keywordAbsent: false", "", "同时配置 keyword"},
		{"null inverse", ", keywordAbsent: null", "", "布尔值"},
		{"minimum", "", "  within: 1h\n", ""},
		{"maximum", "", "  within: 365d\n", ""},
		{"below", "", "  within: 59m\n", "1h～365d"},
		{"above", "", "  within: 365d1s\n", "1h～365d"},
		{"zero", "", "  within: 0s\n", "1h～365d"},
		{"empty within", "", "  within: ''\n", "1h～365d"},
		{"for zero", "", "  for: 0s\n", "不接受 for"},
		{"for empty", "", "  for: ''\n", "不接受 for"},
		{"probe zero", "", "  probe: 0\n", ""},
		{"probe two", "", "  probe: 2\n", ""},
		{"plain HTTP", "", "  probe: 1\n", "HTTPS"},
		{"negative probe", "", "  probe: -1\n", "HTTPS"},
		{"past probes", "", "  probe: 3\n", "HTTPS"},
	} {
		t.Run(test.name, func(t *testing.T) {
			cfg, err := Load(httpChecksFiles(test.probe, test.rule))
			if test.want != "" {
				if err == nil || !strings.Contains(err.Error(), test.want) {
					t.Fatalf("wanted %q, got %v", test.want, err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if test.rule == "" {
				within, err := cfg.Rules[0].CertWithin()
				if err != nil || within != 14*24*time.Hour || !reflect.DeepEqual(cfg.Rules[0].CertificateProbes(cfg.Targets[0]), []int{0, 2}) {
					t.Fatalf("default certificate rule changed: %v %v", within, err)
				}
			}
		})
	}
}

func TestHTTPKeywordFieldsAreRestrictedAndCopied(t *testing.T) {
	cfg, err := Load(httpChecksFiles(", keyword: fatal, keywordAbsent: false", ""))
	if err != nil {
		t.Fatal(err)
	}
	copy := cfg.clone()
	*copy.Targets[0].Probes[0].KeywordAbsent = true
	if *cfg.Targets[0].Probes[0].KeywordAbsent {
		t.Fatal("snapshot leaked keyword option pointer")
	}
	for _, kind := range []string{"tcp", "script"} {
		files := httpChecksFiles(", keyword: fatal, keywordAbsent: false", "")
		replaceFile(t, files, "targets.yaml", "type: http", "type: "+kind)
		_, err := Load(files)
		if err == nil || !strings.Contains(err.Error(), "仅用于 HTTP") {
			t.Fatalf("%s accepted keyword: %v", kind, err)
		}
	}
	files := httpChecksFiles("", "  within: 14d\n")
	replaceFile(t, files, "rules.yaml", "type: cert_expiry", "type: process_absent")
	if _, err := Load(files); err == nil || !strings.Contains(err.Error(), "within 只用于") {
		t.Fatalf("unrelated rule accepted within: %v", err)
	}
}
