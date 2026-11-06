package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestTemplateCommands(t *testing.T) {
	home := filepath.Join(t.TempDir(), "home")
	run := func(args ...string) (int, string, string) {
		t.Helper()
		var stdout, stderr bytes.Buffer
		code := execute(append([]string{"--home", home}, args...), &stdout, &stderr)
		return code, stdout.String(), stderr.String()
	}
	code, stdout, stderr := run("template", "list", "--json")
	var entries []struct{ ID string }
	if code != 0 || stderr != "" || json.Unmarshal([]byte(stdout), &entries) != nil || len(entries) != 4 {
		t.Fatalf("list: %d %s %s", code, stdout, stderr)
	}
	if _, err := os.Stat(home); !os.IsNotExist(err) {
		t.Fatalf("read-only listing created home: %v", err)
	}
	code, stdout, stderr = run("template", "new", "custom", "--from", "detailed")
	if code != 0 || !strings.Contains(stdout, "已生成") || stderr != "" {
		t.Fatalf("new: %d %s %s", code, stdout, stderr)
	}
	path := filepath.Join(home, "config", "card-templates", "custom.yaml")
	original, err := os.ReadFile(path)
	if err != nil || !bytes.Contains(original, []byte("#")) || !bytes.Contains(original, []byte("extends: detailed")) {
		t.Fatalf("scaffold: %s %v", original, err)
	}
	code, _, stderr = run("template", "new", "custom")
	unchanged, err := os.ReadFile(path)
	if code != 1 || err != nil || !bytes.Equal(original, unchanged) || !strings.Contains(stderr, "不会被覆盖") {
		t.Fatalf("overwrite guard: %d %s %v", code, stderr, err)
	}
	for _, state := range []string{"firing", "resolved", "digest"} {
		code, stdout, stderr = run("template", "preview", "custom", "--state", state)
		if code != 0 || stderr != "" || !json.Valid([]byte(stdout)) {
			t.Fatalf("preview %s: %d %s %s", state, code, stdout, stderr)
		}
	}
	code, stdout, stderr = run("template", "validate")
	if code != 0 || stderr != "" || strings.Count(stdout, "三种样例校验通过") != 5 {
		t.Fatalf("validate: %d %s %s", code, stdout, stderr)
	}
	for _, args := range [][]string{{"new", "../escape"}, {"new", "standard"}, {"new", "x", "--from", "custom"}, {"preview", "standard", "--state", "bad"}, {"preview"}} {
		code, _, stderr = run(append([]string{"template"}, args...)...)
		if code != 1 || !strings.Contains(stderr, "--help") {
			t.Fatalf("usage guard %v: %d %s", args, code, stderr)
		}
	}
	code, _, stderr = run("template", "validate", "missing")
	if code != 1 || strings.Contains(stderr, "--help") || !strings.Contains(stderr, "不存在") {
		t.Fatalf("strict validate: %d %s", code, stderr)
	}
	if err := os.WriteFile(path, []byte("id: custom\nextends: standard\ntitle: '{{.Alert.NoSuchField}}'\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	code, _, stderr = run("template", "validate", "custom")
	if code != 1 || !strings.Contains(stderr, "NoSuchField") {
		t.Fatalf("broken validate: %d %s", code, stderr)
	}
	code, stdout, stderr = run("template", "preview", "custom")
	if code != 0 || !json.Valid([]byte(stdout)) || !strings.Contains(stderr, "已使用标准模板") {
		t.Fatalf("fallback preview: %d %s %s", code, stdout, stderr)
	}
}

func TestTemplateValidateRejectsUnboundedSyntaxWithinOneSecond(t *testing.T) {
	home := t.TempDir()
	directory := filepath.Join(home, "config", "card-templates")
	if err := os.MkdirAll(directory, 0o700); err != nil {
		t.Fatal(err)
	}
	for _, source := range []string{"{{range 100000000000}}{{end}}", "{{$v := .Alerts}}{{range $v}}{{end}}", "{{range len .Alerts}}{{end}}", "{{range .Alerts}}{{range $.Alerts}}{{end}}{{end}}", `{{define "loop"}}{{template "loop"}}{{end}}`} {
		if err := os.WriteFile(filepath.Join(directory, "unsafe.yaml"), []byte(fmt.Sprintf("id: unsafe\nextends: standard\ntitle: '%s'\n", source)), 0o600); err != nil {
			t.Fatal(err)
		}
		var stdout, stderr bytes.Buffer
		started := time.Now()
		code := execute([]string{"--home", home, "template", "validate", "unsafe"}, &stdout, &stderr)
		if code != 1 || !strings.Contains(stderr.String(), "unsafe.yaml 第 3 行") || time.Since(started) > time.Second {
			t.Fatalf("unsafe CLI validation: status=%d stderr=%s elapsed=%s", code, stderr.String(), time.Since(started))
		}
	}
}
