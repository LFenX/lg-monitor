package app

import (
	"context"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/LFenX/lg-monitor/internal/api"
	"github.com/LFenX/lg-monitor/internal/config"
)

func TestScheduledTaskMonitorFollowsTheTaskInventory(t *testing.T) {
	f := newAPIFixture(t)
	f.samples(t)
	seedAPIInventory(t, f)

	found := decodeAs[api.ExploreResult](t, f.request(t, "GET", "/api/v1/explore?kind=tasks", "read", "", 200).Body.Bytes())
	if found.Total != 2 || len(found.Items) != 2 || found.Items[0].Suggest == nil || found.Items[0].Suggest.Task != `\fake\daily-backup` || !strings.Contains(found.Items[0].Result, "0x80070005") || found.Items[1].Result != "已禁用" {
		t.Fatalf("tasks=%+v", found)
	}
	if failing := decodeAs[api.ExploreResult](t, f.request(t, "GET", "/api/v1/explore?kind=tasks&state=attention&q=backup", "read", "", 200).Body.Bytes()); failing.Matched != 1 {
		t.Fatalf("failing=%+v", failing)
	}
	f.request(t, "GET", "/api/v1/explore?kind=tasks&state=running", "read", "", 400)
	f.request(t, "POST", "/api/v1/monitors", "config", `{"name":"混用","task":"\\fake\\x","port":80}`, 400)
	f.request(t, "POST", "/api/v1/monitors", "config", `{"name":"缺斜杠","task":"fake"}`, 400)

	added := decodeAs[api.MonitorResult](t, f.request(t, "POST", "/api/v1/monitors", "config", `{"name":"虚构备份","task":"\\fake\\daily-backup","parent":"web"}`, 200).Body.Bytes())
	if added.Monitor.ID != "task-daily-backup" || added.Monitor.Kind != "task" || added.Monitor.Parent != "web" || !strings.Contains(strings.Join(added.Next, " "), "约一分钟") {
		t.Fatalf("added=%+v", added)
	}
	cfg := f.manager.Snapshot()
	target := targetIndex(cfg)["task-daily-backup"]
	if target.Collector != "fake-host" || target.Task != `\fake\daily-backup` || target.Match != (config.Match{Process: "lgmon.exe"}) {
		t.Fatalf("target=%+v", target)
	}
	if !slices.ContainsFunc(cfg.Rules, func(rule config.Rule) bool {
		return rule.ID == "task-daily-backup-task-failed" && rule.Type == "sql" && rule.Target == target.ID && strings.Contains(rule.Query, "task_status")
	}) {
		t.Fatalf("rules=%+v", cfg.Rules)
	}

	// The store records the task's state each maintenance minute; the page and alert read it.
	f.db.SetTaskWatches(TaskWatches(cfg))
	if err := f.db.Prune(context.Background()); err != nil {
		t.Fatal(err)
	}
	f.backend.agent.history, f.backend.agent.listed = nil, nil
	detail := decodeAs[api.MonitorDetail](t, f.request(t, "GET", "/api/v1/monitors/task-daily-backup", "read", "", 200).Body.Bytes())
	if detail.Monitor.State != "down" || len(detail.Monitor.Hosts) != 1 || !strings.Contains(detail.Monitor.Hosts[0].Reason, "0x80070005") || !slices.Contains(detail.Monitor.Watching, `计划任务 \fake\daily-backup`) {
		t.Fatalf("task monitor=%+v", detail.Monitor)
	}
	if last := detail.Slots[len(detail.Slots)-1]; last.State != "down" {
		t.Fatalf("the newest slot must show the failed task: %+v", last)
	}
	if len(detail.Lineage) != 1 || detail.Lineage[0].ID != "web" || len(detail.Boards) != 1 || detail.Rules[0].Text != "计划任务上次运行失败、被禁用或找不到时告警" {
		t.Fatalf("detail=%+v", detail)
	}
	parent := decodeAs[api.MonitorDetail](t, f.request(t, "GET", "/api/v1/monitors/web", "read", "", 200).Body.Bytes())
	if parent.Monitor.Overall != "down" || len(parent.Children) != 1 || parent.Children[0].ID != "task-daily-backup" || parent.Children[0].Depth != 1 {
		t.Fatalf("parent=%+v children=%+v", parent.Monitor, parent.Children)
	}
	if len(parent.Days) != 30 || parent.Days[29].From > f.now.UnixMilli() || parent.Days[29].From+24*time.Hour.Milliseconds() < f.now.UnixMilli() {
		t.Fatalf("days=%+v", parent.Days)
	}
	f.request(t, "GET", "/api/v1/monitors/ghost", "read", "", 404)
}
