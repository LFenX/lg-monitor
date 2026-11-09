package store

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/LFenX/lg-monitor/internal/collector"
	"github.com/LFenX/lg-monitor/internal/config"
	"github.com/LFenX/lg-monitor/internal/pipeline"
)

func exportPoint(snapshot ExportMetricSnapshot, name string, labels map[string]string) (ExportMetricPoint, bool) {
	for _, family := range snapshot.Families {
		if family.Name != "lgmon_"+name {
			continue
		}
		for _, point := range family.Points {
			match := true
			for key, value := range labels {
				if point.Labels[key] != value {
					match = false
				}
			}
			if match {
				return point, true
			}
		}
	}
	return ExportMetricPoint{}, false
}

func TestExportMetricsReadsLatestCommittedFactsAndOmitsStaleProcesses(t *testing.T) {
	ctx := context.Background()
	now := func() time.Time { return time.UnixMilli(5000) }
	s := openStore(t, Options{Now: now, DisableMaintenance: true})
	messages := collectionMessages(2000)
	for _, m := range messages {
		if err := s.Write(ctx, m); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.Flush(ctx); err != nil {
		t.Fatal(err)
	}
	status := pipeline.TargetStatus{State: collector.StateUp, Processes: []pipeline.ProcessRef{{PID: 42, CreatedAt: 1000, Name: "fake.exe"}}}
	up, err := json.Marshal(status)
	if err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{
		`UPDATE collectors SET hostname='fake-host',version='fake-v1',last_seen=4000 WHERE id='fake-more'`,
		`INSERT INTO collectors(id,hostname,first_seen,last_seen,version) VALUES ('other','other',1,100,'v1')`,
		`INSERT INTO target_status VALUES ('fake-more','down',3000,'down','{}'),('fake-more','unknown',3000,'unknown','{}'),('fake-more','deleted',3000,'up','{}')`,
		`INSERT INTO process_samples(collector_id,target_id,ts,pid,cpu,rss,created_at) VALUES ('fake-more','down',4000,42,99,999,1000),('fake-more','unknown',4000,42,99,999,1000),('fake-more','web',3500,42,99,999,500),('fake-more','fresh',3500,42,50,4096,1000)`,
		`INSERT INTO alerts(id,collector_id,severity,state) VALUES ('fake-firing','fake-more','critical','firing'),('fake-resolved','fake-more','critical','resolved'),('fake-global','','critical','firing')`,
	} {
		if _, err := s.db.ExecContext(ctx, q); err != nil {
			t.Fatal(err)
		}
	}
	for _, id := range []string{"web", "fresh"} {
		if _, err := s.db.ExecContext(ctx, `INSERT INTO target_status VALUES ('fake-more',?,3000,'up',?)`, id, string(up)); err != nil {
			t.Fatal(err)
		}
	}
	view, err := OpenViewWithClock(ctx, s.maintenanceView.path, now)
	if err != nil {
		t.Fatal(err)
	}
	defer view.Close()
	targets := []config.Target{{ID: "backup", Collector: "FAKE-HOST", Probes: []config.Probe{{Type: "script"}}}, {ID: "down", Collector: "fake-more"}, {ID: "unknown", Collector: "fake-more"}, {ID: "web", Collector: "fake-more"}, {ID: "fresh", Collector: "fake-more"}}
	snapshot, err := view.ExportMetrics(ctx, targets)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, target string
		value        float64
	}{{"system_cpu_ratio", "", .75}, {"system_memory_used_bytes", "", 200}, {"disk_used_ratio", "", .5}, {"network_send_bytes_per_second", "", 25}, {"target_state", "unknown", -1}, {"target_state", "down", 0}, {"target_process_count", "down", 0}, {"target_cpu_ratio", "fresh", .5}, {"target_rss_bytes", "fresh", 4096}, {"probe_success", "backup", 1}, {"script_value", "backup", 2}, {"service_state", "", 1}, {"collector_alerts_active", "", 1}} {
		labels := map[string]string{"collector_id": "fake-more"}
		if tc.target != "" {
			labels["target_id"] = tc.target
		}
		if tc.name == "script_value" {
			labels["metric"] = "backup_age_hours"
		}
		if tc.name == "collector_alerts_active" {
			labels["severity"] = "critical"
		}
		point, found := exportPoint(snapshot, tc.name, labels)
		if !found || point.Value != tc.value {
			t.Fatal("wrong persisted metric or unit", tc, point, found)
		}
	}
	if point, found := exportPoint(snapshot, "alerts_active", map[string]string{"severity": "critical"}); !found || point.Value != 2 {
		t.Fatal("global rule alerts were lost", point, found)
	}
	for _, id := range []string{"down", "unknown", "web", "deleted"} {
		if _, found := exportPoint(snapshot, "target_cpu_ratio", map[string]string{"collector_id": "fake-more", "target_id": id}); found {
			t.Fatal("stale, removed, or unknown process leaked", id)
		}
	}
	if point, found := exportPoint(snapshot, "system_observed_timestamp_seconds", map[string]string{"collector_id": "fake-more"}); !found || point.Value != 2 || point.TS != 2000 || snapshot.TS != 5000 {
		t.Fatal("source observation replaced by export time", point, snapshot.TS)
	}
	if _, found := exportPoint(snapshot, "probe_success", map[string]string{"collector_id": "other", "target_id": "backup"}); found {
		t.Fatal("hostname assignment leaked to another collector")
	}
	encoded, _ := json.Marshal(snapshot)
	if strings.Contains(string(encoded), "fake.exe") || strings.Contains(string(encoded), "PID") {
		t.Fatal("process identity details became metric labels")
	}
	if _, err := view.db.ExecContext(ctx, `DELETE FROM collectors`); err == nil {
		t.Fatal("export connection can write")
	}
	rows, err := s.db.QueryContext(ctx, `EXPLAIN QUERY PLAN SELECT ts FROM probe_results WHERE collector_id=? AND target_id=? AND probe=? ORDER BY ts DESC LIMIT 1`, "fake-more", "backup", "0")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var plan string
	for rows.Next() {
		var id, parent, unused int
		var detail string
		if err := rows.Scan(&id, &parent, &unused, &detail); err != nil {
			t.Fatal(err)
		}
		plan += detail
	}
	if !strings.Contains(plan, "probe_results_export_latest") {
		t.Fatal("latest probe lookup scans unrelated histories", plan)
	}
}

