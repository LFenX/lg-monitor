// Copyright (c) 2026 LFen (https://lfen.ink) — lg-monitor

package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/LFenX/lg-monitor/internal/blueprint"
	"github.com/LFenX/lg-monitor/internal/config"
)

// TaskWatch is a monitor that follows one scheduled task in a collector's task inventory.
type TaskWatch struct {
	Target    string
	Collector string // empty means every collector that reports a task inventory
	Task      string
}

// TaskDetail is what task_status.detail holds: enough to explain the state, nothing about task actions.
type TaskDetail struct {
	Reason     string  `json:"reason"`
	Name       string  `json:"name,omitempty"`
	Enabled    *bool   `json:"enabled,omitempty"`
	LastRun    int64   `json:"lastRun,omitempty"`
	NextRun    int64   `json:"nextRun,omitempty"`
	LastResult *uint32 `json:"lastResult,omitempty"`
}

type taskEntry struct {
	Enabled    bool   `json:"enabled"`
	State      string `json:"state"`
	LastRun    int64  `json:"lastRun"`
	NextRun    int64  `json:"nextRun"`
	LastResult uint32 `json:"lastResult"`
}

type taskScope struct {
	lastSuccess int64
	initialized bool
	failed      bool
}

// A task list this old no longer says anything about the task: the collector stopped reporting.
const taskInventoryStale = 10 * time.Minute

// ConfirmedSamplesSQL keeps current observations raw while history ignores short failure runs.
// Callers aggregate the returned samples, so the read-only result limit cannot discard a brief run.
func ConfirmedSamplesSQL(observations string, gap int64, until int64) string {
	return fmt.Sprintf(`WITH failure_samples AS (%s),
failure_ordered AS (SELECT *, LAG(state) OVER (PARTITION BY collector_id,target_id ORDER BY ts) AS prev_state,
 LAG(ts) OVER (PARTITION BY collector_id,target_id ORDER BY ts) AS prev_ts,
 LEAD(ts,1,%d) OVER (PARTITION BY collector_id,target_id ORDER BY ts) AS next_ts FROM failure_samples),
failure_marked AS (SELECT *, SUM(CASE WHEN state=prev_state AND ts-prev_ts<=%d THEN 0 ELSE 1 END)
 OVER (PARTITION BY collector_id,target_id ORDER BY ts) AS failure_run FROM failure_ordered),
failure_held AS (SELECT *, MIN(ts) OVER (PARTITION BY collector_id,target_id,failure_run) AS run_start,
 MAX(MIN(next_ts,ts+%d)) OVER (PARTITION BY collector_id,target_id,failure_run) AS run_end FROM failure_marked)
SELECT collector_id,target_id,ts,CASE WHEN state='down' AND run_end-run_start<%d THEN 'up' ELSE state END AS state
FROM failure_held`, observations, until, gap, gap, config.FailureConfirmation.Milliseconds())
}

func HealthGap(target config.Target) time.Duration {
	gap := 30 * time.Second
	if target.Task != "" {
		gap = 3 * time.Minute
	}
	for _, probe := range target.Probes {
		if interval, err := probe.Interval.Parse(); err == nil {
			gap = max(gap, 3*interval)
		}
	}
	return gap
}

func HealthSamplesSQL(target config.Target, from, until int64) string {
	condition := fmt.Sprintf("target_id='%s' AND ts>=%d AND ts<=%d", strings.ReplaceAll(target.ID, "'", "''"), from, until)
	table, state, group := "target_status", "state", ""
	if target.Task != "" {
		table = "task_status"
	}
	if blueprint.ProbeOnly(target) {
		table = "probe_results"
		state = "CASE MIN(ok) WHEN 1 THEN 'up' ELSE 'down' END AS state"
		group = " GROUP BY collector_id,target_id,ts"
	}
	observations := `SELECT collector_id,target_id,ts,` + state + ` FROM (` + MonitorSampleRangeSQL(table, condition) + `)` + group
	return ConfirmedSamplesSQL(observations, HealthGap(target).Milliseconds(), until)
}

