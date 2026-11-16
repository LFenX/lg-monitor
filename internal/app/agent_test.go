package app

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"
	"testing/fstest"
	"time"
	"unicode/utf8"

	"github.com/LFenX/lg-monitor/docs"
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
	if err := json.Unmarshal([]byte(call("add_monitor", map[string]any{"name": "虚构博客", "port": 8080, "collector": "fake-host"})), &added); err != nil || added.Monitor.ID != "port-8080" || added.Board != "main" || !added.BoardCreated || !strings.HasSuffix(added.MonitorURL, "/#/monitors/port-8080") {
		t.Fatalf("added=%+v %v", added, err)
	}
	if !strings.Contains(call("list_monitors", map[string]any{}), "port-8080") {
		t.Fatal("new monitor missing")
	}
	if detail := call("get_monitor", map[string]any{"id": "port-8080"}); !strings.Contains(detail, `"slots":[{`) || !strings.Contains(detail, `"days":[{`) {
		t.Fatalf("get_monitor=%s", detail)
	}
	if !strings.Contains(call("list_dashboard_templates", map[string]any{}), "services") {
		t.Fatal("templates missing")
	}
	call("create_dashboard", map[string]any{"template": "system", "theme": "terminal"})
	call("put_dashboard", map[string]any{"id": "mcp-board", "title": "MCP 面板", "panels": []map[string]any{{"id": "cpu", "type": "gauge", "title": "CPU", "width": 6, "unit": "percent", "query": "SELECT cpu AS value FROM system_metrics ORDER BY ts DESC LIMIT 1"}}})
	call("delete_dashboard", map[string]any{"id": "mcp-board"})
	if boards := call("list_boards", map[string]any{}); !strings.Contains(boards, `"id":"main"`) {
		t.Fatalf("boards=%s", boards)
	}
	if created := call("create_board", map[string]any{"name": "MCP 面板", "id": "mcp-view"}); !strings.Contains(created, `"id":"mcp-view"`) {
		t.Fatalf("create_board=%s", created)
	}
	if updated := call("update_board", map[string]any{"id": "mcp-view", "add": []string{"port-8080"}}); !strings.Contains(updated, `"monitors":["port-8080"]`) {
		t.Fatalf("update_board=%s", updated)
	}
	if view := call("get_board", map[string]any{"id": "mcp-view"}); !strings.Contains(view, `"items":[{`) {
		t.Fatalf("get_board=%s", view)
	}
	call("delete_board", map[string]any{"id": "mcp-view"})
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
	// start_here hands the agent the whole module map, not just the first steps.
	if len(guide.Modules) != len(api.MCPModules) || !strings.Contains(strings.Join(guide.Workflow, ""), "问用户") {
		t.Fatalf("guide modules=%d workflow=%v", len(guide.Modules), guide.Workflow)
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
	if err := json.Unmarshal(response.Body.Bytes(), &created); err != nil || !created.Created || created.Board != "main" || created.NotifierCreated != "" {
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
		if template.Targeted {
			// One targeted copy proves the filter; the plain one below covers the whole category.
			filtered, _ := json.Marshal(map[string]string{"template": template.ID, "target": "web"})
			f.request(t, "POST", "/api/v1/dashboards", "config", string(filtered), 200)
		}
		data, _ := json.Marshal(body)
		var page api.DashboardResult
		if err := json.Unmarshal(f.request(t, "POST", "/api/v1/dashboards", "config", string(data), 200).Body.Bytes(), &page); err != nil || page.URL == "" {
			t.Fatalf("%s: %+v %v", template.ID, page, err)
		}
		var definition api.DashboardDefinition
		if err := json.Unmarshal(f.request(t, "GET", "/api/v1/dashboards/"+page.ID, "read", "", 200).Body.Bytes(), &definition); err != nil || definition.Theme != "" {
			t.Fatalf("%s definition=%+v %v", template.ID, definition, err)
		}
		for _, panel := range definition.Panels {
			f.request(t, "GET", "/api/v1/dashboards/"+page.ID+"/panels/"+panel.ID+"/data?range=6h&collector=fake-host", "read", "", 200)
		}
	}
	f.request(t, "POST", "/api/v1/dashboards", "config", `{"template":"system","target":"web"}`, 400)
	f.request(t, "POST", "/api/v1/dashboards", "config", `{"template":"services","target":"nope"}`, 400)

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
	if _, exists := f.files.MapFS["dashboards/websites.yaml"]; !exists {
		t.Fatal("the shared websites board must outlive one monitor")
	}

	// A page narrowed to one monitor would only show empty cards once the monitor is gone.
	if _, exists := f.files.MapFS["dashboards/services-web.yaml"]; !exists {
		t.Fatal("the targeted services board was not written")
	}
	removed := f.request(t, "DELETE", "/api/v1/monitors/web", "config", "", 200)
	if !strings.Contains(removed.Body.String(), "2 页面板") {
		t.Fatal("removal does not mention the narrowed boards:", removed.Body.String())
	}
	for _, page := range []string{"services-web", "websites-web"} {
		if _, exists := f.files.MapFS["dashboards/"+page+".yaml"]; exists {
			t.Fatal("a board narrowed to a removed monitor was left behind:", page)
		}
	}
	if _, exists := f.files.MapFS["dashboards/services.yaml"]; !exists {
		t.Fatal("the shared services board must outlive one monitor")
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
	if err := json.Unmarshal(f.request(t, "POST", "/api/v1/monitors", "config", `{"name":"nginx","process":"nginx.exe","dashboard":false}`, 200).Body.Bytes(), &result); err != nil || result.NotifierCreated != "desktop" || result.Board != "main" {
		t.Fatalf("result=%+v %v", result, err)
	}
	snapshot := f.manager.Snapshot()
	if len(snapshot.Notifiers) != 1 || snapshot.Notifiers[0].Type != "toast" || len(snapshot.Rules) != 1 || snapshot.Rules[0].Type != "process_absent" {
		t.Fatalf("notifiers=%+v rules=%+v", snapshot.Notifiers, snapshot.Rules)
	}
}

type fakeDeepSeek struct {
	mu     sync.Mutex
	bodies []map[string]any
	keys   []string
	models []string
}

// ServeHTTP plays DeepSeek: a model list, then a conversation that first looks something up and then answers.
func (m *fakeDeepSeek) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	m.mu.Lock()
	m.keys = append(m.keys, r.Header.Get("Authorization"))
	m.mu.Unlock()
	if r.Header.Get("Authorization") != "Bearer FAKE_DEEPSEEK_KEY" {
		http.Error(w, `{"error":{"message":"invalid key"}}`, 401)
		return
	}
	if r.URL.Path == "/models" {
		list := []map[string]string{}
		for _, id := range m.models {
			list = append(list, map[string]string{"id": id})
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"data": list})
		return
	}
	var body map[string]any
	data, _ := io.ReadAll(r.Body)
	_ = json.Unmarshal(data, &body)
	m.mu.Lock()
	m.bodies = append(m.bodies, body)
	m.mu.Unlock()
	encoded, _ := json.Marshal(body["messages"])
	w.Header().Set("Content-Type", "text/event-stream")
	send := func(value string) { fmt.Fprintf(w, "data: %s\n\n", value) }
	if !strings.Contains(string(encoded), `"role":"tool"`) {
		// Arguments arrive in pieces, as DeepSeek streams them.
		send(`{"choices":[{"delta":{"tool_calls":[{"index":0,"id":"call_status","function":{"name":"list_monitors","arguments":"{}"}},{"index":1,"id":"call_help","function":{"name":"search_help","arguments":"{\"q\":"}}]}}]}`)
		send(`{"choices":[{"delta":{"tool_calls":[{"index":1,"function":{"arguments":"\"卡片样式\"}"}}]},"finish_reason":"tool_calls"}]}`)
	} else {
		send(`{"choices":[{"delta":{"content":"web 正常运行。"},"finish_reason":null}]}`)
		send(`{"choices":[{"delta":{"content":"卡片有六种样式。"},"finish_reason":"stop"}]}`)
	}
	fmt.Fprint(w, "data: [DONE]\n\n")
}

