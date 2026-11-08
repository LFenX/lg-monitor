package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/LFenX/lg-monitor/internal/collector"
	"github.com/LFenX/lg-monitor/internal/config"
	"github.com/LFenX/lg-monitor/internal/pipeline"
	"github.com/LFenX/lg-monitor/internal/rules"
)

const eventLogColumns = `e.collector_id,e.channel,e.record_id,e.ts,e.provider,e.event_id,e.level,e.subject,e.message,
(SELECT json_group_array(name) FROM eventlog_matches p WHERE p.collector_id=e.collector_id AND p.channel=e.channel AND p.record_id=e.record_id AND p.ts=e.ts AND p.kind='preset'),
(SELECT json_group_array(json_object('id',name,'via',via)) FROM
 (SELECT m.name,CASE WHEN count(DISTINCT m.kind)=2 THEN 'both' ELSE min(m.kind) END AS via
 FROM eventlog_matches m WHERE m.collector_id=e.collector_id AND m.channel=e.channel AND m.record_id=e.record_id AND m.ts=e.ts AND m.kind IN ('subject','subscription')
 GROUP BY m.name ORDER BY max(m.kind='subject') DESC,m.name))`

type eventLogScanner interface{ Scan(...any) error }

func scanEventLog(row eventLogScanner) (pipeline.Message, error) {
	message := pipeline.Message{Kind: pipeline.KindEventLog, EventLog: &collector.EventLogRecord{}}
	event := message.EventLog
	var presets, targets string
	err := row.Scan(&message.CollectorID, &event.Channel, &event.RecordID, &event.TS, &event.Provider, &event.EventID, &event.Level, &event.Subject, &event.Message, &presets, &targets)
	if err != nil {
		return message, err
	}
	message.TS = event.TS
	if err := json.Unmarshal([]byte(presets), &event.Presets); err != nil {
		return message, fmt.Errorf("读取事件预设归属失败：%w", err)
	}
	if err := json.Unmarshal([]byte(targets), &event.Targets); err != nil {
		return message, fmt.Errorf("读取事件目标归属失败：%w", err)
	}
	return message, nil
}

func eventLogPredicate(window rules.EventLogWindow) (string, []any) {
	where := `e.ts>? AND e.ts<=?`
	args := []any{window.From, window.To}
	if window.CollectorID != "" {
		where += ` AND e.collector_id=?`
		args = append(args, window.CollectorID)
	}
	match := `SELECT 1 FROM eventlog_matches m WHERE m.collector_id=e.collector_id AND m.channel=e.channel AND m.record_id=e.record_id AND m.ts=e.ts`
	if window.Preset != "" {
		where += ` AND EXISTS(` + match + ` AND m.kind='preset' AND m.name=?)`
		args = append(args, window.Preset)
	}
	if window.TargetID != "" {
		kind := "subscription"
		if window.Preset != "" {
			kind = "subject"
		}
		where += ` AND EXISTS(` + match + ` AND m.kind=? AND m.name=?)`
		args = append(args, kind, window.TargetID)
	}
	return where, args
}

func (s *Store) QueryEventLog(ctx context.Context, window rules.EventLogWindow) (rules.EventLogSnapshot, error) {
	return s.maintenanceView.QueryEventLog(ctx, window)
}

