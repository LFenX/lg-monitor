package store

import (
	"context"
	"crypto/rand"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/LFenX/lg-monitor/internal/rules"
)

type Silence struct {
	ID       string `json:"id"`
	RuleID   string `json:"ruleId,omitempty"`
	TargetID string `json:"targetId,omitempty"`
	StartsAt int64  `json:"startsAt"`
	EndsAt   int64  `json:"endsAt"`
	Reason   string `json:"reason"`
}

var ErrAlertUnavailable = errors.New("告警不存在或已恢复，请重新列出触发中告警")

func (s *Store) AddSilence(ctx context.Context, ruleID, targetID string, duration time.Duration, reason string) (Silence, error) {
	now := s.now()
	row := Silence{ID: rand.Text(), RuleID: ruleID, TargetID: targetID, StartsAt: now.UnixMilli(), EndsAt: now.Add(duration).UnixMilli(), Reason: reason}
	if (strings.TrimSpace(ruleID) == "") == (strings.TrimSpace(targetID) == "") || duration <= 0 || row.EndsAt <= row.StartsAt || strings.TrimSpace(reason) == "" || len(reason) > 4096 {
		return Silence{}, fmt.Errorf("静音须指定一个 rule 或 target、正数 for 和不超过 4096 字节的非空 reason")
	}
	err := s.operation(ctx, func(ctx context.Context, tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `INSERT INTO silences(id,rule_id,target_id,starts_at,ends_at,reason) VALUES(?,?,?,?,?,?)`, row.ID, row.RuleID, row.TargetID, row.StartsAt, row.EndsAt, row.Reason)
		if err != nil {
			return fmt.Errorf("保存静音失败：%w", err)
		}
		return nil
	})
	return row, err
}

func (s *Store) AcknowledgeAlert(ctx context.Context, id string) error {
	return s.operation(ctx, func(ctx context.Context, tx *sql.Tx) error {
		result, err := tx.ExecContext(ctx, `UPDATE alerts SET acknowledged_at=CASE WHEN acknowledged_at=0 THEN ? ELSE acknowledged_at END WHERE id=? AND state='firing'`, s.now().UnixMilli(), id)
		if err != nil {
			return fmt.Errorf("确认告警失败：%w", err)
		}
		affected, err := result.RowsAffected()
		if err != nil {
			return fmt.Errorf("读取告警确认结果失败：%w", err)
		}
		if affected == 0 {
			return fmt.Errorf("确认告警失败：%w", ErrAlertUnavailable)
		}
		return nil
	})
}

