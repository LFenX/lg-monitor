package app

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/LFenX/lg-monitor/internal/api"
	"github.com/LFenX/lg-monitor/internal/config"
)

const apiDashboardFixture = "id: overview\ntitle: 虚构面板\nrefresh: 10s\ndefaultRange: 6h\npanels:\n- id: healthy\n  type: stat\n  title: 健康卡片\n  width: 3\n  query: SELECT count(*) AS value FROM collectors WHERE (:collector IS NULL OR id = :collector)\n- id: unsupported\n  type: stat\n  title: 参数错误卡片\n  width: 3\n  query: SELECT :host AS value\n- id: columns\n  type: line\n  title: 错误列卡片\n  width: 6\n  query: SELECT 1 AS amount\n"

func loadAPIDashboard(t *testing.T, f *apiFixture, source string) {
	t.Helper()
	f.files.MapFS["dashboards/overview.yaml"] = &fstest.MapFile{Data: []byte(source)}
	if err := f.manager.Reload(); err != nil {
		t.Fatal(err)
	}
}
func TestDashboardHTTPDefinitionsPanelIsolationAndVersion(t *testing.T) {
	f := newAPIFixture(t)
	loadAPIDashboard(t, f, apiDashboardFixture)
	list := f.request(t, "GET", "/api/v1/dashboards", "read", "", 200)
	if !strings.Contains(list.Body.String(), `"panelCount":3`) {
		t.Fatal(list.Body.String())
	}
	read := f.request(t, "GET", "/api/v1/dashboards/overview", "read", "", 200)
	var definition api.DashboardDefinition
	if err := json.Unmarshal(read.Body.Bytes(), &definition); err != nil {
		t.Fatal(err)
	}
	hash := sha256.Sum256([]byte(apiDashboardFixture))
	if definition.Version != fmt.Sprintf("%x", hash[:8]) || definition.DefaultRange != "6h" || definition.Panels[0].Parameters[0] != "collector" || definition.Panels[1].Error == nil {
		t.Fatalf("definition=%+v", definition)
	}
	response := f.request(t, "GET", "/api/v1/dashboards/overview/panels/healthy/data", "read", "", 200)
	var data api.PanelData
	if err := json.Unmarshal(response.Body.Bytes(), &data); err != nil || data.Params.Collector != nil || data.Params.To-data.Params.From != int64((6*time.Hour)/time.Millisecond) || data.Rows[0][0] != float64(1) {
		t.Fatalf("data=%+v %v", data, err)
	}
	f.request(t, "GET", "/api/v1/dashboards/overview/panels/unsupported/data", "read", "", 400)
	columns := f.request(t, "GET", "/api/v1/dashboards/overview/panels/columns/data", "read", "", 422)
	if !strings.Contains(columns.Body.String(), "panel_columns") || !strings.Contains(columns.Body.String(), "ts、value") || !strings.Contains(columns.Body.String(), "amount") {
		t.Fatal(columns.Body.String())
	}
	f.request(t, "GET", "/api/v1/dashboards/missing", "read", "", 404)
	f.request(t, "GET", "/api/v1/dashboards/overview/panels/missing/data", "read", "", 404)
	changed := strings.Replace(apiDashboardFixture, "count(*) AS value", "count(*)+1 AS value", 1)
	loadAPIDashboard(t, f, changed)
	newData := f.request(t, "GET", "/api/v1/dashboards/overview/panels/healthy/data", "read", "", 200)
	if err := json.Unmarshal(newData.Body.Bytes(), &data); err != nil || data.Version == definition.Version || data.Rows[0][0] != float64(2) {
		t.Fatalf("stale panel data: %+v %v", data, err)
	}
	loadAPIDashboard(t, f, "id: [")
	stale := f.request(t, "GET", "/api/v1/dashboards/overview", "read", "", 200)
	if !strings.Contains(stale.Body.String(), "dashboards/overview.yaml") || !strings.Contains(stale.Body.String(), "虚构面板") {
		t.Fatal("bad file did not retain definition and error")
	}
	// A fresh bad page stays visible, while a healthy page continues to execute.
	f.files.MapFS["dashboards/new-bad.yaml"] = &fstest.MapFile{Data: []byte("id: [")}
	if err := f.manager.Reload(); err != nil {
		t.Fatal(err)
	}
	f.request(t, "GET", "/api/v1/dashboards/new-bad", "read", "", 422)
	f.request(t, "GET", "/api/v1/dashboards/overview/panels/healthy/data", "read", "", 200)
}

