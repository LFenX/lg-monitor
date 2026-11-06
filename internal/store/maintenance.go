package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/LFenX/lg-monitor/internal/config"
)

func (s *Store) SetRetention(policy config.Retention) error {
	value, err := policy.Effective()
	if err != nil {
		return err
	}
	s.retentionMu.Lock()
	s.retention = value
	s.retentionMu.Unlock()
	return nil
}

func (s *Store) maintenanceOperation(ctx context.Context, action func(context.Context, *sql.Tx) error) error {
	req := request{action: action, done: make(chan error, 1), ctx: ctx}
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-s.stopping:
		return s.stoppedError()
	case s.maintenance <- req:
	}
	select {
	case err := <-req.done:
		return err
	case <-ctx.Done():
		return ctx.Err()
	case <-s.stopping:
		return s.stoppedError()
	}
}

func (s *Store) maintenanceLoop(ctx context.Context, ticker Ticker) {
	defer close(s.maintenanceDone)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.Ticks():
			runCtx, cancel := context.WithTimeout(ctx, 50*time.Second)
			err := s.Prune(runCtx)
			cancel()
			if err != nil && ctx.Err() == nil {
				s.logger.Warn("Background aggregation or retention failed; retrying next minute", "error", err)
			}
		}
	}
}

func (s *Store) Prune(ctx context.Context) error { return s.maintain(ctx, s.aggregates) }

func (s *Store) maintain(ctx context.Context, source aggregateSource) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-s.stopping:
		return s.stoppedError()
	case s.maintenanceGate <- struct{}{}:
		defer func() { <-s.maintenanceGate }()
	}
	if err := s.Flush(ctx); err != nil {
		return err
	}
	now := s.now().UnixMilli()
	for _, level := range []string{"minute", "hour"} {
		for {
			if err := ctx.Err(); err != nil {
				return err
			}
			bucket, found, err := source.Next(ctx, level, now)
			if err != nil {
				return err
			}
			if !found {
				break
			}
			if err := s.commitAggregate(ctx, bucket); err != nil {
				return err
			}
		}
	}
	return s.cleanExpired(ctx, now)
}

