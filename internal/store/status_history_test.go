package store

import (
	"context"
	"encoding/json"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/LFenX/lg-monitor/internal/blueprint"
	"github.com/LFenX/lg-monitor/internal/collector"
	"github.com/LFenX/lg-monitor/internal/config"
	"github.com/LFenX/lg-monitor/internal/pipeline"
)

func TestConfirmedHealthHistoryAndArchiveUseTheOneMinuteBoundary(t *testing.T) {
	ctx := context.Background()
	now := time.UnixMilli(2_000_000_000_000)
	s := openStore(t, Options{Now: func() time.Time { return now }, DisableMaintenance: true})
	var targets []config.Target
	for index, duration := range []int64{59999, 60000, 60001} {
		id := "boundary-" + strconv.Itoa(index)
		target := config.Target{ID: id, Match: config.Match{Process: "fake.exe"}}
		targets = append(targets, target)
		start := now.Add(-10 * time.Minute).UnixMilli()
		for _, point := range []struct {
			at    int64
			state string
		}{{start - 1000, "up"}, {start, "down"}, {start + 20000, "down"}, {start + 40000, "down"}, {start + duration, "up"}} {
			if _, err := s.db.ExecContext(ctx, `INSERT INTO target_status(collector_id,target_id,ts,state,detail) VALUES('fake',?,?,?,'{}')`, id, point.at, point.state); err != nil {
				t.Fatal(err)
			}
		}
		var down int
		if err := s.db.QueryRowContext(ctx, `SELECT SUM(CASE WHEN state='down' THEN 1 ELSE 0 END) FROM (`+HealthSamplesSQL(target, start-1000, now.UnixMilli())+`)`).Scan(&down); err != nil {
			t.Fatal(err)
		}
		want := 3
		if duration < 60000 {
			want = 0
		}
		if down != want {
			t.Fatalf("duration=%d down=%d want=%d", duration, down, want)
		}
	}
	s.SetHealthTargets(targets)
	if err := s.rollupStatus(ctx, now.UnixMilli()); err != nil {
		t.Fatal(err)
	}
	for index, target := range targets {
		var worst, confirmed int64
		if err := s.db.QueryRowContext(ctx, `SELECT MIN(worst),MIN(confirmation_ms) FROM status_hour WHERE target_id=?`, target.ID).Scan(&worst, &confirmed); err != nil {
			t.Fatal(err)
		}
		want := int64(0)
		if index == 0 {
			want = 2
		}
		if worst != want || confirmed != 60000 {
			t.Fatalf("archive target=%s worst=%d confirmation=%d", target.ID, worst, confirmed)
		}
	}
}

