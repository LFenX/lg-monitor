package config

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
)

func validFiles() fstest.MapFS {
	return fstest.MapFS{
		"targets.yaml": {Data: []byte(`targets:
  - id: web
    match:
      process: demo.exe
    probes:
      - type: http
        url: http://127.0.0.1:18080/
        interval: 10s
        timeout: 2s
        expectStatus: 200
`)},
		"rules.yaml": {Data: []byte(`rules:
  - id: down
    name: Demo
    type: process_absent
    severity: critical
    for: 10s
    target: web
    notify: [alerts]
    template: oncall
`)},
		"notifiers.yaml": {Data: []byte(`feishuApps:
  - id: demo
    appId: cli_test_not_real
    secretRef: demo-secret
    domain: feishu
    proxy: none
notifiers:
  - id: alerts
    type: feishu_app
    app: demo
    chatId: oc_test_not_real
    template: standard
`)},
		"card-templates/oncall.yaml": {Data: []byte("id: oncall\nextends: standard\n")},
	}
}

func replaceFile(t *testing.T, files fstest.MapFS, file, from, to string) {
	t.Helper()
	before := string(files[file].Data)
	if !strings.Contains(before, from) {
		t.Fatalf("fixture %s does not contain %q", file, from)
	}
	files[file] = &fstest.MapFile{Data: []byte(strings.Replace(before, from, to, 1))}
}

func TestLoadMissingAndEmptyFiles(t *testing.T) {
	for _, files := range []fstest.MapFS{{}, {
		"targets.yaml":   {Data: []byte("# empty\n")},
		"rules.yaml":     {Data: []byte("{}\n")},
		"notifiers.yaml": {Data: []byte("\n")},
	}} {
		cfg, err := Load(files)
		if err != nil {
			t.Fatal(err)
		}
		if len(cfg.Targets)+len(cfg.Rules)+len(cfg.Notifiers)+len(cfg.FeishuApps)+len(cfg.Templates) != 0 {
			t.Fatal("expected an empty configuration")
		}
	}
}

func TestLoadExamples(t *testing.T) {
	cfg, err := Load(os.DirFS(filepath.Join("..", "..", "examples")))
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Targets) != 2 || len(cfg.Rules) != 1 || len(cfg.Templates) != 2 {
		t.Fatalf("examples were not fully loaded: %+v", cfg)
	}
	if cfg.Targets[1].Match.Service == "" {
		t.Fatal("service matching was not parsed")
	}
}

