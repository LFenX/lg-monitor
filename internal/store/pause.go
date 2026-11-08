package store

import (
	"context"
	"crypto/rand"
	"database/sql"
	"errors"
	"fmt"
	"time"
	"unicode/utf8"

	"github.com/LFenX/lg-monitor/internal/rules"
)

type NotificationPause struct {
	Paused     bool    `json:"paused"`
	StartedAt  *int64  `json:"startedAt" jsonschema:"nullable"`
	Until      *int64  `json:"until" jsonschema:"nullable"`
	Reason     *string `json:"reason" jsonschema:"nullable"`
	By         *string `json:"by" jsonschema:"nullable"`
	Suppressed *int64  `json:"suppressed" jsonschema:"nullable"`
}

type pauseQuery interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

func readNotificationPause(ctx context.Context, db pauseQuery, now int64) (NotificationPause, error) {
	var started, until, count int64
	var reason, by string
	err := db.QueryRowContext(ctx, "SELECT started_at,until,reason,by_name,suppressed FROM notification_pauses WHERE ended_at IS NULL AND until>? ORDER BY started_at DESC LIMIT 1", now).Scan(&started, &until, &reason, &by, &count)
	if errors.Is(err, sql.ErrNoRows) {
		return NotificationPause{}, nil
	}
	if err != nil {
		return NotificationPause{}, fmt.Errorf("读取通知暂停状态失败：%w", err)
	}
	return NotificationPause{Paused: true, StartedAt: &started, Until: &until, Reason: &reason, By: &by, Suppressed: &count}, nil
}

func (s *Store) NotificationPause(ctx context.Context) (NotificationPause, error) {
	return readNotificationPause(ctx, s.db, s.now().UnixMilli())
}
func (v *View) NotificationPause(ctx context.Context) (NotificationPause, error) {
	return readNotificationPause(ctx, v.db, v.now().UnixMilli())
}

func (s *Store) SetNotificationPause(ctx context.Context, duration time.Duration, reason, by string) (NotificationPause, error) {
	if duration < time.Minute || duration > 24*time.Hour || !utf8.ValidString(reason) || utf8.RuneCountInString(reason) > 200 {
		return NotificationPause{}, fmt.Errorf("暂停时长必须为 1 分钟到 24 小时，原因最多 200 个字符")
	}
	if by == "" {
		by = "local"
	}
	now := s.now()
	var result NotificationPause
	err := s.operation(ctx, func(ctx context.Context, tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, "UPDATE notification_pauses SET ended_at=until WHERE ended_at IS NULL AND until<=?", now.UnixMilli()); err != nil {
			return fmt.Errorf("结束到期暂停失败：%w", err)
		}
		updated, err := tx.ExecContext(ctx, "UPDATE notification_pauses SET until=?,reason=? WHERE ended_at IS NULL", now.Add(duration).UnixMilli(), reason)
		if err != nil {
			return fmt.Errorf("修改通知暂停失败：%w", err)
		}
		n, err := updated.RowsAffected()
		if err != nil {
			return fmt.Errorf("读取暂停修改结果失败：%w", err)
		}
		if n == 0 {
			if _, err := tx.ExecContext(ctx, "INSERT INTO notification_pauses(id,started_at,until,reason,by_name) VALUES(?,?,?,?,?)", rand.Text(), now.UnixMilli(), now.Add(duration).UnixMilli(), reason, by); err != nil {
				return fmt.Errorf("保存通知暂停失败：%w", err)
			}
		}
		result, err = readNotificationPause(ctx, tx, now.UnixMilli())
		return err
	})
	return result, err
}

func (s *Store) ResumeNotifications(ctx context.Context) (NotificationPause, error) {
	at := s.now().UnixMilli()
	err := s.operation(ctx, func(ctx context.Context, tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, "UPDATE notification_pauses SET ended_at=min(until,?) WHERE ended_at IS NULL", at); err != nil {
			return fmt.Errorf("恢复通知失败：%w", err)
		}
		return nil
	})
	return NotificationPause{}, err
}

