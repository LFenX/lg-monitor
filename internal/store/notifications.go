package store

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/LFenX/lg-monitor/internal/notify"
)

func (s *Store) RecordNotification(ctx context.Context, attempt notify.Attempt) error {
	if attempt.ID == "" || attempt.AlertID == "" || attempt.NotifierID == "" || (attempt.OK && attempt.MessageID == "") {
		return fmt.Errorf("通知记录缺少身份或成功消息 ID")
	}
	return s.operation(ctx, func(ctx context.Context, tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `INSERT INTO notifications(id,alert_id,notifier_id,template,ts,ok,error,message_id) VALUES(?,?,?,?,?,?,?,?)`, attempt.ID, attempt.AlertID, attempt.NotifierID, attempt.Template, attempt.TS, attempt.OK, attempt.Error, attempt.MessageID)
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
