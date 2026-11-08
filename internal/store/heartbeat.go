package store

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/LFenX/lg-monitor/internal/pipeline"
)

func (s *Store) EnsureCollector(ctx context.Context, collector CollectorRecord) error {
	if collector.ID == "" {
		return fmt.Errorf("采集器 ID 不能为空")
	}
	return s.operation(ctx, func(ctx context.Context, tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `INSERT INTO collectors(id,hostname,first_seen,last_seen,version) VALUES(?,?,?,?,?) ON CONFLICT(id) DO NOTHING`, collector.ID, collector.Hostname, collector.FirstSeen, collector.LastSeen, collector.Version)
		if err != nil {
			return fmt.Errorf("补齐配对采集器信息失败：%w", err)
		}
		return nil
	})
}

func (s *Store) Heartbeat(ctx context.Context, id string, cache ...*pipeline.CacheUsage) error {
	if id == "" {
		return fmt.Errorf("心跳缺少采集器身份")
	}
	at := s.now().UnixMilli()
	var bytes, maxBytes, rate any
	if len(cache) > 0 && cache[0] != nil {
		value, err := pipeline.NewCacheUsage(cache[0].Bytes, cache[0].MaxBytes, cache[0].BytesPerHour)
		if err != nil {
			return err
		}
		bytes, maxBytes, rate = value.Bytes, value.MaxBytes, value.BytesPerHour
	}
	return s.operation(ctx, func(ctx context.Context, tx *sql.Tx) error {
		result, err := tx.ExecContext(ctx, `UPDATE collectors SET last_seen=?,cache_bytes=?,cache_max_bytes=?,cache_bytes_per_hour=? WHERE id=?`, at, bytes, maxBytes, rate, id)
		if err != nil {
			return fmt.Errorf("保存采集器心跳失败：%w", err)
		}
		changed, err := result.RowsAffected()
		if err != nil {
			return fmt.Errorf("读取心跳保存结果失败：%w", err)
		}
		if changed != 1 {
			return fmt.Errorf("心跳采集器尚未登记，请重新连接")
		}
		return nil
	})
}
