package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"time"

	"github.com/LFenX/lg-monitor/internal/rules"
)

var ErrNoData = errors.New("尚无监控数据，请先运行 lgmon run --role both")

type View struct {
	db   *sql.DB
	now  func() time.Time
	path string
}

func OpenView(ctx context.Context, path string) (*View, error) {
	return OpenViewWithClock(ctx, path, time.Now)
}

func OpenViewWithClock(ctx context.Context, path string, now func() time.Time) (*View, error) {
	if now == nil {
		return nil, fmt.Errorf("只读查询需要时钟")
	}
	dsn, err := readOnlyDSN(path)
	if err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("打开只读监控数据库失败：%w", err)
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	if err := db.PingContext(ctx); err != nil {
		db.Close()
		return nil, fmt.Errorf("连接只读监控数据库失败：%w", err)
	}
	absolute, err := filepath.Abs(path)
	if err != nil {
		db.Close()
		return nil, fmt.Errorf("保存只读数据库路径失败：%w", err)
	}
	return &View{db: db, now: now, path: absolute}, nil
}

func readOnlyDSN(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", ErrNoData
		}
		if errors.Is(err, os.ErrPermission) {
			return "", fmt.Errorf("读取服务数据需要以管理员身份运行终端：%w", err)
		}
		return "", fmt.Errorf("读取监控数据库信息失败：%w", err)
	}
	defer file.Close()
	absolute, err := filepath.Abs(path)
	if err != nil {
		return "", fmt.Errorf("解析监控数据库路径失败：%w", err)
	}
	uriPath := filepath.ToSlash(absolute)
	if filepath.VolumeName(absolute) != "" {
		uriPath = "/" + uriPath
	}
	uri := url.URL{Scheme: "file", Path: uriPath}
	uri.RawQuery = url.Values{"mode": {"ro"}, "_pragma": {"query_only(1)", "busy_timeout(5000)"}}.Encode()
	return uri.String(), nil
}

func (v *View) Close() error { return v.db.Close() }

func (v *View) OpenReplayQueries(ctx context.Context) (rules.ReplayQueryConnection, error) {
	return OpenRuleQueries(ctx, v.path)
}

func (v *View) CurrentTargets(ctx context.Context) ([]TargetRecord, error) {
	return currentTargets(ctx, v.db)
}

type DeliveryReceipt struct {
	OK        bool   `json:"ok"`
	TS        int64  `json:"ts"`
	MessageID string `json:"messageId"`
}

func (v *View) Delivery(ctx context.Context, alertID, state, notifierID string) (DeliveryReceipt, error) {
	if alertID == "" || notifierID == "" || (state != rules.StateFiring && state != rules.StateResolved) {
		return DeliveryReceipt{}, fmt.Errorf("通知查询需要告警、渠道和 firing/resolved 状态")
	}
	var receipt DeliveryReceipt
	err := v.db.QueryRowContext(ctx, `SELECT ts,message_id FROM notifications WHERE alert_id=? AND state=? AND notifier_id=? AND ok=1 ORDER BY ts DESC,rowid DESC LIMIT 1`, alertID, state, notifierID).Scan(&receipt.TS, &receipt.MessageID)
	if errors.Is(err, sql.ErrNoRows) {
		return receipt, nil
	}
	if err != nil {
		return receipt, fmt.Errorf("查询通知成功记录失败：%w", err)
	}
	receipt.OK = true
	return receipt, nil
}

func (v *View) Alerts(ctx context.Context, all bool) ([]rules.Alert, error) {
	state := "all"
	if !all {
		state = "firing"
	}
	return v.alertHistory(ctx, state, 0, 0)
}

func (v *View) AlertHistory(ctx context.Context, state string, since int64) ([]rules.Alert, error) {
	return v.alertHistory(ctx, state, since, MaxHistoryPoints)
}

func (v *View) alertHistory(ctx context.Context, state string, since int64, limit int) ([]rules.Alert, error) {
	query := `SELECT id,rule_id,collector_id,target_id,state,severity,started_at,coalesce(resolved_at,0),coalesce(last_notified_at,0),summary,instance_key,acknowledged_at,notification_at,notification_key,EXISTS(SELECT 1 FROM alert_groups g WHERE g.rule_id=alerts.rule_id AND g.collector_id=alerts.collector_id AND g.target_id=alerts.target_id AND g.instance_key=alerts.instance_key AND g.flapping=1 AND g.changed_at>?) FROM alerts`
	query += ` WHERE started_at>=?`
	args := []any{v.now().Add(-10 * time.Minute).UnixMilli(), since}
	if state != "all" {
		query += ` AND state=?`
		args = append(args, state)
	}
	query += ` ORDER BY started_at DESC,id`
	if limit > 0 {
		query += ` LIMIT ?`
		args = append(args, limit+1)
	}
	rows, err := v.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("查询告警失败：%w", err)
	}
	defer rows.Close()
	result := make([]rules.Alert, 0)
	for rows.Next() {
		var alert rules.Alert
		if err := rows.Scan(&alert.ID, &alert.RuleID, &alert.CollectorID, &alert.TargetID, &alert.State, &alert.Severity, &alert.StartedAt, &alert.ResolvedAt, &alert.LastNotifiedAt, &alert.Summary, &alert.InstanceKey, &alert.AcknowledgedAt, &alert.NotificationAt, &alert.NotificationKey, &alert.Flapping); err != nil {
			return nil, fmt.Errorf("读取告警失败：%w", err)
		}
		result = append(result, alert)
		if limit > 0 && len(result) > limit {
			return nil, ErrHistoryTooLarge
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("遍历告警失败：%w", err)
	}
	return result, nil
}
