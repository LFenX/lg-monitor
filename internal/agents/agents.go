// Copyright (c) 2026 LFen (https://lfen.ink) — lg-monitor

// Package agents names the coding agent behind an MCP connection, so the web panel can show which agents
// are connected instead of a token name.
package agents

import (
	"path/filepath"
	"strings"
)

// Kind is one agent lgmon recognizes. ID is stable: the web panel picks the icon by it.
type Kind struct {
	ID   string
	Name string
	// exact matches the whole normalized client name; short names would otherwise match inside other words.
	exact []string
	// contains matches part of the normalized client name.
	contains []string
	// prefix matches the start of the normalized client name, for words that occur inside unrelated words.
	prefix []string
	// programs are executable base names (lower case, without .exe) of the agent's own process.
	programs []string
	// packages appear in the command line of a node, python or bun process that runs the agent.
	packages []string
}

// Ordered: a more specific kind comes before one whose words it contains (Claude Code before Claude).
var kinds = []Kind{
	{ID: "claude-code", Name: "Claude Code", contains: []string{"claudecode"}, programs: []string{"claude"}, packages: []string{"@anthropic-ai/claude-code", "claude-code"}},
	{ID: "claude", Name: "Claude", exact: []string{"claudeai", "claude"}, contains: []string{"claudedesktop"}},
	{ID: "codex", Name: "Codex", exact: []string{"openaimcp"}, contains: []string{"codex"}, programs: []string{"codex"}, packages: []string{"@openai/codex"}},
	{ID: "cursor", Name: "Cursor", contains: []string{"cursor"}, programs: []string{"cursor", "cursor-agent"}},
	{ID: "opencode", Name: "OpenCode", contains: []string{"opencode"}, programs: []string{"opencode"}, packages: []string{"opencode-ai"}},
	{ID: "kimi", Name: "Kimi Code", contains: []string{"kimi"}, programs: []string{"kimi"}, packages: []string{"kimi-cli", "kimi_cli", "kimi-code"}},
	{ID: "dsh", Name: "DeepSeek dsh", exact: []string{"dsh"}, contains: []string{"deepseekharness", "deepseekdsh", "deepseek"}, programs: []string{"dsh"}, packages: []string{"@deepseek-ai/dsh", "deepseek-harness"}},
	{ID: "zcode", Name: "ZCode", contains: []string{"zcode"}, programs: []string{"zcode"}},
	{ID: "gemini", Name: "Gemini CLI", contains: []string{"gemini"}, programs: []string{"gemini"}, packages: []string{"@google/gemini-cli"}},
	{ID: "copilot", Name: "GitHub Copilot", contains: []string{"copilot"}, programs: []string{"copilot"}, packages: []string{"@github/copilot"}},
	{ID: "vscode", Name: "VS Code", exact: []string{"vscode"}, contains: []string{"visualstudiocode"}, programs: []string{"code", "code - insiders"}},
	{ID: "windsurf", Name: "Windsurf", contains: []string{"windsurf"}, programs: []string{"windsurf"}},
	{ID: "trae", Name: "Trae", prefix: []string{"trae"}, programs: []string{"trae", "trae cn"}},
	{ID: "qwen", Name: "Qwen Code", contains: []string{"qwen"}, programs: []string{"qwen"}, packages: []string{"@qwen-code/qwen-code"}},
	{ID: "codebuddy", Name: "CodeBuddy", contains: []string{"codebuddy"}, programs: []string{"codebuddy"}, packages: []string{"@tencent-ai/codebuddy-code"}},
	{ID: "qoder", Name: "Qoder", contains: []string{"qoder"}, programs: []string{"qoder", "qodercli"}},
	{ID: "cline", Name: "Cline", contains: []string{"cline"}},
	{ID: "roo", Name: "Roo Code", exact: []string{"roo"}, contains: []string{"roocode"}},
	{ID: "kilo", Name: "Kilo Code", exact: []string{"kilo"}, contains: []string{"kilocode"}},
	{ID: "amp", Name: "Amp", exact: []string{"amp", "ampmcpclient", "ampcode"}, programs: []string{"amp"}, packages: []string{"@sourcegraph/amp"}},
	{ID: "antigravity", Name: "Antigravity", contains: []string{"antigravity"}, programs: []string{"antigravity"}},
	{ID: "pi", Name: "Pi", exact: []string{"pi", "piagent", "pimono"}, contains: []string{"picodingagent"}, programs: []string{"pi"}, packages: []string{"@mariozechner/pi-coding-agent", "pi-coding-agent"}},
	{ID: "muse", Name: "Muse", exact: []string{"muse", "musecode", "museagent"}, contains: []string{"musecli", "musecoding"}, programs: []string{"muse"}},
	{ID: "grok", Name: "Grok", contains: []string{"grok"}, programs: []string{"grok", "grok-cli"}, packages: []string{"grok-cli", "@vibe-kit/grok-cli"}},
	{ID: "goose", Name: "Goose", exact: []string{"goose"}, programs: []string{"goose"}},
	{ID: "iflow", Name: "iFlow CLI", contains: []string{"iflow"}, programs: []string{"iflow"}, packages: []string{"@iflow-ai/iflow-cli"}},
	{ID: "droid", Name: "Factory Droid", exact: []string{"droid"}, contains: []string{"factorycli", "factorydroid"}, programs: []string{"droid"}},
	{ID: "crush", Name: "Crush", exact: []string{"crush"}, programs: []string{"crush"}},
	{ID: "zed", Name: "Zed", exact: []string{"zed", "zededitor"}, programs: []string{"zed"}},
	{ID: "cherry", Name: "Cherry Studio", contains: []string{"cherrystudio"}, programs: []string{"cherry studio"}},
}

