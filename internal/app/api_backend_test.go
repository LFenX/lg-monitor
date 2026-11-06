package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/LFenX/lg-monitor/internal/api"
	"github.com/LFenX/lg-monitor/internal/collector"
	"github.com/LFenX/lg-monitor/internal/config"
	"github.com/LFenX/lg-monitor/internal/feishu"
	"github.com/LFenX/lg-monitor/internal/feishu/setup"
	"github.com/LFenX/lg-monitor/internal/pipeline"
	"github.com/LFenX/lg-monitor/internal/rules"
	"github.com/LFenX/lg-monitor/internal/store"
	validator "github.com/santhosh-tekuri/jsonschema/v6"
)

type apiDocuments struct {
	fstest.MapFS
	writes  int
	failure error
}

func (f *apiDocuments) Replace(_ context.Context, name string, data []byte) error {
	if f.failure != nil {
		return f.failure
	}
	f.writes++
	f.MapFS[name] = &fstest.MapFile{Data: bytes.Clone(data)}
	return nil
}
func (f *apiDocuments) Remove(_ context.Context, name string) error {
	if _, ok := f.MapFS[name]; !ok {
		return fs.ErrNotExist
	}
	delete(f.MapFS, name)
	return nil
}

type apiVault struct {
	values   map[string][]byte
	lastRead []byte
}

func (v *apiVault) Get(_ context.Context, name string) ([]byte, error) {
	value, ok := v.values[name]
	if !ok {
		return nil, fs.ErrNotExist
	}
	v.lastRead = bytes.Clone(value)
	return v.lastRead, nil
}
func (v *apiVault) Set(_ context.Context, name string, value []byte) error {
	v.values[name] = bytes.Clone(value)
	return nil
}

type apiClient struct {
	feishu.Client
	sent                  []feishu.CardMessage
	err, errorCredentials error
	closed                int
}

func (c *apiClient) TenantToken(context.Context) (string, error) {
	return "fake-tenant-token", c.errorCredentials
}
func (c *apiClient) BotInfo(context.Context) (feishu.Bot, error) {
	return feishu.Bot{Name: "虚构机器人", ActivateStatus: 2}, nil
}
func (c *apiClient) AppScopes(context.Context) ([]feishu.Scope, error) {
	return []feishu.Scope{{Name: "im:message:send_as_bot", Granted: true}, {Name: "im:chat:read", Granted: true}}, nil
}
func (c *apiClient) ListChats(context.Context) ([]feishu.Chat, error) {
	return []feishu.Chat{{ID: "oc_fake_api", Name: "虚构测试群"}}, nil
}
func (c *apiClient) ChatInfo(context.Context, string) (feishu.Chat, error) {
	return feishu.Chat{ID: "oc_fake_api", Name: "虚构测试群"}, nil
}
func (c *apiClient) SendCard(_ context.Context, message feishu.CardMessage) (feishu.SendResult, error) {
	c.sent = append(c.sent, message)
	return feishu.SendResult{MessageID: "om_fake_PRIVATE_RESPONSE"}, c.err
}
func (c *apiClient) Close() error { c.closed++; return nil }

type apiFixture struct {
	server  *api.Server
	manager *config.Manager
	files   *apiDocuments
	db      *store.Store
	view    *store.View
	vault   *apiVault
	client  *apiClient
	now     time.Time
	logs    bytes.Buffer
	spec    map[string]any
}

