package store

import (
	"context"
	"database/sql"
	"strings"
	"testing"

	"github.com/LFenX/lg-monitor/internal/pipeline"
)

func ruleQueryFixture(t *testing.T) (*Store, *RuleQueries) {
	t.Helper()
	s := openStore(t, Options{})
	for _, ts := range []int64{1000, 5000} {
		if err := s.Write(context.Background(), pipeline.Message{CollectorID: "fake", Seq: 1, TS: ts, Kind: pipeline.KindProbeResult, ProbeResult: &pipeline.ProbeResult{TargetID: "demo", OK: true, LatencyMS: ts}}); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.Flush(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`CREATE VIEW private_view AS SELECT * FROM probe_results`); err != nil {
		t.Fatal(err)
	}
	q, err := OpenRuleQueries(context.Background(), sPath(t, s))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { q.Close() })
	return s, q
}

func TestRuleQueryCTETimeBoundaryAndSupportedExpressions(t *testing.T) {
	_, q := ruleQueryFixture(t)
	for _, query := range []string{
		`SELECT 'fixed' AS key`,
		`SELECT count(*) AS key FROM probe_results`,
		`WITH own AS (SELECT ts AS key FROM probe_results) SELECT * FROM own ORDER BY key LIMIT 1; -- trailing`,
		`WITH RECURSIVE own(n) AS (SELECT 1 UNION ALL SELECT n+1 FROM own WHERE n<2) SELECT n AS key FROM own`,
		`SELECT p.collector_id AS key FROM probe_results p WHERE p.collector_id='fake' AND p.target_id='demo' AND p.ts=(SELECT max(r.ts) FROM probe_results r WHERE r.collector_id=p.collector_id AND r.target_id=p.target_id)`,
		`SELECT datetime(ts/1000,'unixepoch') AS key FROM probe_results`,
		`SELECT value AS key FROM json_each('[1,2]')`,
		`SELECT :now AS key, 'CURRENT_TIMESTAMP in ordinary text' AS summary`,
	} {
		if _, err := q.QueryRule(context.Background(), query, 2000); err != nil {
			t.Fatalf("supported rule query rejected %s: %v", query, err)
		}
	}
	result, err := q.QueryRule(context.Background(), `SELECT max(ts) AS key FROM probe_results`, 2000)
	if err != nil || len(result.Rows) != 1 || result.Rows[0][0] != int64(1000) {
		t.Fatalf("future data was visible: %+v %v", result, err)
	}
	result, err = q.QueryRule(context.Background(), `SELECT max(ts) AS key FROM probe_results`, 6000)
	if err != nil || result.Rows[0][0] != int64(5000) {
		t.Fatalf("past data did not appear: %+v %v", result, err)
	}
}

func TestRuleQueryAuthorizerRejectsInternalTablesAndSurvivesConnectionRebuild(t *testing.T) {
	s, q := ruleQueryFixture(t)
	for _, query := range []string{
		`SELECT * FROM alerts`, `SELECT count(*) AS key FROM alerts`, `SELECT name AS key FROM sqlite_schema`, `SELECT * FROM pragma_table_info('alerts')`, `SELECT * FROM private_view`, `SELECT last_seen AS key FROM collectors`,
		`SELECT ts AS key FROM main.probe_results`, `SELECT ts AS key FROM "main".probe_results`, `SELECT ts AS key FROM [main].probe_results`,
		"SELECT ts AS key FROM `main`.probe_results",
		`WITH probe_results AS (SELECT * FROM main.probe_results) SELECT ts AS key FROM probe_results`,
	} {
		if _, err := q.QueryRule(context.Background(), query, 2000); err == nil {
			t.Fatalf("forbidden rule query accepted: %s", query)
		}
	}
	q.db.SetMaxIdleConns(0)
	for range 3 {
		if _, err := q.QueryRule(context.Background(), `SELECT count(*) AS key FROM alerts`, 2000); err == nil {
			t.Fatal("new physical connection lost its authorizer")
		}
		if _, err := q.QueryRule(context.Background(), `SELECT 1 AS key`, 2000); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.Write(context.Background(), sampleMessage(99, 1000)); err != nil {
		t.Fatal("rule authorizer affected the writer", err)
	}
	if err := s.Flush(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestRuleQueryUsesIndexesWithoutMaterializingRepeatedCTE(t *testing.T) {
	_, q := ruleQueryFixture(t)
	wrapped, err := ruleSQL(`SELECT p.collector_id AS key FROM probe_results p WHERE p.collector_id='fake' AND p.target_id='demo' AND p.ts=(SELECT max(r.ts) FROM probe_results r WHERE r.collector_id=p.collector_id AND r.target_id=p.target_id)`)
	if err != nil {
		t.Fatal(err)
	}
	rows, err := q.db.QueryContext(context.Background(), "EXPLAIN QUERY PLAN "+wrapped, sql.Named("now", 2000))
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var plan strings.Builder
	for rows.Next() {
		var id, parent, unused int
		var detail string
		if err := rows.Scan(&id, &parent, &unused, &detail); err != nil {
			t.Fatal(err)
		}
		plan.WriteString(detail)
		plan.WriteByte('\n')
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(strings.ToUpper(plan.String()), "MATERIALIZE") || !strings.Contains(plan.String(), "probe_results_target_time") {
		t.Fatalf("rule query plan: %s", plan.String())
	}
}

func TestEveryMonitorTableAndViewHasRulePolicy(t *testing.T) {
	s := openStore(t, Options{})
	rows, err := s.db.Query(`SELECT name FROM sqlite_schema WHERE type IN ('table','view')`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			t.Fatal(err)
		}
		if _, registered := ruleTables[name]; !registered {
			t.Errorf("table/view %s has no rule policy", name)
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
}