type fakeDashScope struct {
	mu       sync.Mutex
	embedded int
	reranks  int
	broken   bool
}

// ServeHTTP plays 百炼: vectors that point at a passage by its words, and a reranker that prefers card styles.
func (m *fakeDashScope) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Header.Get("Authorization") != "Bearer FAKE_DASHSCOPE_KEY" {
		http.Error(w, `{"code":"InvalidApiKey"}`, 401)
		return
	}
	m.mu.Lock()
	broken := m.broken
	m.mu.Unlock()
	if broken {
		http.Error(w, `{"code":"Throttling"}`, 429)
		return
	}
	var body map[string]any
	data, _ := io.ReadAll(r.Body)
	_ = json.Unmarshal(data, &body)
	vector := func(text string) []float64 {
		return []float64{float64(strings.Count(text, "卡片样式")), float64(strings.Count(text, "内网")), float64(strings.Count(text, "配对码")), 0.5}
	}
	switch r.URL.Path {
	case "/compatible-mode/v1/embeddings":
		inputs, _ := body["input"].([]any)
		m.mu.Lock()
		m.embedded += len(inputs)
		m.mu.Unlock()
		items := []map[string]any{}
		for index, input := range inputs {
			text, _ := input.(string)
			items = append(items, map[string]any{"index": index, "embedding": vector(text)})
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"data": items})
	case "/api/v1/services/rerank/text-rerank/text-rerank":
		input, _ := body["input"].(map[string]any)
		documents, _ := input["documents"].([]any)
		m.mu.Lock()
		m.reranks++
		m.mu.Unlock()
		results := []map[string]any{}
		for index, document := range documents {
			text, _ := document.(string)
			results = append(results, map[string]any{"index": index, "relevance_score": 0.05 + float64(strings.Count(text, "卡片样式"))/10})
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"output": map[string]any{"results": results}})
	default:
		http.NotFound(w, r)
	}
}