func TestConfigurationErrors(t *testing.T) {
	for _, ruleType := range []string{"port_closed", "probe_failed"} {
		t.Run(ruleType+" incompatible target", func(t *testing.T) {
			files := validFiles()
			files["targets.yaml"].Data = []byte("targets:\n  - id: web\n    match: {process: demo.exe}\n")
			replaceFile(t, files, "rules.yaml", "type: process_absent", "type: "+ruleType)
			_, err := Load(files)
			var issues Errors
			if !errors.As(err, &issues) || len(issues) != 1 || issues[0].File != "rules.yaml" || issues[0].Line != 4 || !strings.Contains(issues[0].Message, "类型是 "+ruleType+"，但目标 web 没有配置") {
				t.Fatalf("error = %v, want one issue at rules.yaml type on line 4", err)
			}
		})
	}
	cases := []struct{ name, file, from, to, want string }{
		{"invalid ID", "targets.yaml", "id: web", "id: Web_1", "ID 必须"},
		{"empty match", "targets.yaml", "process: demo.exe", "process: ''", "至少需要一个"},
		{"invalid regexp", "targets.yaml", "process: demo.exe", "cmdline: '['", "正则表达式无效"},
		{"zero port", "targets.yaml", "process: demo.exe", "port: 0", "端口必须"},
		{"negative port", "targets.yaml", "process: demo.exe", "port: -1", "端口必须"},
		{"large port", "targets.yaml", "process: demo.exe", "port: 65536", "端口必须"},
		{"fractional port", "targets.yaml", "process: demo.exe", "port: 3000.5", "必须使用整数"},
		{"wrong port type", "targets.yaml", "process: demo.exe", "port: not-a-port", "字段类型错误"},
		{"number ID", "targets.yaml", "id: web", "id: 123", "必须使用字符串"},
		{"unknown target", "rules.yaml", "target: web", "target: missing", "rules.yaml 第 7 行：规则 down 引用的目标 missing 不存在"},
		{"unknown notifier", "rules.yaml", "notify: [alerts]", "notify: [missing]", "引用的通知渠道 missing 不存在"},
		{"repeated notifier", "rules.yaml", "notify: [alerts]", "notify: [alerts, alerts]", "重复引用通知渠道"},
		{"no notifier", "rules.yaml", "notify: [alerts]", "notify: []", "至少需要一个通知渠道"},
		{"unknown rule template", "rules.yaml", "template: oncall", "template: absent", "引用的模板 absent 不存在"},
		{"invalid rule type", "rules.yaml", "type: process_absent", "type: sql", "类型只支持"},
		{"invalid severity", "rules.yaml", "severity: critical", "severity: high", "severity 必须"},
		{"invalid for", "rules.yaml", "for: 10s", "for: tomorrow", "for 必须"},
		{"numeric for", "rules.yaml", "for: 10s", "for: 10", "时长缺少单位，请写成 10s 或 5m 这样的格式"},
		{"numeric interval", "targets.yaml", "interval: 10s", "interval: 10", "时长缺少单位，请写成 10s 或 5m 这样的格式"},
		{"numeric timeout", "targets.yaml", "timeout: 2s", "timeout: 2.5", "时长缺少单位，请写成 10s 或 5m 这样的格式"},
		{"negative for", "rules.yaml", "for: 10s", "for: -1s", "for 必须"},
		{"empty for", "rules.yaml", "for: 10s", "for: ''", "for 必须"},
		{"overflow for", "rules.yaml", "for: 10s", "for: 99999999999999h", "for 必须"},
		{"unknown app", "notifiers.yaml", "app: demo", "app: absent", "引用的飞书应用 absent 不存在"},
		{"unknown channel template", "notifiers.yaml", "template: standard", "template: absent", "引用的模板 absent 不存在"},
		{"no app ID", "notifiers.yaml", "appId: cli_test_not_real", "appId: ''", "缺少 appId"},
		{"no secret reference", "notifiers.yaml", "secretRef: demo-secret", "secretRef: ''", "缺少 secretRef"},
		{"no chat", "notifiers.yaml", "chatId: oc_test_not_real", "chatId: ''", "缺少 chatId"},
		{"invalid domain", "notifiers.yaml", "domain: feishu", "domain: other", "domain 必须"},
		{"invalid proxy", "notifiers.yaml", "proxy: none", "proxy: socks5://localhost:1080", "代理必须"},
		{"invalid notifier type", "notifiers.yaml", "type: feishu_app", "type: webhook", "通知类型只支持"},
		{"unsupported probe", "targets.yaml", "type: http", "type: script", "探测类型只支持"},
		{"invalid HTTP URL", "targets.yaml", "url: http://127.0.0.1:18080/", "url: ftp://example.test/", "完整的 http"},
		{"invalid HTTP port", "targets.yaml", "url: http://127.0.0.1:18080/", "url: http://127.0.0.1:99999/", "完整的 http"},
		{"invalid status", "targets.yaml", "expectStatus: 200", "expectStatus: '<700'", "expectStatus 必须"},
		{"wrong status type", "targets.yaml", "expectStatus: 200", "expectStatus: [200]", "字段类型错误"},
		{"zero interval", "targets.yaml", "interval: 10s", "interval: 0s", "interval 必须"},
		{"missing timeout", "targets.yaml", "        timeout: 2s\n", "", "timeout 必须"},
		{"negative timeout", "targets.yaml", "timeout: 2s", "timeout: -1s", "timeout 必须"},
		{"unknown field", "targets.yaml", "process: demo.exe", "proccess: demo.exe", "不支持字段 proccess"},
		{"duplicate YAML key", "targets.yaml", "process: demo.exe", "process: demo.exe\n      process: other.exe", "字段重复定义"},
		{"invalid extends", "card-templates/oncall.yaml", "extends: standard", "extends: absent", "extends 必须引用"},
		{"no template body", "card-templates/oncall.yaml", "extends: standard", "name: test", "只能选择 extends 或 raw"},
		{"both template modes", "card-templates/oncall.yaml", "extends: standard", "extends: standard\nraw: {}", "只能选择 extends 或 raw"},
		{"invalid raw", "card-templates/oncall.yaml", "extends: standard", "raw: 42", "raw 必须"},
		{"invalid mention", "card-templates/oncall.yaml", "extends: standard", "extends: standard\nmention: {critical: nobody}", "mention 的状态"},
		{"multiple documents", "targets.yaml", "targets:", "---\n{}\n---\ntargets:", "一个 YAML 文档"},
		{"syntax", "targets.yaml", "targets:", "targets: [", "YAML 语法"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			files := validFiles()
			replaceFile(t, files, tc.file, tc.from, tc.to)
			cfg, err := Load(files)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("Load() = %v, want %q", err, tc.want)
			}
			if cfg != nil {
				t.Fatal("invalid configuration was returned")
			}
			var issues Errors
			if !errors.As(err, &issues) || len(issues) == 0 {
				t.Fatalf("expected structured configuration issues: %v", err)
			}
			for _, issue := range issues {
				if issue.File == "" || issue.Line < 1 {
					t.Fatalf("missing source position: %+v", issue)
				}
			}
		})
	}
}

