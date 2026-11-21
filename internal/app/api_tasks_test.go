package app

import (
	"context"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/LFenX/lg-monitor/internal/api"
	"github.com/LFenX/lg-monitor/internal/collector"
	"github.com/LFenX/lg-monitor/internal/config"
	"github.com/LFenX/lg-monitor/internal/pipeline"
)

func TestMonitorHistoryIgnoresShortFailureAndKeepsNowInLastBucket(t *testing.T) {
	for _, probeOnly := range []bool{false, true} {
		t.Run(map[bool]string{false: "target", true: "probe"}[probeOnly], func(t *testing.T) {
			f := newAPIFixture(t)
			if probeOnly {
				f.files.MapFS["targets.yaml"].Data = []byte("targets: [{id: web, match: {process: lgmon.exe}, probes: [{type: http, url: 'https://fake.invalid', interval: 10s, timeout: 2s}]}]\n")
				if err := f.manager.Reload(); err != nil {
					t.Fatal(err)
				}
			}
			for index, observation := range []struct {
				offset time.Duration
				state  string
			}{{-20 * time.Second, "up"}, {-10 * time.Second, "down"}, {-9 * time.Second, "up"}, {0, "down"}} {
				message := pipeline.Message{CollectorID: "fake-host", Seq: uint64(index + 1), TS: f.now.Add(observation.offset).UnixMilli(), Kind: pipeline.KindTargetStatus, TargetStatus: &pipeline.TargetStatus{TargetID: "web", State: observation.state, Process: collector.ProcessFound}}
				if probeOnly {
					message.Kind = pipeline.KindProbeResult
					message.TargetStatus = nil
					message.ProbeResult = &pipeline.ProbeResult{TargetID: "web", Probe: 0, OK: observation.state == "up", Type: "http"}
				}
				if err := f.db.Write(context.Background(), message); err != nil {
					t.Fatal(err)
				}
			}
			if err := f.db.Flush(context.Background()); err != nil {
				t.Fatal(err)
			}
			if probeOnly {
				if err := f.db.Write(context.Background(), pipeline.Message{CollectorID: "fake-host", Seq: 100, TS: f.now.UnixMilli(), Kind: pipeline.KindTargetStatus, TargetStatus: &pipeline.TargetStatus{TargetID: "web", State: "up", Process: collector.ProcessFound}}); err != nil {
					t.Fatal(err)
				}
				if err := f.db.Flush(context.Background()); err != nil {
					t.Fatal(err)
				}
			}
			detail := decodeAs[api.MonitorDetail](t, f.request(t, "GET", "/api/v1/monitors/web", "read", "", 200).Body.Bytes())
			last := detail.Slots[len(detail.Slots)-1]
			if len(detail.Monitor.BeatSlots) != 48 || detail.Monitor.BeatSlots[47].State != "up" || detail.Monitor.BeatSlots[47].Uptime == nil || *detail.Monitor.BeatSlots[47].Uptime != 100 {
				t.Fatal("small history bars did not apply the confirmation threshold")
			}
			if detail.Monitor.State != "down" {
				t.Fatalf("live failure was suppressed: %s", detail.Monitor.State)
			}
			if last.Uptime == nil || *last.Uptime != 100 || detail.Uptime24h == nil || *detail.Uptime24h != 100 {
				t.Fatalf("boundary samples lost: slot=%+v total=%v", last, detail.Uptime24h)
			}
			for _, slots := range [][]api.StatusSlot{detail.Slots, detail.Days} {
				var failures []api.StatusSegment
				for _, slot := range slots {
					for _, segment := range slot.Segments {
						if segment.State == "down" {
							failures = append(failures, segment)
						}
					}
				}
				if len(failures) != 0 {
					t.Fatalf("short failure counted in history: %+v", failures)
				}
			}
		})
	}
}