// Lookup returns the kind with id.
func Lookup(id string) (Kind, bool) {
	for _, kind := range kinds {
		if kind.ID == id {
			return kind, true
		}
	}
	return Kind{}, false
}

// Identify names the agent from what it reported in MCP initialize (clientInfo name and title) or an HTTP
// User-Agent. It returns false for clients it does not know, including generic SDK names such as "mcp".
func Identify(names ...string) (Kind, bool) {
	for _, name := range names {
		key := normalize(name)
		if key == "" {
			continue
		}
		for _, kind := range kinds {
			if contains(kind.exact, func(word string) bool { return key == word }) ||
				contains(kind.contains, func(word string) bool { return strings.Contains(key, word) }) ||
				contains(kind.prefix, func(word string) bool { return strings.HasPrefix(key, word) }) {
				return kind, true
			}
		}
	}
	return Kind{}, false
}

// Process is one ancestor of the MCP server process, nearest first.
type Process struct {
	Executable  string
	CommandLine string
}

// FromProcesses names the agent from the processes that started the MCP server, for agents whose MCP SDK
// reports a generic client name. Only the nearest ancestors are worth reading: farther up is the terminal or
// the desktop, which says nothing about the agent.
func FromProcesses(chain []Process) (Kind, bool) {
	for _, process := range chain {
		program := strings.ToLower(strings.TrimSuffix(filepath.Base(strings.ReplaceAll(process.Executable, `\`, "/")), filepath.Ext(process.Executable)))
		line := strings.ToLower(strings.ReplaceAll(process.CommandLine, `\`, "/"))
		for _, kind := range kinds {
			if contains(kind.programs, func(name string) bool { return program == name }) ||
				contains(kind.packages, func(name string) bool { return strings.Contains(line, name) }) {
				return kind, true
			}
		}
	}
	return Kind{}, false
}

func contains(words []string, match func(string) bool) bool {
	for _, word := range words {
		if match(word) {
			return true
		}
	}
	return false
}

// normalize drops case, versions and separators: "Claude Code", "claude-code" and "claude-code/2.1" are one name.
func normalize(name string) string {
	name, _, _ = strings.Cut(strings.ToLower(name), "/")
	var b strings.Builder
	for _, r := range name {
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' {
			b.WriteRune(r)
		}
	}
	return b.String()
}
