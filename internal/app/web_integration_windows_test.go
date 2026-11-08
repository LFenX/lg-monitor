//go:build windows && integration && webui

package app

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"testing/fstest"
	"time"
	"unsafe"

	"github.com/LFenX/lg-monitor/internal/collector"
	"github.com/LFenX/lg-monitor/internal/feishu"
	"github.com/LFenX/lg-monitor/internal/pipeline"
	"github.com/LFenX/lg-monitor/internal/rules"
	"github.com/LFenX/lg-monitor/internal/transport"
	"golang.org/x/sys/windows"
)

func TestNativeCollectorConfigurationBrowser(t *testing.T) {
	source := &apiConfigurationPresence{state: transport.CollectorConfigState{State: "outdated", Version: "fake-directory-version", Message: "目标 blog 使用 match.workDir，采集器版本过旧，请升级以支持 match_dir"}}
	f := newAPIFixtureWithProcessSource(t, time.Date(2026, 11, 8, 8, 30, 0, 0, time.UTC), time.Now, nil, source)
	runNativeWebBrowser(t, f, "collector-config.spec.ts")
	if len(f.client.sent) != 0 || f.hookFactories != 0 || len(f.toast.values) != 0 {
		t.Fatal("configuration status viewing attempted notifications")
	}
}

func TestNativeWebPanelBrowser(t *testing.T) {
	// Socket deadlines use wall time while business data uses a fixed, reproducible clock.
	f := newAPIFixtureWithHTTPClock(t, time.Date(2026, 11, 7, 8, 30, 0, 0, time.UTC), time.Now)
	if err := f.db.SetClockOffset(context.Background(), "fake-host", 40000); err != nil {
		t.Fatal(err)
	}
	var inspectionChecks atomic.Int32
	f.client.scopes = func(context.Context) ([]feishu.Scope, error) {
		ready := inspectionChecks.Add(1) > 1
		return []feishu.Scope{{Name: "im:message:send_as_bot", Type: "tenant", Granted: ready}, {Name: "im:chat:read", Type: "tenant", Granted: ready}}, nil
	}
	f.client.chats = func(context.Context) ([]feishu.Chat, error) {
		if inspectionChecks.Load() < 2 {
			return nil, nil
		}
		return []feishu.Chat{{ID: "oc_fake_api", Name: "虚构测试群"}}, nil
	}
	f.files.MapFS["targets.yaml"].Data = []byte("targets: [{id: web, name: 示例网站, collector: fake-host, match: {process: fake-web.exe}, probes: [{type: http, url: http://127.0.0.1:18080/health, interval: 10s, timeout: 2s}]}, {id: api, name: 业务接口, collector: fake-host, match: {process: fake-api.exe}}, {id: database, name: 数据库, collector: fake-host, match: {process: fake-db.exe}}]\n")
	if err := f.manager.Reload(); err != nil {
		t.Fatal(err)
	}
	var seq uint64
	write := func(message pipeline.Message) {
		t.Helper()
		seq++
		message.Seq, message.CollectorID = seq, "fake-host"
		if err := f.db.Write(context.Background(), message); err != nil {
			t.Fatal(err)
		}
	}
	for i := 0; i < 21; i++ {
		ts := f.now.Add(time.Duration(i-20) * 5 * time.Second).UnixMilli()
		cpu := 14.5 + float64(i)*1.4
		write(pipeline.Message{TS: ts, Kind: pipeline.KindSystemMetrics, SystemMetrics: &collector.SystemMetrics{TS: ts, CPUPercent: cpu, MemoryTotal: 8 << 30, MemoryUsed: 5 << 30, MemoryAvailable: 3 << 30, MemoryPercent: 62.5, UptimeSeconds: 123456}})
		state, process := "up", collector.ProcessFound
		if i >= 18 {
			state, process = "down", collector.ProcessMissing
		}
		write(pipeline.Message{TS: ts, Kind: pipeline.KindTargetStatus, TargetStatus: &pipeline.TargetStatus{TargetID: "web", State: state, Process: process, Reason: map[bool]string{true: "未发现匹配进程", false: "进程与探测正常"}[i >= 18]}})
		write(pipeline.Message{TS: ts, Kind: pipeline.KindProbeResult, ProbeResult: &pipeline.ProbeResult{TargetID: "web", Probe: 0, Type: "http", OK: i < 18, LatencyMS: int64(18 + i*2)}})
		write(pipeline.Message{TS: ts, Kind: pipeline.KindProcessSample, ProcessSample: &pipeline.ProcessSample{TargetID: "web", PID: 101, CreatedAt: 1000, CPUPercent: float64(i) / 3, RSS: uint64(80+i) << 20}})
	}
	for _, id := range []string{"api", "database"} {
		write(pipeline.Message{TS: f.now.UnixMilli(), Kind: pipeline.KindTargetStatus, TargetStatus: &pipeline.TargetStatus{TargetID: id, State: "up", Process: collector.ProcessFound, Reason: "进程运行正常"}})
	}
	write(pipeline.Message{TS: f.now.UnixMilli(), Kind: pipeline.KindServiceStatus, ServiceStatus: &collector.Service{Name: "fake-database", DisplayName: "虚构数据库服务", State: "running", StartType: "automatic", PID: 303}})
	write(pipeline.Message{TS: f.now.UnixMilli(), Kind: pipeline.KindDataGap, DataGap: &pipeline.DataGap{FromSeq: seq + 1, ToSeq: seq + 1, Dropped: 1, Reason: "虚构断线缺口"}})
	if err := f.db.Flush(context.Background()); err != nil {
		t.Fatal(err)
	}
	alert := rules.Alert{ID: "fake-browser-alert", RuleID: "down", CollectorID: "fake-host", TargetID: "web", State: "firing", Severity: "critical", Summary: "示例网站进程已停止", StartedAt: f.now.Add(-15 * time.Second).UnixMilli()}
	if err := f.db.SaveTransition(context.Background(), alert, rules.Event{AlertID: alert.ID, TS: alert.StartedAt, Kind: "firing", Message: alert.Summary}); err != nil {
		t.Fatal(err)
	}
	runNativeWebBrowser(t, f, "")
	if len(f.client.sent) != 0 || f.hookFactories != 0 || len(f.toast.values) != 0 {
		t.Fatal("browser viewing/controls attempted external notification or native toast")
	}
	if inspectionChecks.Load() < 2 {
		t.Fatal("browser did not verify automatic Feishu inspection")
	}
}