func (v *View) QueryEventLog(parent context.Context, window rules.EventLogWindow) (rules.EventLogSnapshot, error) {
	var result rules.EventLogSnapshot
	if window.CollectorID == "" || window.To < window.From || window.Threshold < 0 || window.TargetID == "" && !config.EventLogPreset(window.Preset) {
		return result, fmt.Errorf("事件日志统计需要采集器、合法时间窗和目标或预设")
	}
	ctx, cancel := context.WithTimeout(parent, QueryBudget)
	defer cancel()
	tx, err := v.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return result, fmt.Errorf("打开事件统计快照失败：%w", err)
	}
	defer tx.Rollback()
	where, args := eventLogPredicate(window)
	if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM eventlog_events e WHERE `+where, args...).Scan(&result.Count); err != nil {
		return result, fmt.Errorf("统计事件窗口失败：%w", err)
	}
	if result.Count == 0 {
		return result, nil
	}
	latest, err := scanEventLog(tx.QueryRowContext(ctx, `SELECT `+eventLogColumns+` FROM eventlog_events e WHERE `+where+` ORDER BY e.ts DESC,e.channel DESC,e.record_id DESC LIMIT 1`, args...))
	if err != nil {
		return result, fmt.Errorf("读取窗口最新事件失败：%w", err)
	}
	result.Latest = latest.EventLog
	if window.Threshold > 0 && result.Count >= int64(window.Threshold) {
		args = append(args, window.Threshold-1)
		if err := tx.QueryRowContext(ctx, `SELECT e.ts FROM eventlog_events e WHERE `+where+` ORDER BY e.ts,e.channel,e.record_id LIMIT 1 OFFSET ?`, args...).Scan(&result.ThresholdTS); err != nil {
			return result, fmt.Errorf("读取事件规则首次满足次数的时间失败：%w", err)
		}
	}
	return result, nil
}

func (v *View) loadEventLogReplay(ctx context.Context, rule config.Rule, since, until int64, data rules.ReplayData) (rules.ReplayData, error) {
	window, err := rule.EventLogWindow()
	if err != nil {
		return data, err
	}
	request := rules.EventLogWindow{TargetID: rule.Target, Preset: rule.Preset, From: since - window.Milliseconds(), To: until}
	where, args := eventLogPredicate(request)
	args = append(args, rules.MaxReplayRecords+1)
	rows, err := v.db.QueryContext(ctx, `SELECT `+eventLogColumns+` FROM eventlog_events e WHERE `+where+` ORDER BY e.ts,e.channel,e.record_id,e.collector_id LIMIT ?`, args...)
	if err != nil {
		return data, fmt.Errorf("读取事件日志回放历史失败：%w", err)
	}
	defer rows.Close()
	for rows.Next() {
		message, err := scanEventLog(rows)
		if err != nil {
			return data, err
		}
		if len(data.Messages) >= rules.MaxReplayRecords {
			return data, fmt.Errorf("事件日志历史超过 %d 条，请缩小回放范围", rules.MaxReplayRecords)
		}
		data.Messages = append(data.Messages, message)
	}
	if err := rows.Err(); err != nil {
		return data, fmt.Errorf("遍历事件日志回放失败：%w", err)
	}
	return data, nil
}

func (s *Store) UpdateEventLogAlert(ctx context.Context, alert rules.Alert) error {
	if alert.ID == "" || alert.EventLog == nil || alert.State != rules.StateFiring {
		return fmt.Errorf("更新事件告警需要触发中告警及事件快照")
	}
	data, err := json.Marshal(alert.EventLog)
	if err != nil {
		return fmt.Errorf("编码事件告警快照失败：%w", err)
	}
	return s.operation(ctx, func(ctx context.Context, tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `UPDATE alerts SET summary=?,eventlog=? WHERE id=? AND state='firing'`, alert.Summary, string(data), alert.ID)
		if err != nil {
			return fmt.Errorf("保存事件告警最新次数失败：%w", err)
		}
		return nil
	})
}

func decodeEventLogDetails(raw string, alert *rules.Alert) error {
	if raw == "" {
		return nil
	}
	if err := json.Unmarshal([]byte(raw), &alert.EventLog); err != nil {
		return fmt.Errorf("读取事件告警快照失败：%w", err)
	}
	return nil
}

func (v *View) EventLogAlert(ctx context.Context, id string) (rules.Alert, bool, error) {
	var alert rules.Alert
	var detail string
	eventColumns := `0,'null'`
	if v.eventLogs {
		eventColumns = `source_ts,eventlog`
	}
	err := v.db.QueryRowContext(ctx, `SELECT id,rule_id,collector_id,target_id,`+eventColumns+` FROM alerts WHERE id=?`, id).Scan(&alert.ID, &alert.RuleID, &alert.CollectorID, &alert.TargetID, &alert.SourceTS, &detail)
	if errors.Is(err, sql.ErrNoRows) {
		return alert, false, nil
	}
	if err != nil {
		return alert, false, fmt.Errorf("读取告警事件窗口失败：%w", err)
	}
	if err := decodeEventLogDetails(detail, &alert); err != nil {
		return alert, false, err
	}
	return alert, true, nil
}
