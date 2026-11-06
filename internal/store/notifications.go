package store

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/LFenX/lg-monitor/internal/notify"
	"github.com/LFenX/lg-monitor/internal/rules"
)

func (s *Store) RecordNotification(ctx context.Context, attempt notify.Attempt) error {
	if attempt.ID == "" || attempt.AlertID == "" || attempt.NotifierID == "" || (attempt.OK && attempt.MessageID == "") {
		return fmt.Errorf("通知记录缺少身份或成功消息 ID")
	}
	return s.operation(ctx, func(ctx context.Context, tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `INSERT INTO notifications(id,alert_id,state,notifier_id,template,ts,ok,error,message_id) VALUES(?,?,?,?,?,?,?,?,?)`, attempt.ID, attempt.AlertID, attempt.State, attempt.NotifierID, attempt.Template, attempt.TS, attempt.OK, attempt.Error, attempt.MessageID)
		if err != nil {
			return fmt.Errorf("保存通知发送结果失败：%w", err)
		}
		if attempt.OK {
			if _, err := tx.ExecContext(ctx, `UPDATE alerts SET last_notified_at=max(coalesce(last_notified_at,0),?) WHERE id=?`, attempt.TS, attempt.AlertID); err != nil {
				return fmt.Errorf("更新告警通知时间失败：%w", err)
			}
		}
		return nil
	})
}

type PendingNotification struct {
	EventID    int64
	Alert      rules.Alert
	ChangedAt  int64
	Successful []string
}

func (s *Store) PendingNotifications(ctx context.Context, since, until, after int64, limit int) ([]PendingNotification, error) {
	if limit < 1 || limit > 1000 {
		return nil, fmt.Errorf("补发查询每页须为 1～1000 条")
	}
	rows, err := s.db.QueryContext(ctx, `SELECT e.rowid,a.id,a.rule_id,a.collector_id,a.target_id,e.kind,a.severity,a.started_at,coalesce(a.resolved_at,0),coalesce(a.last_notified_at,0),e.message,e.ts
FROM alerts a JOIN alert_events e ON e.alert_id=a.id
WHERE e.kind IN ('firing','resolved') AND e.ts>=? AND e.ts<=? AND e.rowid>?
AND (SELECT kind FROM alert_events last WHERE last.alert_id=a.id ORDER BY last.rowid DESC LIMIT 1)!='closed'
ORDER BY e.rowid LIMIT ?`, since, until, after, limit)
	if err != nil {
		return nil, fmt.Errorf("查询待补发告警状态变化失败：%w", err)
	}
	result := make([]PendingNotification, 0)
	for rows.Next() {
		var item PendingNotification
		alert := &item.Alert
		if err := rows.Scan(&item.EventID, &alert.ID, &alert.RuleID, &alert.CollectorID, &alert.TargetID, &alert.State, &alert.Severity, &alert.StartedAt, &alert.ResolvedAt, &alert.LastNotifiedAt, &alert.Summary, &item.ChangedAt); err != nil {
			rows.Close()
			return nil, fmt.Errorf("读取待补发状态变化失败：%w", err)
		}
		if alert.State == rules.StateFiring {
			alert.ResolvedAt = 0
		}
		result = append(result, item)
	}
	readErr := rows.Err()
	rows.Close()
	if readErr != nil {
		return nil, fmt.Errorf("遍历待补发状态变化失败：%w", readErr)
	}
	// Release the first cursor before using the single read/write connection again.
	for i := range result {
		item := &result[i]
		successes, err := s.db.QueryContext(ctx, `SELECT DISTINCT notifier_id FROM notifications WHERE alert_id=? AND state=? AND ok=1 AND ts>=? ORDER BY notifier_id`, item.Alert.ID, item.Alert.State, item.ChangedAt)
		if err != nil {
			return nil, fmt.Errorf("查询告警成功通知渠道失败：%w", err)
		}
		for successes.Next() {
			var id string
			if err := successes.Scan(&id); err != nil {
				successes.Close()
				return nil, fmt.Errorf("读取告警成功通知渠道失败：%w", err)
			}
			item.Successful = append(item.Successful, id)
		}
		readErr := successes.Err()
		successes.Close()
		if readErr != nil {
			return nil, fmt.Errorf("遍历告警成功通知渠道失败：%w", readErr)
		}
	}
	return result, nil
}
