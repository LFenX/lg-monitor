package store

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/LFenX/lg-monitor/internal/config"
	"github.com/LFenX/lg-monitor/internal/pipeline"
	"github.com/LFenX/lg-monitor/internal/rules"
)

type controlNotifications struct{ rows []rules.Alert }

func (n *controlNotifications) Enqueue(_ context.Context, alert rules.Alert) error {
	n.rows = append(n.rows, alert)
	return nil
}

type controlFixture struct {
	t      *testing.T
	db     *Store
	engine *rules.Engine
	now    time.Time
	path   string
	cfg    *config.Config
	sent   controlNotifications
	seq    uint64
}

func newControlFixture(t *testing.T) *controlFixture {
	t.Helper()
	f := &controlFixture{t: t, now: time.UnixMilli(1000000), path: filepath.Join(t.TempDir(), "monitor.db")}
	f.cfg = &config.Config{Targets: []config.Target{{ID: "web", Match: config.Match{Process: "fake.exe"}}}, Rules: []config.Rule{{ID: "down", Type: "process_absent", Target: "web", Renotify: "1m"}}}
	f.reopen()
	t.Cleanup(func() { f.db.Close() })
	return f
}
func (f *controlFixture) reopen() {
	f.t.Helper()
	if f.db != nil {
		if err := f.db.Close(); err != nil {
			f.t.Fatal(err)
		}
	}
	var err error
	f.db, err = Open(context.Background(), Options{Path: f.path, Now: func() time.Time { return f.now }})
	if err != nil {
		f.t.Fatal(err)
	}
	f.engine, err = rules.New(context.Background(), rules.Options{Config: f.cfg, Repository: f.db, Notifications: &f.sent, Now: func() time.Time { return f.now }})
	if err != nil {
		f.t.Fatal(err)
	}
}
func (f *controlFixture) status(up bool) {
	f.t.Helper()
	f.seq++
	state, process := "down", "missing"
	if up {
		state, process = "up", "found"
	}
	if err := f.engine.Handle(context.Background(), pipeline.Message{CollectorID: "fake", Seq: f.seq, TS: f.now.UnixMilli(), Kind: pipeline.KindTargetStatus, TargetStatus: &pipeline.TargetStatus{TargetID: "web", State: state, Process: process}}); err != nil {
		f.t.Fatal(err)
	}
}
func (f *controlFixture) advance(duration time.Duration) {
	f.t.Helper()
	f.now = f.now.Add(duration)
	if err := f.engine.Evaluate(context.Background()); err != nil {
		f.t.Fatal(err)
	}
}

func TestAcknowledgementPersistsStopsRemindersButKeepsRecovery(t *testing.T) {
	f := newControlFixture(t)
	f.status(false)
	id := f.sent.rows[0].ID
	if err := f.db.AcknowledgeAlert(context.Background(), id); err != nil {
		t.Fatal(err)
	}
	f.advance(time.Minute)
	if len(f.sent.rows) != 1 {
		t.Fatal("acknowledged alert sent reminder")
	}
	f.reopen()
	active, err := f.db.ActiveAlerts(context.Background())
	if err != nil || len(active) != 1 || active[0].AcknowledgedAt == 0 {
		t.Fatalf("ack lost after restart: %+v %v", active, err)
	}
	f.status(false)
	f.advance(time.Minute)
	if len(f.sent.rows) != 1 {
		t.Fatal("restart re-enabled acknowledged reminder")
	}
	f.status(true)
	if len(f.sent.rows) != 2 || f.sent.rows[1].State != rules.StateResolved || f.sent.rows[1].ID != id {
		t.Fatalf("ack suppressed recovery: %+v", f.sent.rows)
	}
	f.status(false)
	if len(f.sent.rows) != 3 || f.sent.rows[2].ID == id {
		t.Fatal("new incident inherited acknowledgement")
	}
}

func TestSilenceKeepsStatesAndSurvivesRestartWithoutReplay(t *testing.T) {
	f := newControlFixture(t)
	row, err := f.db.AddSilence(context.Background(), "", "web", 2*time.Hour, "test maintenance")
	if err != nil || row.EndsAt-row.StartsAt != (2*time.Hour).Milliseconds() {
		t.Fatalf("silence: %+v %v", row, err)
	}
	f.status(false)
	if len(f.sent.rows) != 0 {
		t.Fatal("silenced firing sent")
	}
	active, _ := f.db.ActiveAlerts(context.Background())
	if len(active) != 1 {
		t.Fatal("silence hid recorded state")
	}
	f.reopen()
	f.advance(time.Minute)
	f.status(true)
	if len(f.sent.rows) != 0 {
		t.Fatal("silence/restart allowed reminder or recovery")
	}
	pending, err := f.db.PendingNotifications(context.Background(), 0, f.now.UnixMilli(), 0, 100)
	if err != nil || len(pending) != 0 {
		t.Fatalf("suppressed events queued for replay: %+v %v", pending, err)
	}
	f.advance(2 * time.Hour)
	f.status(false)
	if len(f.sent.rows) != 1 {
		t.Fatal("silence did not expire")
	}
}

