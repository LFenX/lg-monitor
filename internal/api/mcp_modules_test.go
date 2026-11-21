// Copyright (c) 2026 LFen (https://lfen.ink) — lg-monitor

package api

import (
	"io/fs"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/LFenX/lg-monitor/docs"
)

func TestMCPModulesCoverEveryToolExactlyOnce(t *testing.T) {
	seen := map[string]string{}
	for _, module := range MCPModules {
		if module.Name == "" || module.Purpose == "" || len(module.Tools) == 0 {
			t.Fatalf("module %+v is incomplete", module)
		}
		for _, tool := range module.Tools {
			if other, ok := seen[tool]; ok {
				t.Fatalf("%s is in both %s and %s", tool, other, module.Name)
			}
			seen[tool] = module.Name
		}
	}
	var bindings []string
	for _, binding := range mcpBindings() {
		bindings = append(bindings, binding.name)
		if seen[binding.name] == "" {
			t.Errorf("tool %s belongs to no module", binding.name)
		}
	}
	for tool := range seen {
		if !slices.Contains(bindings, tool) {
			t.Errorf("module lists %s, which is not a tool", tool)
		}
	}
}

func TestMCPInstructionsCarryTheModuleMapAndSayHowToGetEverything(t *testing.T) {
	full, short := mcpInstructions(false), mcpInstructions(true)
	for _, module := range MCPModules {
		if !strings.Contains(full, "- "+module.Name+"：") {
			t.Fatalf("instructions miss module %s", module.Name)
		}
	}
	if !strings.Contains(full, "start_here") || !strings.Contains(full, "直接问用户") || strings.Contains(full, "--tools all") {
		t.Fatalf("full instructions: %s", full)
	}
	if !strings.Contains(short, "--tools all") {
		t.Fatal("essential instructions do not say how to get the full tool set")
	}
}

func TestMCPGuideDocumentsEveryTool(t *testing.T) {
	data, err := fs.ReadFile(docs.Guides, "mcp.md")
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	for _, binding := range mcpBindings() {
		if !regexp.MustCompile("`" + binding.name + "`").MatchString(text) {
			t.Errorf("docs/mcp.md does not describe %s", binding.name)
		}
	}
	for _, module := range MCPModules {
		if !strings.Contains(text, "### "+module.Name) {
			t.Errorf("docs/mcp.md has no section for %s", module.Name)
		}
	}
}