func TestExportMetricsTwentyCollectorsFiftyTargetsWithoutCrossAssignment(t *testing.T) {
	ctx := context.Background()
	s := openStore(t, Options{DisableMaintenance: true})
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	var targets []config.Target
	for j := 0; j < 50; j++ {
		targets = append(targets, config.Target{ID: fmt.Sprintf("target-%02d", j), Match: config.Match{Process: "fake.exe"}, Probes: []config.Probe{{Type: "tcp"}}})
	}
	for i := 0; i < 20; i++ {
		cid := fmt.Sprintf("fake-collector-%02d", i)
		if _, err := tx.ExecContext(ctx, `INSERT INTO collectors(id,hostname,last_seen) VALUES (?,?,1000)`, cid, cid); err != nil {
			t.Fatal(err)
		}
		for _, target := range targets {
			if _, err := tx.ExecContext(ctx, `INSERT INTO target_status VALUES (?,?,1000,'up','{"processes":[{"pid":42,"createdAt":500,"name":"fake.exe"}]}')`, cid, target.ID); err != nil {
				t.Fatal(err)
			}
			if _, err := tx.ExecContext(ctx, `INSERT INTO process_samples(collector_id,target_id,ts,pid,cpu,rss,created_at) VALUES (?,?,1000,42,25,4096,500)`, cid, target.ID); err != nil {
				t.Fatal(err)
			}
			if _, err := tx.ExecContext(ctx, `INSERT INTO probe_results(collector_id,target_id,probe,ts,ok,latency_ms) VALUES (?,?,'0',1000,1,125)`, cid, target.ID); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	view, err := OpenView(ctx, s.maintenanceView.path)
	if err != nil {
		t.Fatal(err)
	}
	defer view.Close()
	snapshot, err := view.ExportMetrics(ctx, targets)
	if err != nil {
		t.Fatal(err)
	}
	states := 0
	processes, probes := 0, 0
	for _, family := range snapshot.Families {
		if family.Name == "lgmon_target_state" {
			states = len(family.Points)
		}
		if family.Name == "lgmon_target_cpu_ratio" {
			processes = len(family.Points)
			for _, p := range family.Points {
				if p.Value != .25 {
					t.Fatal("scale CPU changed unit", p)
				}
			}
		}
		if family.Name == "lgmon_probe_latency_seconds" {
			probes = len(family.Points)
			for _, p := range family.Points {
				if p.Value != .125 {
					t.Fatal("scale probe latency changed unit", p)
				}
			}
		}
	}
	if states != 1000 || processes != 1000 || probes != 1000 {
		t.Fatal("20x50 export lost state, process or probe", states, processes, probes)
	}
}
