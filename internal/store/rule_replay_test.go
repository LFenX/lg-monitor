package store

import (
	"context"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/LFenX/lg-monitor/internal/config"
	"github.com/LFenX/lg-monitor/internal/pipeline"
	"github.com/LFenX/lg-monitor/internal/rules"
)

type replayCapture struct {
	events []rules.ReplayEvent
	sent   int
}

func (*replayCapture) ActiveAlerts(context.Context) ([]rules.Alert, error) { return nil, nil }
func (c *replayCapture) SaveTransition(_ context.Context, alert rules.Alert, event rules.Event) error {
	row := rules.ReplayEvent{Key: alert.InstanceKey, State: event.Kind, TS: event.TS}
	if event.Kind == rules.StateFiring {
		row.StartedAt, row.Summary = alert.StartedAt, alert.Summary
	}
	c.events = append(c.events, row)
	return nil
}
func (c *replayCapture) Enqueue(context.Context, rules.Alert) error { c.sent++; return nil }

func TestSQLReplayMatchesLiveClockAndDoesNotWriteOrSend(t *testing.T) {
	ctx := context.Background()
	s := openStore(t, Options{})
	start := int64(1000000)
	insert := func(offset, latency int64) {
		t.Helper()
		if err := s.Write(ctx, pipeline.Message{CollectorID: "fake", Seq: 1, TS: start + offset, Kind: pipeline.KindProbeResult, ProbeResult: &pipeline.ProbeResult{TargetID: "web", OK: true, LatencyMS: latency}}); err != nil {
			t.Fatal(err)
		}
		if err := s.Flush(ctx); err != nil {
			t.Fatal(err)
		}
	}
	insert(0, 1)
	queries, err := OpenRuleQueries(ctx, sPath(t, s))
	if err != nil {
		t.Fatal(err)
	}
	defer queries.Close()
	query := `SELECT p.collector_id || '/' || p.target_id AS key, '探测耗时 ' || p.latency_ms AS summary FROM probe_results p
WHERE p.ts=(SELECT max(r.ts) FROM probe_results r WHERE r.collector_id=p.collector_id AND r.target_id=p.target_id) AND p.latency_ms>2000`
	before, err := queries.QueryRule(ctx, query, start)
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range []struct{ offset, latency int64 }{{10000, 3000}, {30000, 1}, {40000, 4000}, {60000, 1}} {
		insert(row.offset, row.latency)
	}
	after, err := queries.QueryRule(ctx, query, start)
	if err != nil || !reflect.DeepEqual(before, after) {
		t.Fatalf("future inserts changed past evaluation: %+v %+v %v", before, after, err)
	}
	rule := config.Rule{ID: "slow", Type: "sql", Query: query, Interval: "10s", For: "10s"}
	cfg := &config.Config{Rules: []config.Rule{rule}}
	now := time.UnixMilli(start)
	live := &replayCapture{}
	engine, err := rules.New(ctx, rules.Options{Config: cfg, Repository: live, Notifications: live, Queries: queries, Now: func() time.Time { return now }})
	if err != nil {
		t.Fatal(err)
	}
	for offset := int64(0); offset <= 60000; offset += 10000 {
		now = time.UnixMilli(start + offset)
		if err := engine.Evaluate(ctx); err != nil {
			t.Fatal(err)
		}
	}
	if len(live.events) != 4 || live.events[0].TS != start+20000 || live.events[1].TS != start+30000 || live.events[2].TS != start+50000 || live.events[3].TS != start+60000 || live.sent != 4 {
		t.Fatalf("live SQL lifecycle: %+v sent=%d", live.events, live.sent)
	}
	view, err := OpenView(ctx, sPath(t, s))
	if err != nil {
		t.Fatal(err)
	}
	defer view.Close()
	result, err := rules.Replay(ctx, rules.ReplayOptions{Config: cfg, Rule: rule, Since: time.UnixMilli(start), Until: now, Source: view})
	if err != nil || !result.Complete || !reflect.DeepEqual(result.Events, live.events) || result.Evaluations != 7 || result.Totals.Fired != 2 || result.Totals.Resolved != 2 || result.Totals.FiringAtEnd != 0 {
		t.Fatalf("live/replay mismatch: %+v %v; live=%+v", result, err, live.events)
	}
	for _, table := range []string{"alerts", "alert_events", "notifications"} {
		if count(t, s, table) != 0 {
			t.Fatalf("replay wrote %s", table)
		}
	}
	if live.sent != 4 {
		t.Fatal("replay sent a notification")
	}
}

func TestRuleQueryCapsRowsAndReportsLastSeenAlternative(t *testing.T) {
	_, queries := ruleQueryFixture(t)
	query := `WITH RECURSIVE n(x) AS (SELECT 1 UNION ALL SELECT x+1 FROM n WHERE x<101) SELECT x AS key FROM n`
	if _, err := queries.QueryRule(context.Background(), query, 2000); err == nil {
		t.Fatal("SQL rule accepted 101 rows")
	}
	result, err := queries.QueryRule(context.Background(), query+" LIMIT 100", 2000)
	if err != nil || len(result.Rows) != 100 {
		t.Fatalf("SQL rule rejected 100 rows: %+v %v", result, err)
	}
	if _, err := queries.QueryRule(context.Background(), `SELECT last_seen AS key FROM collectors`, 2000); err == nil || !strings.Contains(err.Error(), "collector_offline") {
		t.Fatalf("missing last_seen alternative: %v", err)
	}
}
