package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"

	"github.com/LFenX/lg-monitor/internal/notify"
	"github.com/LFenX/lg-monitor/internal/rules"
)

func (s *Store) RecordNotification(ctx context.Context, attempt notify.Attempt) error {
	if attempt.ID == "" || attempt.AlertID == "" || attempt.NotifierID == "" || (attempt.OK && attempt.Skipped) || (attempt.Skipped && attempt.Error == "") {
		return fmt.Errorf("通知记录缺少身份、跳过原因或状态冲突")
	}
	return s.operation(ctx, func(ctx context.Context, tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `INSERT INTO notifications(id,alert_id,state,notifier_id,template,ts,ok,error,message_id,event_key,skipped,status_code) VALUES(?,?,?,?,?,?,?,?,?,?,?,?)`, attempt.ID, attempt.AlertID, attempt.State, attempt.NotifierID, attempt.Template, attempt.TS, attempt.OK, attempt.Error, attempt.MessageID, attempt.EventKey, attempt.Skipped, attempt.StatusCode)
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
	Skipped    []string
}

func (s *Store) PendingNotifications(ctx context.Context, since, until, after int64, limit int) ([]PendingNotification, error) {
	if limit < 1 || limit > 1000 {
		return nil, fmt.Errorf("补发查询每页须为 1～1000 条")
	}
	rows, err := s.db.QueryContext(ctx, `SELECT e.rowid,a.id,a.rule_id,a.collector_id,a.target_id,CASE WHEN e.kind='renotify' THEN 'firing' ELSE e.kind END,a.severity,a.started_at,coalesce(a.resolved_at,0),coalesce(a.last_notified_at,0),e.message,e.ts,a.instance_key,a.acknowledged_at,e.event_key,e.cert_json,a.source_ts,e.eventlog
FROM alerts a JOIN alert_events e ON e.alert_id=a.id
WHERE e.kind IN ('firing','resolved','renotify') AND e.ts>=? AND e.ts<=? AND e.rowid>?
AND NOT EXISTS(SELECT 1 FROM notification_events n WHERE n.alert_id=a.id AND n.event_key=coalesce(nullif(e.event_key,''),e.kind||':'||e.ts) AND n.suppressed!='')
AND (SELECT kind FROM alert_events last WHERE last.alert_id=a.id ORDER BY last.rowid DESC LIMIT 1)!='closed'
ORDER BY e.rowid LIMIT ?`, since, until, after, limit)
	if err != nil {
		return nil, fmt.Errorf("查询待补发告警状态变化失败：%w", err)
	}
	result := make([]PendingNotification, 0)
	for rows.Next() {
		var item PendingNotification
		alert := &item.Alert
		var certificate, eventlog string
		if err := rows.Scan(&item.EventID, &alert.ID, &alert.RuleID, &alert.CollectorID, &alert.TargetID, &alert.State, &alert.Severity, &alert.StartedAt, &alert.ResolvedAt, &alert.LastNotifiedAt, &alert.Summary, &item.ChangedAt, &alert.InstanceKey, &alert.AcknowledgedAt, &alert.NotificationKey, &certificate, &alert.SourceTS, &eventlog); err != nil {
			rows.Close()
			return nil, fmt.Errorf("读取待补发状态变化失败：%w", err)
		}
		if err := json.Unmarshal([]byte(certificate), &alert.Cert); err != nil {
			rows.Close()
			return nil, fmt.Errorf("读取待补发证书快照失败：%w", err)
		}
		if err := decodeEventLogDetails(eventlog, alert); err != nil {
			rows.Close()
			return nil, err
		}
		if alert.State == rules.StateFiring {
			alert.ResolvedAt = 0
		}
		alert.NotificationAt = item.ChangedAt
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
		successes, err := s.db.QueryContext(ctx, `SELECT notifier_id,max(ok),max(skipped) FROM notifications WHERE alert_id=? AND state=? AND (ok=1 OR skipped=1) AND ts>=? AND event_key=? GROUP BY notifier_id ORDER BY notifier_id`, item.Alert.ID, item.Alert.State, item.ChangedAt, item.Alert.NotificationKey)
		if err != nil {
			return nil, fmt.Errorf("查询告警成功通知渠道失败：%w", err)
		}
		for successes.Next() {
			var id string
			var ok, skipped bool
			if err := successes.Scan(&id, &ok, &skipped); err != nil {
				successes.Close()
				return nil, fmt.Errorf("读取告警成功通知渠道失败：%w", err)
			}
			if ok {
				item.Successful = append(item.Successful, id)
			} else if skipped {
				item.Skipped = append(item.Skipped, id)
			}
		}
		readErr := successes.Err()
		successes.Close()
		if readErr != nil {
			return nil, fmt.Errorf("遍历告警成功通知渠道失败：%w", readErr)
		}
	}
	return result, nil
}