func TestNativeFeishuWizardBrowser(t *testing.T) {
	f := newAPIFixtureWithHTTPClock(t, time.Date(2026, 11, 7, 8, 30, 0, 0, time.UTC), time.Now)
	f.files.MapFS["notifiers.yaml"] = &fstest.MapFile{Data: []byte("# browser fake channels\nnotifiers: [{id: desktop, type: toast}]\n")}
	f.files.MapFS["rules.yaml"] = &fstest.MapFile{Data: []byte("rules: [{id: down, name: 网站宕机, type: process_absent, target: web, for: 0s, severity: critical, notify: [desktop]}]\n")}
	if err := f.manager.Reload(); err != nil {
		t.Fatal(err)
	}
	chats := []feishu.Chat{{ID: "oc_fake_duty", Name: "虚构值班群"}, {ID: "oc_fake_notify", Name: "虚构通知群"}, {ID: "oc_fake_draft", Name: "虚构模板测试群"}}
	f.client.chats = func(context.Context) ([]feishu.Chat, error) { return chats, nil }
	f.client.chatInfo = func(_ context.Context, id string) (feishu.Chat, error) {
		for _, chat := range chats {
			if chat.ID == id {
				return chat, nil
			}
		}
		return feishu.Chat{}, fmt.Errorf("unknown fake group")
	}
	runNativeWebBrowser(t, f, "feishu.spec.ts")
	if len(f.client.sent) != 4 || f.hookFactories != 0 || len(f.toast.values) != 0 {
		t.Fatalf("fake sends=%d; unexpected external/native attempts", len(f.client.sent))
	}
	draft := f.client.sent[3]
	if draft.ChatID != "oc_fake_draft" || draft.State != "resolved" || !strings.Contains(string(draft.Card), "虚构尚未保存的试发标题") || !strings.Contains(string(draft.Card), "这是测试消息") {
		t.Fatalf("editor did not send current unsaved draft: %s", draft.Card)
	}
	if len(f.manager.Snapshot().Templates) != 0 || len(f.manager.Snapshot().Notifiers) != 3 {
		t.Fatal("draft testing saved a template or activated its test group")
	}
}

func runNativeWebBrowser(t *testing.T, f *apiFixture, suite string, handlers ...http.Handler) {
	t.Helper()
	var handler http.Handler = f.server
	if len(handlers) > 0 {
		handler = handlers[0]
	}
	server := httptest.NewServer(handler)
	defer server.Close()
	node, err := exec.LookPath("node")
	if err != nil {
		t.Fatal("browser verification requires installed Node 22")
	}
	webRoot, err := filepath.Abs(filepath.Join("..", "..", "web"))
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, node, filepath.Join(webRoot, "tests", "run-browser.mjs"))
	cmd.Dir = webRoot
	cmd.Env = append(os.Environ(), "LGMON_BROWSER_URL="+server.URL, fmt.Sprintf("LGMON_BROWSER_NOW=%d", f.now.UnixMilli()))
	cmd.Env = append(cmd.Env, "LGMON_BROWSER_SUITE="+suite)
	var output bytes.Buffer
	cmd.Stdout, cmd.Stderr = &output, &output
	input, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	defer input.Close()
	job, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer windows.CloseHandle(job)
	var limits windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION
	limits.BasicLimitInformation.LimitFlags = windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE
	if _, err := windows.SetInformationJobObject(job, windows.JobObjectExtendedLimitInformation, uintptr(unsafe.Pointer(&limits)), uint32(unsafe.Sizeof(limits))); err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	assigned := false
	defer func() {
		if !assigned {
			_ = cmd.Process.Kill()
			_ = cmd.Wait()
		}
	}()
	process, err := windows.OpenProcess(windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE, false, uint32(cmd.Process.Pid))
	if err != nil {
		t.Fatal(err)
	}
	err = windows.AssignProcessToJobObject(job, process)
	windows.CloseHandle(process)
	if err != nil {
		t.Fatal(err)
	}
	assigned = true
	// The handshake prevents a child browser escaping before its parent belongs to our Job.
	if _, err := input.Write([]byte("start\n")); err != nil {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		t.Fatal(err)
	}
	input.Close()
	err = cmd.Wait()
	t.Log(output.String())
	if err != nil {
		t.Fatalf("browser checks failed: %v", err)
	}
}
