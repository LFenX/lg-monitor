package config

import (
	"strings"
	"testing"
	"testing/fstest"
)

const dashboardFixture = "id: overview\ntitle: 概览\nrefresh: 10s\npanels:\n  - id: online\n    type: stat\n    title: 在线\n    query: SELECT 1 AS value\n    width: 3\n"

func TestDashboardLoadRejectsInvalidFilesAtTheirSource(t *testing.T) {
	for _, tc := range []struct{ name, source, want string }{
		{"width", strings.Replace(dashboardFixture, "width: 3", "width: 13", 1), "第 9 行"},
		{"type", strings.Replace(dashboardFixture, "type: stat", "type: unknown", 1), "第 6 行"},
		{"sql write", strings.Replace(dashboardFixture, "SELECT 1 AS value", "DELETE FROM alerts", 1), "第 8 行"},
		{"sql multi", strings.Replace(dashboardFixture, "SELECT 1 AS value", "SELECT 1; SELECT 2", 1), "第 8 行"},
		{"refresh", strings.Replace(dashboardFixture, "10s", "0s", 1), "第 3 行"},
		{"unknown key", dashboardFixture + "    source: unsupported\n", "第 10 行"},
		{"wrong scalar", strings.Replace(dashboardFixture, "width: 3", "width: '3'", 1), "第 9 行"},
		{"bad card id", strings.Replace(dashboardFixture, "id: online", "id: bad_id", 1), "第 5 行"},
		{"duplicate card id", dashboardFixture + strings.Split(dashboardFixture, "panels:\n")[1], "第 10 行"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg, err := Load(fstest.MapFS{"dashboards/overview.yaml": {Data: []byte(tc.source)}})
			if err != nil || !strings.Contains(cfg.DashboardIssues().Error(), "dashboards/overview.yaml "+tc.want) {
				t.Fatalf("error not located: %v %+v", err, cfg)
			}
		})
	}
	if cfg, err := Load(fstest.MapFS{"dashboards/a.yaml": {Data: []byte(dashboardFixture)}}); err != nil || !strings.Contains(cfg.DashboardIssues().Error(), "文件名必须") {
		t.Fatalf("mismatched filename accepted: %v", err)
	}
}

func TestDashboardReloadAndSnapshotsRetainTheLastValidDefinition(t *testing.T) {
	files := fstest.MapFS{"dashboards/overview.yaml": {Data: []byte(dashboardFixture)}}
	manager, err := NewManager(FSLoader{FS: files}, nil)
	if err != nil {
		t.Fatal(err)
	}
	snapshot := manager.Snapshot()
	snapshot.Dashboards[0].Panels[0].Query = "SELECT 999 AS value"
	if manager.Snapshot().Dashboards[0].Panels[0].Query != "SELECT 1 AS value" {
		t.Fatal("snapshot aliases active dashboard panels")
	}
	files["dashboards/overview.yaml"] = &fstest.MapFile{Data: []byte(strings.Replace(dashboardFixture, "width: 3", "width: 99", 1))}
	if err := manager.Reload(); err != nil || manager.Snapshot().Dashboards[0].Panels[0].Width != 3 || manager.Snapshot().DashboardStates[0].Error == nil {
		t.Fatal("invalid reload replaced valid dashboard")
	}
	delete(files, "dashboards/overview.yaml")
	if err := manager.Reload(); err != nil || len(manager.Snapshot().Dashboards) != 0 {
		t.Fatalf("deletion was not reflected: %v", err)
	}
}