func TestYMLTemplateExtensionIsRejected(t *testing.T) {
	files := validFiles()
	files["card-templates/x.yml"] = &fstest.MapFile{Data: []byte("id: x\nextends: standard\n")}
	_, err := Load(files)
	var issues Errors
	if !errors.As(err, &issues) || len(issues) != 1 || issues[0].File != "card-templates/x.yml" || issues[0].Message != "模板文件请使用 .yaml 扩展名：card-templates/x.yml" {
		t.Fatalf("Load() error = %v", err)
	}
}

func TestDuplicateIDs(t *testing.T) {
	cases := []struct{ name, file, addition, want string }{
		{"target", "targets.yaml", "  - id: web\n    match: {service: demo}\n", "目标 web 的 ID 重复"},
		{"rule", "rules.yaml", "  - id: down\n    type: process_absent\n    target: web\n    severity: warning\n    notify: [alerts]\n", "规则 down 的 ID 重复"},
		{"notifier", "notifiers.yaml", "  - id: alerts\n    type: feishu_app\n    app: demo\n    chatId: oc_test_not_real\n", "通知渠道 alerts 的 ID 重复"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			files := validFiles()
			files[tc.file].Data = append(files[tc.file].Data, []byte(tc.addition)...)
			if _, err := Load(files); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error = %v, want %q", err, tc.want)
			}
		})
	}
	t.Run("app", func(t *testing.T) {
		files := validFiles()
		replaceFile(t, files, "notifiers.yaml", "notifiers:", "  - id: demo\n    appId: cli_test_duplicate\n    secretRef: test\n    domain: lark\nnotifiers:")
		if _, err := Load(files); err == nil || !strings.Contains(err.Error(), "飞书应用 demo 的 ID 重复") {
			t.Fatalf("error = %v", err)
		}
	})
	t.Run("template", func(t *testing.T) {
		files := validFiles()
		files["card-templates/other.yaml"] = &fstest.MapFile{Data: files["card-templates/oncall.yaml"].Data}
		if _, err := Load(files); err == nil || !strings.Contains(err.Error(), "模板 oncall 的 ID 重复") {
			t.Fatalf("error = %v", err)
		}
	})
	t.Run("preset collision", func(t *testing.T) {
		files := validFiles()
		files["card-templates/standard.yaml"] = &fstest.MapFile{Data: []byte("id: standard\nextends: compact\n")}
		if _, err := Load(files); err == nil || !strings.Contains(err.Error(), "模板 standard 的 ID 重复") {
			t.Fatalf("error = %v", err)
		}
	})
}

