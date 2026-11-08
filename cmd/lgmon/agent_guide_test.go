package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/LFenX/lg-monitor/internal/config"
	"github.com/LFenX/lg-monitor/internal/notify/cards"
	"github.com/LFenX/lg-monitor/internal/pipeline"
	"github.com/LFenX/lg-monitor/internal/rules"
	"github.com/LFenX/lg-monitor/internal/store"
)

func TestDirectoryExamplesInGuideAndEmbeddedSkillLoad(t *testing.T) {
	for _, file := range []string{filepath.Join("..", "..", "docs", "agent-guide.md"), "agent-skill.md"} {
		data, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		text := strings.ReplaceAll(string(data), "\r\n", "\n")
		example := regexp.MustCompile("(?s)```yaml\\n(- id: node-blog-a\\n.*?)\\n```").FindStringSubmatch(text)
		if len(example) != 2 {
			t.Fatalf("missing directory recipe in %s", file)
		}
		cfg, err := config.Load(fstest.MapFS{"targets.yaml": {Data: []byte("targets:\n" + example[1])}})
		if err != nil || len(cfg.Targets) != 2 || cfg.Targets[0].Match.WorkDir == cfg.Targets[1].Match.WorkDir {
			t.Fatalf("invalid two-blog recipe in %s: %+v %v", file, cfg, err)
		}
	}
}

func agentGuideExamples(t *testing.T) fstest.MapFS {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "..", "docs", "agent-guide.md"))
	if err != nil {
		t.Fatal(err)
	}
	text := strings.ReplaceAll(string(data), "\r\n", "\n")
	matches := regexp.MustCompile("(?s)<!-- lgmon-example: ([^\\n]+) -->\\n```yaml\\n(.*?)\\n```").FindAllStringSubmatch(text, -1)
	files := fstest.MapFS{}
	for _, match := range matches {
		if files[match[1]] != nil {
			t.Fatalf("duplicate example %s", match[1])
		}
		files[match[1]] = &fstest.MapFile{Data: []byte(match[2])}
	}
	if len(files) != 6 {
		t.Fatalf("expected full six-file example, got %d", len(files))
	}
	return files
}

func TestAgentGuideExamplesLoadAndProduceRealPanelDataAndBothCards(t *testing.T) {
	files := agentGuideExamples(t)
	manager, err := config.NewManager(config.FSLoader{FS: files}, nil)
	if err != nil {
		t.Fatal(err)
	}
	cfg := manager.Snapshot()
	if issues := cfg.DashboardIssues(); len(issues) != 0 || len(cfg.Dashboards) != 1 {
		t.Fatalf("guide dashboard invalid: %v", issues)
	}
	now := time.UnixMilli(2000000000000)
	clock := func() time.Time { return now }
	renderer, err := cards.New(manager, cards.Options{Now: clock})
	if err != nil {
		t.Fatal(err)
	}
	for _, state := range []string{"firing", "resolved"} {
		result := renderer.Render("oncall", cards.Sample(state))
		if result.Fallback || !json.Valid(result.JSON) {
			t.Fatalf("guide %s card invalid: %+v", state, result)
		}
	}
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "lgmon.db")
	db, err := store.Open(ctx, store.Options{Path: path, Now: clock, DisableMaintenance: true})
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := db.RegisterCollector(ctx, store.CollectorRecord{ID: "fake-collector", Hostname: "虚构样例主机", FirstSeen: now.UnixMilli(), LastSeen: now.UnixMilli()}); err != nil {
		t.Fatal(err)
	}
	messages := []pipeline.Message{
		{CollectorID: "fake-collector", Seq: 1, TS: now.UnixMilli(), Kind: pipeline.KindTargetStatus, TargetStatus: &pipeline.TargetStatus{TargetID: "demo-web", State: "down", Process: "missing"}},
		{CollectorID: "fake-collector", Seq: 2, TS: now.UnixMilli(), Kind: pipeline.KindProbeResult, ProbeResult: &pipeline.ProbeResult{TargetID: "demo-web", Probe: 0, Type: "http", OK: false, StatusCode: 500, LatencyMS: 1200}},
	}
	for _, m := range messages {
		if err := db.Write(ctx, m); err != nil {
			t.Fatal(err)
		}
	}
	if err := db.Flush(ctx); err != nil {
		t.Fatal(err)
	}
	a := rules.Alert{ID: "fake-guide-alert", RuleID: "demo-web-down", CollectorID: "fake-collector", TargetID: "demo-web", State: "firing", Severity: "critical", StartedAt: now.UnixMilli(), Summary: "虚构网站停止"}
	if err := db.SaveTransition(ctx, a, rules.Event{AlertID: a.ID, TS: a.StartedAt, Kind: a.State, Message: a.Summary}); err != nil {
		t.Fatal(err)
	}
	view, err := store.OpenViewWithClock(ctx, path, clock)
	if err != nil {
		t.Fatal(err)
	}
	defer view.Close()
	for _, collector := range []any{nil, "fake-collector"} {
		for _, panel := range cfg.Dashboards[0].Panels {
			result, err := view.QueryParams(ctx, panel.Query, map[string]any{"from": now.Add(-time.Hour).UnixMilli(), "to": now.UnixMilli(), "collector": collector})
			if err != nil {
				t.Fatalf("guide panel %s query: %v", panel.ID, err)
			}
			if err := store.ValidatePanelResult(panel.Type, result); err != nil || len(result.Rows) != 1 || result.Truncated {
				t.Fatalf("guide panel %s contract/data: %+v %v", panel.ID, result, err)
			}
		}
	}
}

func TestAgentGuideRelativeReferencesExist(t *testing.T) {
	path := filepath.Join("..", "..", "docs", "agent-guide.md")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range regexp.MustCompile(`\]\(([^)]+)\)`).FindAllStringSubmatch(string(data), -1) {
		ref := strings.Split(m[1], "#")[0]
		if ref == "" || strings.Contains(ref, "://") {
			continue
		}
		if _, err := os.Stat(filepath.Join(filepath.Dir(path), filepath.FromSlash(ref))); err != nil {
			t.Errorf("broken guide reference %s: %v", ref, err)
		}
	}
}
