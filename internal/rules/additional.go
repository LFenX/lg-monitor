package rules

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"reflect"
	"slices"
	"strings"
	"time"

	"github.com/LFenX/lg-monitor/internal/collector"
	"github.com/LFenX/lg-monitor/internal/config"
)

const SystemTargetID = "@system"
const SQLTargetID = "@sql"
const SQLCollectorID = "@monitor"

type Decision struct {
	Suppressed string
	Digest     bool
}
type NotificationPolicy interface {
	EvaluateNotification(context.Context, Alert, time.Time) (Decision, error)
}
type QueryResult struct {
	Columns   []string `json:"columns"`
	Rows      [][]any  `json:"rows"`
	Truncated bool     `json:"truncated"`
}
type QueryExecutor interface {
	QueryRule(context.Context, string, int64) (QueryResult, error)
}
type sqlSnapshot struct {
	next      time.Time
	known     bool
	rows      map[string]string
	inFlight  bool
	lastError RuleError
}

func cloneMetrics(values map[string]float64) map[string]float64 { return maps.Clone(values) }
func cloneSystem(value collector.SystemMetrics) collector.SystemMetrics {
	value.Disks, value.Networks, value.Errors = slices.Clone(value.Disks), slices.Clone(value.Networks), slices.Clone(value.Errors)
	return value
}
func sameCondition(a, b config.Rule) bool {
	return a.Type == b.Type && a.Target == b.Target && a.Query == b.Query && a.Metric == b.Metric && a.Op == b.Op && a.Resource == b.Resource && a.Within == b.Within && reflect.DeepEqual(a.Threshold, b.Threshold) && reflect.DeepEqual(a.Probe, b.Probe)
}

func (e *Engine) renotify(ctx context.Context, spec ruleSpec, state *ruleState, now time.Time) error {
	if spec.rule.Renotify == "" {
		return nil
	}
	interval, _ := spec.rule.Renotify.Parse()
	last := max(state.alert.NotificationAt, state.alert.StartedAt, state.alert.LastNotifiedAt)
	if now.UnixMilli()-last < interval.Milliseconds() {
		return nil
	}
	return e.notificationTransition(ctx, state, state.alert, "renotify", now)
}

func (e *Engine) scriptCondition(spec ruleSpec, id targetKey) (bool, bool, string) {
	known := true
	for index, probe := range spec.target.Probes {
		if probe.Type != "script" {
			continue
		}
		result, exists := e.probes[id][index]
		if !exists {
			known = false
			continue
		}
		if !result.ok {
			return true, true, fmt.Sprintf("第 %d 个脚本检查失败：%s", index+1, result.message)
		}
	}
	return false, known, ""
}

func (e *Engine) metricCondition(spec ruleSpec, collectorID string) (bool, bool, string) {
	rule := spec.rule
	var values []float64
	system, exists := e.systems[collectorID]
	switch rule.Metric {
	case "system.cpu_percent":
		if exists {
			values = []float64{system.CPUPercent}
		}
	case "system.memory_percent":
		if exists {
			values = []float64{system.MemoryPercent}
		}
	case "system.uptime_seconds":
		if exists {
			values = []float64{float64(system.UptimeSeconds)}
		}
	case "disk.used_percent", "disk.free_bytes":
		if exists {
			for _, disk := range system.Disks {
				if rule.Resource != "" && !strings.EqualFold(rule.Resource, disk.Path) {
					continue
				}
				value := disk.UsedPercent
				if rule.Metric == "disk.free_bytes" {
					value = float64(disk.Free)
				}
				values = append(values, value)
			}
		}
	case "network.send_bytes_per_sec", "network.receive_bytes_per_sec":
		if exists {
			for _, network := range system.Networks {
				if rule.Resource != "" && rule.Resource != network.Name {
					continue
				}
				value := network.SendBytesPerSec
				if rule.Metric == "network.receive_bytes_per_sec" {
					value = network.ReceiveBytesPerSec
				}
				values = append(values, value)
			}
		}
	case "process.cpu_percent", "process.rss_bytes":
		id := targetKey{collectorID, spec.target.ID}
		if target, known := e.targets[id]; known && target.process == collector.ProcessFound && len(e.processes[id]) > 0 {
			var total float64
			for _, process := range e.processes[id] {
				if rule.Metric == "process.cpu_percent" {
					total += process.CPUPercent
				} else {
					total += float64(process.RSS)
				}
			}
			values = []float64{total}
		}
	default:
		if strings.HasPrefix(rule.Metric, "script.") {
			for index, probe := range spec.target.Probes {
				if probe.Type != "script" || rule.Probe != nil && *rule.Probe != index {
					continue
				}
				if result, ok := e.probes[targetKey{collectorID, spec.target.ID}][index]; ok {
					if value, ok := result.metrics[strings.TrimPrefix(rule.Metric, "script.")]; ok {
						values = append(values, value)
					}
				}
			}
		}
	}
	if len(values) == 0 {
		return false, false, ""
	}
	threshold := *rule.Threshold
	for _, value := range values {
		matched := false
		switch rule.Op {
		case "gt":
			matched = value > threshold
		case "gte":
			matched = value >= threshold
		case "lt":
			matched = value < threshold
		case "lte":
			matched = value <= threshold
		case "eq":
			matched = value == threshold
		case "ne":
			matched = value != threshold
		}
		if matched {
			return true, true, fmt.Sprintf("指标 %s 当前 %g，条件 %s %g", rule.Metric, value, rule.Op, threshold)
		}
	}
	return false, true, ""
}