func pauseNotification(ctx context.Context, tx *sql.Tx, alert rules.Alert, key string, observed int64) (bool, error) {
	var id string
	err := tx.QueryRowContext(ctx, "SELECT id FROM notification_pauses WHERE started_at<=? AND ?<coalesce(ended_at,until) ORDER BY started_at DESC LIMIT 1", observed, observed).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("查询通知暂停区间失败：%w", err)
	}
	changed, err := tx.ExecContext(ctx, `INSERT INTO notification_events(alert_id,event_key,state,ts,suppressed,pause_id)
VALUES(?,?,?,?,'paused',?) ON CONFLICT(alert_id,event_key) DO UPDATE SET suppressed='paused',pause_id=excluded.pause_id WHERE notification_events.suppressed=''`, alert.ID, key, alert.State, observed, id)
	if err != nil {
		return false, fmt.Errorf("保存暂停通知决策失败：%w", err)
	}
	n, err := changed.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("读取暂停通知计数失败：%w", err)
	}
	if n != 0 {
		if _, err := tx.ExecContext(ctx, "UPDATE notification_pauses SET suppressed=suppressed+1,resumed_at=NULL WHERE id=?", id); err != nil {
			return false, fmt.Errorf("保存暂停通知计数失败：%w", err)
		}
	}
	return true, nil
}

func (s *Store) PreparePauseResumes(ctx context.Context) error {
	at := s.now().UnixMilli()
	return s.operation(ctx, func(ctx context.Context, tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, "UPDATE notification_pauses SET ended_at=until WHERE ended_at IS NULL AND until<=?", at); err != nil {
			return fmt.Errorf("处理暂停到期失败：%w", err)
		}
		// Catch-up has its own durable identity; it does not add another alert lifecycle transition.
		_, err := tx.ExecContext(ctx, `INSERT INTO notification_events(alert_id,event_key,state,ts,resume_pause_id)
SELECT a.id,'resume:'||p.id,a.state,?,p.id FROM notification_pauses p JOIN alerts a
WHERE p.ended_at IS NOT NULL AND p.resumed_at IS NULL
AND EXISTS(SELECT 1 FROM notification_events e WHERE e.pause_id=p.id AND e.alert_id=a.id AND e.suppressed='paused')
AND (a.state='firing' OR (a.state='resolved' AND EXISTS(SELECT 1 FROM notifications n WHERE n.alert_id=a.id AND n.state='firing' AND n.ok=1 AND n.ts<p.started_at)))
AND coalesce((SELECT kind FROM alert_events e WHERE e.alert_id=a.id ORDER BY e.rowid DESC LIMIT 1),'')!='closed'
ON CONFLICT(alert_id,event_key) DO NOTHING`, at)
		if err != nil {
			return fmt.Errorf("登记暂停补发失败：%w", err)
		}
		return completePauseRows(ctx, tx, at)
	})
}

func completePauseRows(ctx context.Context, tx *sql.Tx, at int64) error {
	_, err := tx.ExecContext(ctx, `UPDATE notification_pauses SET resumed_at=? WHERE ended_at IS NOT NULL AND resumed_at IS NULL
AND NOT EXISTS(SELECT 1 FROM notification_events e WHERE e.resume_pause_id=notification_pauses.id AND e.completed_at IS NULL)`, at)
	if err != nil {
		return fmt.Errorf("保存暂停补发完成时间失败：%w", err)
	}
	return nil
}

type PauseNotification struct {
	EventID    int64
	Alert      rules.Alert
	Successful []string
	Skipped    []string
	Allowed    []string
	Attempted  map[string]int64
}

func (s *Store) BeginPauseRecovery(ctx context.Context) (int64, error) {
	at := s.now()
	var watermark int64
	err := s.operation(ctx, func(ctx context.Context, tx *sql.Tx) error {
		if err := tx.QueryRowContext(ctx, "SELECT coalesce(max(rowid),0) FROM notifications").Scan(&watermark); err != nil {
			return fmt.Errorf("读取暂停补发重试水位失败：%w", err)
		}
		// Completed failures receive one new delivery round at startup, within the ordinary retry window.
		if _, err := tx.ExecContext(ctx, `UPDATE notification_events SET completed_at=NULL
WHERE resume_pause_id!='' AND completed_at IS NOT NULL AND ts>=? AND ts<=?
AND EXISTS(SELECT 1 FROM notifications n WHERE n.alert_id=notification_events.alert_id
AND n.event_key=notification_events.event_key AND n.ok=0 AND n.skipped=0
AND NOT EXISTS(SELECT 1 FROM notifications done WHERE done.alert_id=n.alert_id AND done.event_key=n.event_key
AND done.notifier_id=n.notifier_id AND (done.ok=1 OR done.skipped=1)))`, at.Add(-24*time.Hour).UnixMilli(), at.UnixMilli()); err != nil {
			return fmt.Errorf("恢复暂停补发失败渠道失败：%w", err)
		}
		if _, err := tx.ExecContext(ctx, `UPDATE notification_pauses SET resumed_at=NULL
WHERE EXISTS(SELECT 1 FROM notification_events e WHERE e.resume_pause_id=notification_pauses.id AND e.completed_at IS NULL)`); err != nil {
			return fmt.Errorf("恢复暂停补发进度失败：%w", err)
		}
		return nil
	})
	return watermark, err
}