func TestTaskStatusFollowsInventoryResults(t *testing.T) {
	ctx := context.Background()
	now := time.UnixMilli(2_000_000_000_000)
	s := openStore(t, Options{Now: func() time.Time { return now }, DisableMaintenance: true})
	exec := func(query string, args ...any) {
		t.Helper()
		if _, err := s.db.ExecContext(ctx, query, args...); err != nil {
			t.Fatal(err)
		}
	}
	at := now.UnixMilli()
	exec(`INSERT INTO inventory_scopes(collector_id,kind,scope,seq,ts,last_success,initialized) VALUES('fake-a','tasks','fake-scope',1,?,?,1),('fake-b','tasks','fake-scope',1,?,?,1),('fake-old','tasks','fake-scope',1,?,?,1)`, at, at, at, at, at-time.Hour.Milliseconds(), at-time.Hour.Milliseconds())
	task := func(collector, id, detail string) {
		exec(`INSERT INTO inventory_current(collector_id,kind,scope,id,name,ts,detail) VALUES(?,'tasks','fake-scope',?,?,?,?)`, collector, id, "FakeTask", at, detail)
	}
	task("fake-a", `\fake\ok`, `{"enabled":true,"state":"ready","lastRun":1,"nextRun":2,"lastResult":0}`)
	task("fake-a", `\fake\failed`, `{"enabled":true,"state":"ready","lastResult":2147942402}`)
	task("fake-a", `\fake\off`, `{"enabled":false,"state":"disabled","lastResult":0}`)
	task("fake-a", `\fake\running`, `{"enabled":true,"state":"running","lastResult":267009}`)
	task("fake-old", `\fake\ok`, `{"enabled":true,"lastResult":0}`)
	s.SetTaskWatches([]TaskWatch{
		{Target: "ok", Collector: "fake-a", Task: `\Fake\OK`},
		{Target: "failed", Collector: "fake-a", Task: `\fake\failed`},
		{Target: "off", Collector: "fake-a", Task: `\fake\off`},
		{Target: "running", Collector: "fake-a", Task: `\fake\running`},
		{Target: "gone", Collector: "fake-a", Task: `\fake\gone`},
		{Target: "elsewhere", Collector: "fake-unseen", Task: `\fake\ok`},
		{Target: "everywhere", Task: `\fake\ok`},
	})
	if err := s.recordTasks(ctx, at); err != nil {
		t.Fatal(err)
	}
	rows, err := s.db.QueryContext(ctx, `SELECT collector_id,target_id,state,detail FROM task_status ORDER BY target_id,collector_id`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var got []string
	for rows.Next() {
		var collector, target, state, raw string
		if err := rows.Scan(&collector, &target, &state, &raw); err != nil {
			t.Fatal(err)
		}
		var detail TaskDetail
		if err := json.Unmarshal([]byte(raw), &detail); err != nil {
			t.Fatal(err)
		}
		got = append(got, target+"@"+collector+"="+state+":"+detail.Reason)
	}
	want := []string{
		"elsewhere@fake-unseen=unknown:还没有读到这台电脑的计划任务",
		"everywhere@fake-a=up:上次运行成功",
		"everywhere@fake-b=down:找不到这个计划任务，可能已被删除或改名",
		"everywhere@fake-old=unknown:这台电脑的计划任务清单超过 10 分钟没有更新",
		"failed@fake-a=down:上次运行失败，结果代码 0x80070002",
		"gone@fake-a=down:找不到这个计划任务，可能已被删除或改名",
		"off@fake-a=down:任务已被禁用",
		"ok@fake-a=up:上次运行成功",
		"running@fake-a=up:正在运行",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("task states:\n%s", strings.Join(got, "\n"))
	}
}

func TestStatusHourCountsRecentHoursAndUsesTaskStates(t *testing.T) {
	ctx := context.Background()
	now := time.UnixMilli(2_000_000_000_000)
	s := openStore(t, Options{Now: func() time.Time { return now }, DisableMaintenance: true})
	hour := floorBucket(now.UnixMilli(), hourMS)
	insert := func(table, target, state string, ts int64) {
		t.Helper()
		query := `INSERT INTO target_status(collector_id,target_id,ts,state,detail) VALUES('fake-a',?,?,?,'{}')`
		if table == "task_status" {
			query = `INSERT INTO task_status(collector_id,target_id,ts,state,detail) VALUES('fake-a',?,?,?,'{}')`
		}
		if _, err := s.db.ExecContext(ctx, query, target, ts, state); err != nil {
			t.Fatal(err)
		}
	}
	for i, state := range []string{"up", "up", "down", "unknown"} {
		insert("target_status", "web", state, hour+int64(i)*1000)
	}
	insert("target_status", "web", "up", hour-hourMS+5)
	// The placeholder match of a task monitor must not count; its recorded task state does.
	insert("target_status", "nightly", "up", hour+1)
	insert("task_status", "nightly", "down", hour+2)
	s.SetTaskWatches([]TaskWatch{{Target: "nightly", Collector: "fake-a", Task: `\fake\nightly`}})
	if err := s.rollupStatus(ctx, now.UnixMilli()); err != nil {
		t.Fatal(err)
	}
	read := func() string {
		rows, err := s.db.QueryContext(ctx, `SELECT target_id,ts,up,total,worst FROM status_hour ORDER BY target_id,ts`)
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		var lines []string
		for rows.Next() {
			var target string
			var ts, up, total, worst int64
			if err := rows.Scan(&target, &ts, &up, &total, &worst); err != nil {
				t.Fatal(err)
			}
			lines = append(lines, strings.Join([]string{target, map[bool]string{true: "now", false: "before"}[ts == hour], strconv.FormatInt(up, 10), strconv.FormatInt(total, 10), strconv.FormatInt(worst, 10)}, " "))
		}
		return strings.Join(lines, "; ")
	}
	if got := read(); got != "nightly now 0 1 0; web before 1 1 2; web now 3 3 1" {
		t.Fatalf("status_hour=%s", got)
	}
	// Recomputing is idempotent, and runs at most every few minutes.
	insert("target_status", "web", "up", hour+10_000)
	if err := s.rollupStatus(ctx, now.UnixMilli()+1000); err != nil {
		t.Fatal(err)
	}
	if got := read(); !strings.HasSuffix(got, "web now 3 3 1") {
		t.Fatalf("a rollup inside the quiet period must not run again: %s", got)
	}
	if err := s.rollupStatus(ctx, now.UnixMilli()+statusRollupEvery.Milliseconds()); err != nil {
		t.Fatal(err)
	}
	if got := read(); !strings.HasSuffix(got, "web now 4 4 1") {
		t.Fatalf("recomputed hour=%s", got)
	}
}

func TestTaskRuleQueryRunsUnderRuleRestrictions(t *testing.T) {
	ctx := context.Background()
	s, q := ruleQueryFixture(t)
	now := int64(2_000_000_000_000)
	for _, row := range []struct {
		collector, target, state, reason string
		age                              int64
	}{
		{"fake-a", "nightly", "up", "上次运行成功", 120_000},
		{"fake-a", "nightly", "down", "上次运行失败，结果代码 0x80070002", 60_000},
		{"fake-b", "nightly", "down", "任务已被禁用", 120_000},
		{"fake-b", "nightly", "up", "上次运行成功", 60_000},
		{"fake-a", "stale", "down", "找不到这个计划任务", 20 * 60_000},
	} {
		if _, err := s.db.ExecContext(ctx, `INSERT INTO task_status(collector_id,target_id,ts,state,detail) VALUES(?,?,?,?,json_object('reason',?))`, row.collector, row.target, now-row.age, row.state, row.reason); err != nil {
			t.Fatal(err)
		}
	}
	result, err := q.QueryRule(ctx, blueprint.TaskRuleQuery("nightly"), now)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Rows) != 1 || result.Rows[0][0] != "fake-a" || !strings.Contains(result.Rows[0][1].(string), "0x80070002") {
		t.Fatalf("only the newest down state fires: %+v", result.Rows)
	}
	// A state that went quiet says nothing, so it does not fire.
	if result, err := q.QueryRule(ctx, blueprint.TaskRuleQuery("stale"), now); err != nil || len(result.Rows) != 0 {
		t.Fatalf("stale task state fired: %+v %v", result, err)
	}
}

