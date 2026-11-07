package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
)

func agentFixture(t *testing.T) (agentDependencies, agentContext, string) {
	t.Helper()
	userHome := t.TempDir()
	home := filepath.Join(t.TempDir(), "home's $() [test]")
	exe := filepath.Join(t.TempDir(), "lgmon.exe")
	deps := agentDependencies{Executable: func() (string, error) { return exe, nil }, UserHome: func() (string, error) { return userHome, nil }, Paths: nativeAgentPaths{}}
	return deps, agentContext{Executable: exe, Home: home, Config: filepath.Join(home, "config")}, userHome
}

func executeAgent(t *testing.T, deps agentDependencies, home string, args ...string) ([]byte, error) {
	t.Helper()
	root := &cobra.Command{Use: "test", SilenceUsage: true, SilenceErrors: true}
	root.PersistentFlags().String("home", home, "test home")
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&out)
	root.AddCommand(newAgentCommand(deps))
	root.SetArgs(append([]string{"agent"}, args...))
	err := root.ExecuteContext(context.Background())
	return out.Bytes(), err
}

func TestAgentInstallBothTargetsAndRefreshesOnlyItsOwnFile(t *testing.T) {
	deps, local, userHome := agentFixture(t)
	output, err := executeAgent(t, deps, local.Home, "install", "--json")
	var result agentInstallResult
	if err != nil || json.Unmarshal(output, &result) != nil || !result.OK || len(result.Installed) != 2 {
		t.Fatalf("install: %s %v", output, err)
	}
	expected := []string{filepath.Join(userHome, ".claude", "skills", "lg-monitor", "SKILL.md"), filepath.Join(userHome, ".codex", "skills", "lg-monitor", "SKILL.md")}
	if !reflect.DeepEqual(result.Installed, expected) {
		t.Fatalf("wrong destinations: %v", result.Installed)
	}
	for _, path := range expected {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		parts := strings.SplitN(string(data), "\n---\n", 2)
		var meta struct{ Name, Description string }
		if len(parts) != 2 || yaml.Unmarshal([]byte(strings.TrimPrefix(parts[0], "---\n")), &meta) != nil || meta.Name != "lg-monitor" || meta.Description == "" {
			t.Fatalf("invalid skill metadata: %s", path)
		}
		var paths agentContext
		_, tail, ok := strings.Cut(parts[1], "```json\n")
		contextJSON, _, _ := strings.Cut(tail, "\n```")
		if !ok || json.Unmarshal([]byte(contextJSON), &paths) != nil || paths != local {
			t.Fatalf("skill lost actual paths: %+v", paths)
		}
		if strings.Contains(string(data), "{{.Paths}}") {
			t.Fatal("unresolved skill context")
		}
	}
	sibling := filepath.Join(filepath.Dir(expected[1]), "notes.md")
	if err := os.WriteFile(sibling, []byte("user notes"), 0600); err != nil {
		t.Fatal(err)
	}
	local.Home = filepath.Join(t.TempDir(), "new home")
	if output, err := executeAgent(t, deps, local.Home, "install", "--target", "codex", "--json"); err != nil {
		t.Fatalf("update: %s %v", output, err)
	}
	data, err := os.ReadFile(expected[1])
	if err != nil || !strings.Contains(string(data), quotedPowerShell(local.Home)) {
		t.Fatal("managed skill was not updated")
	}
	data, err = os.ReadFile(sibling)
	if err != nil || string(data) != "user notes" {
		t.Fatal("installer changed unrelated file")
	}
	entries, err := os.ReadDir(filepath.Dir(expected[1]))
	if err != nil || len(entries) != 2 {
		t.Fatalf("temporary files retained: %v %v", entries, err)
	}
}