func saveNotificationEvent(ctx context.Context, tx *sql.Tx, alert rules.Alert, event rules.Event) error {
	if event.Kind == "closed" || alert.NotificationKey == "" {
		return nil
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO notification_events(alert_id,event_key,state,ts) VALUES(?,?,?,?) ON CONFLICT DO NOTHING`, alert.ID, alert.NotificationKey, alert.State, event.TS); err != nil {
		return fmt.Errorf("保存通知决策事件失败：%w", err)
	}
	var changed int64
	var flapping int
	var digestAlert, digestKey string
	err := tx.QueryRowContext(ctx, `SELECT changed_at,flapping,digest_alert,digest_key FROM alert_groups WHERE rule_id=? AND collector_id=? AND target_id=? AND instance_key=?`, alert.RuleID, alert.CollectorID, alert.TargetID, alert.InstanceKey).Scan(&changed, &flapping, &digestAlert, &digestKey)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("读取抖动状态失败：%w", err)
	}
	if event.TS-changed >= (10 * time.Minute).Milliseconds() {
		flapping, digestAlert, digestKey = 0, "", ""
	}
	if event.Kind == rules.StateFiring {
		var triggers int
		if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM alert_events e JOIN alerts a ON a.id=e.alert_id WHERE a.rule_id=? AND a.collector_id=? AND a.target_id=? AND a.instance_key=? AND e.kind='firing' AND e.ts>? AND e.ts<=?`, alert.RuleID, alert.CollectorID, alert.TargetID, alert.InstanceKey, event.TS-(10*time.Minute).Milliseconds(), event.TS).Scan(&triggers); err != nil {
			return fmt.Errorf("读取触发历史失败：%w", err)
		}
		if flapping == 0 && triggers > 3 {
			flapping, digestAlert, digestKey = 1, alert.ID, alert.NotificationKey
		}
	}
	if event.Kind != "renotify" {
		changed = event.TS
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO alert_groups(rule_id,collector_id,target_id,instance_key,changed_at,flapping,digest_alert,digest_key) VALUES(?,?,?,?,?,?,?,?) ON CONFLICT(rule_id,collector_id,target_id,instance_key) DO UPDATE SET changed_at=excluded.changed_at,flapping=excluded.flapping,digest_alert=excluded.digest_alert,digest_key=excluded.digest_key`, alert.RuleID, alert.CollectorID, alert.TargetID, alert.InstanceKey, changed, flapping, digestAlert, digestKey); err != nil {
		return fmt.Errorf("保存抖动状态失败：%w", err)
	}
	if flapping != 0 {
		if alert.ID == digestAlert && alert.NotificationKey == digestKey {
			_, err = tx.ExecContext(ctx, `UPDATE notification_events SET digest=1 WHERE alert_id=? AND event_key=?`, alert.ID, alert.NotificationKey)
		} else {
			_, err = tx.ExecContext(ctx, `UPDATE notification_events SET suppressed='flapping' WHERE alert_id=? AND event_key=?`, alert.ID, alert.NotificationKey)
		}
		if err != nil {
			return fmt.Errorf("保存抖动通知策略失败：%w", err)
		}
	}
	return nil
}

func (s *Store) EvaluateNotification(ctx context.Context, alert rules.Alert, at time.Time) (rules.Decision, error) {
	var decision rules.Decision
	eventKey := alert.NotificationKey
	if eventKey == "" {
		eventAt := alert.NotificationAt
		if eventAt == 0 {
			eventAt = alert.StartedAt
			if alert.State == rules.StateResolved {
				eventAt = alert.ResolvedAt
			}
		}
		eventKey = fmt.Sprintf("%s:%d", alert.State, eventAt)
	}
	err := s.operation(ctx, func(ctx context.Context, tx *sql.Tx) error {
		{
			err := tx.QueryRowContext(ctx, `SELECT suppressed,digest FROM notification_events WHERE alert_id=? AND event_key=?`, alert.ID, eventKey).Scan(&decision.Suppressed, &decision.Digest)
			if err != nil && !errors.Is(err, sql.ErrNoRows) {
				return fmt.Errorf("读取通知决策失败：%w", err)
			}
			if decision.Suppressed != "" {
				return nil
			}
		}
		var silent bool
		observedAt := alert.NotificationAt
		if observedAt == 0 {
			observedAt = alert.StartedAt
			if alert.State == rules.StateResolved {
				observedAt = alert.ResolvedAt
			}
		}
		silenceAt := observedAt
		if strings.HasPrefix(eventKey, "resume:") {
			silenceAt = at.UnixMilli()
		}
		if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM silences WHERE ((origin!='feishu_card' AND (rule_id=? OR target_id=?)) OR (origin='feishu_card' AND scope_rule_id=? AND collector_id=? AND target_id=? AND instance_key=?)) AND starts_at<=? AND ends_at>?)`, alert.RuleID, alert.TargetID, alert.RuleID, alert.CollectorID, alert.TargetID, alert.InstanceKey, at.UnixMilli(), silenceAt).Scan(&silent); err != nil {
			return fmt.Errorf("查询静音失败：%w", err)
		}
		if silent {
			decision.Suppressed = "silenced"
		}
		if !silent {
			paused, err := pauseNotification(ctx, tx, alert, eventKey, observedAt)
			if err != nil {
				return err
			}
			if paused {
				decision.Suppressed = "paused"
				return nil
			}
		}
		if !decision.Digest && !strings.HasPrefix(eventKey, "resume:") {
			var flapping bool
			var changed int64
			err := tx.QueryRowContext(ctx, `SELECT flapping,changed_at FROM alert_groups WHERE rule_id=? AND collector_id=? AND target_id=? AND instance_key=?`, alert.RuleID, alert.CollectorID, alert.TargetID, alert.InstanceKey).Scan(&flapping, &changed)
			if err != nil && !errors.Is(err, sql.ErrNoRows) {
				return fmt.Errorf("查询当前抖动状态失败：%w", err)
			}
			if flapping && at.UnixMilli()-changed < (10*time.Minute).Milliseconds() {
				decision.Suppressed = "flapping"
			}
		}
		if strings.HasPrefix(alert.NotificationKey, "renotify:") || (alert.State == rules.StateFiring && strings.HasPrefix(eventKey, "resume:")) {
			var ack int64
			if err := tx.QueryRowContext(ctx, `SELECT acknowledged_at FROM alerts WHERE id=?`, alert.ID).Scan(&ack); err != nil {
				return fmt.Errorf("查询确认状态失败：%w", err)
			}
			if ack > 0 {
				decision.Suppressed = "acknowledged"
			}
		}
		if decision.Suppressed != "" {
			if _, err := tx.ExecContext(ctx, `INSERT INTO notification_events(alert_id,event_key,state,ts,suppressed) VALUES(?,?,?,?,?) ON CONFLICT(alert_id,event_key) DO UPDATE SET suppressed=excluded.suppressed`, alert.ID, eventKey, alert.State, observedAt, decision.Suppressed); err != nil {
				return fmt.Errorf("保存通知抑制原因失败：%w", err)
			}
		}
		return nil
	})
	return decision, err
}