func sqlInstances(result QueryResult) (map[string]string, error) {
	if result.Truncated || len(result.Rows) > 100 {
		return nil, fmt.Errorf("SQL 规则结果超过 100 行，请缩小查询范围")
	}
	keyIndex, summaryIndex := -1, -1
	seen := map[string]bool{}
	for i, column := range result.Columns {
		name := strings.ToLower(column)
		if seen[name] {
			return nil, fmt.Errorf("SQL 规则列名重复：%s", column)
		}
		seen[name] = true
		if name == "key" {
			keyIndex = i
		}
		if name == "summary" {
			summaryIndex = i
		}
	}
	if keyIndex < 0 {
		return nil, fmt.Errorf("SQL 规则必须返回 key 列")
	}
	rows := make(map[string]string, len(result.Rows))
	for _, row := range result.Rows {
		if len(row) != len(result.Columns) || row[keyIndex] == nil {
			return nil, fmt.Errorf("SQL 规则实例缺少非空 key")
		}
		key := fmt.Sprint(row[keyIndex])
		if value, ok := row[keyIndex].([]byte); ok {
			key = string(value)
		}
		if strings.TrimSpace(key) == "" {
			return nil, fmt.Errorf("SQL key 必须非空")
		}
		if _, exists := rows[key]; exists {
			continue
		}
		summary := "SQL 查询匹配实例 " + key
		if summaryIndex >= 0 && row[summaryIndex] != nil {
			summary = fmt.Sprint(row[summaryIndex])
			if value, ok := row[summaryIndex].([]byte); ok {
				summary = string(value)
			}
		}
		rows[key] = summary
	}
	return rows, nil
}

type RuleError struct {
	RuleID      string `json:"ruleId"`
	Error       string `json:"error"`
	At          int64  `json:"at"`
	Consecutive int    `json:"consecutive"`
	Active      bool   `json:"active"`
}
type RuleErrorRecorder interface {
	RecordRuleEvaluation(context.Context, RuleError) error
}
type sqlJob struct {
	spec     ruleSpec
	snapshot *sqlSnapshot
	at       time.Time
}

func (e *Engine) prepareSQL(at time.Time) []sqlJob {
	var jobs []sqlJob
	for _, spec := range e.specs {
		if spec.rule.Type != "sql" {
			continue
		}
		snapshot := e.sql[spec.rule.ID]
		if snapshot == nil {
			snapshot = &sqlSnapshot{}
			e.sql[spec.rule.ID] = snapshot
		}
		if snapshot.inFlight || at.Before(snapshot.next) {
			continue
		}
		interval, _ := spec.rule.SQLInterval()
		snapshot.inFlight, snapshot.next = true, at.Add(interval)
		jobs = append(jobs, sqlJob{spec: spec, snapshot: snapshot, at: at})
	}
	return jobs
}