func TestAgentInstallPreflightsAllTargetsAndPreservesForeignSkill(t *testing.T) {
	deps, local, userHome := agentFixture(t)
	dest := filepath.Join(userHome, ".codex", "skills", "lg-monitor")
	if err := os.MkdirAll(dest, 0700); err != nil {
		t.Fatal(err)
	}
	original := []byte("---\nname: another-skill\ndescription: user instructions\n---\nContains an example " + agentManaged + "\n")
	file := filepath.Join(dest, "SKILL.md")
	if err := os.WriteFile(file, original, 0600); err != nil {
		t.Fatal(err)
	}
	output, err := executeAgent(t, deps, local.Home, "install", "--json")
	var result agentInstallResult
	if err == nil || json.Unmarshal(output, &result) != nil || result.OK || len(result.Installed) != 0 || result.Error == "" {
		t.Fatalf("conflict falsely succeeded: %s %v", output, err)
	}
	data, _ := os.ReadFile(file)
	if !bytes.Equal(data, original) {
		t.Fatal("foreign skill overwritten")
	}
	if _, err := os.Stat(filepath.Join(userHome, ".claude")); !os.IsNotExist(err) {
		t.Fatal("first target written before all-target preflight")
	}
}

type failingAgentPaths struct {
	nativeAgentPaths
	fail string
}

func (p failingAgentPaths) Replace(from, to string) error {
	if strings.Contains(to, p.fail) {
		return errors.New("fake replacement failure")
	}
	return p.nativeAgentPaths.Replace(from, to)
}
func TestAgentInstallReportsPartialWritesAndCanRetry(t *testing.T) {
	deps, local, userHome := agentFixture(t)
	deps.Paths = failingAgentPaths{fail: ".codex"}
	output, err := executeAgent(t, deps, local.Home, "install", "--json")
	var result agentInstallResult
	if err == nil || json.Unmarshal(output, &result) != nil || result.OK || len(result.Installed) != 1 || !strings.Contains(result.Installed[0], ".claude") {
		t.Fatalf("partial result: %s %v", output, err)
	}
	dir := filepath.Join(userHome, ".codex", "skills", "lg-monitor")
	items, err := os.ReadDir(dir)
	if err != nil || len(items) != 0 {
		t.Fatalf("failed write retained a file: %v %v", items, err)
	}
	deps.Paths = nativeAgentPaths{}
	if output, err := executeAgent(t, deps, local.Home, "install", "--json"); err != nil {
		t.Fatalf("retry: %s %v", output, err)
	}
}

func TestAgentPromptIsSelfContainedPreservesWishAndDoesNotInstall(t *testing.T) {
	deps, local, userHome := agentFixture(t)
	wish := "给 Nebula 加监控，创建面板；' $(Write-Output BAD)\n{{.Home}}"
	deps.UserHome = func() (string, error) { t.Fatal("prompt read user installation directory"); return "", nil }
	output, err := executeAgent(t, deps, local.Home, "prompt", "--wish", wish, "--json")
	var result struct {
		agentContext
		Prompt string `json:"prompt"`
	}
	if err != nil || json.Unmarshal(output, &result) != nil || result.agentContext != local {
		t.Fatalf("prompt: %s %v", output, err)
	}
	encoded, _ := json.Marshal(wish)
	if !strings.Contains(result.Prompt, string(encoded)) || !strings.Contains(result.Prompt, quotedPowerShell(local.Home)) || !strings.Contains(result.Prompt, "get_panel_data") || !strings.Contains(result.Prompt, "detailed") {
		t.Fatal("prompt omitted task, actual paths or operating instructions")
	}
	if strings.HasPrefix(result.Prompt, "---") {
		t.Fatal("prompt exposed skill frontmatter instead of task instructions")
	}
	entries, err := os.ReadDir(userHome)
	if err != nil || len(entries) != 0 {
		t.Fatal("prompt installed or wrote a skill")
	}
}

func TestAgentRejectsBadTargetsBeforeAccessingTheSystem(t *testing.T) {
	deps, local, _ := agentFixture(t)
	deps.Executable = func() (string, error) { t.Fatal("invalid target reached environment"); return "", nil }
	if output, err := executeAgent(t, deps, local.Home, "install", "--target", "invalid", "--json"); err == nil || !bytes.Contains(output, []byte(`"ok":false`)) {
		t.Fatalf("invalid target: %s %v", output, err)
	}
	for _, wish := range []string{"", "  ", "bad\x00wish"} {
		if _, err := executeAgent(t, deps, local.Home, "prompt", "--wish", wish); err == nil {
			t.Fatal("invalid wish accepted")
		}
	}
}
