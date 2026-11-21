// Copyright (c) 2026 LFen (https://lfen.ink) — lg-monitor

package app

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/LFenX/lg-monitor/internal/api"
	"github.com/LFenX/lg-monitor/internal/jobs"
)

// The AICS acceptance scenarios, each a job of its own reporting through the real HTTP API, judged on 2026-11-18
// 10:00 Beijing time (the 02:00 run's grace is over).
func TestJobAcceptanceScenarios(t *testing.T) {
	type check struct {
		status   jobs.Status
		summary  string
		attempts int
		board    string
	}
	stages := func(run string, hour string, states map[string]string, receipts bool) []string {
		var events []string
		for _, name := range []string{"fetch", "verify", "package", "backup", "upload_cn", "upload_overseas", "manifest"} {
			state, ok := states[name]
			if !ok {
				state = "success"
			}
			if state == "" {
				continue
			}
			extra := ""
			if state == "success" && receipts && strings.HasPrefix(name, "upload") {
				extra = `,"receipt":"etag-fake-` + name + `"`
			}
			if state == "failed" {
				extra = `,"error":"虚构错误 ` + name + `"`
			}
			events = append(events, jobStage(run, name, state, "2026-11-18 "+hour+":10", extra))
		}
		return events
	}
	run := func(id, trigger, hour, end string, body []string) []string {
		events := append([]string{jobAttempt(id, "running", "2026-11-18 "+hour+":00", trigger)}, body...)
		if end != "" {
			events = append(events, jobAttempt(id, end, "2026-11-18 "+hour+":30", trigger))
		}
		return events
	}
	partition := func(run, region, upload string) string {
		return fmt.Sprintf(`{"event_id":"%s-%s","attempt_id":"%s","kind":"partition","region":"%s","hour":1794873600000,"unique_recipients":812,"unique_senders":95,"received_rows":10233,"sent_rows":1408,"gz_files":1,"compressed_bytes":3355443,"target_dir":"fake://%s/2026/11/17/08/","verify":"ok","upload":"%s","ts":%d}`, run, region, run, region, region, upload, jobTS("2026-11-18 02:20"))
	}
	scenarios := map[string]struct {
		events []string
		want   check
	}{
		"正常执行":   {append(run("r1", "scheduled", "02", "finished", stages("r1", "02", nil, true)), partition("r1", "cn", "ok"), partition("r1", "overseas", "ok")), check{jobs.Success, "全部阶段成功", 1, "up"}},
		"中途失败":   {run("r1", "scheduled", "02", "aborted", stages("r1", "02", map[string]string{"package": "failed", "backup": "", "upload_cn": "", "upload_overseas": "", "manifest": ""}, true)), check{jobs.Failed, "打包失败：虚构错误 package", 1, "down"}},
		"单端上传失败": {append(run("r1", "scheduled", "02", "finished", stages("r1", "02", map[string]string{"upload_overseas": "failed"}, true)), partition("r1", "cn", "ok"), partition("r1", "overseas", "failed")), check{jobs.Partial, "海外 S3 上传", 1, "down"}},
		"未知回执":   {run("r1", "scheduled", "02", "finished", stages("r1", "02", map[string]string{"upload_overseas": "unknown"}, true)), check{jobs.Unknown, "海外 S3 上传结果未知", 1, "unknown"}},
		"成功但没回执": {run("r1", "scheduled", "02", "finished", stages("r1", "02", nil, false)), check{jobs.Unknown, "没有回执", 1, "unknown"}},
		"漏跑":     {nil, check{jobs.Missed, "过了宽限 2 小时还没有任何运行", 0, "down"}},
		"手动补跑": {append(run("r1", "scheduled", "02", "finished", stages("r1", "02", map[string]string{"upload_cn": "failed", "upload_overseas": "failed"}, true)),
			run("m1", "manual", "08", "finished", stages("m1", "08", nil, true))...), check{jobs.Success, "全部阶段成功（共 2 次运行）", 2, "up"}},
		// The process ended with exit code 0 and the port answered, but no stage said so: not success.
		"退出码为零不算成功": {run("r1", "scheduled", "02", "finished", nil), check{jobs.Unknown, "没有上报结果", 1, "unknown"}},
	}
	f := newAPIFixtureAt(t, jobNow)
	targets := "targets: [{id: web, match: {process: fake.exe}}"
	ids := map[string]string{}
	index := 0
	for name := range scenarios {
		index++
		ids[name] = fmt.Sprintf("scenario-%d", index)
		targets += fmt.Sprintf(", {id: %s, name: %s, job: {schedule: {at: '02:00'}}}", ids[name], name)
	}
	f.files.MapFS["targets.yaml"] = &fstest.MapFile{Data: []byte(targets + "]\n")}
	if err := f.manager.Reload(); err != nil {
		t.Fatal(err)
	}
	for name, scenario := range scenarios {
		id := ids[name]
		if len(scenario.events) > 0 {
			body := jobEvents(scenario.events...)
			first := decodeJobReport(t, f.request(t, "POST", "/api/v1/jobs/"+id+"/events", "config", body, 200))
			// 重复事件: the reporter resends the whole batch after a lost response.
			again := decodeJobReport(t, f.request(t, "POST", "/api/v1/jobs/"+id+"/events", "config", body, 200))
			if first.Accepted != len(scenario.events) || again.Accepted != 0 || again.Duplicates != len(scenario.events) {
				t.Fatalf("%s: first %+v, again %+v", name, first, again)
			}
		}
		days := decodeJobDays(t, f.request(t, "GET", "/api/v1/jobs/"+id+"/days?days=1", "read", "", 200))
		got := days.Days[0]
		if got.Status != scenario.want.status || !strings.Contains(got.Summary, scenario.want.summary) || len(got.Attempts) != scenario.want.attempts {
			t.Errorf("%s: %s %q with %d runs, want %s containing %q with %d", name, got.Status, got.Summary, len(got.Attempts), scenario.want.status, scenario.want.summary, scenario.want.attempts)
		}
	}
	var monitors []api.Monitor
	if err := json.Unmarshal(f.request(t, "GET", "/api/v1/monitors", "read", "", 200).Body.Bytes(), &monitors); err != nil {
		t.Fatal(err)
	}
	for name, scenario := range scenarios {
		for _, monitor := range monitors {
			if monitor.ID == ids[name] && monitor.State != scenario.want.board {
				t.Errorf("%s: board row state %s, want %s", name, monitor.State, scenario.want.board)
			}
		}
	}
}