func TestAllSemanticErrorsAreReported(t *testing.T) {
	files := validFiles()
	replaceFile(t, files, "rules.yaml", "target: web", "target: absent")
	replaceFile(t, files, "rules.yaml", "for: 10s", "for: invalid")
	replaceFile(t, files, "notifiers.yaml", "app: demo", "app: absent")
	_, err := Load(files)
	for _, want := range []string{"目标 absent 不存在", "for 必须", "飞书应用 absent 不存在"} {
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Fatalf("missing %q in %v", want, err)
		}
	}
}

func TestZeroAndMissingFor(t *testing.T) {
	for _, replacement := range []string{"    for: 0s\n", ""} {
		files := validFiles()
		replaceFile(t, files, "rules.yaml", "    for: 10s\n", replacement)
		cfg, err := Load(files)
		if err != nil {
			t.Fatal(err)
		}
		if value, err := cfg.Rules[0].For.Parse(); err != nil || value != 0 {
			t.Fatalf("for = %v, error = %v", value, err)
		}
	}
}

func TestStatusVariantsAndTCP(t *testing.T) {
	for _, status := range []string{"200", "0xC8", `"200"`, `"2xx"`, `"<500"`, `">=200"`} {
		files := validFiles()
		replaceFile(t, files, "targets.yaml", "expectStatus: 200", "expectStatus: "+status)
		if _, err := Load(files); err != nil {
			t.Fatalf("status %s: %v", status, err)
		}
	}
	for _, address := range []string{"127.0.0.1:3000", "example.test:443", "[::1]:3000"} {
		files := validFiles()
		replaceFile(t, files, "targets.yaml", "type: http", "type: tcp")
		replaceFile(t, files, "targets.yaml", "url: http://127.0.0.1:18080/", "address: '"+address+"'")
		replaceFile(t, files, "targets.yaml", "        expectStatus: 200\n", "")
		if _, err := Load(files); err != nil {
			t.Fatalf("TCP %s: %v", address, err)
		}
	}
}

func TestUnknownSecretFieldIsNotEchoed(t *testing.T) {
	files := validFiles()
	const secret = "fake-secret-that-must-not-be-printed"
	replaceFile(t, files, "notifiers.yaml", "secretRef: demo-secret", "appSecret: "+secret)
	_, err := Load(files)
	if err == nil || !strings.Contains(err.Error(), "不支持字段 appSecret") || strings.Contains(err.Error(), secret) {
		t.Fatalf("unsafe diagnostic: %v", err)
	}
}

type deniedFS struct{}

func (deniedFS) Open(string) (fs.File, error) { return nil, fs.ErrPermission }

func TestReadFailure(t *testing.T) {
	_, err := Load(deniedFS{})
	if err == nil || !errors.Is(err, fs.ErrPermission) {
		t.Fatalf("permission error was not preserved: %v", err)
	}
}

func TestYAMLAliasesAndMerge(t *testing.T) {
	files := validFiles()
	files["targets.yaml"] = &fstest.MapFile{Data: []byte(`targets:
  - id: web
    match: &base
      process: demo.exe
  - id: another
    match:
      <<: *base
      service: demo-service
`)}
	cfg, err := Load(files)
	if err != nil || cfg.Targets[1].Match.Process != "demo.exe" {
		t.Fatalf("YAML merge failed: %v", err)
	}
}
