package store

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/LFenX/lg-monitor/internal/collector"
	"github.com/LFenX/lg-monitor/internal/pipeline"
)

func shareQueryFixture(t *testing.T) *View {
	t.Helper()
	ctx := context.Background()
	now := time.UnixMilli(2000000000000)
	s := openStore(t, Options{DisableMaintenance: true, Now: func() time.Time { return now }})
	for _, host := range []string{"fake-a", "outside-host"} {
		if err := s.RegisterCollector(ctx, CollectorRecord{ID: host, Hostname: host, FirstSeen: 1, LastSeen: now.UnixMilli()}); err != nil {
			t.Fatal(err)
		}
		for i, target := range []string{"web", "outside-target"} {
			if err := s.Write(ctx, pipeline.Message{CollectorID: host, Seq: uint64(i + 1), TS: now.UnixMilli(), Kind: pipeline.KindTargetStatus, TargetStatus: &pipeline.TargetStatus{TargetID: target, State: "up"}}); err != nil {
				t.Fatal(err)
			}
		}
		if err := s.Write(ctx, pipeline.Message{CollectorID: host, Seq: 3, TS: now.UnixMilli(), Kind: pipeline.KindSystemMetrics, SystemMetrics: &collector.SystemMetrics{TS: now.UnixMilli(), CPUPercent: 5}}); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.Flush(ctx); err != nil {
		t.Fatal(err)
	}
	for _, host := range []string{"fake-a", "outside-host"} {
		for i, target := range []string{"web", "outside-target"} {
			for _, table := range []string{"metric_minute", "metric_hour"} {
				if _, err := s.db.Exec(`INSERT INTO `+table+`(source_table,collector_id,target_id,name,resource,probe,ts,samples,sum_value,max_value) VALUES('process_samples',?,?,'cpu','','',?,2,10,8)`, host, target, now.UnixMilli()); err != nil {
					t.Fatal(err)
				}
			}
			for _, table := range []string{"probe_minute", "probe_hour"} {
				if _, err := s.db.Exec(`INSERT INTO `+table+`(collector_id,target_id,probe,ts,samples,successes,latency_sum) VALUES(?,?,'http',?,2,1,15)`, host, target, now.UnixMilli()); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := s.db.Exec(`INSERT INTO eventlog_events(collector_id,channel,record_id,ts,provider,event_id,level,subject,message) VALUES(?,'Application',?,?,'Application Error',1000,2,'fake.exe',?)`, host, i+1, now.UnixMilli(), host+":"+target); err != nil {
				t.Fatal(err)
			}
			if _, err := s.db.Exec(`INSERT INTO eventlog_matches(collector_id,channel,record_id,ts,kind,name) VALUES(?,'Application',?,?,'subject',?)`, host, i+1, now.UnixMilli(), target); err != nil {
				t.Fatal(err)
			}
		}
	}
	if _, err := s.db.Exec(`CREATE TABLE unregistered(collector_id TEXT,target_id TEXT,value TEXT); INSERT INTO unregistered VALUES('outside-host','outside-target','outside-secret')`); err != nil {
		t.Fatal(err)
	}
	v, err := OpenViewWithClock(ctx, s.maintenanceView.path, func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { v.Close() })
	return v
}

func TestSharedQueriesFilterTablesViewsJoinsCTEsAndRealTime(t *testing.T) {
	v := shareQueryFixture(t)
	scope := ShareScope{Collectors: []string{"fake-a"}, Targets: []string{"web"}}
	for _, query := range []string{
		`SELECT collector_id,target_id FROM target_status`,
		`SELECT collector_id,target_id FROM v_target_current`,
		`SELECT * FROM (SELECT collector_id,target_id FROM target_status)`,
		`WITH mine AS (SELECT * FROM target_status) SELECT collector_id,target_id FROM mine;`,
		`SELECT a.collector_id,a.target_id FROM target_status a JOIN v_target_current b USING(collector_id,target_id)`,
		`SELECT collector_id,target_id FROM v_eventlog_targets`,
		`WITH mine AS MATERIALIZED (SELECT * FROM v_eventlog_targets) SELECT collector_id,target_id FROM mine`,
		`SELECT collector_id,target_id FROM metric_minute`,
		`SELECT collector_id,target_id FROM metric_hour`,
		`SELECT collector_id,target_id FROM probe_minute`,
		`SELECT collector_id,target_id FROM probe_hour`,
	} {
		result, err := v.QueryShare(context.Background(), query, scope, nil)
		if err != nil {
			t.Errorf("query %s: %v", query, err)
			continue
		}
		if len(result.Rows) != 1 || result.Rows[0][0] != "fake-a" || result.Rows[0][1] != "web" {
			t.Errorf("scope escaped in %s: %+v", query, result)
		}
	}
	result, err := v.QueryShare(context.Background(), `SELECT collector_id FROM system_metrics`, ShareScope{Collectors: []string{"fake-a"}}, nil)
	if err != nil || len(result.Rows) != 1 || result.Rows[0][0] != "fake-a" {
		t.Fatal("collector-only filter", result, err)
	}
	result, err = v.QueryShare(context.Background(), `SELECT collector_id FROM target_status`, ShareScope{}, nil)
	if err != nil || len(result.Rows) != 4 {
		t.Fatal("unrestricted share changed real-time data", result, err)
	}
	result, err = v.QueryShare(context.Background(), `SELECT value FROM unregistered`, ShareScope{}, nil)
	if err != nil || len(result.Rows) != 1 || result.Rows[0][0] != "outside-secret" {
		t.Fatal("unrestricted owner-selected SQL was silently narrowed", result, err)
	}
	result, err = v.QueryShare(context.Background(), `SELECT :from AS value`, scope, map[string]any{"from": int64(123)})
	if err != nil || len(result.Rows) != 1 || result.Rows[0][0] != int64(123) {
		t.Fatal("bound period", result, err)
	}
}

func TestSharedQueriesRejectScopeBypassesUnknownObjectsAndMissingDimensions(t *testing.T) {
	v := shareQueryFixture(t)
	scope := ShareScope{Collectors: []string{"fake-a"}, Targets: []string{"web"}}
	for _, query := range []string{
		`SELECT * FROM main.target_status`,
		`SELECT * FROM "main"."target_status"`,
		`SELECT * FROM 'main'.'target_status'`,
		`SELECT * FROM temp.target_status`,
		`WITH v_eventlog_targets AS(SELECT * FROM eventlog_events) SELECT * FROM v_eventlog_targets`,
		`WITH mine AS(SELECT 1), target_status AS(SELECT * FROM eventlog_events) SELECT * FROM target_status`,
		`SELECT * FROM( WITH "V_EVENTLOG_TARGETS" AS (SELECT * FROM eventlog_events) SELECT * FROM "V_EVENTLOG_TARGETS")`,
		`SELECT * FROM sqlite_schema`,
		`SELECT * FROM pragma_table_info('target_status')`,
		`SELECT * FROM unregistered`,
		`SELECT * FROM system_metrics`,
		`SELECT * FROM v_collectors`,
		`SELECT * FROM eventlog_events`,
		`SELECT * FROM target_status; SELECT * FROM collectors`,
		`SELECT readfile('fake-private')`,
		`DELETE FROM target_status`,
	} {
		if result, err := v.QueryShare(context.Background(), query, scope, nil); err == nil {
			data, _ := json.Marshal(result)
			t.Errorf("bypass accepted %s: %s", query, data)
		}
	}
	if _, err := v.QueryShare(context.Background(), `SELECT * FROM system_metrics`, scope, nil); !errors.Is(err, ErrShareDimension) {
		t.Fatal("dimension denial lost", err)
	}
	if _, err := v.QueryShare(context.Background(), `SELECT 1`, ShareScope{Collectors: []string{}}, nil); err == nil {
		t.Fatal("empty filter broadened access")
	}
	if _, err := v.QueryShare(context.Background(), `SELECT 1`, ShareScope{}, map[string]any{"sql": "secret"}); err == nil {
		t.Fatal("extra query argument accepted")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := v.QueryShare(ctx, `SELECT 1`, scope, nil); !errors.Is(err, context.Canceled) {
		t.Fatal("cancellation lost", err)
	}
	result, err := v.QueryShare(context.Background(), `WITH RECURSIVE mine(x) AS(SELECT 1 UNION ALL SELECT x+1 FROM mine WHERE x<10002) SELECT x FROM mine`, scope, nil)
	if err != nil || len(result.Rows) != QueryLimit || !result.Truncated {
		t.Fatal("shared row limit", len(result.Rows), err)
	}
	if _, err := v.QueryShare(context.Background(), `SELECT hex(zeroblob(8500000)) AS value`, ShareScope{}, nil); !errors.Is(err, ErrShareResultSize) {
		t.Fatal("shared byte limit", err)
	}
	if strings.Contains(ErrShareDimension.Error(), "outside") {
		t.Fatal("unexpected secret in public diagnostic")
	}
}
