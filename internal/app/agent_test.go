package app

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"
	"testing/fstest"
	"time"

	"github.com/LFenX/lg-monitor/internal/api"
	"github.com/LFenX/lg-monitor/internal/blueprint"
	"github.com/LFenX/lg-monitor/internal/collector"
	"github.com/LFenX/lg-monitor/internal/config"
	"github.com/LFenX/lg-monitor/internal/pipeline"
)

func exerciseMCPIntents(t *testing.T, f *apiFixture, call func(string, any) string) {
	t.Helper()
	if guide := call("start_here", map[string]any{}); !strings.Contains(guide, "add_monitor") || !strings.Contains(guide, `"canConfigure":true`) {
		t.Fatalf("guide=%s", guide)
	}
	if ports := call("explore", map[string]any{"kind": "ports", "q": "8080"}); !strings.Contains(ports, `"port":8080`) || !strings.Contains(ports, `"suggest"`) {
		t.Fatalf("explore=%s", ports)
	}
	var added api.MonitorResult
	if err := json.Unmarshal([]byte(call("add_monitor", map[string]any{"name": "虚构博客", "port": 8080, "collector": "fake-host"})), &added); err != nil || added.Monitor.ID != "port-8080" || added.Dashboard != "port-8080" {
		t.Fatalf("added=%+v %v", added, err)
	}
	if !strings.Contains(call("list_monitors", map[string]any{}), "port-8080") {
		t.Fatal("new monitor missing")
	}
	if !strings.Contains(call("list_dashboard_templates", map[string]any{}), "pc-overview") {
		t.Fatal("templates missing")
	}
	call("create_dashboard", map[string]any{"template": "pc-overview", "theme": "terminal"})
	call("put_dashboard", map[string]any{"id": "mcp-board", "title": "MCP 面板", "panels": []map[string]any{{"id": "cpu", "type": "gauge", "title": "CPU", "width": 6, "unit": "percent", "query": "SELECT cpu AS value FROM system_metrics ORDER BY ts DESC LIMIT 1"}}})
	call("delete_dashboard", map[string]any{"id": "mcp-board"})
	call("remove_monitor", map[string]any{"id": "port-8080"})
	if slices.ContainsFunc(f.manager.Snapshot().Targets, func(target config.Target) bool { return target.ID == "port-8080" }) {
		t.Fatal("monitor was not removed")
	}
}

