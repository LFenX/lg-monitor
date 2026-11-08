package store

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"slices"
	"strconv"

	"github.com/LFenX/lg-monitor/internal/collector"
	"github.com/LFenX/lg-monitor/internal/rules"
)

var ErrEventLogPage = errors.New("事件日志分页参数无效")

type EventLogEntry struct {
	TS       int64  `json:"ts"`
	Channel  string `json:"channel"`
	Provider string `json:"provider"`
	EventID  uint32 `json:"eventId"`
	Level    uint8  `json:"level"`
	RecordID string `json:"recordId"`
	Subject  string `json:"subject"`
	Message  string `json:"message"`
}
type EventLogPage struct {
	AlertID    string          `json:"alertId"`
	From       *int64          `json:"from" jsonschema:"nullable"`
	To         *int64          `json:"to" jsonschema:"nullable"`
	Events     []EventLogEntry `json:"events"`
	NextCursor *string         `json:"nextCursor" jsonschema:"nullable"`
}
type eventLogCursor struct {
	Alert   string `json:"alert"`
	Source  int64  `json:"source"`
	TS      int64  `json:"ts"`
	Channel string `json:"channel"`
	ID      int64  `json:"id"`
}

func (s *Store) DiagnosticEvents(parent context.Context, alert rules.Alert) ([]collector.EventLogRecord, error) {
	result := []collector.EventLogRecord{}
	if alert.SourceTS <= 0 || alert.SourceTS > math.MaxInt64-60000 {
		return result, nil
	}
	ctx, cancel := context.WithTimeout(parent, QueryBudget)
	defer cancel()
	rows, err := s.maintenanceView.db.QueryContext(ctx, `SELECT e.channel,e.provider,e.event_id,e.level,e.record_id,e.ts,e.subject,e.message FROM eventlog_events e
WHERE e.collector_id=? AND e.ts>=? AND e.ts<=? AND EXISTS(
SELECT 1 FROM eventlog_matches m WHERE m.collector_id=e.collector_id AND m.channel=e.channel AND m.record_id=e.record_id AND m.ts=e.ts AND m.kind='subject' AND m.name=?)
ORDER BY e.ts DESC,e.channel DESC,e.record_id DESC LIMIT 5`, alert.CollectorID, max(int64(0), alert.SourceTS-60000), alert.SourceTS+60000, alert.TargetID)
	if err != nil {
		return result, fmt.Errorf("读取通知诊断事件失败：%w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var event collector.EventLogRecord
		if err := rows.Scan(&event.Channel, &event.Provider, &event.EventID, &event.Level, &event.RecordID, &event.TS, &event.Subject, &event.Message); err != nil {
			return result, fmt.Errorf("读取通知诊断事件字段失败：%w", err)
		}
		result = append(result, event)
	}
	if err := rows.Err(); err != nil {
		return result, fmt.Errorf("遍历通知诊断事件失败：%w", err)
	}
	slices.Reverse(result)
	return result, nil
}

func (v *View) AlertEventLogs(parent context.Context, id string, limit int, cursor string) (EventLogPage, error) {
	result := EventLogPage{AlertID: id, Events: []EventLogEntry{}}
	if limit < 1 || limit > 500 || len(cursor) > 2048 {
		return result, fmt.Errorf("limit 必须在 1～500 之间，cursor 最多 2048 字节：%w", ErrEventLogPage)
	}
	ctx, cancel := context.WithTimeout(parent, QueryBudget)
	defer cancel()
	alert, exists, err := v.EventLogAlert(ctx, id)
	if err != nil {
		return result, err
	}
	if !exists {
		return result, sql.ErrNoRows
	}
	if alert.SourceTS <= 0 || alert.SourceTS > math.MaxInt64-60000 {
		if cursor != "" {
			return result, fmt.Errorf("告警没有事件窗口，不能使用 cursor：%w", ErrEventLogPage)
		}
		return result, nil
	}
	from, to := max(int64(0), alert.SourceTS-60000), alert.SourceTS+60000
	result.From, result.To = &from, &to
	query := `SELECT e.ts,e.channel,e.provider,e.event_id,e.level,e.record_id,e.subject,e.message FROM eventlog_events e
WHERE e.collector_id=? AND e.ts>=? AND e.ts<=? AND EXISTS(
SELECT 1 FROM eventlog_matches m WHERE m.collector_id=e.collector_id AND m.channel=e.channel AND m.record_id=e.record_id AND m.ts=e.ts AND m.kind='subject' AND m.name=?)`
	args := []any{alert.CollectorID, from, to, alert.TargetID}
	if cursor != "" {
		data, err := base64.RawURLEncoding.DecodeString(cursor)
		var after eventLogCursor
		if err != nil || json.Unmarshal(data, &after) != nil || after.Alert != id || after.Source != alert.SourceTS || after.TS < from || after.TS > to || !collector.EventLogChannel(after.Channel) || after.ID <= 0 {
			return result, fmt.Errorf("cursor 无效或不属于本次告警窗口：%w", ErrEventLogPage)
		}
		query += ` AND (e.ts,e.channel,e.record_id)>(?,?,?)`
		args = append(args, after.TS, after.Channel, after.ID)
	}
	query += ` ORDER BY e.ts,e.channel,e.record_id LIMIT ?`
	args = append(args, limit+1)
	rows, err := v.db.QueryContext(ctx, query, args...)
	if err != nil {
		return result, fmt.Errorf("读取告警前后事件失败：%w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var event EventLogEntry
		var recordID int64
		if err := rows.Scan(&event.TS, &event.Channel, &event.Provider, &event.EventID, &event.Level, &recordID, &event.Subject, &event.Message); err != nil {
			return result, fmt.Errorf("读取告警事件字段失败：%w", err)
		}
		if len(result.Events) == limit {
			last := result.Events[len(result.Events)-1]
			lastID, _ := strconv.ParseInt(last.RecordID, 10, 64)
			data, _ := json.Marshal(eventLogCursor{Alert: id, Source: alert.SourceTS, TS: last.TS, Channel: last.Channel, ID: lastID})
			value := base64.RawURLEncoding.EncodeToString(data)
			result.NextCursor = &value
			break
		}
		event.RecordID = strconv.FormatInt(recordID, 10)
		result.Events = append(result.Events, event)
	}
	if err := rows.Err(); err != nil {
		return result, fmt.Errorf("遍历告警事件失败：%w", err)
	}
	return result, nil
}
