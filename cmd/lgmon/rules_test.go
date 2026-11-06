package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/LFenX/lg-monitor/internal/pipeline"
	"github.com/LFenX/lg-monitor/internal/rules"
	"github.com/LFenX/lg-monitor/internal/store"
)

func TestRulesTestCLIReplaysWithoutNotificationSetup(t *testing.T) {
	home := t.TempDir()
	directory := filepath.Join(home, "config")
	if err := os.MkdirAll(directory, 0700); err != nil {
		t.Fatal(err)
	}
	for name, text := range map[string]string{"targets.yaml": "targets: [{id: web, match: {process: fake.exe}}]", "rules.yaml": "rules: [{id: down, type: process_absent, target: web, for: 2s, severity: warning, notify: [fake]}]", "notifiers.yaml": "feishuApps: [{id: fake, appId: cli_fake, secretRef: no-such-secret, domain: feishu}]\nnotifiers: [{id: fake, type: feishu_app, app: fake, chatId: oc_fake}]"} {
		if err := os.WriteFile(filepath.Join(directory, name), []byte(text), 0600); err != nil {
			t.Fatal(err)
		}
	}
	db, err := store.Open(context.Background(), store.Options{Path: filepath.Join(home, "data", "lgmon.db")})
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	now := time.Now()
	for _, row := range []struct {
		offset time.Duration
		up     bool
	}{{-2 * time.Minute, true}, {-time.Minute, false}, {-30 * time.Second, true}} {
		state, process := "down", "missing"
		if row.up {
			state, process = "up", "found"
		}
		if err := db.Write(context.Background(), pipeline.Message{CollectorID: "fake", Seq: 1, TS: now.Add(row.offset).UnixMilli(), Kind: pipeline.KindTargetStatus, TargetStatus: &pipeline.TargetStatus{TargetID: "web", State: state, Process: process}}); err != nil {
			t.Fatal(err)
		}
	}
	if err := db.Flush(context.Background()); err != nil {
		t.Fatal(err)
	}
	var out, stderr bytes.Buffer
	if code := execute([]string{"rules", "test", "down", "--since", "90s", "--home", home, "--json"}, &out, &stderr); code != 0 {
		t.Fatalf("rule replay CLI: %d %s", code, stderr.String())
	}
	var result rules.ReplayResult
	if err := json.Unmarshal(out.Bytes(), &result); err != nil || len(result.Events) != 2 || !result.Complete {
		t.Fatalf("rule replay JSON: %s %v", out.String(), err)
	}
	if rows, err := db.ActiveAlerts(context.Background()); err != nil || len(rows) != 0 {
		t.Fatal("CLI replay wrote alerts")
	}
}

func TestRulesDraftCLIClipsRetentionAndLeavesConfigurationUnchanged(t *testing.T) {
	home := t.TempDir()
	directory := filepath.Join(home, "config")
	if err := os.MkdirAll(directory, 0700); err != nil {
		t.Fatal(err)
	}
	for name, text := range map[string]string{"rules.yaml": "rules: []\n", "notifiers.yaml": "feishuApps: [{id: fake, appId: cli_fake, secretRef: nonexistent, domain: feishu}]\nnotifiers: [{id: fake, type: feishu_app, app: fake, chatId: oc_fake}]"} {
		if err := os.WriteFile(filepath.Join(directory, name), []byte(text), 0600); err != nil {
			t.Fatal(err)
		}
	}
	db, err := store.Open(context.Background(), store.Options{Path: filepath.Join(home, "data", "lgmon.db")})
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	draft := filepath.Join(home, "draft.yaml")
	if err := os.WriteFile(draft, []byte("id: unsaved\ntype: sql\nquery: SELECT 'fake' AS key\ninterval: 24h\nseverity: warning\nnotify: [fake]\n"), 0600); err != nil {
		t.Fatal(err)
	}
	var out, stderr bytes.Buffer
	if code := execute([]string{"rules", "test", "--file", draft, "--since", "48h", "--home", home, "--json"}, &out, &stderr); code != 0 {
		t.Fatalf("draft CLI: %d %s", code, stderr.String())
	}
	var result rules.ReplayResult
	if err := json.Unmarshal(out.Bytes(), &result); err != nil || !result.Complete || result.Rule != "unsaved" || result.Evaluations != 2 || result.Totals.Fired != 1 || result.From-result.RequestedFrom != (24*time.Hour).Milliseconds() || !strings.Contains(strings.Join(result.Notes, ""), "截断") {
		t.Fatalf("draft replay JSON: %s %v", out.String(), err)
	}
	if content, err := os.ReadFile(filepath.Join(directory, "rules.yaml")); err != nil || string(content) != "rules: []\n" {
		t.Fatalf("draft modified stored configuration: %s %v", content, err)
	}
	out.Reset()
	stderr.Reset()
	if code := execute([]string{"rules", "test", "existing", "--file", draft, "--home", home}, &out, &stderr); code != 1 {
		t.Fatalf("conflicting rule inputs: %d %s", code, stderr.String())
	}
}

func TestStatusShowsPersistedRecentRuleError(t *testing.T) {
	home := t.TempDir()
	db, err := store.Open(context.Background(), store.Options{Path: filepath.Join(home, "data", "lgmon.db")})
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx := context.Background()
	failure := rules.RuleError{RuleID: "sql-fake", Error: "假 SQL 查询失败", At: 1000000, Consecutive: 2, Active: true}
	if err := db.RecordRuleEvaluation(ctx, failure); err != nil {
		t.Fatal(err)
	}
	if err := db.RecordRuleEvaluation(ctx, rules.RuleError{RuleID: failure.RuleID}); err != nil {
		t.Fatal(err)
	}
	var out, stderr bytes.Buffer
	if code := execute([]string{"status", "--home", home, "--json"}, &out, &stderr); code != 0 {
		t.Fatalf("status CLI: %d %s", code, stderr.String())
	}
	var status statusResult
	if err := json.Unmarshal(out.Bytes(), &status); err != nil || len(status.RuleErrors) != 1 || status.RuleErrors[0].Error != failure.Error || status.RuleErrors[0].Active || status.RuleErrors[0].Consecutive != 0 {
		t.Fatalf("recent error was lost after recovery: %s %v", out.String(), err)
	}
	out.Reset()
	if code := execute([]string{"status", "--home", home}, &out, &stderr); code != 0 || !strings.Contains(out.String(), failure.Error) {
		t.Fatalf("status text omitted recent SQL error: %s %s", out.String(), stderr.String())
	}
}