func TestAgentWorkflowBuildsCompleteMonitorsAndWorkingDashboards(t *testing.T) {
	now := time.UnixMilli(2000000000000)
	processes, _ := processLink(t, now)
	f := newAPIFixtureWithProcessSource(t, now, nil, processes)
	f.samples(t)
	for i, service := range []collector.Service{
		{Name: "fake-db", DisplayName: "虚构数据库", State: collector.ServiceStopped, StartType: "automatic"},
		{Name: "fake-cache", DisplayName: "虚构缓存", State: collector.ServiceRunning, StartType: "manual", PID: 77},
	} {
		value := service
		if err := f.db.Write(context.Background(), pipeline.Message{CollectorID: "fake-host", Seq: uint64(20 + i), TS: now.UnixMilli(), Kind: pipeline.KindServiceStatus, ServiceStatus: &value}); err != nil {
			t.Fatal(err)
		}
	}
	if err := f.db.Flush(context.Background()); err != nil {
		t.Fatal(err)
	}

	var guide api.Guide
	if err := json.Unmarshal(f.request(t, "GET", "/api/v1/guide", "read", "", 200).Body.Bytes(), &guide); err != nil || guide.CanConfigure || len(guide.Steps) != 5 || guide.State.Monitors != 1 {
		t.Fatalf("guide=%+v %v", guide, err)
	}
	var services api.ExploreResult
	if err := json.Unmarshal(f.request(t, "GET", "/api/v1/explore?kind=services&state=attention", "read", "", 200).Body.Bytes(), &services); err != nil || services.Total != 2 || services.Matched != 1 || services.Items[0].Name != "fake-db" || services.Items[0].Suggest == nil || services.Items[0].Suggest.Service != "fake-db" {
		t.Fatalf("services=%+v %v", services, err)
	}
	f.request(t, "GET", "/api/v1/explore?kind=everything", "read", "", 400)

	f.request(t, "POST", "/api/v1/monitors", "read", `{"name":"虚构接口","port":9000}`, 403)
	missing := f.request(t, "POST", "/api/v1/monitors", "config", `{"name":"虚构接口"}`, 400)
	if !strings.Contains(missing.Body.String(), "explore") {
		t.Fatal("missing-target error does not point to explore:", missing.Body.String())
	}
	var created api.MonitorResult
	response := f.request(t, "POST", "/api/v1/monitors", "config", `{"name":"Fake API","url":"https://fake.example.test/health"}`, 200)
	if err := json.Unmarshal(response.Body.Bytes(), &created); err != nil || !created.Created || created.Dashboard != "fake-api" || created.NotifierCreated != "" {
		t.Fatalf("created=%+v %v", created, err)
	}
	snapshot := f.manager.Snapshot()
	rulesFor := func(target string) []string {
		var ids []string
		for _, rule := range f.manager.Snapshot().Rules {
			if rule.Target == target {
				ids = append(ids, rule.ID+"→"+strings.Join(rule.Notify, ","))
			}
		}
		slices.Sort(ids)
		return ids
	}
	if got := rulesFor("fake-api"); !slices.Equal(got, []string{"fake-api-cert-expiry→fake-channel", "fake-api-unreachable→fake-channel"}) {
		t.Fatalf("rules=%v", got)
	}
	if !slices.ContainsFunc(snapshot.Rules, func(rule config.Rule) bool { return rule.ID == "down" }) || len(snapshot.Notifiers) != 1 {
		t.Fatal("existing rules or channels were not preserved")
	}
	// The same ID with a different shape updates in place and drops rules that no longer apply.
	f.request(t, "POST", "/api/v1/monitors", "config", `{"name":"Fake API","id":"fake-api","port":9000}`, 200)
	if got := rulesFor("fake-api"); !slices.Equal(got, []string{"fake-api-unreachable→fake-channel"}) {
		t.Fatalf("updated rules=%v", got)
	}
	var monitors []api.Monitor
	if err := json.Unmarshal(f.request(t, "GET", "/api/v1/monitors", "read", "", 200).Body.Bytes(), &monitors); err != nil || len(monitors) != 2 {
		t.Fatalf("monitors=%+v %v", monitors, err)
	}

	for _, template := range blueprint.Templates() {
		body := map[string]string{"template": template.ID}
		if template.NeedsTarget {
			body["target"], body["id"] = "web", "web-"+template.ID
		}
		data, _ := json.Marshal(body)
		var page api.DashboardResult
		if err := json.Unmarshal(f.request(t, "POST", "/api/v1/dashboards", "config", string(data), 200).Body.Bytes(), &page); err != nil || page.URL == "" {
			t.Fatalf("%s: %+v %v", template.ID, page, err)
		}
		var definition api.DashboardDefinition
		if err := json.Unmarshal(f.request(t, "GET", "/api/v1/dashboards/"+page.ID, "read", "", 200).Body.Bytes(), &definition); err != nil || definition.Theme != template.Theme {
			t.Fatalf("%s definition=%+v %v", template.ID, definition, err)
		}
		for _, panel := range definition.Panels {
			f.request(t, "GET", "/api/v1/dashboards/"+page.ID+"/panels/"+panel.ID+"/data?range=6h&collector=fake-host", "read", "", 200)
		}
	}
	f.request(t, "POST", "/api/v1/dashboards", "config", `{"template":"service-health"}`, 400)
	f.request(t, "POST", "/api/v1/dashboards", "config", `{"template":"service-health","target":"nope"}`, 400)

	bad := f.request(t, "PUT", "/api/v1/dashboards/bad-board", "config", `{"id":"bad-board","title":"错误","panels":[{"id":"trend-card","type":"trend","title":"X","width":6,"query":"SELECT ts, cpu AS amount FROM system_metrics"}]}`, 422)
	if !strings.Contains(bad.Body.String(), "trend-card") || !strings.Contains(bad.Body.String(), "value") {
		t.Fatal("query failure does not name the card and column:", bad.Body.String())
	}
	if _, exists := f.files.MapFS["dashboards/bad-board.yaml"]; exists {
		t.Fatal("a failing dashboard was written")
	}

	f.request(t, "DELETE", "/api/v1/monitors/fake-api", "config", "", 200)
	if got := rulesFor("fake-api"); len(got) != 0 {
		t.Fatalf("rules left behind: %v", got)
	}
	if _, exists := f.files.MapFS["dashboards/fake-api.yaml"]; exists {
		t.Fatal("generated dashboard left behind")
	}
}

func TestAddMonitorCreatesDesktopChannelWhenNoneExists(t *testing.T) {
	f := newAPIFixture(t)
	f.files.MapFS["rules.yaml"] = &fstest.MapFile{Data: []byte("rules: []\n")}
	f.files.MapFS["notifiers.yaml"] = &fstest.MapFile{Data: []byte("notifiers: []\n")}
	if err := f.manager.Reload(); err != nil {
		t.Fatal(err)
	}
	var result api.MonitorResult
	if err := json.Unmarshal(f.request(t, "POST", "/api/v1/monitors", "config", `{"name":"nginx","process":"nginx.exe","dashboard":false}`, 200).Body.Bytes(), &result); err != nil || result.NotifierCreated != "desktop" || result.Dashboard != "" {
		t.Fatalf("result=%+v %v", result, err)
	}
	snapshot := f.manager.Snapshot()
	if len(snapshot.Notifiers) != 1 || snapshot.Notifiers[0].Type != "toast" || len(snapshot.Rules) != 1 || snapshot.Rules[0].Type != "process_absent" {
		t.Fatalf("notifiers=%+v rules=%+v", snapshot.Notifiers, snapshot.Rules)
	}
}