func newAPIFixture(t *testing.T) *apiFixture {
	t.Helper()
	f := &apiFixture{now: time.UnixMilli(2000000000000), client: &apiClient{}, vault: &apiVault{values: map[string][]byte{"fake-ref": []byte("FAKE_PRIVATE_SECRET")}}}
	f.files = &apiDocuments{MapFS: fstest.MapFS{
		"targets.yaml":   {Data: []byte("targets: [{id: web, match: {process: fake.exe}}]\n")},
		"rules.yaml":     {Data: []byte("rules: [{id: down, type: process_absent, target: web, for: 0s, severity: critical, notify: [fake-channel]}]\n")},
		"notifiers.yaml": {Data: []byte("# preserve this comment\nfeishuApps: [{id: fake-app, appId: cli_fake_api, secretRef: fake-ref, domain: feishu}]\nnotifiers: [{id: fake-channel, type: feishu_app, app: fake-app, chatId: oc_fake_api}]\n")},
		"settings.yaml":  {Data: []byte("publicBaseUrl: http://panel.example.test\n")},
	}}
	logger := slog.New(slog.NewJSONHandler(&f.logs, nil))
	var err error
	f.manager, err = config.NewManager(config.FSLoader{FS: f.files}, logger)
	if err != nil {
		t.Fatal(err)
	}
	editor, err := config.NewEditor(f.files, f.manager)
	if err != nil {
		t.Fatal(err)
	}
	databasePath := filepath.Join(t.TempDir(), "lgmon.db")
	f.db, err = store.Open(context.Background(), store.Options{Path: databasePath, Now: func() time.Time { return f.now }, DisableMaintenance: true})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { f.db.Close() })
	f.view, err = store.OpenViewWithClock(context.Background(), databasePath, func() time.Time { return f.now })
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { f.view.Close() })
	if err := f.db.RegisterCollector(context.Background(), store.CollectorRecord{ID: "fake-host", Hostname: "虚构主机", Version: "fake-v1", FirstSeen: f.now.UnixMilli(), LastSeen: f.now.UnixMilli()}); err != nil {
		t.Fatal(err)
	}
	backend, err := NewAPIBackend(APIBackendOptions{Config: f.manager, Editor: editor, Read: f.view, Write: f.db, Now: func() time.Time { return f.now }, Secrets: f.vault, Logger: logger, NewID: func() string { return fmt.Sprintf("fake-id-%d", len(f.vault.values)+len(f.client.sent)) }, Collectors: presenceSourceFunc(func(context.Context) ([]rules.CollectorPresence, error) {
		return []rules.CollectorPresence{{ID: "fake-host", Hostname: "虚构主机", LastSeen: f.now.UnixMilli(), Connected: true}, {ID: "fake-pending", Hostname: "无采样主机", LastSeen: f.now.Add(-time.Hour).UnixMilli()}}, nil
	}), NewClient: func(_ config.FeishuApp, secret []byte) (feishu.Client, error) {
		if len(secret) == 0 {
			t.Fatal("empty secret passed to client")
		}
		return f.client, nil
	}})
	if err != nil {
		t.Fatal(err)
	}
	authDB, err := store.OpenAuthDB(context.Background(), filepath.Join(t.TempDir(), "auth.db"), func() time.Time { return f.now })
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { authDB.Close() })
	for _, scope := range []string{"read", "config", "admin"} {
		hash := api.TokenHash("fake-" + scope)
		if _, err := authDB.AddAPIToken(context.Background(), hash[:], "fake-"+scope, scope); err != nil {
			t.Fatal(err)
		}
	}
	hosts, err := api.NewHostPolicy("127.0.0.1:7444", f.manager.Snapshot().Settings.PublicBaseURL, nil)
	if err != nil {
		t.Fatal(err)
	}
	auth, err := api.NewAuthenticator(api.AuthOptions{Hosts: hosts, Logger: logger, Entropy: bytes.NewReader(make([]byte, 32)), Tokens: api.TokenLookupFunc(func(ctx context.Context, hash [32]byte) (api.Principal, bool, error) {
		row, found, err := authDB.Authenticate(ctx, hash[:])
		return api.Principal{Name: row.Name, Scope: api.Scope(row.Scope)}, found, err
	})})
	if err != nil {
		t.Fatal(err)
	}
	broker := api.NewBroker()
	t.Cleanup(broker.Close)
	f.server, err = api.NewServer(api.ServerOptions{Auth: auth, Broker: broker, Actions: backend.Actions(), Now: func() time.Time { return f.now }, Logger: logger})
	if err != nil {
		t.Fatal(err)
	}
	response := f.request(t, "GET", "/api/v1/openapi.json", "read", "", 200)
	if err := json.Unmarshal(response.Body.Bytes(), &f.spec); err != nil {
		t.Fatal(err)
	}
	return f
}

