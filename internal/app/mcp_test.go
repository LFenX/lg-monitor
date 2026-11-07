package app

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/LFenX/lg-monitor/internal/api"
	"github.com/LFenX/lg-monitor/internal/collector"
	"github.com/LFenX/lg-monitor/internal/config"
	"github.com/LFenX/lg-monitor/internal/rules"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type mcpTestTransport struct {
	token  string
	status *atomic.Int32
}

func (transport mcpTestTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	request = request.Clone(request.Context())
	request.Header.Set("Authorization", "Bearer "+transport.token)
	response, err := http.DefaultTransport.RoundTrip(request)
	if response != nil && transport.status != nil {
		transport.status.Store(int32(response.StatusCode))
	}
	return response, err
}

func mcpSession(t *testing.T, address, token string, proxy bool, statuses ...*atomic.Int32) *mcp.ClientSession {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	t.Cleanup(cancel)
	client := mcp.NewClient(&mcp.Implementation{Name: "lgmon-test", Version: "fake"}, nil)
	var status *atomic.Int32
	if len(statuses) > 0 {
		status = statuses[0]
	}
	var transport mcp.Transport = &mcp.StreamableClientTransport{Endpoint: address + "/mcp", HTTPClient: &http.Client{Transport: mcpTestTransport{token: token, status: status}}, DisableStandaloneSSE: true, MaxRetries: -1}
	if proxy {
		caller, err := api.NewMCPHTTPClient(api.MCPHTTPClientOptions{BaseURL: address, Token: token})
		if err != nil {
			t.Fatal(err)
		}
		server, err := api.NewMCPServer(api.MCPOptions{Caller: caller})
		if err != nil {
			t.Fatal(err)
		}
		serverTransport, clientTransport := mcp.NewInMemoryTransports()
		session, err := server.Connect(ctx, serverTransport, nil)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { session.Close() })
		transport = clientTransport
	}
	session, err := client.Connect(ctx, transport, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { session.Close() })
	return session
}

func mcpCall(t *testing.T, session *mcp.ClientSession, name string, arguments any, wantError bool) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	result, err := session.CallTool(ctx, &mcp.CallToolParams{Name: name, Arguments: arguments})
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	data, err := json.Marshal(result.StructuredContent)
	if err != nil || result.IsError != wantError || string(data) == "null" || len(result.Content) == 0 {
		t.Fatalf("%s: error=%t want=%t data=%s marshal=%v content=%+v", name, result.IsError, wantError, data, err, result.Content)
	}
	return string(data)
}

