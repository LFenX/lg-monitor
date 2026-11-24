// Copyright (c) 2026 LFen (https://lfen.ink) — lg-monitor

package agents

import "testing"

func TestIdentifyNamesTheAgentsPeopleUse(t *testing.T) {
	for name, want := range map[string]string{
		"claude-code":                   "claude-code",
		"Claude Code":                   "claude-code",
		"claude-code/2.1.7 (cli)":       "claude-code",
		"claude-ai":                     "claude",
		"codex-mcp-client":              "codex",
		"Codex":                         "codex",
		"openai-mcp":                    "codex",
		"cursor-vscode":                 "cursor",
		"opencode":                      "opencode",
		"kimi-code":                     "kimi",
		"Kimi CLI":                      "kimi",
		"dsh":                           "dsh",
		"deepseek-harness":              "dsh",
		"ZCode":                         "zcode",
		"gemini-cli-mcp-client":         "gemini",
		"github-copilot-developer":      "copilot",
		"Visual Studio Code":            "vscode",
		"Visual-Studio-Code":            "vscode",
		"windsurf-client":               "windsurf",
		"Trae CN":                       "trae",
		"qwen-code":                     "qwen",
		"Cline":                         "cline",
		"Roo-Code":                      "roo",
		"Kilo-Code":                     "kilo",
		"amp-mcp-client":                "amp",
		"antigravity-client":            "antigravity",
		"pi":                            "pi",
		"pi-coding-agent":               "pi",
		"Muse":                          "muse",
		"grok-cli":                      "grok",
		"goose":                         "goose",
		"factory-cli":                   "droid",
		"crush":                         "crush",
		"Zed":                           "zed",
		"Cherry Studio":                 "cherry",
		"Xcode-copilot-xcode":           "copilot",
		"JetBrains-IU-copilot-intellij": "copilot",
	} {
		kind, ok := Identify(name)
		if !ok || kind.ID != want {
			t.Errorf("Identify(%q) = %q, %t; want %q", name, kind.ID, ok, want)
		}
	}
}

func TestIdentifyLeavesGenericAndLookalikeNamesUnknown(t *testing.T) {
	for _, name := range []string{"", "mcp", "fastmcp", "python-sdk", "Mozilla/5.0", "node", "extraeditor", "example", "my-client"} {
		if kind, ok := Identify(name); ok {
			t.Errorf("Identify(%q) = %q; want unknown", name, kind.ID)
		}
	}
}

func TestIdentifyTriesEachNameInTurn(t *testing.T) {
	// A generic clientInfo name falls through to the title.
	kind, ok := Identify("mcp", "Kimi Code")
	if !ok || kind.ID != "kimi" || kind.Name != "Kimi Code" {
		t.Fatalf("got %+v %t", kind, ok)
	}
}

func TestFromProcessesReadsTheNearestAncestorThatNamesAnAgent(t *testing.T) {
	for _, test := range []struct {
		chain []Process
		want  string
	}{
		{[]Process{{Executable: `C:\Users\fake\.local\bin\kimi.exe`}}, "kimi"},
		{[]Process{{Executable: `C:\Python\python.exe`, CommandLine: `python -m kimi_cli`}, {Executable: `C:\Windows\explorer.exe`}}, "kimi"},
		{[]Process{{Executable: `C:\node\node.exe`, CommandLine: `node C:\npm\node_modules\@deepseek-ai\dsh\bin\dsh.js`}}, "dsh"},
		{[]Process{{Executable: `C:\node\node.exe`, CommandLine: `node C:\npm\node_modules\@anthropic-ai\claude-code\cli.js`}, {Executable: `C:\VSCode\Code.exe`}}, "claude-code"},
		{[]Process{{Executable: `C:\Program Files\ZCode\ZCode.exe`}}, "zcode"},
		{[]Process{{Executable: `C:\tools\opencode.exe`}}, "opencode"},
		{[]Process{{Executable: `C:\Windows\System32\cmd.exe`}, {Executable: `C:\Program Files\Cursor\Cursor.exe`}}, "cursor"},
	} {
		kind, ok := FromProcesses(test.chain)
		if !ok || kind.ID != test.want {
			t.Errorf("FromProcesses(%+v) = %q, %t; want %q", test.chain, kind.ID, ok, test.want)
		}
	}
	if kind, ok := FromProcesses([]Process{{Executable: `C:\Windows\System32\cmd.exe`, CommandLine: "cmd /c lgmon mcp"}, {Executable: `C:\Windows\explorer.exe`}}); ok {
		t.Fatalf("a shell named %q", kind.ID)
	}
}

func TestLookupKnowsEveryKindByID(t *testing.T) {
	seen := map[string]bool{}
	for _, kind := range kinds {
		if seen[kind.ID] || kind.Name == "" {
			t.Fatalf("kind %q repeated or unnamed", kind.ID)
		}
		seen[kind.ID] = true
		if found, ok := Lookup(kind.ID); !ok || found.Name != kind.Name {
			t.Fatalf("Lookup(%q) = %+v", kind.ID, found)
		}
	}
	if _, ok := Lookup("unknown"); ok {
		t.Fatal("unknown kind found")
	}
}