func TestSmallHistoryRefreshesOnRecoveryWithoutWaitingForTheHistoryCache(t *testing.T) {
	f := newAPIFixture(t)
	for index, offset := range []time.Duration{-61 * time.Second, -41 * time.Second, -21 * time.Second, -time.Second} {
		if err := f.db.Write(context.Background(), pipeline.Message{CollectorID: "fake-host", Seq: uint64(index + 1), TS: f.now.Add(offset).UnixMilli(), Kind: pipeline.KindTargetStatus, TargetStatus: &pipeline.TargetStatus{TargetID: "web", State: "down", Process: collector.ProcessMissing}}); err != nil {
			t.Fatal(err)
		}
	}
	if err := f.db.Flush(context.Background()); err != nil {
		t.Fatal(err)
	}
	before := decodeAs[[]api.Monitor](t, f.request(t, "GET", "/api/v1/monitors", "read", "", 200).Body.Bytes())[0]
	if before.State != "down" || before.Uptime == nil || *before.Uptime != 0 {
		t.Fatalf("missing live failure before recovery: %s", before.State)
	}
	f.now = f.now.Add(3 * time.Second)
	if err := f.db.Write(context.Background(), pipeline.Message{CollectorID: "fake-host", Seq: 5, TS: f.now.UnixMilli(), Kind: pipeline.KindTargetStatus, TargetStatus: &pipeline.TargetStatus{TargetID: "web", State: "up", Process: collector.ProcessFound}}); err != nil {
		t.Fatal(err)
	}
	if err := f.db.Flush(context.Background()); err != nil {
		t.Fatal(err)
	}
	after := decodeAs[[]api.Monitor](t, f.request(t, "GET", "/api/v1/monitors", "read", "", 200).Body.Bytes())[0]
	if after.State == "down" || after.Uptime == nil || *after.Uptime <= 0 {
		t.Fatal("recovery reused the stale failure history")
	}
}

func TestMonitorListConfirmsFailuresAcrossTheHistoryBoundary(t *testing.T) {
	f := newAPIFixture(t)
	ctx := context.Background()
	start := f.now.Add(-24 * time.Hour)
	for index, second := range []int{-45, -25, -5, 15, 35, 55} {
		state := collector.StateDown
		if second == 55 {
			state = collector.StateUp
		}
		message := pipeline.Message{CollectorID: "fake-host", Seq: uint64(index + 1), TS: start.Add(time.Duration(second) * time.Second).UnixMilli(), Kind: pipeline.KindTargetStatus, TargetStatus: &pipeline.TargetStatus{TargetID: "web", State: state, Process: collector.ProcessFound}}
		if err := f.db.Write(ctx, message); err != nil {
			t.Fatal(err)
		}
	}
	if err := f.db.Flush(ctx); err != nil {
		t.Fatal(err)
	}
	items := decodeAs[[]api.Monitor](t, f.request(t, "GET", "/api/v1/monitors", "read", "", 200).Body.Bytes())
	slot := items[0].BeatSlots[0]
	if slot.State != collector.StateDown || slot.Uptime == nil || *slot.Uptime != 100.0/3 {
		t.Fatalf("lookback did not confirm the failure for availability: %+v", slot)
	}
	want := api.StatusSegment{From: start.Add(15 * time.Second).UnixMilli(), To: start.Add(85 * time.Second).UnixMilli(), State: collector.StateUp}
	if !slices.Equal(slot.Segments, []api.StatusSegment{want}) {
		t.Fatalf("visible range must start at its first observation: got %+v want %+v", slot.Segments, want)
	}
}