type fakeModel struct {
	mu       sync.Mutex
	requests []map[string]any
	keys     []string
}

func (m *fakeModel) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	var body map[string]any
	data, _ := io.ReadAll(r.Body)
	_ = json.Unmarshal(data, &body)
	m.mu.Lock()
	m.requests = append(m.requests, body)
	m.keys = append(m.keys, r.Header.Get("Authorization"))
	turn := len(m.requests)
	m.mu.Unlock()
	w.Header().Set("Content-Type", "text/event-stream")
	send := func(event string, value string) { fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event, value) }
	send("message_start", `{"type":"message_start","message":{"id":"msg_fake","type":"message","role":"assistant","model":"claude-opus-5-5","content":[],"stop_reason":null,"stop_sequence":null,"usage":{"input_tokens":10,"output_tokens":1}}}`)
	if turn == 1 {
		send("content_block_start", `{"type":"content_block_start","index":0,"content_block":{"type":"tool_use","id":"toolu_fake","name":"list_monitors","input":{}}}`)
		send("content_block_delta", `{"type":"content_block_delta","index":0,"delta":{"type":"input_json_delta","partial_json":"{}"}}`)
		send("content_block_stop", `{"type":"content_block_stop","index":0}`)
		send("message_delta", `{"type":"message_delta","delta":{"stop_reason":"tool_use","stop_sequence":null},"usage":{"output_tokens":5}}`)
	} else {
		send("content_block_start", `{"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`)
		send("content_block_delta", `{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"web 正常"}}`)
		send("content_block_delta", `{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"运行。"}}`)
		send("content_block_stop", `{"type":"content_block_stop","index":0}`)
		send("message_delta", `{"type":"message_delta","delta":{"stop_reason":"end_turn","stop_sequence":null},"usage":{"output_tokens":5}}`)
	}
	send("message_stop", `{"type":"message_stop"}`)
}

func TestAssistantAnswersThroughReadOnlyToolsAndStreams(t *testing.T) {
	f := newAPIFixture(t)
	f.samples(t)
	status := f.request(t, "GET", "/api/v1/assistant", "read", "", 200)
	if !strings.Contains(status.Body.String(), `"configured":false`) || !strings.Contains(status.Body.String(), "assistant-api-key") {
		t.Fatal(status.Body.String())
	}
	model := &fakeModel{}
	upstream := httptest.NewServer(model)
	t.Cleanup(upstream.Close)
	f.vault.values["assistant-api-key"] = []byte("FAKE_MODEL_KEY")
	f.files.MapFS["settings.yaml"] = &fstest.MapFile{Data: []byte("publicBaseUrl: http://panel.example.test\nassistant:\n  model: claude-opus-5-5\n  baseUrl: " + upstream.URL + "\n")}
	if err := f.manager.Reload(); err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest("POST", "http://127.0.0.1:7444/api/v1/assistant/chat", strings.NewReader(`{"messages":[{"role":"user","text":"web 现在怎么样？"}]}`))
	r.RemoteAddr = "192.0.2.5:1234"
	r.Header.Set("Authorization", "Bearer fake-read")
	w := httptest.NewRecorder()
	f.server.ServeHTTP(w, r)
	if w.Code != 200 || !strings.HasPrefix(w.Header().Get("Content-Type"), "text/event-stream") {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	events := map[string][]string{}
	scanner := bufio.NewScanner(strings.NewReader(w.Body.String()))
	name := ""
	for scanner.Scan() {
		line := scanner.Text()
		if value, ok := strings.CutPrefix(line, "event: "); ok {
			name = value
		}
		if value, ok := strings.CutPrefix(line, "data: "); ok {
			events[name] = append(events[name], value)
		}
	}
	text := strings.Join(events["text"], "")
	if !strings.Contains(text, "web 正常") || !strings.Contains(text, "运行。") || len(events["done"]) != 1 || len(events["tool"]) != 2 || !strings.Contains(events["tool"][1], `"state":"done"`) {
		t.Fatalf("events=%v", events)
	}
	if len(model.requests) != 2 || model.keys[0] != "Bearer FAKE_MODEL_KEY" {
		t.Fatalf("requests=%d keys=%v", len(model.requests), model.keys)
	}
	followUp, _ := json.Marshal(model.requests[1]["messages"])
	if !strings.Contains(string(followUp), "tool_result") || !strings.Contains(string(followUp), `\"id\":\"web\"`) {
		t.Fatal("tool result did not carry monitor data:", string(followUp))
	}
	tools, _ := json.Marshal(model.requests[0]["tools"])
	for _, forbidden := range []string{"add_monitor", "put_config", "ack_alert", "restart"} {
		if strings.Contains(string(tools), forbidden) {
			t.Fatalf("assistant was offered a writing tool %s", forbidden)
		}
	}
	if strings.Contains(f.logs.String(), "FAKE_MODEL_KEY") {
		t.Fatal("model key leaked into logs")
	}
	f.request(t, "POST", "/api/v1/assistant/chat", "read", `{"messages":[{"role":"assistant","text":"只有助手"}]}`, 400)
}
