package store

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"testing"
	"testing/fstest"
	"time"

	"github.com/LFenX/lg-monitor/internal/config"
)

func TestDashboardViewsReturnLatestObservedFactsAndOnlyActiveAlerts(t *testing.T) {
	s := openStore(t, Options{DisableMaintenance: true})
	ctx := context.Background()
	for _, statement := range []string{
		"INSERT INTO collectors (id,hostname,first_seen,last_seen,version) VALUES ('fake-host','虚构主机',1,10,'fake-v1')",
		"INSERT INTO target_status VALUES ('fake-host','web',10,'up','{}'),('fake-host','web',20,'unknown','{}'),('other','web',15,'up','{}')",
		"INSERT INTO alerts (id,state) VALUES ('fake-firing','firing'),('fake-resolved','resolved'),('fake-closed','closed')",
	} {
		if _, err := s.db.ExecContext(ctx, statement); err != nil {
			t.Fatal(err)
		}
	}
	view, err := OpenView(ctx, s.maintenanceView.path)
	if err != nil {
		t.Fatal(err)
	}
	defer view.Close()
	result, err := view.Query(ctx, "SELECT collector_id,target_id,ts,state FROM v_target_current ORDER BY collector_id")
	if err != nil || len(result.Rows) != 2 || result.Rows[0][0] != "fake-host" || result.Rows[0][3] != "unknown" {
		t.Fatalf("latest status/timestamp tie: %+v %v", result, err)
	}
	active, err := view.Query(ctx, "SELECT id FROM v_alerts_active")
	if err != nil || len(active.Rows) != 1 || active.Rows[0][0] != "fake-firing" {
		t.Fatalf("active alerts: %+v %v", active, err)
	}
	collectors, err := view.Query(ctx, "SELECT collector_id,hostname,last_seen FROM v_collectors")
	if err != nil || len(collectors.Rows) != 1 || collectors.Rows[0][1] != "虚构主机" {
		t.Fatalf("collectors: %+v %v", collectors, err)
	}
	if _, err := view.Query(ctx, "DELETE FROM v_target_current"); err == nil {
		t.Fatal("dashboard view allowed write")
	}
	rules, err := OpenRuleQueries(ctx, s.maintenanceView.path)
	if err != nil {
		t.Fatal(err)
	}
	defer rules.Close()
	for _, name := range []string{"v_target_current", "v_alerts_active", "v_collectors"} {
		if _, err := rules.QueryRule(ctx, "SELECT * FROM "+name, 1); err == nil {
			t.Fatalf("current dashboard view %s bypassed event-time rule isolation", name)
		}
	}
}