func TestMCPAllDefinedToolsUseRealHTTPBackend(t *testing.T) {
	for _, proxy := range []bool{false, true} {
		name := "streamable"
		if proxy {
			name = "stdio-http-proxy"
		}
		t.Run(name, func(t *testing.T) {
			now := time.UnixMilli(2000000000000)
			processes, _ := processLink(t, now)
			f := newAPIFixtureWithProcessSource(t, now, nil, processes)
			f.samples(t)
			loadAPIDashboard(t, f, apiDashboardFixture)
			alert := rules.Alert{ID: "fake-alert", RuleID: "down", CollectorID: "fake-host", TargetID: "web", State: rules.StateFiring, Severity: "critical", StartedAt: f.now.Add(-time.Minute).UnixMilli(), Summary: "虚构故障"}
			if err := f.db.SaveTransition(context.Background(), alert, rules.Event{AlertID: alert.ID, TS: alert.StartedAt, Kind: rules.StateFiring, Message: alert.Summary}); err != nil {
				t.Fatal(err)
			}
			httpServer := httptest.NewServer(f.server)
			t.Cleanup(httpServer.Close)
			session := mcpSession(t, httpServer.URL, "fake-admin", proxy)
			tools, err := session.ListTools(context.Background(), nil)
			if err != nil || len(tools.Tools) != 25 {
				t.Fatalf("tools=%+v err=%v", tools, err)
			}
			listed := map[string]bool{}
			for _, tool := range tools.Tools {
				if tool.Description == "" || tool.InputSchema == nil || strings.Contains(tool.Name, "secret") || strings.Contains(tool.Name, "credentials") {
					t.Fatalf("invalid or secret-writing tool: %+v", tool)
				}
				listed[tool.Name] = true
			}
			called := map[string]bool{}
			call := func(name string, arguments any) string {
				called[name] = true
				return mcpCall(t, session, name, arguments, false)
			}
			if !strings.Contains(call("list_collectors", map[string]any{}), "fake-host") || !strings.Contains(call("list_targets", map[string]any{}), "web") {
				t.Fatal("collector or target missing")
			}
			call("get_target_history", map[string]any{"collector": "fake-host", "target": "web", "from": f.now.Add(-time.Minute).UnixMilli(), "to": f.now.UnixMilli(), "step": "raw"})
			processList := call("list_processes", map[string]any{"id": "fake-host", "name": "PYT"})
			var snapshot collector.ProcessList
			if err := json.Unmarshal([]byte(processList), &snapshot); err != nil || len(snapshot.Processes) != 2 || len(snapshot.Processes[0].Ports) != 1 || len(snapshot.Processes[0].Services) != 1 || strings.Contains(processList, "s3cret") || strings.Contains(processList, "cmdline") {
				t.Fatalf("process list=%s %v", processList, err)
			}
			f.now = f.now.Add(2 * time.Second)
			matched := call("test_target_match", map[string]any{"id": "fake-host", "match": map[string]any{"process": "python", "cmdline": "blog.py"}})
			if err := json.Unmarshal([]byte(matched), &snapshot); err != nil || len(snapshot.Processes) != 1 || snapshot.Processes[0].PID != 1234 || strings.Contains(matched, "s3cret") || strings.Contains(matched, "cmdline") {
				t.Fatalf("match=%s %v", matched, err)
			}
			call("validate_config", map[string]any{"files": map[string]any{"card-templates/not-present.yaml": nil}})
			query := call("query_sql", map[string]any{"sql": "SELECT :from AS start, :to AS finish, :collector AS host", "params": map[string]any{"range": "6h", "collector": "fake-host"}})
			var data api.QueryResult
			if err := json.Unmarshal([]byte(query), &data); err != nil || data.Params.To-data.Params.From != (6*time.Hour).Milliseconds() || data.Rows[0][2] != "fake-host" {
				t.Fatalf("query params=%s %v", query, err)
			}
			if !strings.Contains(call("get_schema", map[string]any{}), "v_target_current") {
				t.Fatal("database schema omitted")
			}
			call("list_alerts", map[string]any{"state": "all", "since": f.now.Add(-time.Hour).UnixMilli()})
			call("ack_alert", map[string]any{"id": "fake-alert"})
			call("silence", map[string]any{"rule": "down", "for": "1h", "reason": "虚构维护"})
			for _, kind := range []string{"targets", "rules", "notifiers", "settings"} {
				document := call("get_config", map[string]any{"kind": kind})
				var value any
				if err := json.Unmarshal([]byte(document), &value); err != nil {
					t.Fatal(err)
				}
				call("put_config", map[string]any{"kind": kind, "config": value})
			}
			call("test_rule", map[string]any{"id": "down", "from": f.now.Add(-time.Minute).UnixMilli(), "to": f.now.UnixMilli()})
			call("feishu_check", map[string]any{"app": "fake-app"})
			call("feishu_list_chats", map[string]any{"app": "fake-app"})
			call("list_templates", map[string]any{})
			template := config.Template{ID: "mcp-card", Extends: "compact", Title: "MCP 虚构模板"}
			call("preview_template", map[string]any{"id": "draft", "template": template, "state": "resolved"})
			call("put_template", map[string]any{"id": template.ID, "template": template})
			call("test_notifier", map[string]any{"id": "fake-channel", "draft": template})
			f.now = f.now.Add(11 * time.Second)
			call("feishu_test_chat", map[string]any{"app": "fake-app", "chatId": "oc_fake_api", "draft": template, "state": "resolved"})
			call("feishu_save_chat", map[string]any{"app": "fake-app", "chatId": "oc_fake_api", "template": "compact", "rules": []string{"down"}})
			call("list_dashboards", map[string]any{})
			call("get_dashboard", map[string]any{"id": "overview"})
			panel := call("get_panel_data", map[string]any{"id": "overview", "panel": "healthy", "range": "1h", "collector": "fake-host"})
			if !strings.Contains(panel, `"version"`) || !strings.Contains(panel, `"value"`) {
				t.Fatal(panel)
			}
			for name := range listed {
				if !called[name] {
					t.Errorf("listed tool %s was not called", name)
				}
			}
			alerts, err := f.view.Alerts(context.Background(), false)
			if err != nil || len(alerts) != 1 || alerts[0].AcknowledgedAt == 0 || len(f.client.sent) != 2 || f.manager.Snapshot().Notifiers[0].Template != "compact" {
				t.Fatalf("side effects missing: alerts=%+v sent=%d %v", alerts, len(f.client.sent), err)
			}
			if strings.Contains(f.logs.String(), "FAKE_PRIVATE_SECRET") || strings.Contains(f.logs.String(), "Bearer fake-admin") {
				t.Fatal("credentials leaked into logs")
			}
		})
	}
}