// MonitorSampleRangeSQL seeks to each collector's rows using the existing
// (collector_id, target_id, ts) index. Walking distinct collector prefixes also
// finds retained samples from collectors that no longer appear in the registry.
func MonitorSampleRangeSQL(source, condition string) string {
	return `WITH RECURSIVE sampled_collectors(id) AS (
 SELECT MIN(collector_id) FROM ` + source + `
 UNION ALL
 SELECT (SELECT MIN(collector_id) FROM ` + source + ` WHERE collector_id>sampled_collectors.id)
 FROM sampled_collectors WHERE id IS NOT NULL
)
SELECT samples.* FROM sampled_collectors CROSS JOIN ` + source + ` AS samples
WHERE samples.collector_id=sampled_collectors.id AND ` + condition
}

// VisitMonitorSamples reads a history source once in per-(collector, target) time order via covering indexes.
// The caller summarizes the stream without going through the row limit for user-supplied SQL queries.
func (v *View) VisitMonitorSamples(ctx context.Context, source string, ids []string, from, until int64, visit func(string, string, int64, string)) error {
	if len(ids) == 0 {
		return nil
	}
	unique := slices.Clone(ids)
	slices.Sort(unique)
	unique = slices.Compact(unique)
	filter, err := json.Marshal(unique)
	if err != nil {
		return fmt.Errorf("编码监控历史目标失败：%w", err)
	}
	collectorsCTE := `WITH RECURSIVE sampled_collectors(id) AS (
 SELECT MIN(collector_id) FROM ` + source + `
 UNION ALL
 SELECT (SELECT MIN(collector_id) FROM ` + source + ` WHERE collector_id>sampled_collectors.id)
 FROM sampled_collectors WHERE id IS NOT NULL
) `
	switch source {
	case "target_status", "task_status":
		query := collectorsCTE + `SELECT samples.target_id, samples.collector_id, samples.ts, samples.state
FROM sampled_collectors
CROSS JOIN json_each(?) AS wanted
CROSS JOIN ` + source + ` AS samples
WHERE samples.collector_id = sampled_collectors.id
  AND samples.target_id = wanted.value
  AND samples.ts >= ? AND samples.ts <= ?`
		rows, err := v.db.QueryContext(ctx, query, string(filter), from, until)
		if err != nil {
			return fmt.Errorf("查询监控历史采样失败：%w", err)
		}
		defer rows.Close()
		for rows.Next() {
			var target, collector, state string
			var at int64
			if err := rows.Scan(&target, &collector, &at, &state); err != nil {
				return fmt.Errorf("读取监控历史采样失败：%w", err)
			}
			visit(target, collector, at, state)
		}
		if err := rows.Err(); err != nil {
			return fmt.Errorf("遍历监控历史采样失败：%w", err)
		}
		return nil
	case "probe_results":
		query := collectorsCTE + `SELECT samples.target_id, samples.collector_id, samples.ts, samples.ok
FROM sampled_collectors
CROSS JOIN json_each(?) AS wanted
CROSS JOIN probe_results AS samples
WHERE samples.collector_id = sampled_collectors.id
  AND samples.target_id = wanted.value
  AND samples.ts >= ? AND samples.ts <= ?`
		rows, err := v.db.QueryContext(ctx, query, string(filter), from, until)
		if err != nil {
			return fmt.Errorf("查询监控历史采样失败：%w", err)
		}
		defer rows.Close()
		var have bool
		var curTarget, curCollector string
		var curTS int64
		var curOK int64
		flush := func() {
			if !have {
				return
			}
			state := "down"
			if curOK == 1 {
				state = "up"
			}
			visit(curTarget, curCollector, curTS, state)
			have = false
		}
		for rows.Next() {
			var target, collector string
			var at, okVal int64
			if err := rows.Scan(&target, &collector, &at, &okVal); err != nil {
				return fmt.Errorf("读取监控历史采样失败：%w", err)
			}
			if have && target == curTarget && collector == curCollector && at == curTS {
				if okVal == 0 {
					curOK = 0
				}
				continue
			}
			flush()
			curTarget, curCollector, curTS, curOK, have = target, collector, at, okVal, true
		}
		flush()
		if err := rows.Err(); err != nil {
			return fmt.Errorf("遍历监控历史采样失败：%w", err)
		}
		return nil
	default:
		return fmt.Errorf("无效的监控历史来源 %q", source)
	}
}

