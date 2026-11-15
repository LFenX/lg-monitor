package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"time"

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
	detail.Reason = config.TaskResultText(entry.LastResult)
	if config.TaskResultOK(entry.LastResult) {
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
	s.rollupMu.Unlock()
	if !due {
		return nil
	}
	tasks := map[string]bool{}
	for _, watch := range s.taskWatches() {
		tasks[watch.Target] = true
	}
	s.retentionMu.RLock()
	raw := s.retention.Raw
	s.retentionMu.RUnlock()
	err := s.maintenanceOperation(ctx, func(ctx context.Context, tx *sql.Tx) error {
		start := floorBucket(now-3*hourMS, hourMS)
		var seeded bool
		if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM status_hour)`).Scan(&seeded); err != nil {
			return fmt.Errorf("检查小时状态汇总失败：%w", err)
		}
		if !seeded {
			start = floorBucket(now-raw.Milliseconds(), hourMS) + hourMS
		}
		excluded, args := "", []any{start}
		if len(tasks) > 0 {
			marks := make([]string, 0, len(tasks))
			for target := range tasks {
				marks, args = append(marks, "?"), append(args, target)
			}
			excluded = " AND target_id NOT IN (" + strings.Join(marks, ",") + ")"
		}
		args = append(args, start)
		if _, err := tx.ExecContext(ctx, `DELETE FROM status_hour WHERE ts>=?`, start); err != nil {
			return fmt.Errorf("替换小时状态汇总失败：%w", err)
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO status_hour(collector_id,target_id,ts,up,total,worst)
SELECT collector_id,target_id,ts/3600000*3600000,
       sum(CASE WHEN state='up' THEN 1 ELSE 0 END),
       sum(CASE WHEN state IN ('up','down') THEN 1 ELSE 0 END),
       min(CASE state WHEN 'down' THEN 0 WHEN 'up' THEN 2 ELSE 1 END)
FROM (SELECT collector_id,target_id,ts,state FROM target_status WHERE ts>=?`+excluded+`
      UNION ALL SELECT collector_id,target_id,ts,state FROM task_status WHERE ts>=?)
GROUP BY 1,2,3`, args...); err != nil {
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