func (s *Store) PendingPauseNotifications(ctx context.Context) ([]PauseNotification, error) {
	return s.PendingPauseNotificationsAfter(ctx, 0)
}

func (s *Store) PendingPauseNotificationsAfter(ctx context.Context, after int64) ([]PauseNotification, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT e.rowid,a.id,a.rule_id,a.collector_id,a.target_id,e.state,a.severity,a.started_at,coalesce(a.resolved_at,0),
coalesce(a.last_notified_at,0),a.summary,a.instance_key,a.acknowledged_at,e.ts,e.event_key,p.started_at
FROM notification_events e JOIN alerts a ON a.id=e.alert_id JOIN notification_pauses p ON p.id=e.resume_pause_id
WHERE e.completed_at IS NULL AND e.rowid>? ORDER BY e.rowid LIMIT 256`, after)
	if err != nil {
		return nil, fmt.Errorf("查询暂停补发失败：%w", err)
	}
	var starts []int64
	result := []PauseNotification{}
	for rows.Next() {
		var item PauseNotification
		var start int64
		a := &item.Alert
		if err := rows.Scan(&item.EventID, &a.ID, &a.RuleID, &a.CollectorID, &a.TargetID, &a.State, &a.Severity, &a.StartedAt, &a.ResolvedAt, &a.LastNotifiedAt, &a.Summary, &a.InstanceKey, &a.AcknowledgedAt, &a.NotificationAt, &a.NotificationKey, &start); err != nil {
			rows.Close()
			return nil, fmt.Errorf("读取暂停补发失败：%w", err)
		}
		if a.State == rules.StateFiring {
			a.ResolvedAt = 0
		}
		result, starts = append(result, item), append(starts, start)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, fmt.Errorf("遍历暂停补发失败：%w", err)
	}
	for i := range result {
		item := &result[i]
		attempts, err := s.db.QueryContext(ctx, "SELECT notifier_id,max(ok),max(skipped),max(rowid) FROM notifications WHERE alert_id=? AND event_key=? GROUP BY notifier_id", item.Alert.ID, item.Alert.NotificationKey)
		if err != nil {
			return nil, fmt.Errorf("查询暂停补发发送记录失败：%w", err)
		}
		item.Attempted = map[string]int64{}
		for attempts.Next() {
			var id string
			var ok, skipped bool
			var last int64
			if err := attempts.Scan(&id, &ok, &skipped, &last); err != nil {
				attempts.Close()
				return nil, fmt.Errorf("读取暂停补发发送记录失败：%w", err)
			}
			item.Attempted[id] = last
			if ok {
				item.Successful = append(item.Successful, id)
			} else if skipped {
				item.Skipped = append(item.Skipped, id)
			}
		}
		err = attempts.Err()
		attempts.Close()
		if err != nil {
			return nil, fmt.Errorf("遍历暂停补发发送记录失败：%w", err)
		}
		if item.Alert.State == rules.StateResolved {
			channels, err := s.db.QueryContext(ctx, "SELECT DISTINCT notifier_id FROM notifications WHERE alert_id=? AND state='firing' AND ok=1 AND ts<?", item.Alert.ID, starts[i])
			if err != nil {
				return nil, fmt.Errorf("查询暂停前触发渠道失败：%w", err)
			}
			for channels.Next() {
				var id string
				if err := channels.Scan(&id); err != nil {
					channels.Close()
					return nil, fmt.Errorf("读取暂停前通知渠道失败：%w", err)
				}
				item.Allowed = append(item.Allowed, id)
			}
			err = channels.Err()
			channels.Close()
			if err != nil {
				return nil, fmt.Errorf("遍历暂停前通知渠道失败：%w", err)
			}
		}
	}
	return result, nil
}

func (s *Store) CompletePauseNotification(ctx context.Context, alert, key string) error {
	at := s.now().UnixMilli()
	return s.operation(ctx, func(ctx context.Context, tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, "UPDATE notification_events SET completed_at=? WHERE alert_id=? AND event_key=? AND resume_pause_id!=''", at, alert, key); err != nil {
			return fmt.Errorf("保存暂停补发结果失败：%w", err)
		}
		return completePauseRows(ctx, tx, at)
	})
}