// VisitTargetSamples streams one target's samples in ascending time order per collector using covering indexes.
func (v *View) VisitTargetSamples(ctx context.Context, source, targetID string, from, until int64, visit func(string, int64, string)) error {
	if targetID == "" {
		return nil
	}
	collectorsCTE := `WITH RECURSIVE sampled_collectors(id) AS (
 SELECT MIN(collector_id) FROM ` + source + `
 UNION ALL
 SELECT (SELECT MIN(collector_id) FROM ` + source + ` WHERE collector_id>sampled_collectors.id)
 FROM sampled_collectors WHERE id IS NOT NULL
) `
	switch source {
	case "target_status", "task_status":
		query := collectorsCTE + `SELECT samples.collector_id, samples.ts, samples.state
FROM sampled_collectors
CROSS JOIN ` + source + ` AS samples
WHERE samples.collector_id = sampled_collectors.id
  AND samples.target_id = ?
  AND samples.ts >= ? AND samples.ts <= ?`
		rows, err := v.db.QueryContext(ctx, query, targetID, from, until)
		if err != nil {
			return fmt.Errorf("查询目标历史采样失败：%w", err)
		}
		defer rows.Close()
		for rows.Next() {
			var collector, state string
			var at int64
			if err := rows.Scan(&collector, &at, &state); err != nil {
				return fmt.Errorf("读取目标历史采样失败：%w", err)
			}
			visit(collector, at, state)
		}
		if err := rows.Err(); err != nil {
			return fmt.Errorf("遍历目标历史采样失败：%w", err)
		}
		return nil
	case "probe_results":
		query := collectorsCTE + `SELECT samples.collector_id, samples.ts, samples.ok
FROM sampled_collectors
CROSS JOIN probe_results AS samples
WHERE samples.collector_id = sampled_collectors.id
  AND samples.target_id = ?
  AND samples.ts >= ? AND samples.ts <= ?`
		rows, err := v.db.QueryContext(ctx, query, targetID, from, until)
		if err != nil {
			return fmt.Errorf("查询目标探测历史采样失败：%w", err)
		}
		defer rows.Close()
		var have bool
		var curCollector string
		var curTS, curOK int64
		flush := func() {
			if !have {
				return
			}
			state := "down"
			if curOK == 1 {
				state = "up"
			}
			visit(curCollector, curTS, state)
			have = false
		}
		for rows.Next() {
			var collector string
			var at, okVal int64
			if err := rows.Scan(&collector, &at, &okVal); err != nil {
				return fmt.Errorf("读取目标探测历史采样失败：%w", err)
			}
			if have && collector == curCollector && at == curTS {
				if okVal == 0 {
					curOK = 0
				}
				continue
			}
			flush()
			curCollector, curTS, curOK, have = collector, at, okVal, true
		}
		flush()
		if err := rows.Err(); err != nil {
			return fmt.Errorf("遍历目标探测历史采样失败：%w", err)
		}
		return nil
	default:
		return fmt.Errorf("无效的监控历史来源 %q", source)
	}
}

// TargetSampleReason fetches the reason recorded at one specific failure sample without scanning healthy rows.
func (v *View) TargetSampleReason(ctx context.Context, source, collectorID, targetID string, ts int64) (string, error) {
	var query string
	switch source {
	case "target_status", "task_status":
		query = `SELECT COALESCE(json_extract(detail, '$.reason'), '') FROM ` + source + ` WHERE collector_id=? AND target_id=? AND ts=? LIMIT 1`
	case "probe_results":
		query = `SELECT COALESCE(MAX(COALESCE(error, '')), '') FROM probe_results WHERE collector_id=? AND target_id=? AND ts=?`
	default:
		return "", fmt.Errorf("无效的监控历史来源 %q", source)
	}
	var reason string
	err := v.db.QueryRowContext(ctx, query, collectorID, targetID, ts).Scan(&reason)
	if err == sql.ErrNoRows {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("读取采样原因失败：%w", err)
	}
	return reason, nil
}

func (s *Store) SetHealthTargets(targets []config.Target) {
	s.tasksMu.Lock()
	s.healthTargets = slices.Clone(targets)
	s.tasksMu.Unlock()
	s.rollupMu.Lock()
	s.lastRollup = 0
	s.rollupMu.Unlock()
}

