package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"github.com/LFenX/lg-monitor/internal/rules"
)

func (s *Store) ActiveAlerts(ctx context.Context) ([]rules.Alert, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id,rule_id,collector_id,target_id,state,severity,started_at,coalesce(resolved_at,0),coalesce(last_notified_at,0),summary,instance_key,acknowledged_at,notification_at,notification_key,EXISTS(SELECT 1 FROM alert_groups g WHERE g.rule_id=alerts.rule_id AND g.collector_id=alerts.collector_id AND g.target_id=alerts.target_id AND g.instance_key=alerts.instance_key AND g.flapping=1 AND g.changed_at>?),cert_json,source_ts,eventlog FROM alerts WHERE state='firing' ORDER BY collector_id,rule_id,target_id,instance_key`, s.now().Add(-10*time.Minute).UnixMilli())
	if err != nil {
		return nil, fmt.Errorf("查询触发中告警失败：%w", err)
	}
	defer rows.Close()
	result := make([]rules.Alert, 0)
	for rows.Next() {
		var alert rules.Alert
		var certificate, eventlog string
		if err := rows.Scan(&alert.ID, &alert.RuleID, &alert.CollectorID, &alert.TargetID, &alert.State, &alert.Severity, &alert.StartedAt, &alert.ResolvedAt, &alert.LastNotifiedAt, &alert.Summary, &alert.InstanceKey, &alert.AcknowledgedAt, &alert.NotificationAt, &alert.NotificationKey, &alert.Flapping, &certificate, &alert.SourceTS, &eventlog); err != nil {
			return nil, fmt.Errorf("读取触发中告警失败：%w", err)
		}
		if err := json.Unmarshal([]byte(certificate), &alert.Cert); err != nil {
			return nil, fmt.Errorf("读取告警证书快照失败：%w", err)
		}
		if err := decodeEventLogDetails(eventlog, &alert); err != nil {
			return nil, err
		}
		result = append(result, alert)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("遍历触发中告警失败：%w", err)
	}
	return result, nil
}

func (s *Store) SaveTransition(ctx context.Context, alert rules.Alert, event rules.Event) error {
	validEvent := event.Kind == alert.State || (event.Kind == "renotify" && alert.State == rules.StateFiring) || (event.Kind == "closed" && alert.State == rules.StateResolved)
	if alert.ID == "" || alert.RuleID == "" || alert.CollectorID == "" || alert.TargetID == "" || event.AlertID != alert.ID || !validEvent || (alert.State != rules.StateFiring && alert.State != rules.StateResolved) {
		return fmt.Errorf("告警状态或事件身份无效")
	}
	certificate, err := json.Marshal(alert.Cert)
	if err != nil {
		return fmt.Errorf("编码告警证书快照失败：%w", err)
	}
	eventlog, err := json.Marshal(alert.EventLog)
	if err != nil {
		return fmt.Errorf("编码事件告警快照失败：%w", err)
	}
	return s.operation(ctx, func(ctx context.Context, tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `INSERT INTO alerts(id,rule_id,collector_id,target_id,state,severity,started_at,resolved_at,last_notified_at,summary,instance_key,acknowledged_at,notification_at,notification_key,cert_json,source_ts,eventlog) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?) ON CONFLICT(id) DO UPDATE SET state=excluded.state,severity=excluded.severity,resolved_at=excluded.resolved_at,last_notified_at=max(coalesce(alerts.last_notified_at,0),excluded.last_notified_at),summary=excluded.summary,notification_at=excluded.notification_at,notification_key=excluded.notification_key,cert_json=excluded.cert_json,eventlog=excluded.eventlog`, alert.ID, alert.RuleID, alert.CollectorID, alert.TargetID, alert.State, alert.Severity, alert.StartedAt, alert.ResolvedAt, alert.LastNotifiedAt, alert.Summary, alert.InstanceKey, alert.AcknowledgedAt, alert.NotificationAt, alert.NotificationKey, string(certificate), alert.SourceTS, string(eventlog))
		if err != nil {
			return fmt.Errorf("保存告警失败：%w", err)
		}
		inserted, err := tx.ExecContext(ctx, `INSERT INTO alert_events(alert_id,ts,kind,message,event_key,cert_json,eventlog) SELECT ?,?,?,?,?,?,? WHERE NOT EXISTS (SELECT 1 FROM alert_events WHERE alert_id=? AND ts=? AND kind=?)`, event.AlertID, event.TS, event.Kind, event.Message, alert.NotificationKey, string(certificate), string(eventlog), event.AlertID, event.TS, event.Kind)
		if err != nil {
			return fmt.Errorf("保存告警事件失败：%w", err)
		}
		rows, err := inserted.RowsAffected()
		if err != nil {
			return fmt.Errorf("读取事件保存结果失败：%w", err)
		}
		if rows == 0 {
			return nil
		}
		return saveNotificationEvent(ctx, tx, alert, event)
	})
}