func TestMCPScopesValidationAndSharedNotificationLimit(t *testing.T) {
	f := newAPIFixture(t)
	loadAPIDashboard(t, f, apiDashboardFixture)
	httpServer := httptest.NewServer(f.server)
	t.Cleanup(httpServer.Close)
	read := mcpSession(t, httpServer.URL, "fake-read", false)
	editor := mcpSession(t, httpServer.URL, "fake-config", false)
	for _, input := range []struct {
		name string
		args any
	}{
		{"ack_alert", map[string]any{"id": "fake-alert"}},
		{"silence", map[string]any{"for": "1h", "reason": "fake"}},
		{"put_config", map[string]any{"kind": "targets", "config": map[string]any{"targets": []any{}}}},
		{"test_notifier", map[string]any{"id": "fake-channel"}},
		{"feishu_check", map[string]any{"app": "fake-app"}},
		{"test_target_match", map[string]any{"id": "fake-host", "match": map[string]any{"process": "python", "cmdline": "blog.py"}}},
	} {
		if !strings.Contains(mcpCall(t, read, input.name, input.args, true), "insufficient_scope") {
			t.Fatalf("%s bypassed scope", input.name)
		}
	}
	if !strings.Contains(mcpCall(t, editor, "put_config", map[string]any{"kind": "settings", "config": map[string]any{}}, true), "insufficient_scope") {
		t.Fatal("config token wrote admin settings")
	}
	for _, input := range []struct {
		name string
		args any
	}{
		{"get_config", map[string]any{"kind": "../settings"}},
		{"get_dashboard", map[string]any{"id": "../overview"}},
		{"query_sql", map[string]any{"sql": 7}},
		{"list_targets", map[string]any{"token": "FAKE_PRIVATE_SECRET"}},
		{"put_config", map[string]any{"kind": "notifiers", "config": map[string]any{"secret": "FAKE_PRIVATE_SECRET"}}},
	} {
		result := mcpCall(t, editor, input.name, input.args, true)
		if !strings.Contains(result, "invalid_arguments") || strings.Contains(result, "FAKE_PRIVATE_SECRET") {
			t.Fatal(result)
		}
	}
	if !strings.Contains(mcpCall(t, read, "query_sql", map[string]any{"sql": "DELETE FROM collectors"}, true), "invalid_query") {
		t.Fatal("MCP SQL was writable")
	}
	if !strings.Contains(mcpCall(t, read, "get_panel_data", map[string]any{"id": "overview", "panel": "columns"}, true), "panel_columns") {
		t.Fatal("panel contract error was hidden")
	}
	f.request(t, "POST", "/api/v1/notifiers/fake-channel/test", "config", "{}", 200)
	limited := mcpCall(t, editor, "feishu_test_chat", map[string]any{"app": "fake-app", "chatId": "oc_fake_api"}, true)
	if !strings.Contains(limited, "notification_rate_limited") || len(f.client.sent) != 1 {
		t.Fatal("MCP bypassed shared HTTP notification limit")
	}
	if f.files.writes != 0 {
		t.Fatal("invalid or unauthorized tools modified config")
	}
}

func TestMCPMissingCredentialsDirectUserToWebOrCLI(t *testing.T) {
	f := newAPIFixture(t)
	delete(f.vault.values, "fake-ref")
	server := httptest.NewServer(f.server)
	t.Cleanup(server.Close)
	session := mcpSession(t, server.URL, "fake-config", false)
	for _, test := range []struct {
		name   string
		args   any
		failed bool
	}{
		{"feishu_check", map[string]any{"app": "fake-app"}, true},
		{"test_notifier", map[string]any{"id": "fake-channel"}, false},
	} {
		result := mcpCall(t, session, test.name, test.args, test.failed)
		if !strings.Contains(result, "lgmon feishu setup") || !strings.Contains(result, "网页") || strings.Contains(result, "FAKE_PRIVATE_SECRET") {
			t.Fatal("missing credentials did not direct the user to a protected input")
		}
	}
	if len(f.client.sent) != 0 || f.vault.setCalls != 0 || f.files.writes != 0 {
		t.Fatal("MCP attempted to write credentials or send without credentials")
	}
}
