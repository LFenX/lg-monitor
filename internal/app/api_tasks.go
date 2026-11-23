// Copyright (c) 2026 LFen (https://lfen.ink) — lg-monitor

package app

import (
	"context"
	"encoding/json"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/LFenX/lg-monitor/internal/api"
	"github.com/LFenX/lg-monitor/internal/config"
	"github.com/LFenX/lg-monitor/internal/store"
)

// exploreTasks lists the collector's scheduled tasks. Windows ships hundreds of its own under
// \Microsoft\, which nobody means when they say "my task", so those only appear when searched for.
func (b *APIBackend) exploreTasks(ctx context.Context, collectorID string, result *api.ExploreResult, covered watchers, contains func(...string) bool, failing, searching bool, items *[]api.ExploreItem) (int, error) {
	rows, err := b.options.SQL.QueryParams(ctx, `SELECT id, name, ts, detail FROM inventory_current WHERE collector_id = :collector AND kind = 'tasks' ORDER BY id`, map[string]any{"collector": collectorID})
	if err != nil {
		return 0, err
	}
	hidden := 0
	for _, row := range rows.Rows {
		id, _ := row[0].(string)
		name, _ := row[1].(string)
		var entry struct {
			Enabled    bool   `json:"enabled"`
			State      string `json:"state"`
			LastRun    int64  `json:"lastRun"`
			LastResult uint32 `json:"lastResult"`
		}
		if detail, ok := row[3].(string); !ok || json.Unmarshal([]byte(detail), &entry) != nil {
			continue
		}
		if ts, ok := row[2].(int64); ok && ts > result.CollectedAt {
			result.CollectedAt = ts
		}
		builtin := strings.HasPrefix(id, `\microsoft\`)
		if builtin && !searching {
			hidden++
			continue
		}
		result.Total++
		item := api.ExploreItem{Kind: "task", Name: name, DisplayName: name, Task: id, State: entry.State, LastRun: entry.LastRun, Result: config.TaskStatusText(entry.State, entry.LastResult)}
		bad := !entry.Enabled || !config.TaskHealthy(entry.State, entry.LastResult)
		if !entry.Enabled {
			item.Result = "已禁用"
		}
		result.Counts[entry.State]++
		if bad {
			result.Counts["attention"]++
		}
		item.MonitoredBy = covered.tasks[id]
		if item.MonitoredBy != "" {
			result.Counts["monitored"]++
		} else {
			item.Suggest = &api.ExploreSuggestion{Name: name, Task: id}
		}
		if !contains(name, id) || (failing && !bad) {
			continue
		}
		*items = append(*items, item)
	}
	return hidden, nil
}

type taskState struct {
	state, reason string
	ts            int64
}

// taskStates reads the newest recorded state of every task monitor; the store writes one a minute.
func (b *APIBackend) taskStates(ctx context.Context, cfg *config.Config) (map[[2]string]taskState, error) {
	var ids []string
	for _, target := range cfg.Targets {
		if target.Task != "" {
			ids = append(ids, "'"+target.ID+"'")
		}
	}
	result := map[[2]string]taskState{}
	if len(ids) == 0 {
		return result, nil
	}
	since := strconv.FormatInt(b.options.Now().Add(-time.Hour).UnixMilli(), 10)
	rows, err := b.options.SQL.Query(ctx, `SELECT t.collector_id, t.target_id, t.state, t.detail, t.ts FROM task_status t WHERE t.target_id IN (`+strings.Join(ids, ",")+`) AND t.ts >= `+since+` AND t.ts = (SELECT max(n.ts) FROM task_status n WHERE n.collector_id = t.collector_id AND n.target_id = t.target_id)`)
	if err != nil {
		return nil, err
	}
	for _, row := range rows.Rows {
		collectorID, _ := row[0].(string)
		targetID, _ := row[1].(string)
		state, _ := row[2].(string)
		ts, _ := row[4].(int64)
		var detail store.TaskDetail
		if raw, ok := row[3].(string); ok {
			_ = json.Unmarshal([]byte(raw), &detail)
		}
		result[[2]string{collectorID, targetID}] = taskState{state: state, reason: detail.Reason, ts: ts}
	}
	return result, nil
}

// TaskWatches lists the task monitors the store records every minute.
func TaskWatches(cfg *config.Config) []store.TaskWatch {
	var result []store.TaskWatch
	for _, target := range cfg.Targets {
		if target.Task != "" {
			result = append(result, store.TaskWatch{Target: target.ID, Collector: target.Collector, Task: target.Task})
		}
	}
	return slices.Clip(result)
}