func TestFourthTriggerProducesOneDigestUntilTenMinutesStable(t *testing.T) {
	f := newControlFixture(t)
	for i := 0; i < 4; i++ {
		f.status(false)
		if i < 3 {
			f.now = f.now.Add(time.Second)
			f.status(true)
			f.now = f.now.Add(time.Second)
		}
	}
	if len(f.sent.rows) != 7 || !f.sent.rows[6].Flapping {
		t.Fatalf("fourth trigger digest: %+v", f.sent.rows)
	}
	f.now = f.now.Add(time.Second)
	f.status(true)
	f.now = f.now.Add(time.Second)
	f.status(false)
	if len(f.sent.rows) != 7 {
		t.Fatal("flapping sent additional cards")
	}
	f.reopen()
	active, err := f.db.ActiveAlerts(context.Background())
	if err != nil || len(active) != 1 || !active[0].Flapping {
		t.Fatalf("flapping state lost: %+v %v", active, err)
	}
	f.status(false)
	if len(f.sent.rows) != 7 {
		t.Fatal("restart sent another digest")
	}
	f.advance(10 * time.Minute)
	if len(f.sent.rows) != 8 || f.sent.rows[7].Flapping {
		t.Fatalf("stable alert failed to resume normal reminders: %+v", f.sent.rows)
	}
	active, _ = f.db.ActiveAlerts(context.Background())
	if active[0].Flapping {
		t.Fatal("stable alert still marked flapping")
	}
}

func TestReadOnlyRuleQueryRejectsWritesMultipleStatementsAndCancelsWork(t *testing.T) {
	s := openStore(t, Options{})
	view, err := OpenView(context.Background(), sPath(t, s))
	if err != nil {
		t.Fatal(err)
	}
	defer view.Close()
	for _, text := range []string{"DELETE FROM alerts", "SELECT 1; SELECT 2", "WITH x AS (SELECT 1) DELETE FROM alerts", "PRAGMA query_only=OFF"} {
		if _, err := view.Query(context.Background(), text); err == nil {
			t.Fatalf("unsafe SQL accepted: %s", text)
		}
	}
	result, err := view.Query(context.Background(), "-- comment\nWITH x(v) AS (SELECT ';') SELECT v AS key FROM x; /* end */")
	if err != nil || len(result.Rows) != 1 || result.Rows[0][0] != ";" {
		t.Fatalf("valid quoted SQL: %+v %v", result, err)
	}
	result, err = view.Query(context.Background(), "WITH RECURSIVE x(n) AS (SELECT 1 UNION ALL SELECT n+1 FROM x WHERE n<10001) SELECT n AS key FROM x")
	if err != nil || len(result.Rows) != 10000 || !result.Truncated {
		t.Fatalf("query row limit: %d %v %v", len(result.Rows), result.Truncated, err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	if _, err := view.Query(ctx, "WITH RECURSIVE x(n) AS (SELECT 1 UNION ALL SELECT n+1 FROM x WHERE n<1000000000) SELECT sum(n) FROM x"); err == nil {
		t.Fatal("long query did not cancel")
	}
	if count(t, s, "alerts") != 0 {
		t.Fatal("read-only tests changed data")
	}
}

func sPath(t *testing.T, s *Store) string {
	t.Helper()
	var seq int
	var name, path string
	if err := s.db.QueryRow(`PRAGMA database_list`).Scan(&seq, &name, &path); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestSQLAlertKeysPersistAsSeparateActiveInstances(t *testing.T) {
	s := openStore(t, Options{})
	for i := 0; i < 2; i++ {
		alert := testAlert()
		alert.ID = fmt.Sprintf("sql-%d", i)
		alert.InstanceKey = fmt.Sprint(i)
		if err := s.SaveTransition(context.Background(), alert, testEvent(alert)); err != nil {
			t.Fatal(err)
		}
	}
	rows, err := s.ActiveAlerts(context.Background())
	if err != nil || len(rows) != 2 || rows[0].InstanceKey == rows[1].InstanceKey {
		t.Fatalf("SQL instances merged: %+v %v", rows, err)
	}
	for _, text := range []string{"/*", "SELECT 'unclosed", "SELECT 1; /* open", "", strings.Repeat("x", 300000)} {
		if _, err := (&View{db: s.db, now: s.now}).Query(context.Background(), text); err == nil {
			t.Fatalf("invalid query accepted: %q", text[:min(len(text), 50)])
		}
	}
}