func readEvents(t *testing.T, body string) map[string][]string {
	t.Helper()
	events := map[string][]string{}
	scanner := bufio.NewScanner(strings.NewReader(body))
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
	return events
}

func TestAssistantConfiguresDeepSeekAndBailianAndAnswersFromDataAndGuides(t *testing.T) {
	f := newAPIFixture(t)
	f.samples(t)
	chat := &fakeDeepSeek{models: []string{"deepseek-chat", "deepseek-reasoner"}}
	deepseek := httptest.NewServer(chat)
	t.Cleanup(deepseek.Close)
	search := &fakeDashScope{}
	dashscope := httptest.NewServer(search)
	t.Cleanup(dashscope.Close)
	f.backend.options.DeepSeekURL, f.backend.options.DashScopeURL = deepseek.URL, dashscope.URL

	var status api.AssistantStatus
	if err := json.Unmarshal(f.request(t, "GET", "/api/v1/assistant", "read", "", 200).Body.Bytes(), &status); err != nil || status.Configured || status.DeepSeekKey || status.DashScopeKey || !strings.Contains(status.Problem, "DeepSeek") || !strings.Contains(status.Setup, "assistant set") {
		t.Fatalf("first status=%+v %v", status, err)
	}
	// Asking before both keys exist explains what is missing.
	ask := func(question string) *httptest.ResponseRecorder {
		r := httptest.NewRequest("POST", "http://127.0.0.1:7444/api/v1/assistant/chat", strings.NewReader(`{"messages":[{"role":"user","text":"`+question+`"}]}`))
		r.RemoteAddr = "192.0.2.5:1234"
		r.Header.Set("Authorization", "Bearer fake-read")
		w := httptest.NewRecorder()
		f.server.ServeHTTP(w, r)
		return w
	}
	if refused := ask("hi"); !strings.Contains(refused.Body.String(), "event: error") || !strings.Contains(refused.Body.String(), "DeepSeek") {
		t.Fatal(refused.Body.String())
	}
	// Keys are write-only and need config, which the person's own page and an agent's token both have.
	f.request(t, "PUT", "/api/v1/assistant", "read", `{"deepseekKey":"FAKE_DEEPSEEK_KEY"}`, 403)
	// A rejected key, a model the account lacks or a broken 百炼 key saves nothing.
	if bad := f.request(t, "PUT", "/api/v1/assistant", "config", `{"deepseekKey":"FAKE_WRONG"}`, 400); !strings.Contains(bad.Body.String(), "没有保存") {
		t.Fatal(bad.Body.String())
	}
	if bad := f.request(t, "PUT", "/api/v1/assistant", "config", `{"deepseekKey":"FAKE_DEEPSEEK_KEY","model":"deepseek-moon"}`, 400); !strings.Contains(bad.Body.String(), "deepseek-chat") {
		t.Fatal(bad.Body.String())
	}
	if bad := f.request(t, "PUT", "/api/v1/assistant", "config", `{"dashscopeKey":"FAKE_WRONG"}`, 400); !strings.Contains(bad.Body.String(), "百炼") {
		t.Fatal(bad.Body.String())
	}
	if f.vault.values[config.DeepSeekKeyRef] != nil || f.vault.values[config.DashScopeKeyRef] != nil {
		t.Fatal("a failed check saved a key")
	}
	if err := json.Unmarshal(f.request(t, "PUT", "/api/v1/assistant", "config", `{"deepseekKey":"FAKE_DEEPSEEK_KEY","model":"deepseek-reasoner","dashscopeKey":"FAKE_DASHSCOPE_KEY","region":"singapore"}`, 200).Body.Bytes(), &status); err != nil || !status.Configured || status.Model != "deepseek-reasoner" || status.Region != "singapore" || len(status.Models) != 2 {
		t.Fatalf("configured status=%+v %v", status, err)
	}
	saved := string(f.files.MapFS["settings.yaml"].Data)
	if string(f.vault.values[config.DeepSeekKeyRef]) != "FAKE_DEEPSEEK_KEY" || string(f.vault.values[config.DashScopeKeyRef]) != "FAKE_DASHSCOPE_KEY" || !strings.Contains(saved, "model: deepseek-reasoner") || !strings.Contains(saved, "region: singapore") {
		t.Fatalf("saved settings=%s vault=%v", saved, f.vault.values)
	}
	for _, leak := range []string{saved, f.logs.String(), f.request(t, "GET", "/api/v1/assistant", "read", "", 200).Body.String()} {
		if strings.Contains(leak, "FAKE_DEEPSEEK_KEY") || strings.Contains(leak, "FAKE_DASHSCOPE_KEY") {
			t.Fatal("an API key left the secret store")
		}
	}

	w := ask("web 现在怎么样？卡片样式有哪些？")
	if w.Code != 200 || !strings.HasPrefix(w.Header().Get("Content-Type"), "text/event-stream") {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	events := readEvents(t, w.Body.String())
	text := strings.Join(events["text"], "")
	if !strings.Contains(text, "web 正常运行") || !strings.Contains(text, "卡片有六种样式") || len(events["done"]) != 1 {
		t.Fatalf("events=%v", events)
	}
	tools := strings.Join(events["tool"], "\n")
	if !strings.Contains(tools, `"name":"search_help"`) || !strings.Contains(tools, "查 lgmon 的说明") || strings.Count(tools, `"state":"done"`) != 2 {
		t.Fatalf("tool events=%s", tools)
	}
	chat.mu.Lock()
	first, _ := json.Marshal(chat.bodies[0])
	followUp, _ := json.Marshal(chat.bodies[1]["messages"])
	chat.mu.Unlock()
	if !strings.Contains(string(first), `"model":"deepseek-reasoner"`) || !strings.Contains(string(first), "波西米亚") {
		t.Fatal("conversation did not use the chosen model and persona:", string(first))
	}
	for _, forbidden := range []string{"add_monitor", "put_config", "ack_alert", "restart"} {
		if strings.Contains(string(first), `"name":"`+forbidden+`"`) {
			t.Fatalf("assistant was offered a writing tool %s", forbidden)
		}
	}
	// The guides were embedded, the closest passages reranked, and the answer is grounded in the card-style guide.
	if !strings.Contains(string(followUp), `\"id\":\"web\"`) || !strings.Contains(string(followUp), "卡片样式") || !strings.Contains(string(followUp), "网页使用说明") {
		t.Fatal("tool results did not carry data and guide passages:", string(followUp))
	}
	search.mu.Lock()
	embedded, reranks := search.embedded, search.reranks
	search.mu.Unlock()
	if embedded < 50 || reranks < 2 {
		t.Fatalf("embedded=%d reranks=%d", embedded, reranks)
	}
	// A second question reuses the index and only embeds the question.
	hits, note, err := f.backend.searchHelp(context.Background(), f.manager.Snapshot().Settings.Assistant, "内网共享怎么申请")
	if err != nil || note != "" || len(hits) == 0 {
		t.Fatalf("hits=%v note=%q err=%v", hits, note, err)
	}
	search.mu.Lock()
	again := search.embedded - embedded
	search.broken = true
	search.mu.Unlock()
	if again != 1 {
		t.Fatalf("the index was rebuilt: %d new embeddings", again)
	}
	// When 百炼 is unavailable the guides are still searched by keywords, and the note says so.
	hits, note, err = f.backend.searchHelp(context.Background(), f.manager.Snapshot().Settings.Assistant, "内网共享 配对码")
	if err != nil || !strings.Contains(note, "关键词") || len(hits) == 0 || !strings.Contains(hits[0].Text+hits[0].Section, "配对码") {
		t.Fatalf("fallback hits=%v note=%q err=%v", hits, note, err)
	}
	f.request(t, "POST", "/api/v1/assistant/chat", "read", `{"messages":[{"role":"assistant","text":"只有助手"}]}`, 400)
}

func TestHelpChunksCoverTheGuidesBySection(t *testing.T) {
	chunks, err := helpChunks()
	if err != nil {
		t.Fatal(err)
	}
	documents := map[string]bool{}
	for _, chunk := range chunks {
		documents[chunk.Doc] = true
		if utf8.RuneCountInString(chunk.Text) > helpChunkRunes+10 || strings.TrimSpace(chunk.Text) == "" {
			t.Fatalf("chunk size %d in %s / %s", utf8.RuneCountInString(chunk.Text), chunk.Doc, chunk.Title)
		}
	}
	for _, name := range []string{"网页使用说明", "内网共享", "Agent 指南", "MCP 工具", "告警规则"} {
		if !documents[name] {
			t.Fatalf("guide %s missing from %v", name, documents)
		}
	}
	if len(chunks) < 100 {
		t.Fatalf("only %d passages", len(chunks))
	}
}

func TestGuidesChunkTheSameFromAWindowsCheckout(t *testing.T) {
	data, err := fs.ReadFile(docs.Guides, "web-panel.md")
	if err != nil {
		t.Fatal(err)
	}
	lf := strings.ReplaceAll(string(data), "\r\n", "\n")
	want := chunkGuide("网页使用说明", []byte(lf))
	got := chunkGuide("网页使用说明", []byte(strings.ReplaceAll(lf, "\n", "\r\n")))
	if len(want) < 2 || !slices.Equal(got, want) {
		t.Fatalf("CRLF guide gave %d passages, LF gave %d", len(got), len(want))
	}
}