func (s *Store) commitAggregate(ctx context.Context, bucket aggregateBucket) error {
	return s.maintenanceOperation(ctx, func(ctx context.Context, tx *sql.Tx) error {
		var version int64
		if err := tx.QueryRowContext(ctx, `SELECT version FROM rollup_dirty WHERE source_table=? AND level=? AND ts=?`, bucket.source, bucket.level, bucket.ts).Scan(&version); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return nil
			}
			return fmt.Errorf("检查汇总版本失败：%w", err)
		}
		// A writer may have committed after the read snapshot; rebuild instead of publishing stale totals.
		if version != bucket.version {
			return nil
		}
		if !bucket.finalized {
			query, args := "DELETE FROM metric_"+bucket.level+" WHERE source_table=? AND ts=?", []any{bucket.source, bucket.ts}
			if bucket.source == "probe_results" {
				query, args = "DELETE FROM probe_"+bucket.level+" WHERE ts=?", []any{bucket.ts}
			}
			if _, err := tx.ExecContext(ctx, query, args...); err != nil {
				return fmt.Errorf("替换时间桶旧汇总失败：%w", err)
			}
			metric, err := tx.PrepareContext(ctx, "INSERT INTO metric_"+bucket.level+"(source_table,collector_id,target_id,name,resource,probe,ts,samples,sum_value,max_value) VALUES(?,?,?,?,?,?,?,?,?,?)")
			if err != nil {
				return fmt.Errorf("准备指标汇总写入失败：%w", err)
			}
			defer metric.Close()
			for _, row := range bucket.metrics {
				if _, err := metric.ExecContext(ctx, bucket.source, row.collector, row.target, row.name, row.resource, row.probe, bucket.ts, row.samples, row.sum, row.maximum); err != nil {
					return fmt.Errorf("保存指标汇总失败：%w", err)
				}
			}
			probe, err := tx.PrepareContext(ctx, "INSERT INTO probe_"+bucket.level+"(collector_id,target_id,probe,ts,samples,successes,latency_sum) VALUES(?,?,?,?,?,?,?)")
			if err != nil {
				return fmt.Errorf("准备探测汇总写入失败：%w", err)
			}
			defer probe.Close()
			for _, row := range bucket.probes {
				if _, err := probe.ExecContext(ctx, row.collector, row.target, row.probe, bucket.ts, row.samples, row.successes, row.sum); err != nil {
					return fmt.Errorf("保存探测汇总失败：%w", err)
				}
			}
			if _, err := tx.ExecContext(ctx, `INSERT INTO rollup_state(source_table,level,ts) VALUES(?,?,?) ON CONFLICT DO NOTHING`, bucket.source, bucket.level, bucket.ts); err != nil {
				return fmt.Errorf("保存时间桶汇总状态失败：%w", err)
			}
			if bucket.level == "minute" {
				if _, err := tx.ExecContext(ctx, `INSERT INTO rollup_dirty(source_table,level,ts,version) VALUES(?,'hour',?,1) ON CONFLICT(source_table,level,ts) DO UPDATE SET version=version+1`, bucket.source, floorBucket(bucket.ts, hourMS)); err != nil {
					return fmt.Errorf("标记小时汇总更新失败：%w", err)
				}
			}
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM rollup_dirty WHERE source_table=? AND level=? AND ts=? AND version=?`, bucket.source, bucket.level, bucket.ts, version); err != nil {
			return fmt.Errorf("清除已汇总时间桶失败：%w", err)
		}
		return nil
	})
}

func (s *Store) maintenanceBatches(ctx context.Context, query string, args ...any) error {
	for {
		var affected int64
		if err := s.maintenanceOperation(ctx, func(ctx context.Context, tx *sql.Tx) error {
			result, err := tx.ExecContext(ctx, query, args...)
			if err != nil {
				return fmt.Errorf("清理分批数据失败：%w", err)
			}
			affected, err = result.RowsAffected()
			if err != nil {
				return fmt.Errorf("读取后台批次结果失败：%w", err)
			}
			return nil
		}); err != nil {
			return err
		}
		if affected == 0 {
			return nil
		}
	}
}

func (s *Store) cleanExpired(ctx context.Context, now int64) error {
	s.retentionMu.RLock()
	policy := s.retention
	s.retentionMu.RUnlock()
	rawCutoff := floorBucket(now-policy.Raw.Milliseconds(), hourMS)
	minuteCutoff := floorBucket(now-policy.Minute.Milliseconds(), hourMS)
	hourCutoff := floorBucket(now-policy.Hour.Milliseconds(), hourMS)
	for _, table := range []string{"probe_results", "process_samples", "system_metrics", "disk_metrics", "network_metrics", "script_metrics", "target_status", "collector_events"} {
		if err := s.cleanRawTable(ctx, table, rawCutoff); err != nil {
			return fmt.Errorf("清理 %s 原始数据失败：%w", table, err)
		}
	}
	if err := s.maintenanceBatches(ctx, `UPDATE rollup_state SET finalized=1 WHERE rowid IN
(SELECT s.rowid FROM rollup_state s JOIN raw_watermarks w ON w.table_name=s.source_table
WHERE s.level='minute' AND s.finalized=0 AND s.ts+60000<=w.raw_complete_from LIMIT 500)`); err != nil {
		return err
	}
	if err := s.maintenanceBatches(ctx, `UPDATE rollup_state SET finalized=1 WHERE rowid IN
(SELECT s.rowid FROM rollup_state s WHERE s.level='hour' AND s.finalized=0 AND s.ts+3600000<=?
AND NOT EXISTS(SELECT 1 FROM rollup_dirty d WHERE d.source_table=s.source_table AND
((d.level='hour' AND d.ts=s.ts) OR (d.level='minute' AND d.ts>=s.ts AND d.ts<s.ts+3600000))) LIMIT 500)`, minuteCutoff); err != nil {
		return err
	}
	if err := s.maintenanceBatches(ctx, `DELETE FROM service_status WHERE rowid IN
(SELECT s.rowid FROM service_status s WHERE ts<? AND ts<(SELECT max(l.ts) FROM service_status l WHERE l.collector_id=s.collector_id AND l.name=s.name) LIMIT 500)`, rawCutoff); err != nil {
		return err
	}
	for _, table := range []string{"metric_minute", "probe_minute"} {
		source := "r.source_table"
		if table == "probe_minute" {
			source = "'probe_results'"
		}
		if err := s.maintenanceBatches(ctx, `DELETE FROM `+table+` WHERE rowid IN
(SELECT r.rowid FROM `+table+` r WHERE ts<? AND EXISTS
(SELECT 1 FROM rollup_state s WHERE s.source_table=`+source+` AND s.level='hour' AND s.ts=r.ts/3600000*3600000 AND s.finalized=1) LIMIT 500)`, minuteCutoff); err != nil {
			return err
		}
	}
	for _, table := range []string{"metric_hour", "probe_hour"} {
		if err := s.maintenanceBatches(ctx, "DELETE FROM "+table+" WHERE rowid IN (SELECT rowid FROM "+table+" WHERE ts<? LIMIT 500)", hourCutoff); err != nil {
			return err
		}
	}
	return s.maintenanceBatches(ctx, `DELETE FROM rollup_state WHERE rowid IN
(SELECT s.rowid FROM rollup_state s WHERE ts<? AND NOT EXISTS
(SELECT 1 FROM rollup_dirty d WHERE d.source_table=s.source_table AND d.level=s.level AND d.ts=s.ts) LIMIT 500)`, hourCutoff)
}

func (s *Store) cleanRawTable(ctx context.Context, table string, cutoff int64) error {
	for {
		var affected int64
		if err := s.maintenanceOperation(ctx, func(ctx context.Context, tx *sql.Tx) error {
			var dirty bool
			if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM rollup_dirty WHERE source_table=? AND level='minute' AND ts<?)`, table, cutoff).Scan(&dirty); err != nil {
				return fmt.Errorf("检查清理前汇总进度失败：%w", err)
			}
			if dirty {
				return nil
			}
			if _, err := tx.ExecContext(ctx, `INSERT INTO raw_watermarks(table_name,raw_complete_from) VALUES(?,?) ON CONFLICT(table_name) DO UPDATE SET raw_complete_from=max(raw_complete_from,excluded.raw_complete_from)`, table, cutoff); err != nil {
				return fmt.Errorf("保存原始数据完整起点失败：%w", err)
			}
			result, err := tx.ExecContext(ctx, "DELETE FROM "+table+" WHERE rowid IN (SELECT rowid FROM "+table+" WHERE ts<(SELECT raw_complete_from FROM raw_watermarks WHERE table_name=?) LIMIT 500)", table)
			if err != nil {
				return fmt.Errorf("清理原始数据分批失败：%w", err)
			}
			affected, err = result.RowsAffected()
			if err != nil {
				return fmt.Errorf("读取原始清理结果失败：%w", err)
			}
			return nil
		}); err != nil {
			return err
		}
		if affected == 0 {
			return nil
		}
	}
}
