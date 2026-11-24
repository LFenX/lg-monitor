// Copyright (c) 2026 LFen (https://lfen.ink) — lg-monitor

package app

import (
	"testing"

	"github.com/LFenX/lg-monitor/internal/collector"
	"github.com/LFenX/lg-monitor/internal/config"
)

func TestProgramRowsTakeOwnerOrServiceNames(t *testing.T) {
	port := 80
	cfg := &config.Config{Targets: []config.Target{
		{ID: "read-model", Name: "读取模型", Match: config.Match{Process: "node", Cmdline: "sync-mailbox-read-model"}},
		{ID: "watch", Name: "实时巡检", Match: config.Match{Process: "node", Cmdline: "live-watch"}},
		{ID: "any-node", Name: "随便哪个 node", Match: config.Match{Process: "node"}},
		{ID: "gateway", Name: "nginx 网关", Match: config.Match{Process: "nginx", Port: &port}},
		{ID: "pg", Name: "数据库", Match: config.Match{Process: "postgres"}},
	}}
	cfg.Settings.ProgramNames = []config.ProgramName{
		{Name: "所有 Python", Process: "python3"},
		{Name: "收信看门狗", Process: "python3", Detail: "app.imap_watchdog"},
	}
	rows := nameProgram(cfg, []collector.ProcessUsage{
		{Name: "node", Detail: "35-sync-mailbox-read-model.ts"},
		{Name: "node", Detail: "21-live-watch.ts"},
		{Name: "node", Detail: "task-worker.ts"},
		{Name: "python3", Detail: "app.imap_watchdog"},
		{Name: "python3", Detail: "server.py"},
		{Name: "nginx"},
		{Name: "postgres"},
		{Name: "postgres.exe"},
	})
	want := []string{"读取模型", "实时巡检", "", "收信看门狗", "所有 Python", "", "数据库", "数据库"}
	for index, row := range rows {
		if row.DisplayName != want[index] {
			t.Fatalf("row %d %s/%s: %q, want %q", index, row.Name, row.Detail, row.DisplayName, want[index])
		}
	}
}

func TestProgramNamesAreSavedAndRemoved(t *testing.T) {
	f := newAPIFixture(t)
	f.request(t, "PUT", "/api/v1/devices/program-names", "read", `{"process":"node","name":"x"}`, 403)
	f.request(t, "PUT", "/api/v1/devices/program-names", "config", `{"process":"","name":"x"}`, 400)
	f.request(t, "PUT", "/api/v1/devices/program-names", "config", `{"process":"node","detail":"task-worker.ts","name":"任务 worker"}`, 200)
	f.request(t, "PUT", "/api/v1/devices/program-names", "config", `{"process":"node","detail":"task-worker.ts","name":"任务处理"}`, 200)
	if names := f.manager.Snapshot().Settings.ProgramNames; len(names) != 1 || names[0].Name != "任务处理" {
		t.Fatalf("names=%+v", names)
	}
	f.request(t, "PUT", "/api/v1/devices/program-names", "config", `{"process":"NODE","detail":"Task-Worker.ts","name":""}`, 200)
	if names := f.manager.Snapshot().Settings.ProgramNames; len(names) != 0 {
		t.Fatalf("not removed: %+v", names)
	}
}