func (f *apiFixture) request(t *testing.T, method, path, role, body string, want int) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequest(method, "http://127.0.0.1:7444"+path, strings.NewReader(body))
	r.RemoteAddr = "192.0.2.5:1234"
	r.Header.Set("Authorization", "Bearer fake-"+role)
	w := httptest.NewRecorder()
	f.server.ServeHTTP(w, r)
	if w.Code != want {
		t.Fatalf("%s %s status=%d want=%d body=%s", method, path, w.Code, want, w.Body.String())
	}
	if !json.Valid(w.Body.Bytes()) {
		t.Fatalf("non-JSON response: %s", w.Body.String())
	}
	if f.spec != nil && want == 200 && path != "/api/v1/openapi.json" {
		f.checkResponseSchema(t, method, path, w.Body.Bytes())
	}
	return w
}

func (f *apiFixture) checkResponseSchema(t *testing.T, method, path string, body []byte) {
	t.Helper()
	base := strings.SplitN(path, "?", 2)[0]
	for _, route := range api.Routes() {
		if route.Method != method {
			continue
		}
		pattern := strings.Split(route.Path, "/")
		actual := strings.Split(base, "/")
		if len(pattern) != len(actual) {
			continue
		}
		matches := true
		for i, part := range pattern {
			if !strings.HasPrefix(part, "{") && part != actual[i] {
				matches = false
			}
		}
		if !matches {
			continue
		}
		operation := f.spec["paths"].(map[string]any)[route.Path].(map[string]any)[strings.ToLower(method)].(map[string]any)
		schema := operation["responses"].(map[string]any)["200"].(map[string]any)["content"].(map[string]any)["application/json"].(map[string]any)["schema"]
		compiler := validator.NewCompiler()
		const uri = "https://example.test/response.json"
		if err := compiler.AddResource(uri, schema); err != nil {
			t.Fatal(err)
		}
		compiled, err := compiler.Compile(uri)
		if err != nil {
			t.Fatalf("%s OpenAPI schema: %v", route.ID, err)
		}
		var result any
		if err := json.Unmarshal(body, &result); err != nil {
			t.Fatal(err)
		}
		if err := compiled.Validate(result); err != nil {
			t.Fatalf("%s response violates OpenAPI: %v body=%s", route.ID, err, body)
		}
		return
	}
	t.Fatalf("response route missing from registry: %s %s", method, path)
}

