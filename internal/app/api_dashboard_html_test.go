package app

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/LFenX/lg-monitor/internal/api"
)

func TestHTMLDashboardReadAPIAndMCPKeepSourceInAuthenticatedJSON(t *testing.T) {
	f := newAPIFixture(t)
	source := "<h1>虚构 HTML</h1><script>lgmon.query('SELECT 1')</script>"
	f.files.MapFS["dashboards/overview.html"] = &fstest.MapFile{Data: []byte(source)}
	loadAPIDashboard(t, f, "id: overview\ntitle: HTML 面板\nrefresh: 10s\nhtml: overview.html\n")
	list := f.request(t, "GET", "/api/v1/dashboards", "read", "", 200)
	if !strings.Contains(list.Body.String(), `"html":true`) {
		t.Fatal(list.Body.String())
	}
	read := f.request(t, "GET", "/api/v1/dashboards/overview", "read", "", 200)
	var definition api.DashboardDefinition
	if err := json.Unmarshal(read.Body.Bytes(), &definition); err != nil || definition.HTML == nil || definition.HTML.Source != source || len(definition.Panels) != 0 || strings.Contains(read.Body.String(), "<script>") {
		t.Fatalf("definition=%+v %v", definition, err)
	}
	server := httptest.NewServer(f.server)
	defer server.Close()
	session := mcpSession(t, server.URL, "fake-read", false)
	var throughMCP api.DashboardDefinition
	if err := json.Unmarshal([]byte(mcpCall(t, session, "get_dashboard", map[string]any{"id": "overview"}, false)), &throughMCP); err != nil || throughMCP.HTML == nil || throughMCP.HTML.Source != source {
		t.Fatal("MCP source differs", err)
	}
	f.request(t, "GET", "/api/v1/dashboards/overview", "invalid", "", 401)
}
