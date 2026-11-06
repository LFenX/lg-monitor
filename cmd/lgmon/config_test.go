package main

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestConfigValidationOutputAndExitCode(t *testing.T) {
	logger := slog.Default()
	t.Cleanup(func() { slog.SetDefault(logger) })
	home := t.TempDir()
	if err := os.Mkdir(filepath.Join(home, "config"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, "config", "targets.yaml"), []byte("targets:\n  - id: Bad_ID\n    match: {}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, format := range []string{"text", "json"} {
		t.Run(format, func(t *testing.T) {
			args := []string{"config", "validate", "--home", home}
			if format == "json" {
				args = append(args, "--json")
			}
			var stdout, stderr bytes.Buffer
			if code := execute(args, &stdout, &stderr); code != 1 {
				t.Fatalf("exit = %d, want 1", code)
			}
			if format == "text" {
				if stdout.Len() != 0 || !strings.HasPrefix(stderr.String(), "配置有 2 处错误：\n") || strings.Count(stderr.String(), "targets.yaml 第 ") != 2 || strings.Contains(stderr.String(), "--help") {
					t.Fatalf("text output: stdout=%q stderr=%q", stdout.String(), stderr.String())
				}
				return
			}
			var result struct {
				OK     bool `json:"ok"`
				Errors []struct {
					File    string `json:"file"`
					Line    int    `json:"line"`
					Message string `json:"message"`
				} `json:"errors"`
			}
			if err := json.Unmarshal(stdout.Bytes(), &result); err != nil || result.OK || len(result.Errors) != 2 || stderr.Len() != 0 {
				t.Fatalf("JSON result=%+v err=%v stdout=%q stderr=%q", result, err, stdout.String(), stderr.String())
			}
			for i, issue := range result.Errors {
				if issue.File != "targets.yaml" || issue.Line != i+2 || issue.Message == "" {
					t.Fatalf("incorrect issue position: %+v", issue)
				}
			}
		})
	}
	var stdout, stderr bytes.Buffer
	if code := execute([]string{"config", "validate", "--json", "--home", t.TempDir()}, &stdout, &stderr); code != 0 || stdout.String() != "{\"ok\":true,\"errors\":[]}\n" || stderr.Len() != 0 {
		t.Fatalf("valid JSON exit=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
}

func TestHelpHintOnlyForUsageErrors(t *testing.T) {
	logger := slog.Default()
	t.Cleanup(func() { slog.SetDefault(logger) })
	for _, args := range [][]string{{"config", "validate", "--unknown"}, {"version", "extra"}} {
		var stdout, stderr bytes.Buffer
		if code := execute(args, &stdout, &stderr); code != 1 || !strings.Contains(stderr.String(), "--help") {
			t.Fatalf("usage exit=%d stderr=%q", code, stderr.String())
		}
	}
	file := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(file, []byte("owned test file"), 0o600); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	if code := execute([]string{"config", "schema", "--output", file}, &stdout, &stderr); code != 1 || strings.Contains(stderr.String(), "--help") {
		t.Fatalf("runtime exit=%d stderr=%q", code, stderr.String())
	}
}

func TestConfigValidate(t *testing.T) {
	logger := slog.Default()
	t.Cleanup(func() { slog.SetDefault(logger) })
	home := t.TempDir()
	root := newRootCommand()
	var output bytes.Buffer
	root.SetOut(&output)
	root.SetArgs([]string{"config", "validate", "--home", home})
	if err := root.Execute(); err != nil || !strings.Contains(output.String(), "配置校验通过") {
		t.Fatalf("empty configuration: %v, output %q", err, output.String())
	}
	if _, err := os.Stat(filepath.Join(home, "config")); !os.IsNotExist(err) {
		t.Fatalf("validation created or accessed an unexpected directory: %v", err)
	}
	if err := os.Mkdir(filepath.Join(home, "config"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, "config", "targets.yaml"), []byte("targets: [{id: bad, match: {}}]\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	root = newRootCommand()
	root.SetOut(&output)
	root.SetArgs([]string{"--home", home, "config", "validate"})
	if err := root.Execute(); err == nil || !strings.Contains(err.Error(), "targets.yaml 第 1 行") {
		t.Fatalf("invalid configuration: %v", err)
	}
}

func TestConfigValidateUsesEnvironmentHome(t *testing.T) {
	logger := slog.Default()
	t.Cleanup(func() { slog.SetDefault(logger) })
	home := t.TempDir()
	t.Setenv("LGMON_HOME", home)
	if err := os.Mkdir(filepath.Join(home, "config"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, "config", "targets.yaml"), []byte("unknown: true\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	root := newRootCommand()
	root.SetArgs([]string{"config", "validate"})
	if err := root.Execute(); err == nil || !strings.Contains(err.Error(), "不支持字段 unknown") {
		t.Fatalf("LGMON_HOME was not used: %v", err)
	}
}

func TestConfigSchemaOutput(t *testing.T) {
	logger := slog.Default()
	t.Cleanup(func() { slog.SetDefault(logger) })
	directory := filepath.Join(t.TempDir(), "schemas")
	root := newRootCommand()
	var output bytes.Buffer
	root.SetOut(&output)
	root.SetArgs([]string{"config", "schema", "--output", directory, "--home", t.TempDir()})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	files, err := os.ReadDir(directory)
	if err != nil || len(files) != 4 {
		t.Fatalf("schemas = %v, error = %v", files, err)
	}
}
