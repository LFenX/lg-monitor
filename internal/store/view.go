package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"

	"github.com/LFenX/lg-monitor/internal/rules"
)

var ErrNoData = errors.New("尚无监控数据，请先运行 lgmon run --role both")

type View struct{ db *sql.DB }

func OpenView(ctx context.Context, path string) (*View, error) {
	if _, err := os.Stat(path); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, ErrNoData
		}
		return nil, fmt.Errorf("读取监控数据库信息失败：%w", err)
	}
	absolute, err := filepath.Abs(path)
	if err != nil {
		return nil, fmt.Errorf("解析监控数据库路径失败：%w", err)
	}
	uriPath := filepath.ToSlash(absolute)
	if filepath.VolumeName(absolute) != "" {
		uriPath = "/" + uriPath
	}
	uri := url.URL{Scheme: "file", Path: uriPath}
	uri.RawQuery = url.Values{"mode": {"ro"}, "_pragma": {"query_only(1)", "busy_timeout(5000)"}}.Encode()
	db, err := sql.Open("sqlite", uri.String())
	if err != nil {
		return nil, fmt.Errorf("打开只读监控数据库失败：%w", err)
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	if err := db.PingContext(ctx); err != nil {
		db.Close()
		return nil, fmt.Errorf("连接只读监控数据库失败：%w", err)
	}
	return &View{db: db}, nil
}

func (v *View) Close() error { return v.db.Close() }

func (v *View) CurrentTargets(ctx context.Context) ([]TargetRecord, error) {
	return currentTargets(ctx, v.db)
}

func (v *View) Alerts(ctx context.Context, all bool) ([]rules.Alert, error) {
	query := `SELECT id,rule_id,collector_id,target_id,state,severity,started_at,coalesce(resolved_at,0),coalesce(last_notified_at,0),summary FROM alerts`
	if !all {
		query += ` WHERE state='firing'`
	}
	query += ` ORDER BY started_at DESC,id`
	rows, err := v.db.QueryContext(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("查询告警失败：%w", err)
	}
	defer rows.Close()
	result := make([]rules.Alert, 0)
	for rows.Next() {
		var alert rules.Alert
		if err := rows.Scan(&alert.ID, &alert.RuleID, &alert.CollectorID, &alert.TargetID, &alert.State, &alert.Severity, &alert.StartedAt, &alert.ResolvedAt, &alert.LastNotifiedAt, &alert.Summary); err != nil {
			return nil, fmt.Errorf("读取告警失败：%w", err)
		}
		result = append(result, alert)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("遍历告警失败：%w", err)
	}
	return result, nil
}
