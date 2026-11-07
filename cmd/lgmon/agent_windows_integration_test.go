//go:build windows && integration

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestNativeAgentPowerShellCommandsPreserveLiteralPaths(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "程序's $() [scope] `name")
	if err := os.Mkdir(dir, 0700); err != nil {
		t.Fatal(err)
	}
	exe := filepath.Join(dir, "fake-lgmon.ps1")
	if err := os.WriteFile(exe, []byte("ConvertTo-Json -Compress -InputObject @($args)\n"), 0600); err != nil {
		t.Fatal(err)
	}
	home := filepath.Join(t.TempDir(), "目录's $(throw 'BAD') [scope] `text; #")
	text, err := renderAgentSkill(agentContext{Executable: exe, Home: home, Config: filepath.Join(home, "config")})
	if err != nil {
		t.Fatal(err)
	}
	_, tail, ok := strings.Cut(text, "```powershell\n")
	body, _, _ := strings.Cut(tail, "\n```")
	if !ok {
		t.Fatal("missing runnable PowerShell commands")
	}
	script := filepath.Join(t.TempDir(), "commands.ps1")
	// Windows PowerShell 5.1 reads a saved UTF-8 script correctly when its BOM is present.
	scriptText := "\ufeff$ErrorActionPreference='Stop'\n[Console]::OutputEncoding = New-Object System.Text.UTF8Encoding\n" + body + "\n"
	if err := os.WriteFile(script, []byte(scriptText), 0600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "powershell.exe", "-NoProfile", "-ExecutionPolicy", "Bypass", "-File", script).CombinedOutput()
	if err != nil {
		t.Fatalf("generated commands failed: %v %s", err, out)
	}
	lines := bytes.Split(bytes.TrimSpace(out), []byte("\n"))
	expected := [][]string{{"version"}, {"status", "--home", home, "--json"}, {"config", "validate", "--home", home, "--json"}, {"query", "--schema", "--home", home, "--json"}}
	if len(lines) != len(expected) {
		t.Fatalf("unexpected execution/output count: %s", out)
	}
	for i, line := range lines {
		var args []string
		if err := json.Unmarshal(bytes.TrimSpace(line), &args); err != nil || !reflect.DeepEqual(args, expected[i]) {
			t.Fatalf("path interpreted instead of passed literally: %s %v", line, err)
		}
	}
}

func TestNativeAgentInstallRejectsLinkedDestination(t *testing.T) {
	for _, leaf := range []bool{false, true} {
		t.Run(map[bool]string{false: "directory", true: "file"}[leaf], func(t *testing.T) {
			deps, local, userHome := agentFixture(t)
			outside := t.TempDir()
			sentinel := filepath.Join(outside, "SKILL.md")
			if err := os.WriteFile(sentinel, []byte("user-owned sentinel"), 0600); err != nil {
				t.Fatal(err)
			}
			destination := filepath.Join(userHome, ".codex")
			source := outside
			if leaf {
				directory := filepath.Join(destination, "skills", "lg-monitor")
				if err := os.MkdirAll(directory, 0700); err != nil {
					t.Fatal(err)
				}
				destination = filepath.Join(directory, "SKILL.md")
				source = sentinel
			}
			if err := os.Symlink(source, destination); err != nil {
				t.Skipf("test account cannot create temporary link: %v", err)
			}
			if out, err := executeAgent(t, deps, local.Home, "install", "--json"); err == nil {
				t.Fatalf("linked destination accepted: %s", out)
			}
			data, _ := os.ReadFile(sentinel)
			if string(data) != "user-owned sentinel" {
				t.Fatal("linked file overwritten")
			}
			if _, err := os.Stat(filepath.Join(userHome, ".claude")); !os.IsNotExist(err) {
				t.Fatal("preflight partially installed before rejecting link")
			}
		})
	}
}
