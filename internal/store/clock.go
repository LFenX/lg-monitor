package store

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/LFenX/lg-monitor/internal/collector"
)

func (s *Store) SetClockOffset(ctx context.Context, id string, offset int64) error {
	at := s.now().UnixMilli()
	return s.operation(ctx, func(ctx context.Context, tx *sql.Tx) error {
		var previous int64
		if err := tx.QueryRowContext(ctx, "SELECT clock_offset_ms FROM collectors WHERE id=?", id).Scan(&previous); err != nil {
			return fmt.Errorf("读取采集器时钟偏差失败：%w", err)
		}
		if _, err := tx.ExecContext(ctx, "UPDATE collectors SET clock_offset_ms=? WHERE id=?", offset, id); err != nil {
			return fmt.Errorf("保存采集器时钟偏差失败：%w", err)
		}
		before, after := collector.ClockSkewed(previous), collector.ClockSkewed(offset)
		if before == after {
			return nil
		}
		kind, message := "clock_skew", collector.ClockWarning(offset)
		if !after {
			kind, message = "clock_synced", "采集器时钟偏差已恢复到十秒以内"
		}
		_, err := tx.ExecContext(ctx, `INSERT INTO collector_events(collector_id,seq,ts,kind,message,gap_from,gap_to,dropped)
VALUES(?,(SELECT min(coalesce(min(seq),0),0)-1 FROM collector_events WHERE collector_id=?),?,?,?,0,0,0)`, id, id, at, kind, message)
		if err != nil {
			return fmt.Errorf("保存采集器时钟变化事件失败：%w", err)
		}
		return nil
	})
}

func (v *View) Collectors(ctx context.Context) ([]CollectorRecord, error) {
	return collectors(ctx, v.db, v.cacheStats)
}

func (s *Store) Collectors(ctx context.Context) ([]CollectorRecord, error) {
	return collectors(ctx, s.db, true)
}

func collectors(ctx context.Context, db *sql.DB, cache bool) ([]CollectorRecord, error) {
	rows, err := db.QueryContext(ctx, "SELECT "+collectorColumns(cache)+" FROM collectors ORDER BY id")
	if err != nil {
		return nil, fmt.Errorf("查询采集器时钟信息失败：%w", err)
	}
	defer rows.Close()
	result := []CollectorRecord{}
	for rows.Next() {
		item, err := scanCollector(rows)
		if err != nil {
			return nil, fmt.Errorf("读取采集器时钟信息失败：%w", err)
		}
		result = append(result, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("遍历采集器时钟信息失败：%w", err)
	}
	return result, nil
}