func TestQueryHTTPParameterResolutionTableAndErrors(t *testing.T) {
	f := newAPIFixture(t)
	now := f.now.UnixMilli()
	for _, tc := range []struct {
		name     string
		params   config.QueryParams
		from, to int64
	}{
		{"both", config.QueryParams{From: pointerInt64(1000), To: pointerInt64(2000)}, 1000, 2000},
		{"range", config.QueryParams{Range: pointerDuration("2h")}, now - 7200000, now},
		{"range and to", config.QueryParams{Range: pointerDuration("1h"), To: pointerInt64(4000000)}, 400000, 4000000},
		{"from", config.QueryParams{From: pointerInt64(0)}, 0, now},
		{"to", config.QueryParams{To: pointerInt64(4000000)}, 400000, 4000000},
		{"none", config.QueryParams{}, now - 3600000, now},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body := feishuBody(t, api.QueryInput{SQL: "SELECT :from AS start, :to AS stop, :collector AS host", Params: tc.params})
			response := f.request(t, "POST", "/api/v1/query", "read", body, 200)
			var data api.QueryResult
			if err := json.Unmarshal(response.Body.Bytes(), &data); err != nil || data.Params.From != tc.from || data.Params.To != tc.to || data.Params.Collector != nil || data.Rows[0][2] != nil {
				t.Fatalf("params=%+v %v", data, err)
			}
		})
	}
	for _, body := range []string{
		`{"sql":"SELECT 1","params":{"range":"1h","from":0}}`,
		`{"sql":"SELECT 1","params":{"range":"0s"}}`,
		`{"sql":"SELECT 1","params":{"from":2,"to":1}}`,
		`{"sql":"SELECT 1","params":{"collector":"unknown-fake"}}`,
		`{"sql":"SELECT :form"}`,
	} {
		f.request(t, "POST", "/api/v1/query", "read", body, 400)
	}
	known := f.request(t, "POST", "/api/v1/query", "read", `{"sql":"SELECT :collector AS host","params":{"collector":"fake-host"}}`, 200)
	if !strings.Contains(known.Body.String(), `"collector":"fake-host"`) {
		t.Fatal(known.Body.String())
	}
}
func pointerInt64(value int64) *int64                        { return &value }
func pointerDuration(value config.Duration) *config.Duration { return &value }

func TestLocalSessionCanReadAllThreeDashboardEndpoints(t *testing.T) {
	f := newAPIFixture(t)
	loadAPIDashboard(t, f, apiDashboardFixture)
	request := httptest.NewRequest("GET", "http://127.0.0.1:7444/", nil)
	request.RemoteAddr = "127.0.0.1:1234"
	response := httptest.NewRecorder()
	f.server.ServeHTTP(response, request)
	if response.Code == 503 {
		t.Skip("local bootstrap verification requires the webui build")
	}
	match := regexp.MustCompile(`(?s)<script[^>]*id="lgmon-session"[^>]*>(.*?)</script>`).FindStringSubmatch(response.Body.String())
	if len(match) != 2 {
		t.Fatal("missing local bootstrap session")
	}
	var session struct {
		Token string `json:"token"`
	}
	if err := json.Unmarshal([]byte(match[1]), &session); err != nil || session.Token == "" {
		t.Fatal("invalid bootstrap")
	}
	for _, path := range []string{"/api/v1/dashboards", "/api/v1/dashboards/overview", "/api/v1/dashboards/overview/panels/healthy/data"} {
		request = httptest.NewRequest("GET", "http://127.0.0.1:7444"+path, nil)
		request.RemoteAddr = "127.0.0.1:1234"
		request.Header.Set("Authorization", "Bearer "+session.Token)
		response = httptest.NewRecorder()
		f.server.ServeHTTP(response, request)
		if response.Code != 200 {
			t.Fatalf("%s: %d %s", path, response.Code, response.Body.String())
		}
	}
}
