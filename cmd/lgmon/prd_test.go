package main

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"path/filepath"
	"strings"
	"testing"

	"github.com/LFenX/lg-monitor/internal/app"
	"github.com/spf13/cobra"
)

func TestEveryCommandSupportsJSON(t *testing.T) {
	var visit func(*cobra.Command)
	visit = func(command *cobra.Command) {
		{
			flag := command.Flags().Lookup("json")
			if flag == nil {
				flag = command.PersistentFlags().Lookup("json")
			}
			if flag == nil {
				flag = command.InheritedFlags().Lookup("json")
			}
			if flag == nil || flag.Value.Type() != "bool" {
				t.Errorf("PRD G2 requires --json: %s", command.CommandPath())
			}
		}
		for _, child := range command.Commands() {
			visit(child)
		}
	}
	visit(newRootCommand())
}

func TestCLIJSONActualPayloadsAndErrors(t *testing.T) {
	previous := slog.Default()
	t.Cleanup(func() { slog.SetDefault(previous) })
	home := t.TempDir()
	run := func(want int, args ...string) map[string]any {
		t.Helper()
		var out, errOut bytes.Buffer
		args = append(args, "--home", home, "--json")
		if code := execute(args, &out, &errOut); code != want {
			t.Fatalf("%v code=%d want=%d out=%s err=%s", args, code, want, out.String(), errOut.String())
		}
		decoder := json.NewDecoder(&out)
		var value map[string]any
		if err := decoder.Decode(&value); err != nil {
			t.Fatalf("invalid JSON: %v %s", err, out.String())
		}
		if strings.TrimSpace(out.String()) != "" {
			t.Fatal("stdout mixed JSON/text", out.String())
		}
		return value
	}
	if value := run(0, "version"); value["version"] != version || value["commit"] != commit || value["buildTime"] != buildTime {
		t.Fatal(value)
	}
	for _, group := range [][]string{nil, {"config"}, {"pair"}, {"team"}, {"rules"}, {"template"}, {"notify"}} {
		value := run(0, group...)
		if !strings.HasPrefix(value["command"].(string), "lgmon") {
			t.Fatal("command group did not return JSON help", value)
		}
	}
	if value := run(0, "help", "config"); value["command"] != "lgmon config" || !strings.Contains(value["usage"].(string), "delete") {
		t.Fatal(value)
	}
	if value := run(0, "version", "--help"); value["command"] != "lgmon version" {
		t.Fatal(value)
	}
	if value := run(0, "config", "schema", "--output", filepath.Join(t.TempDir(), "schemas")); value["status"] != "ok" || len(value["files"].([]any)) == 0 {
		t.Fatal(value)
	}
	if value := run(0, "template", "new", "oncall"); value["id"] != "oncall" || value["path"] != filepath.Join(home, "config", "card-templates", "oncall.yaml") {
		t.Fatal(value)
	}
	if value := run(0, "template", "validate", "oncall"); value["valid"] != true {
		t.Fatal(value)
	}
	if value := run(0, "template", "preview", "oncall"); value["schema"] != "2.0" {
		t.Fatal(value)
	}
	if value := run(1, "template", "validate", "not-created"); value["valid"] != false || len(value["results"].([]any)) != 1 {
		t.Fatal(value)
	}
	if value := run(1, "version", "unexpected"); value["error"].(map[string]any)["code"] != "usage_error" {
		t.Fatal(value)
	}
	if value := run(1, "template", "new", "oncall"); value["error"].(map[string]any)["code"] != "command_failed" {
		t.Fatal(value)
	}
}

func TestRunJSONNoticeAndLogsUseSeparateStreams(t *testing.T) {
	var out, errOut bytes.Buffer
	command := newRunCommand(func(_ context.Context, opts app.LocalOptions) error {
		opts.Logger.Info("fixture", "sample", 1)
		return opts.Notice("需要本人接入")
	})
	command.PersistentFlags().String("home", t.TempDir(), "")
	command.SetOut(&out)
	command.SetErr(&errOut)
	command.SetArgs([]string{"--role", "collector", "--json"})
	if err := command.Execute(); err != nil {
		t.Fatal(err)
	}
	var notice, log map[string]any
	if json.Unmarshal(out.Bytes(), &notice) != nil || notice["event"] != "notice" || notice["message"] != "需要本人接入" {
		t.Fatal(out.String())
	}
	if json.Unmarshal(errOut.Bytes(), &log) != nil || log["msg"] != "fixture" || log["sample"] != float64(1) {
		t.Fatal(errOut.String())
	}
}

func TestMCPJSONStartupErrorKeepsProtocolStdoutEmpty(t *testing.T) {
	var out, errOut bytes.Buffer
	if code := execute([]string{"mcp", "--json", "--url", ":bad"}, &out, &errOut); code == 0 || out.Len() != 0 || !json.Valid(errOut.Bytes()) {
		t.Fatalf("MCP stdout polluted: out=%s err=%s", out.String(), errOut.String())
	}
}