func (e *Engine) retrySQL(ctx context.Context, spec ruleSpec, now time.Time) error {
	var failures []error
	snapshot := e.sql[spec.rule.ID]
	for id, state := range e.states {
		if id.rule != spec.rule.ID || state.closing {
			continue
		}
		if err := e.finishTransition(ctx, state); err != nil {
			failures = append(failures, err)
			continue
		}
		if state.phase == StateFiring && snapshot != nil && snapshot.known && !snapshot.inFlight && now.Before(snapshot.next) {
			if err := e.renotify(ctx, spec, state, now); err != nil {
				failures = append(failures, err)
			}
		}
	}
	return errors.Join(failures...)
}

func (e *Engine) runSQL(ctx context.Context, jobs []sqlJob) error {
	var failures []error
	for _, job := range jobs {
		var result QueryResult
		var queryErr error
		if e.queries == nil {
			queryErr = fmt.Errorf("SQL 规则没有专用只读连接")
		} else {
			queryCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
			result, queryErr = e.queries.QueryRule(queryCtx, job.spec.rule.Query, job.at.UnixMilli())
			cancel()
		}
		var rows map[string]string
		if queryErr == nil {
			rows, queryErr = sqlInstances(result)
		}
		e.mu.Lock()
		if e.sql[job.spec.rule.ID] != job.snapshot {
			e.mu.Unlock()
			continue
		}
		snapshot := job.snapshot
		snapshot.inFlight = false
		if queryErr != nil {
			snapshot.known = false
			for id, state := range e.states {
				if id.rule == job.spec.rule.ID && state.phase == StatePending {
					state.phase, state.pendingAt = StateOK, time.Time{}
				}
			}
			snapshot.lastError = RuleError{RuleID: job.spec.rule.ID, Error: queryErr.Error(), At: job.at.UnixMilli(), Consecutive: snapshot.lastError.Consecutive + 1, Active: true}
			e.logger.Warn("SQL rule evaluation failed", "rule", job.spec.rule.ID, "consecutiveFailures", snapshot.lastError.Consecutive)
			failures = append(failures, fmt.Errorf("执行 SQL 规则 %s 失败：%w", job.spec.rule.ID, queryErr))
		} else {
			snapshot.rows, snapshot.known = rows, true
			snapshot.lastError.RuleID, snapshot.lastError.Active, snapshot.lastError.Consecutive = job.spec.rule.ID, false, 0
			if err := e.evaluateSQLRows(ctx, job.spec, job.at, rows); err != nil {
				failures = append(failures, err)
			}
		}
		status := snapshot.lastError
		if recorder, ok := e.repository.(RuleErrorRecorder); ok {
			if err := recorder.RecordRuleEvaluation(ctx, status); err != nil {
				failures = append(failures, fmt.Errorf("保存 SQL 规则最近错误失败：%w", err))
			}
		}
		e.mu.Unlock()
	}
	return errors.Join(failures...)
}

func (e *Engine) evaluateSQLRows(ctx context.Context, spec ruleSpec, now time.Time, rows map[string]string) error {
	instances := map[string]bool{}
	for id := range rows {
		instances[id] = true
	}
	for id := range e.states {
		if id.rule == spec.rule.ID {
			instances[id.instance] = true
		}
	}
	keys := make([]string, 0, len(instances))
	for id := range instances {
		keys = append(keys, id)
	}
	slices.Sort(keys)
	var failures []error
	for _, instance := range keys {
		reason, met := rows[instance]
		identity := key{spec.rule.ID, SQLCollectorID, spec.target.ID, instance}
		if err := e.evaluateCondition(ctx, spec, identity, now, met, true, reason); err != nil {
			failures = append(failures, err)
			continue
		}
		// Historical keys need no memory after recovery and its notification have drained.
		if state := e.states[identity]; !met && state != nil && state.phase != StateFiring && state.next == nil && !state.notify {
			delete(e.states, identity)
		}
	}
	return errors.Join(failures...)
}

func (e *Engine) RecentErrors() []RuleError {
	e.mu.Lock()
	defer e.mu.Unlock()
	rows := []RuleError{}
	for _, snapshot := range e.sql {
		if snapshot.lastError.Error != "" {
			rows = append(rows, snapshot.lastError)
		}
	}
	for _, status := range e.capabilityErrors {
		if status.Error != "" {
			rows = append(rows, status)
		}
	}
	slices.SortFunc(rows, func(a, b RuleError) int { return strings.Compare(a.RuleID, b.RuleID) })
	return rows
}
