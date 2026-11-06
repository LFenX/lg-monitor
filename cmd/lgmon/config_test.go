package main

import (
	"bytes"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

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