func TestFirstTaskInventoryIsJudgedWithoutWaitingForTheMinute(t *testing.T) {
	clock := &testClock{created: make(chan *testTicker, 2)}
	now := time.UnixMilli(2_000_000_000_000)
	s := openStore(t, Options{Now: func() time.Time { return now }, Clock: clock})
	s.SetTaskWatches([]TaskWatch{{Target: "nightly", Collector: "fake-a", Task: `\fake\nightly`}})
	entry := collector.InventoryEntry{ID: `\fake\nightly`, Name: "FakeNightly", State: "ready", Enabled: true, LastRun: 1}
	message := pipeline.Message{CollectorID: "fake-a", Seq: 1, TS: now.UnixMilli(), Kind: pipeline.KindInventory, Inventory: &collector.InventorySnapshot{Kind: "tasks", Scope: "machine", Epoch: "0123456789abcdef", Entries: []collector.InventoryEntry{entry}}}
	if err := s.Write(context.Background(), message); err != nil {
		t.Fatal(err)
	}
	if err := s.Flush(context.Background()); err != nil {
		t.Fatal(err)
	}
	// No minute tick is sent: the state must still appear shortly after the inventory.
	deadline := time.Now().Add(5 * time.Second)
	for count(t, s, "task_status") == 0 {
		if time.Now().After(deadline) {
			t.Fatal("the first task inventory was not judged until the next minute")
		}
		time.Sleep(20 * time.Millisecond)
	}
}
