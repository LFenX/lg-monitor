package store

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/LFenX/lg-monitor/internal/rules"
)

func (s *Store) ActiveAlerts(ctx context.Context) ([]rules.Alert, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id,rule_id,collector_id,target_id,state,severity,started_at,coalesce(resolved_at,0),coalesce(last_notified_at,0),summary FROM alerts WHERE state='firing' ORDER BY collector_id,rule_id,target_id`)
	if err != nil {
		return nil, fmt.Errorf("查询触发中告警失败：%w", err)
	}
	defer rows.Close()
	result := make([]rules.Alert, 0)
	for rows.Next() {
		var alert rules.Alert
		if err := rows.Scan(&alert.ID, &alert.RuleID, &alert.CollectorID, &alert.TargetID, &alert.State, &alert.Severity, &alert.StartedAt, &alert.ResolvedAt, &alert.LastNotifiedAt, &alert.Summary); err != nil {
			return nil, fmt.Errorf("读取触发中告警失败：%w", err)
		}
		result = append(result, alert)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("遍历触发中告警失败：%w", err)
	}
	return result, nil
}

func (s *Store) SaveTransition(ctx context.Context, alert rules.Alert, event rules.Event) error {
	validEvent := event.Kind == alert.State || (event.Kind == "closed" && alert.State == rules.StateResolved)
	if alert.ID == "" || alert.RuleID == "" || alert.CollectorID == "" || alert.TargetID == "" || event.AlertID != alert.ID || !validEvent || (alert.State != rules.StateFiring && alert.State != rules.StateResolved) {
		return fmt.Errorf("告警状态或事件身份无效")
	}
	return s.operation(ctx, func(ctx context.Context, tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `INSERT INTO alerts(id,rule_id,collector_id,target_id,state,severity,started_at,resolved_at,last_notified_at,summary) VALUES(?,?,?,?,?,?,?,?,?,?) ON CONFLICT(id) DO UPDATE SET state=excluded.state,severity=excluded.severity,resolved_at=excluded.resolved_at,last_notified_at=max(coalesce(alerts.last_notified_at,0),excluded.last_notified_at),summary=excluded.summary`, alert.ID, alert.RuleID, alert.CollectorID, alert.TargetID, alert.State, alert.Severity, alert.StartedAt, alert.ResolvedAt, alert.LastNotifiedAt, alert.Summary)
		if err != nil {
			return fmt.Errorf("保存告警失败：%w", err)
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO alert_events(alert_id,ts,kind,message) SELECT ?,?,?,? WHERE NOT EXISTS (SELECT 1 FROM alert_events WHERE alert_id=? AND ts=? AND kind=?)`, event.AlertID, event.TS, event.Kind, event.Message, event.AlertID, event.TS, event.Kind); err != nil {
			return fmt.Errorf("保存告警事件失败：%w", err)
		}
		return nil
	})
}
