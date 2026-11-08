package config

import (
	"strings"
	"testing"
	"testing/fstest"
)

const htmlDashboardFixture = "id: custom\ntitle: 自定义面板\nrefresh: 10s\nhtml: custom.html\n"

func TestHTMLDashboardLoadsVersionsAndRetainsLastGoodSource(t *testing.T) {
	files := fstest.MapFS{"dashboards/custom.yaml": {Data: []byte(htmlDashboardFixture)}, "dashboards/custom.html": {Data: []byte("<h1>虚构面板</h1><script>lgmon.query('SELECT 1')</script>")}}
	manager, err := NewManager(FSLoader{FS: files}, nil)
	if err != nil {
		t.Fatal(err)
	}
	first := manager.Snapshot()
	if len(first.Dashboards) != 1 || first.DashboardStates[0].Error != nil || first.Dashboards[0].HTML != "custom.html" || !strings.Contains(first.Dashboards[0].HTMLContent, "虚构面板") {
		t.Fatalf("snapshot=%+v", first)
	}
	files["dashboards/custom.html"] = &fstest.MapFile{Data: []byte("<h1>更新内容</h1>")}
	if err := manager.Reload(); err != nil {
		t.Fatal(err)
	}
	second := manager.Snapshot()
	if second.DashboardStates[0].Version == first.DashboardStates[0].Version || !strings.Contains(second.Dashboards[0].HTMLContent, "更新内容") {
		t.Fatal("HTML-only edit did not change version/source")
	}
	files["dashboards/custom.html"] = &fstest.MapFile{Data: []byte("invalid\x00html")}
	if err := manager.Reload(); err != nil {
		t.Fatal(err)
	}
	bad := manager.Snapshot()
	if bad.DashboardStates[0].Error == nil || bad.Dashboards[0].HTMLContent != second.Dashboards[0].HTMLContent || bad.DashboardStates[0].Version != second.DashboardStates[0].Version {
		t.Fatal("invalid HTML replaced last good source")
	}
	delete(files, "dashboards/custom.yaml")
	if err := manager.Reload(); err != nil || len(manager.Snapshot().Dashboards) != 0 {
		t.Fatal("descriptor deletion retained page", err)
	}
}

func TestHTMLDashboardRejectsUnsafeReferencesAndInvalidText(t *testing.T) {
	for _, ref := range []string{"../secret.html", "C:/fake/secret.html", "sub/custom.html", "other.html"} {
		cfg, err := Load(fstest.MapFS{"dashboards/custom.yaml": {Data: []byte(strings.Replace(htmlDashboardFixture, "custom.html", ref, 1))}})
		if err != nil || len(cfg.Dashboards) != 0 || !strings.Contains(cfg.DashboardIssues().Error(), "同目录") {
			t.Fatalf("reference %s accepted: %+v %v", ref, cfg, err)
		}
	}
	for _, text := range []string{htmlDashboardFixture + "panels: []\n", htmlDashboardFixture + "panels: null\n"} {
		cfg, err := Load(fstest.MapFS{"dashboards/custom.yaml": {Data: []byte(text)}, "dashboards/custom.html": {Data: []byte("safe")}})
		if err != nil || !strings.Contains(cfg.DashboardIssues().Error(), "不能同时") {
			t.Fatal("mixed mode accepted", err)
		}
	}
	for _, text := range []string{dashboardFixture + "html: ''\n", dashboardFixture + "html: null\n"} {
		cfg, err := Load(fstest.MapFS{"dashboards/overview.yaml": {Data: []byte(text)}})
		if err != nil || len(cfg.Dashboards) != 0 {
			t.Fatal("empty HTML field silently selected declarative mode", err)
		}
	}
	for _, content := range [][]byte{[]byte(strings.Repeat("x", MaxDraftFileBytes+1)), {0xff}, {'a', 0, 'b'}} {
		cfg, err := Load(fstest.MapFS{"dashboards/custom.yaml": {Data: []byte(htmlDashboardFixture)}, "dashboards/custom.html": {Data: content}})
		if err != nil || len(cfg.Dashboards) != 0 || !strings.Contains(cfg.DashboardIssues().Error(), "HTML 面板") {
			t.Fatal("invalid HTML text accepted", err)
		}
	}
	if data, err := readDashboardHTML(fstest.MapFS{"custom.html": {Data: []byte(strings.Repeat("x", MaxDraftFileBytes))}}, "custom.html"); err != nil || len(data) != MaxDraftFileBytes {
		t.Fatal("exactly 1 MiB rejected", err)
	}
	html := "<p>草稿</p>"
	desc := htmlDashboardFixture
	result, err := ValidateFiles(fstest.MapFS{}, map[string]*string{"dashboards/custom.yaml": &desc, "dashboards/custom.html": &html})
	if err != nil || !result.Valid || len(result.Files) != 2 {
		t.Fatalf("draft=%+v %v", result, err)
	}
}