// statusRollupEvery spaces out the hourly recomputation; the newest hour only has to be roughly current.
const statusRollupEvery = 5 * time.Minute

func (s *Store) SetTaskWatches(items []TaskWatch) {
	s.tasksMu.Lock()
	s.tasks = slices.Clone(items)
	s.tasksMu.Unlock()
}

func (s *Store) taskWatches() []TaskWatch {
	s.tasksMu.RLock()
	defer s.tasksMu.RUnlock()
	return slices.Clone(s.tasks)
}

// evaluateTask turns one inventory observation into a monitor state with a plain reason.
func evaluateTask(entry *taskEntry, name string, scope taskScope, found bool, now int64) (string, TaskDetail) {
	switch {
	case !scope.initialized && !found:
		return "unknown", TaskDetail{Reason: "还没有读到这台电脑的计划任务"}
	case scope.lastSuccess > 0 && now-scope.lastSuccess > taskInventoryStale.Milliseconds():
		return "unknown", TaskDetail{Reason: "这台电脑的计划任务清单超过 10 分钟没有更新"}
	case !found && scope.failed:
		return "unknown", TaskDetail{Reason: "读取这台电脑的计划任务失败"}
	case !found:
		return "down", TaskDetail{Reason: "找不到这个计划任务，可能已被删除或改名"}
	}
	enabled, result := entry.Enabled, entry.LastResult
	detail := TaskDetail{Name: name, Enabled: &enabled, LastRun: entry.LastRun, NextRun: entry.NextRun, LastResult: &result}
	if !entry.Enabled {
		detail.Reason = "任务已被禁用"
		return "down", detail
	}
	detail.Reason = config.TaskStatusText(entry.State, entry.LastResult)
	if config.TaskHealthy(entry.State, entry.LastResult) {
		return "up", detail
	}
	return "down", detail
}

func (s *Store) recordTasks(ctx context.Context, now int64) error {
	watches := s.taskWatches()
	if len(watches) == 0 {
		return nil
	}
	return s.maintenanceOperation(ctx, func(ctx context.Context, tx *sql.Tx) error {
		scopes := map[string]taskScope{}
		rows, err := tx.QueryContext(ctx, `SELECT collector_id,last_success,initialized,error_code FROM inventory_scopes WHERE kind='tasks'`)
		if err != nil {
			return fmt.Errorf("读取计划任务清单状态失败：%w", err)
		}
		for rows.Next() {
			var collector, code string
			var success int64
			var initialized bool
			if err := rows.Scan(&collector, &success, &initialized, &code); err != nil {
				rows.Close()
				return fmt.Errorf("读取计划任务清单状态字段失败：%w", err)
			}
			current := scopes[collector]
			current.lastSuccess = max(current.lastSuccess, success)
			current.initialized = current.initialized || initialized
			current.failed = current.failed || code != ""
			scopes[collector] = current
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return fmt.Errorf("遍历计划任务清单状态失败：%w", err)
		}
		insert, err := tx.PrepareContext(ctx, `INSERT INTO task_status(collector_id,target_id,ts,state,detail) VALUES(?,?,?,?,?) ON CONFLICT(collector_id,target_id,ts) DO UPDATE SET state=excluded.state,detail=excluded.detail`)
		if err != nil {
			return fmt.Errorf("准备计划任务状态写入失败：%w", err)
		}
		defer insert.Close()
		for _, watch := range watches {
			collectors := []string{watch.Collector}
			if watch.Collector == "" {
				collectors = collectors[:0]
				for collector := range scopes {
					collectors = append(collectors, collector)
				}
				slices.Sort(collectors)
			}
			for _, collector := range collectors {
				var raw, name string
				err := tx.QueryRowContext(ctx, `SELECT detail,name FROM inventory_current WHERE collector_id=? AND kind='tasks' AND id=? LIMIT 1`, collector, config.TaskID(watch.Task)).Scan(&raw, &name)
				found := err == nil
				if err != nil && err != sql.ErrNoRows {
					return fmt.Errorf("读取计划任务 %s 失败：%w", watch.Task, err)
				}
				var entry taskEntry
				if found && json.Unmarshal([]byte(raw), &entry) != nil {
					found = false
				}
				state, detail := evaluateTask(&entry, name, scopes[collector], found, now)
				data, _ := json.Marshal(detail)
				if _, err := insert.ExecContext(ctx, collector, watch.Target, now, state, string(data)); err != nil {
					return fmt.Errorf("保存计划任务状态失败：%w", err)
				}
			}
		}
		return nil
	})
}