func TestMonitorSegmentsDoNotLoseFailuresBeyondTheEventListLimit(t *testing.T) {
	f := newAPIFixture(t)
	var seq uint64
	for index := 0; index < 60; index++ {
		for _, point := range []struct {
			offset int
			state  string
		}{{0, "down"}, {20, "down"}, {40, "down"}, {60, "down"}, {65, "up"}} {
			seq++
			if err := f.db.Write(context.Background(), pipeline.Message{CollectorID: "fake-host", Seq: seq, TS: f.now.Add(time.Duration(index*120-7200+point.offset) * time.Second).UnixMilli(), Kind: pipeline.KindTargetStatus, TargetStatus: &pipeline.TargetStatus{TargetID: "web", State: point.state, Process: collector.ProcessFound}}); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := f.db.Flush(context.Background()); err != nil {
		t.Fatal(err)
	}
	detail := decodeAs[api.MonitorDetail](t, f.request(t, "GET", "/api/v1/monitors/web", "read", "", 200).Body.Bytes())
	failures := 0
	var failedMS int64
	for _, slot := range detail.Slots {
		for _, segment := range slot.Segments {
			if segment.State == "down" {
				failures++
				failedMS += segment.To - segment.From
			}
		}
	}
	if failures < 60 || failedMS != 60*65000 || len(detail.Events) != 50 {
		t.Fatalf("lost timeline failures: failures=%d events=%d", failures, len(detail.Events))
	}
}

func TestMonitorSegmentsClipAcrossBucketsAndLeaveSamplingGapsEmpty(t *testing.T) {
	f := newAPIFixture(t)
	points := []struct {
		offset time.Duration
		state  string
	}{{-time.Hour, "up"}, {-15*time.Minute - 30*time.Second, "down"}, {-15*time.Minute - 10*time.Second, "down"}, {-15*time.Minute + 10*time.Second, "down"}, {-15*time.Minute + 30*time.Second, "up"}, {-time.Second, "up"}}
	for index, point := range points {
		if err := f.db.Write(context.Background(), pipeline.Message{CollectorID: "fake-host", Seq: uint64(index + 1), TS: f.now.Add(point.offset).UnixMilli(), Kind: pipeline.KindTargetStatus, TargetStatus: &pipeline.TargetStatus{TargetID: "web", State: point.state, Process: collector.ProcessFound}}); err != nil {
			t.Fatal(err)
		}
	}
	if err := f.db.Flush(context.Background()); err != nil {
		t.Fatal(err)
	}
	detail := decodeAs[api.MonitorDetail](t, f.request(t, "GET", "/api/v1/monitors/web", "read", "", 200).Body.Bytes())
	var failures []api.StatusSegment
	for _, slot := range detail.Slots {
		for _, segment := range slot.Segments {
			if segment.State == "down" {
				failures = append(failures, segment)
			}
			if segment.From < f.now.Add(-15*time.Minute-31*time.Second).UnixMilli() && segment.To > f.now.Add(-time.Hour+30*time.Second).UnixMilli() {
				t.Fatalf("sampling gap painted healthy: %+v", segment)
			}
		}
	}
	if len(failures) != 2 || failures[0].To-failures[0].From != 30000 || failures[1].To-failures[1].From != 30000 || failures[0].To != failures[1].From {
		t.Fatalf("cross-bucket failure misplaced: %+v", failures)
	}
}

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
	if last := detail.Slots[len(detail.Slots)-1]; last.State != "up" {
		t.Fatalf("an unconfirmed task failure must not count in history: %+v", last)
	}
	if len(detail.Lineage) != 1 || detail.Lineage[0].ID != "web" || len(detail.Boards) != 1 || detail.Rules[0].Text != "计划任务上次运行失败、被禁用或找不到时告警" {
		t.Fatalf("detail=%+v", detail)
	}
	parent := decodeAs[api.MonitorDetail](t, f.request(t, "GET", "/api/v1/monitors/web", "read", "", 200).Body.Bytes())
	if parent.Monitor.Overall != parent.Monitor.State || len(parent.Children) != 1 || parent.Children[0].ID != "task-daily-backup" || parent.Children[0].Depth != 1 || parent.Children[0].State != "down" {
		t.Fatalf("parent=%+v children=%+v", parent.Monitor, parent.Children)
	}
	if len(parent.Days) != 30 || parent.Days[29].From > f.now.UnixMilli() || parent.Days[29].From+24*time.Hour.Milliseconds() < f.now.UnixMilli() {
		t.Fatalf("days=%+v", parent.Days)
	}
	f.request(t, "GET", "/api/v1/monitors/ghost", "read", "", 404)
}
