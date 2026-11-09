package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/LFenX/lg-monitor/internal/rules"
)

var ErrCardActionBinding = errors.New("回调与已发送告警不符，请从原通知群操作")

type CardNotification struct {
	ID         string
	NotifierID string
	AlertID    string
	EventKey   string
	MessageID  string
}

type CardActionInput struct {
	AppID        string
	EventID      string
	OperatorHash string
	Operation    string
	Notification CardNotification
}

type CardActionResult struct {
	AlertID        string `json:"alertId"`
	RuleID         string `json:"ruleId"`
	CollectorID    string `json:"collectorId"`
	TargetID       string `json:"targetId"`
	InstanceKey    string `json:"instanceKey"`
	Operation      string `json:"operation"`
	TS             int64  `json:"ts"`
	AcknowledgedAt int64  `json:"acknowledgedAt"`
	SilenceID      string `json:"silenceId,omitempty"`
	SilenceUntil   int64  `json:"silenceUntil,omitempty"`
	Duplicate      bool   `json:"duplicate"`
}

func (s *Store) CardActionAlert(ctx context.Context, id string) (rules.Alert, error) {
	var alert rules.Alert
	err := s.db.QueryRowContext(ctx, `SELECT id,rule_id,collector_id,target_id,instance_key,state,severity,acknowledged_at FROM alerts WHERE id=?`, id).Scan(&alert.ID, &alert.RuleID, &alert.CollectorID, &alert.TargetID, &alert.InstanceKey, &alert.State, &alert.Severity, &alert.AcknowledgedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return alert, ErrAlertUnavailable
	}
	if err != nil {
		return alert, fmt.Errorf("读取卡片告警归属失败：%w", err)
	}
	return alert, nil
}

func (s *Store) CardNotification(ctx context.Context, messageID, alertID, eventKey string) (CardNotification, error) {
	var result CardNotification
	if messageID == "" || alertID == "" || eventKey == "" {
		return result, ErrCardActionBinding
	}
	err := s.db.QueryRowContext(ctx, `SELECT id,notifier_id,alert_id,event_key,message_id FROM notifications WHERE message_id=? AND alert_id=? AND event_key=? AND state='firing' AND ok=1 AND skipped=0 ORDER BY ts DESC,rowid DESC LIMIT 1`, messageID, alertID, eventKey).Scan(&result.ID, &result.NotifierID, &result.AlertID, &result.EventKey, &result.MessageID)
	if errors.Is(err, sql.ErrNoRows) {
		return result, ErrCardActionBinding
	}
	if err != nil {
		return result, fmt.Errorf("核对卡片发送记录失败：%w", err)
	}
	return result, nil
}

func (s *Store) ApplyCardAction(ctx context.Context, input CardActionInput) (CardActionResult, error) {
	var result CardActionResult
	if input.AppID == "" || input.EventID == "" || len(input.OperatorHash) != 64 || (input.Operation != "ack" && input.Operation != "silence_1h") || input.Notification.ID == "" {
		return result, ErrCardActionBinding
	}
	if _, err := hex.DecodeString(input.OperatorHash); err != nil {
		return result, ErrCardActionBinding
	}
	input.OperatorHash = strings.ToLower(input.OperatorHash)
	err := s.operation(ctx, func(ctx context.Context, tx *sql.Tx) error {
		var bound bool
		if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM notifications WHERE id=? AND notifier_id=? AND alert_id=? AND event_key=? AND message_id=? AND state='firing' AND ok=1 AND skipped=0)`, input.Notification.ID, input.Notification.NotifierID, input.Notification.AlertID, input.Notification.EventKey, input.Notification.MessageID).Scan(&bound); err != nil {
			return fmt.Errorf("重新核对动作消息失败：%w", err)
		}
		if !bound {
			return ErrCardActionBinding
		}
		var previousNotification, previousOperator string
		err := tx.QueryRowContext(ctx, `SELECT alert_id,rule_id,collector_id,target_id,instance_key,operation,ts,acknowledged_at,silence_id,silence_until,notification_id,operator_hash FROM card_action_events WHERE app_id=? AND event_id=?`, input.AppID, input.EventID).Scan(&result.AlertID, &result.RuleID, &result.CollectorID, &result.TargetID, &result.InstanceKey, &result.Operation, &result.TS, &result.AcknowledgedAt, &result.SilenceID, &result.SilenceUntil, &previousNotification, &previousOperator)
		if err == nil {
			if result.AlertID != input.Notification.AlertID || result.Operation != input.Operation || previousOperator != input.OperatorHash || previousNotification != input.Notification.ID {
				return ErrCardActionBinding
			}
			result.Duplicate = true
			return nil
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("核对回调去重记录失败：%w", err)
		}
		var state string
		err = tx.QueryRowContext(ctx, `SELECT id,rule_id,collector_id,target_id,instance_key,state,acknowledged_at FROM alerts WHERE id=?`, input.Notification.AlertID).Scan(&result.AlertID, &result.RuleID, &result.CollectorID, &result.TargetID, &result.InstanceKey, &state, &result.AcknowledgedAt)
		if errors.Is(err, sql.ErrNoRows) || err == nil && state != "firing" {
			return ErrAlertUnavailable
		}
		if err != nil {
			return fmt.Errorf("读取卡片告警状态失败：%w", err)
		}
		result.Operation = input.Operation
		result.TS = s.now().UnixMilli()
		if input.Operation == "ack" {
			if result.AcknowledgedAt == 0 {
				result.AcknowledgedAt = max(1, result.TS)
			}
			if _, err := tx.ExecContext(ctx, `UPDATE alerts SET acknowledged_at=? WHERE id=?`, result.AcknowledgedAt, result.AlertID); err != nil {
				return fmt.Errorf("确认卡片告警失败：%w", err)
			}
		} else {
			identity, _ := json.Marshal([]string{result.RuleID, result.CollectorID, result.TargetID, result.InstanceKey})
			digest := sha256.Sum256(identity)
			result.SilenceID = "card-" + hex.EncodeToString(digest[:])
			result.SilenceUntil = result.TS + time.Hour.Milliseconds()
			if result.SilenceUntil <= result.TS {
				return fmt.Errorf("静音截止时间无效")
			}
			_, err := tx.ExecContext(ctx, `INSERT INTO silences(id,scope_rule_id,target_id,starts_at,ends_at,reason,collector_id,instance_key,origin) VALUES(?,?,?,?,?,'飞书卡片静音 1 小时',?,?,'feishu_card') ON CONFLICT(id) DO UPDATE SET starts_at=excluded.starts_at,ends_at=excluded.ends_at,reason=excluded.reason`, result.SilenceID, result.RuleID, result.TargetID, result.TS, result.SilenceUntil, result.CollectorID, result.InstanceKey)
			if err != nil {
				return fmt.Errorf("保存卡片静音失败：%w", err)
			}
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO card_action_events(app_id,event_id,notification_id,alert_id,collector_id,target_id,rule_id,instance_key,operator_hash,operation,ts,silence_id,silence_until,acknowledged_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?)`, input.AppID, input.EventID, input.Notification.ID, result.AlertID, result.CollectorID, result.TargetID, result.RuleID, result.InstanceKey, strings.ToLower(input.OperatorHash), result.Operation, result.TS, result.SilenceID, result.SilenceUntil, result.AcknowledgedAt)
		if err != nil {
			return fmt.Errorf("保存卡片动作审计失败：%w", err)
		}
		return nil
	})
	if err != nil {
		return CardActionResult{}, err
	}
	return result, nil
}