// rollupStatus rebuilds the hourly health of recent hours from raw states, so 30-day bars
// outlive the 24 hours of raw data. Task monitors use their recorded task state instead of
// the collector's view of their placeholder match.
func (s *Store) rollupStatus(ctx context.Context, now int64) error {
	s.rollupMu.Lock()
	due := now-s.lastRollup >= statusRollupEvery.Milliseconds()
	firstRun := s.lastRollup == 0
	s.rollupMu.Unlock()
	if !due {
		return nil
	}
	tasks := map[string]bool{}
	for _, watch := range s.taskWatches() {
		tasks[watch.Target] = true
	}
	s.tasksMu.RLock()
	healthTargets := slices.Clone(s.healthTargets)
	s.tasksMu.RUnlock()
	s.retentionMu.RLock()
	raw := s.retention.Raw
	s.retentionMu.RUnlock()
	err := s.maintenanceOperation(ctx, func(ctx context.Context, tx *sql.Tx) error {
		start := floorBucket(now-3*hourMS, hourMS)
		var seeded bool
		if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM status_hour)`).Scan(&seeded); err != nil {
			return fmt.Errorf("检查小时状态汇总失败：%w", err)
		}
		if !seeded || firstRun {
			start = floorBucket(now-raw.Milliseconds(), hourMS) + hourMS
		}
		excluded := ""
		if len(tasks) > 0 {
			marks := make([]string, 0, len(tasks))
			for target := range tasks {
				marks = append(marks, "'"+strings.ReplaceAll(target, "'", "''")+"'")
			}
			excluded = " AND target_id NOT IN (" + strings.Join(marks, ",") + ")"
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM status_hour WHERE ts>=?`, start); err != nil {
			return fmt.Errorf("替换小时状态汇总失败：%w", err)
		}
		lookback := start - 4*time.Minute.Milliseconds()
		var sources []string
		activeFilter := ""
		if len(healthTargets) > 0 {
			var ids []string
			for _, target := range healthTargets {
				ids = append(ids, "'"+strings.ReplaceAll(target.ID, "'", "''")+"'")
				sources = append(sources, "SELECT * FROM ("+HealthSamplesSQL(target, lookback, now)+")")
			}
			activeFilter = " AND target_id NOT IN (" + strings.Join(ids, ",") + ")"
		}
		// Retired monitors still own history even though they no longer appear in the active configuration.
		for _, source := range []struct {
			table, filter string
			gap           time.Duration
		}{{"target_status", excluded + activeFilter, 30 * time.Second}, {"task_status", activeFilter, 3 * time.Minute}} {
			observations := fmt.Sprintf("SELECT collector_id,target_id,ts,state FROM %s WHERE ts>=%d AND ts<=%d%s", source.table, lookback, now, source.filter)
			sources = append(sources, "SELECT * FROM ("+ConfirmedSamplesSQL(observations, source.gap.Milliseconds(), now)+")")
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO status_hour(collector_id,target_id,ts,up,total,worst,confirmation_ms)
SELECT collector_id,target_id,ts/3600000*3600000,
       sum(CASE WHEN state='up' THEN 1 ELSE 0 END),
       sum(CASE WHEN state IN ('up','down') THEN 1 ELSE 0 END),
       min(CASE state WHEN 'down' THEN 0 WHEN 'up' THEN 2 ELSE 1 END),60000
FROM (`+strings.Join(sources, " UNION ALL ")+`) WHERE ts>=?
GROUP BY 1,2,3`, start); err != nil {
			return fmt.Errorf("计算小时状态汇总失败：%w", err)
		}
		return nil
	})
	if err == nil {
		s.rollupMu.Lock()
		s.lastRollup = now
		s.rollupMu.Unlock()
	}
	return err
}