func (f *apiFixture) samples(t *testing.T) {
	t.Helper()
	for i, state := range []string{"up", "down", "up"} {
		ts := f.now.Add(time.Duration(-10+i*3) * time.Second).UnixMilli()
		process := collector.ProcessFound
		if state == "down" {
			process = collector.ProcessMissing
		}
		if err := f.db.Write(context.Background(), pipeline.Message{CollectorID: "fake-host", Seq: uint64(i + 1), TS: ts, Kind: pipeline.KindTargetStatus, TargetStatus: &pipeline.TargetStatus{TargetID: "web", State: state, Process: process}}); err != nil {
			t.Fatal(err)
		}
	}
	ts := f.now.Add(-10 * time.Second).UnixMilli()
	for _, message := range []pipeline.Message{
		{CollectorID: "fake-host", Seq: 4, TS: ts, Kind: pipeline.KindSystemMetrics, SystemMetrics: &collector.SystemMetrics{TS: ts, CPUPercent: 5, MemoryTotal: 100, MemoryUsed: 20, MemoryAvailable: 80, MemoryPercent: 20, UptimeSeconds: 100}},
		{CollectorID: "fake-host", Seq: 5, TS: ts, Kind: pipeline.KindProcessSample, ProcessSample: &pipeline.ProcessSample{TargetID: "web", PID: 1, CreatedAt: 1, CPUPercent: 10, RSS: 20}},
		{CollectorID: "fake-host", Seq: 6, TS: ts, Kind: pipeline.KindProbeResult, ProbeResult: &pipeline.ProbeResult{TargetID: "web", Probe: 0, OK: true, LatencyMS: 5}},
	} {
		if err := f.db.Write(context.Background(), message); err != nil {
			t.Fatal(err)
		}
	}
	if err := f.db.Flush(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestHTTPBackendReadsHistoryFiltersAndReplayUsePersistedData(t *testing.T) {
	f := newAPIFixture(t)
	f.samples(t)
	for _, path := range []string{"/api/v1/collectors", "/api/v1/targets", "/api/v1/targets/fake-host/web/history", "/api/v1/metrics?collector=fake-host&name=system.cpu_percent"} {
		f.request(t, "GET", path, "read", "", 200)
	}
	collectors := f.request(t, "GET", "/api/v1/collectors", "read", "", 200).Body.String()
	if !strings.Contains(collectors, `"version":"fake-v1"`) || !strings.Contains(collectors, `"online":false`) {
		t.Fatal("collector version or offline state missing")
	}
	targets := f.request(t, "GET", "/api/v1/targets", "read", "", 200).Body.String()
	if !strings.Contains(targets, `"state":"unknown"`) || !strings.Contains(targets, `"state":"up"`) {
		t.Fatal("pending or current target state missing")
	}
	result := f.request(t, "POST", "/api/v1/rules/down/test", "read", fmt.Sprintf(`{"from":%d,"to":%d}`, f.now.Add(-11*time.Second).UnixMilli(), f.now.UnixMilli()), 200)
	var replay rules.ReplayResult
	if err := json.Unmarshal(result.Body.Bytes(), &replay); err != nil || !replay.Complete || replay.Totals.Fired != 1 || replay.Totals.Resolved != 1 || len(f.client.sent) != 0 {
		t.Fatalf("replay did not use stored history: %+v %v", replay, err)
	}
	f.request(t, "POST", "/api/v1/rules/draft/test", "read", `{"rule":{"id":"draft","type":"process_absent","target":"web","severity":"warning","notify":["fake-channel"]}}`, 200)
	f.request(t, "POST", "/api/v1/rules/draft/test", "read", `{"id":"draft","type":"process_absent","target":"web","severity":"warning","notify":["fake-channel"]}`, 200)
	f.request(t, "GET", "/api/v1/metrics?from=2&to=1", "read", "", 400)
	f.request(t, "GET", "/api/v1/metrics?step=invalid", "read", "", 400)
	f.request(t, "POST", "/api/v1/rules/missing/test", "read", "", 404)
	f.request(t, "POST", "/api/v1/query", "read", `{"sql":"SELECT * FROM api_tokens"}`, 501)
	active, err := f.db.ActiveAlerts(context.Background())
	if err != nil || len(active) != 0 {
		t.Fatalf("replay persisted actual alerts: %+v %v", active, err)
	}
}

func TestHTTPBackendAckAndSilencePersistWithoutSending(t *testing.T) {
	f := newAPIFixture(t)
	alert := rules.Alert{ID: "fake-alert", RuleID: "down", CollectorID: "fake-host", TargetID: "web", State: rules.StateFiring, Severity: "critical", StartedAt: f.now.Add(-time.Minute).UnixMilli(), Summary: "虚构故障"}
	if err := f.db.SaveTransition(context.Background(), alert, rules.Event{AlertID: alert.ID, TS: alert.StartedAt, Kind: rules.StateFiring, Message: alert.Summary}); err != nil {
		t.Fatal(err)
	}
	f.request(t, "GET", "/api/v1/alerts?state=all", "read", "", 200)
	f.request(t, "POST", "/api/v1/alerts/fake-alert/ack", "read", "", 403)
	f.request(t, "POST", "/api/v1/alerts/fake-alert/ack", "config", "", 200)
	rows, err := f.view.Alerts(context.Background(), false)
	if err != nil || len(rows) != 1 || rows[0].AcknowledgedAt != f.now.UnixMilli() {
		t.Fatalf("ack was not saved: %+v %v", rows, err)
	}
	result := f.request(t, "POST", "/api/v1/silences", "config", `{"rule":"down","for":"1h","reason":"虚构维护"}`, 200)
	var silence store.Silence
	if err := json.Unmarshal(result.Body.Bytes(), &silence); err != nil || silence.EndsAt-silence.StartsAt != time.Hour.Milliseconds() {
		t.Fatalf("silence not persisted: %+v %v", silence, err)
	}
	f.request(t, "DELETE", "/api/v1/silences/"+silence.ID, "config", "", 200)
	f.request(t, "DELETE", "/api/v1/silences/"+silence.ID, "config", "", 404)
	f.request(t, "POST", "/api/v1/silences", "config", `{"rule":"missing","for":"1h","reason":"fake"}`, 404)
	f.request(t, "POST", "/api/v1/alerts/missing/ack", "config", "", 404)
	if len(f.client.sent) != 0 {
		t.Fatal("ack or silence sent a notification")
	}
}

func TestHTTPBackendConfigTemplatesAreValidatedAndMatchOpenAPI(t *testing.T) {
	f := newAPIFixture(t)
	for _, kind := range []string{"targets", "rules", "notifiers", "settings"} {
		role := "config"
		if kind == "settings" {
			role = "admin"
		}
		f.request(t, "GET", "/api/v1/config/"+kind, "read", "", 200)
		f.request(t, "PUT", "/api/v1/config/"+kind, role, string(f.files.MapFS[kind+".yaml"].Data), 200)
	}
	writes := f.files.writes
	response := f.request(t, "PUT", "/api/v1/config/targets", "config", "targets:\n- id: web\n  match: {cmdline: '['}\n", 422)
	if !strings.Contains(response.Body.String(), `"line":3`) || f.files.writes != writes {
		t.Fatal("bad config lost its location or was saved")
	}
	f.request(t, "PUT", "/api/v1/config/notifiers", "config", strings.ReplaceAll(string(f.files.MapFS["notifiers.yaml"].Data), "fake-ref", "transport-ca-key"), 403)
	f.request(t, "GET", "/api/v1/templates", "read", "", 200)
	f.request(t, "GET", "/api/v1/templates/standard", "read", "", 200)
	f.request(t, "PUT", "/api/v1/templates/extra", "config", "id: extra\nextends: standard\ntitle: '虚构标题'\n", 200)
	f.request(t, "GET", "/api/v1/templates/extra", "read", "", 200)
	preview := f.request(t, "POST", "/api/v1/templates/extra/preview", "read", `{"state":"resolved"}`, 200)
	if !strings.Contains(preview.Body.String(), "虚构标题") || !strings.Contains(preview.Body.String(), "http://panel.example.test") {
		t.Fatal("preview or dashboard link missing")
	}
	for _, title := range []string{"{{range 100000000000}}{{end}}", "{{$v := .Alerts}}{{range $v}}{{end}}", "{{range len .Alerts}}{{end}}", "{{range .Alerts}}{{range $.Alerts}}{{end}}{{end}}", `{{define "loop"}}{{template "loop"}}{{end}}`} {
		start := time.Now()
		definition := map[string]any{"id": "draft", "extends": "standard", "title": title}
		payload, _ := json.Marshal(definition)
		f.request(t, "PUT", "/api/v1/templates/draft", "config", string(payload), 422)
		payload, _ = json.Marshal(map[string]any{"template": definition})
		f.request(t, "POST", "/api/v1/templates/draft/preview", "read", string(payload), 422)
		if time.Since(start) > time.Second {
			t.Fatal("unsafe template save/preview exceeded one second")
		}
	}
	f.request(t, "POST", "/api/v1/templates/draft/preview", "read", `{"template":{"id":"draft","extends":"compact","title":"仅预览草稿"}}`, 200)
	f.request(t, "POST", "/api/v1/templates/draft/preview", "read", `{"id":"draft","extends":"compact","title":"直接预览草稿"}`, 200)
	if len(f.manager.Snapshot().Templates) != 1 || len(f.client.sent) != 0 {
		t.Fatal("preview saved a template or sent a card")
	}
	f.request(t, "DELETE", "/api/v1/templates/extra", "config", "", 200)
	f.request(t, "DELETE", "/api/v1/templates/standard", "config", "", 400)
	f.request(t, "GET", "/api/v1/templates/missing", "read", "", 404)
}

func TestHTTPBackendFeishuCredentialsAreWriteOnlyAndTestRepliesArePrivate(t *testing.T) {
	f := newAPIFixture(t)
	check := f.request(t, "GET", "/api/v1/feishu/check", "config", "", 200)
	var report setup.Report
	if err := json.Unmarshal(check.Body.Bytes(), &report); err != nil || len(report.Steps) == 0 {
		t.Fatalf("Feishu check report: %s %v", check.Body.String(), err)
	}
	f.request(t, "GET", "/api/v1/feishu/chats", "config", "", 200)
	result := f.request(t, "POST", "/api/v1/notifiers/fake-channel/test", "config", `{}`, 200)
	if strings.Contains(result.Body.String(), "PRIVATE_RESPONSE") || len(f.client.sent) != 1 || !strings.Contains(string(f.client.sent[0].Card), "这是测试消息") {
		t.Fatal("test result leaked server response or did not send a labelled sample")
	}
	f.request(t, "POST", "/api/v1/notifiers/fake-channel/test", "config", `{}`, 429)
	f.now = f.now.Add(11 * time.Second)
	f.client.err = &feishu.Error{Kind: feishu.CardRejected, Message: "PRIVATE_SERVER_BODY"}
	result = f.request(t, "POST", "/api/v1/notifiers/fake-channel/test", "config", `{}`, 200)
	if strings.Contains(result.Body.String(), "PRIVATE_SERVER_BODY") || !strings.Contains(result.Body.String(), `"ok":false`) {
		t.Fatal("failure exposed raw server response")
	}
	f.request(t, "POST", "/api/v1/feishu/apps", "config", `{"id":"fake-app","appId":"cli_fake_rotated","domain":"feishu","secret":"FAKE_ROTATED_SECRET"}`, 403)
	f.request(t, "POST", "/api/v1/feishu/apps", "admin", `{"id":"fake-app","appId":"cli_fake_rotated","domain":"feishu","secret":"FAKE_ROTATED_SECRET"}`, 200)
	app := f.manager.Snapshot().FeishuApps[0]
	if app.SecretRef == "fake-ref" || string(f.vault.values[app.SecretRef]) != "FAKE_ROTATED_SECRET" || string(f.vault.values["fake-ref"]) != "FAKE_PRIVATE_SECRET" {
		t.Fatal("credential rotation reused a cached reference or overwrote old in-flight credentials")
	}
	if !bytes.Equal(f.vault.lastRead, make([]byte, len(f.vault.lastRead))) {
		t.Fatal("read secret buffer was not cleared")
	}
	for _, kind := range []string{"targets", "rules", "notifiers", "settings"} {
		response := f.request(t, "GET", "/api/v1/config/"+kind, "read", "", 200)
		if strings.Contains(response.Body.String(), "FAKE_ROTATED_SECRET") || strings.Contains(response.Body.String(), "FAKE_PRIVATE_SECRET") {
			t.Fatal("configuration returned a credential")
		}
	}
	if strings.Contains(string(f.files.MapFS["notifiers.yaml"].Data), "FAKE_ROTATED_SECRET") || !strings.Contains(string(f.files.MapFS["notifiers.yaml"].Data), "preserve this comment") || strings.Contains(f.logs.String(), "FAKE_ROTATED_SECRET") {
		t.Fatal("credential leaked or existing comment was discarded")
	}
	f.files.failure = errors.New("fake write failure")
	oldRef := app.SecretRef
	f.request(t, "POST", "/api/v1/feishu/apps", "admin", `{"id":"fake-app","appId":"cli_fake_rotated","domain":"feishu","secret":"FAKE_SECOND_SECRET"}`, 500)
	if f.manager.Snapshot().FeishuApps[0].SecretRef != oldRef {
		t.Fatal("failed save replaced active credentials")
	}
}
