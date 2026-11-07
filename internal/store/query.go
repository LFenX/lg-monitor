package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
)

func (s *Store) Collector(ctx context.Context, id string) (CollectorRecord, bool, error) {
	return collectorRecord(ctx, s.db, id)
}

func (v *View) Collector(ctx context.Context, id string) (CollectorRecord, bool, error) {
	return collectorRecord(ctx, v.db, id)
}

func collectorRecord(ctx context.Context, db *sql.DB, id string) (CollectorRecord, bool, error) {
	var record CollectorRecord
	err := db.QueryRowContext(ctx, `SELECT id,coalesce(hostname,''),coalesce(first_seen,0),coalesce(last_seen,0),coalesce(version,''),clock_offset_ms FROM collectors WHERE id=?`, id).Scan(&record.ID, &record.Hostname, &record.FirstSeen, &record.LastSeen, &record.Version, &record.ClockOffsetMS)
	if errors.Is(err, sql.ErrNoRows) {
		return record, false, nil
	}
	if err != nil {
		return record, false, fmt.Errorf("查询采集器信息失败：%w", err)
	}
	return record, true, nil
}

type TargetRecord struct {
	CollectorID string          `json:"collectorId"`
	TargetID    string          `json:"targetId"`
	TS          int64           `json:"ts"`
	State       string          `json:"state"`
	Detail      json.RawMessage `json:"detail"`
}

func (s *Store) CurrentTargets(ctx context.Context) ([]TargetRecord, error) {
	return currentTargets(ctx, s.db)
}

func currentTargets(ctx context.Context, db *sql.DB) ([]TargetRecord, error) {
	rows, err := db.QueryContext(ctx, `SELECT t.collector_id,t.target_id,t.ts,t.state,t.detail FROM target_status t JOIN (SELECT collector_id,target_id,max(ts) AS ts FROM target_status GROUP BY collector_id,target_id) latest USING(collector_id,target_id,ts) ORDER BY t.collector_id,t.target_id`)
	if err != nil {
		return nil, fmt.Errorf("查询当前目标状态失败：%w", err)
	}
	defer rows.Close()
	result := make([]TargetRecord, 0)
	for rows.Next() {
		var row TargetRecord
		var detail string
		if err := rows.Scan(&row.CollectorID, &row.TargetID, &row.TS, &row.State, &detail); err != nil {
			return nil, fmt.Errorf("读取当前目标状态失败：%w", err)
		}
		row.Detail = json.RawMessage(detail)
		result = append(result, row)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("遍历目标状态失败：%w", err)
	}
	return result, nil
}