func TestThreeExampleDashboardsQueryTheActualMonitorSchema(t *testing.T) {
	files, err := filepath.Glob(filepath.Join("..", "..", "examples", "dashboards", "*.yaml"))
	if err != nil || len(files) != 4 {
		t.Fatalf("expected three declarative and one HTML dashboard examples: %v %v", files, err)
	}
	input := fstest.MapFS{}
	for _, file := range files {
		data, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		input["dashboards/"+filepath.Base(file)] = &fstest.MapFile{Data: data}
	}
	htmlFiles, err := filepath.Glob(filepath.Join("..", "..", "examples", "dashboards", "*.html"))
	if err != nil || len(htmlFiles) != 1 {
		t.Fatalf("expected one HTML source: %v %v", htmlFiles, err)
	}
	for _, file := range htmlFiles {
		data, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		input["dashboards/"+filepath.Base(file)] = &fstest.MapFile{Data: data}
	}
	cfg, err := config.Load(input)
	if err != nil || len(cfg.DashboardIssues()) != 0 || len(cfg.Dashboards) != 4 {
		t.Fatalf("examples failed to load: %v %+v", err, cfg)
	}
	s := openStore(t, Options{DisableMaintenance: true})
	ctx := context.Background()
	for _, statement := range []string{
		"INSERT INTO collectors (id,hostname,last_seen) VALUES ('fake-host','虚构主机',2000)",
		"INSERT INTO target_status VALUES ('fake-host','web',2000,'up','{}')",
		"INSERT INTO probe_results (collector_id,target_id,probe,ts,ok,latency_ms) VALUES ('fake-host','web','0',2000,1,12)",
		"INSERT INTO system_metrics (collector_id,ts,cpu) VALUES ('fake-host',2000,0)",
		"INSERT INTO alerts (id,collector_id,state,severity,summary,started_at) VALUES ('fake-alert','fake-host','firing','critical','虚构告警',1000)",
		"INSERT INTO alert_events (alert_id,ts,kind,message) VALUES ('fake-alert',1000,'firing','虚构日志')",
	} {
		if _, err := s.db.ExecContext(ctx, statement); err != nil {
			t.Fatal(err)
		}
	}
	view, err := OpenView(ctx, s.maintenanceView.path)
	if err != nil {
		t.Fatal(err)
	}
	defer view.Close()
	required := map[string][]string{
		"stat": {"value"}, "line": {"ts", "value"}, "bar": {"label", "value"}, "status_grid": {"label", "state"},
		"alerts": {"summary", "severity", "state"}, "log": {"ts", "message"}, "table": {},
	}
	seen := map[string]bool{}
	declarative, custom := 0, 0
	for _, dashboard := range cfg.Dashboards {
		if dashboard.HTML != "" {
			custom++
			queries := regexp.MustCompile(`lgmon[.]query\(\s*"([^"]+)"`).FindAllStringSubmatch(dashboard.HTMLContent, -1)
			if len(queries) != 1 {
				t.Fatal("HTML example did not expose its complete documented SQL")
			}
			result, err := view.QueryParams(ctx, queries[0][1], map[string]any{"from": int64(0), "to": int64(3000), "collector": "fake-host"})
			if err != nil || len(result.Rows) != 1 || !slices.Equal(result.Columns, []string{"state", "value"}) || result.Rows[0][0] != "up" || result.Rows[0][1] != int64(1) {
				t.Fatalf("HTML example query failed: %+v %v", result, err)
			}
			continue
		}
		declarative++
		for _, panel := range dashboard.Panels {
			t.Run(dashboard.ID+"/"+panel.Title, func(t *testing.T) {
				seen[panel.Type] = true
				result, err := view.QueryParams(ctx, panel.Query, map[string]any{"from": int64(0), "to": int64(3000), "collector": "fake-host"})
				if err != nil || len(result.Rows) == 0 {
					t.Fatalf("example did not return seeded real rows: %+v %v", result, err)
				}
				for _, column := range required[panel.Type] {
					if !slices.Contains(result.Columns, column) {
						t.Fatalf("missing %s: %v", column, result.Columns)
					}
				}
			})
		}
	}
	if declarative != 3 || custom != 1 {
		t.Fatalf("example modes: declarative=%d HTML=%d", declarative, custom)
	}
	if len(seen) != 7 {
		t.Fatalf("examples did not cover all seven types: %v", seen)
	}
}

func TestDashboardQueryParametersAreBoundWithoutChangingSQLText(t *testing.T) {
	s := openStore(t, Options{DisableMaintenance: true})
	ctx := context.Background()
	view, err := OpenView(ctx, s.maintenanceView.path)
	if err != nil {
		t.Fatal(err)
	}
	defer view.Close()
	injection := "fake' OR 1=1; DROP TABLE collectors; --"
	result, err := view.QueryParams(ctx, "SELECT ':from' AS literal, :from AS start, :to AS stop, :collector AS host", map[string]any{"from": int64(0), "to": int64(2000000000000), "collector": injection})
	if err != nil || len(result.Rows) != 1 || result.Rows[0][0] != ":from" || result.Rows[0][1] != int64(0) || result.Rows[0][3] != injection {
		t.Fatalf("parameters were interpolated or lost: %+v %v", result, err)
	}
	if _, err := view.QueryParams(ctx, "SELECT :now", map[string]any{"now": int64(1)}); err == nil {
		t.Fatal("caller replaced the injected server clock")
	}
	if _, err := view.QueryParams(ctx, "DELETE FROM collectors WHERE id=:collector", map[string]any{"collector": injection}); err == nil {
		t.Fatal("parameterized query bypassed read-only guard")
	}
}

func TestSlowDashboardQueryDoesNotQueueOtherPanels(t *testing.T) {
	s := openStore(t, Options{DisableMaintenance: true})
	view, err := OpenView(context.Background(), s.maintenanceView.path)
	if err != nil {
		t.Fatal(err)
	}
	defer view.Close()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	started := make(chan struct{})
	go func() {
		close(started)
		_, err := view.QueryParams(ctx, "WITH RECURSIVE x(n) AS(SELECT 1 UNION ALL SELECT n+1 FROM x) SELECT sum(n) AS value FROM x", nil)
		done <- err
	}()
	defer func() {
		cancel()
		if err := <-done; !errors.Is(err, context.Canceled) {
			t.Errorf("slow panel did not cancel: %v", err)
		}
	}()
	<-started
	for i := 0; i < 10; i++ {
		fast, stop := context.WithTimeout(context.Background(), 2*time.Second)
		result, err := view.QueryParams(fast, "SELECT 42 AS value", nil)
		stop()
		if err != nil || len(result.Rows) != 1 {
			t.Fatalf("slow card blocked a different card: %+v %v", result, err)
		}
	}
}
