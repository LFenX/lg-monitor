// Copyright (c) 2026 LFen (https://lfen.ink) — lg-monitor

package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/LFenX/lg-monitor/internal/config"
)

// jobEventRetention is how long batch job events are kept.
const jobEventRetention = 400 * 24 * time.Hour

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

func (s *Store) rawMaintenanceOperation(ctx context.Context, action func(context.Context, *sql.DB) error) error {
	req := request{rawAction: action, done: make(chan error, 1), ctx: ctx}
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
		case <-s.tasksArrived:
			// Without this, scheduled tasks wait up to a minute after start before their state is known.
			runCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
			err := s.recordTasks(runCtx, s.now().UnixMilli())
			cancel()
			if err != nil && ctx.Err() == nil {
				s.logger.Warn("Recording task states after the first inventory failed; retrying next minute", "error", err)
			}
		case <-ticker.Ticks():
			runCtx, cancel := context.WithTimeout(ctx, 50*time.Second)
			err := s.Prune(runCtx)
			cancel()
			if err != nil && ctx.Err() == nil {
				s.logger.Warn("Background aggregation or retention failed; retrying next minute", "error", err)
				continue
			}
			s.autoVacuumIfBloated(ctx)
		}
	}
}

// MaintenanceStats reports the most recent timestamps of background store housekeeping.
type MaintenanceStats struct {
	LastPruneAt         int64 `json:"lastPruneAt,omitempty"`
	LastVacuumAt        int64 `json:"lastVacuumAt,omitempty"`
	LastArchiveAt       int64 `json:"lastArchiveAt,omitempty"`
	LastArchivedRecords int   `json:"lastArchivedRecords,omitempty"`
}

func (s *Store) MaintenanceStats() MaintenanceStats {
	s.rollupMu.Lock()
	defer s.rollupMu.Unlock()
	return MaintenanceStats{
		LastPruneAt:         s.lastPruneAt,
		LastVacuumAt:        s.lastVacuum,
		LastArchiveAt:       s.lastArchiveAt,
		LastArchivedRecords: s.lastArchivedRecords,
	}
}

// autoVacuumIfBloated reclaims disk space when deleted rows leave >= 16 MiB (4096 pages) and >= 20% of
// the database file, or >= 64 MiB (16384 pages) regardless of ratio, on the SQLite freelist,
// throttled to at most once every 30 minutes.
func (s *Store) autoVacuumIfBloated(ctx context.Context) {
	now := s.now().UnixMilli()
	s.rollupMu.Lock()
	if now-s.lastVacuum < int64(30*time.Minute/time.Millisecond) {
		s.rollupMu.Unlock()
		return
	}
	s.rollupMu.Unlock()

	var pages, free int64
	if err := s.maintenanceView.db.QueryRowContext(ctx, `SELECT (SELECT page_count FROM pragma_page_count()), (SELECT freelist_count FROM pragma_freelist_count())`).Scan(&pages, &free); err != nil {
		return
	}
	if !(free >= 4096 && free*5 >= pages) && free < 16384 {
		return
	}
	s.rollupMu.Lock()
	s.lastVacuum = now
	s.rollupMu.Unlock()

	vacCtx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	_ = s.Checkpoint(vacCtx, "PASSIVE")
	if err := s.rawMaintenanceOperation(vacCtx, func(ctx context.Context, db *sql.DB) error {
		_, err := db.ExecContext(ctx, "VACUUM")
		return err
	}); err == nil {
		s.truncateWALWithRetry(vacCtx)
	}
}

func (s *Store) truncateWALWithRetry(ctx context.Context) {
	for attempt := 0; attempt < 3; attempt++ {
		if err := s.Checkpoint(ctx, "TRUNCATE"); err == nil {
			return
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(200 * time.Millisecond):
		}
	}
	_ = s.Checkpoint(ctx, "PASSIVE")
}

// Compact prunes expired samples, trims bulky historical full-table inventory snapshots beyond 6 hours,
// runs SQLite VACUUM on the writer connection, and truncates the WAL file.
func (s *Store) Compact(ctx context.Context) error {
	if err := s.Prune(ctx); err != nil {
		s.logger.Warn("Pre-compact prune returned warning", "error", err)
	}
	now := s.now().UnixMilli()
	invCutoff := now - int64(6*time.Hour/time.Millisecond)
	_ = s.maintenanceBatches(ctx, `DELETE FROM inventory_snapshots WHERE rowid IN (SELECT rowid FROM inventory_snapshots WHERE ts<? LIMIT 5000)`, invCutoff)
	_ = s.Checkpoint(ctx, "PASSIVE")
	if err := s.rawMaintenanceOperation(ctx, func(ctx context.Context, db *sql.DB) error {
		_, err := db.ExecContext(ctx, "VACUUM")
		return err
	}); err != nil {
		return fmt.Errorf("压缩监控数据库失败：%w", err)
	}
	if err := s.Checkpoint(ctx, "TRUNCATE"); err != nil {
		s.truncateWALWithRetry(ctx)
	}
	s.rollupMu.Lock()
	s.lastVacuum = now
	s.rollupMu.Unlock()
	return nil
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
	if err := s.recordTasks(ctx, now); err != nil {
		return err
	}
	if err := s.rollupStatus(ctx, now); err != nil {
		return err
	}
	if err := s.cleanExpired(ctx, now); err != nil {
		return err
	}
	s.rollupMu.Lock()
	s.lastPruneAt = now
	s.rollupMu.Unlock()
	return nil
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
	archiving := !s.disableArchive && s.archiveDir != "" && policy.Archive > 0
	// Active cards keep their deduplication receipts even when raw observations expire.
	if err := s.maintenanceBatches(ctx, `DELETE FROM card_action_events WHERE rowid IN (SELECT e.rowid FROM card_action_events e WHERE e.ts<? AND NOT EXISTS(SELECT 1 FROM alerts a WHERE a.id=e.alert_id AND a.state='firing') LIMIT 5000)`, rawCutoff); err != nil {
		return err
	}
	if err := s.maintenanceBatches(ctx, `DELETE FROM silences WHERE rowid IN (SELECT rowid FROM silences WHERE origin='feishu_card' AND ends_at<? LIMIT 5000)`, rawCutoff); err != nil {
		return err
	}
	if err := s.maintenanceBatches(ctx, `DELETE FROM silences WHERE rowid IN (SELECT rowid FROM silences WHERE ends_at<? LIMIT 5000)`, minuteCutoff); err != nil {
		return err
	}
	// Batch job events are business history: kept for a year and a month whatever the raw policy.
	if err := s.maintenanceBatches(ctx, `DELETE FROM job_events WHERE (job_id,event_id) IN (SELECT job_id,event_id FROM job_events WHERE received_at<? LIMIT 5000)`, now-jobEventRetention.Milliseconds()); err != nil {
		return err
	}
	rawArchivedMax := make(map[string]int64)
	if archiving {
		for _, table := range []string{"probe_results", "target_status", "collector_events", "eventlog_events", "inventory_changes"} {
			effectiveCutoff, dirty, err := s.effectiveRawCleanupCutoff(ctx, table, rawCutoff)
			if err != nil {
				return fmt.Errorf("检查 %s 归档水位失败：%w", table, err)
			}
			if dirty {
				continue
			}
			maxID, _, err := s.archiveExpiredForTable(ctx, "raw", table, "ts<?", []any{effectiveCutoff}, now)
			if err != nil {
				return fmt.Errorf("归档 %s 历史数据失败：%w", table, err)
			}
			rawArchivedMax[table] = maxID
		}
	}
	for _, table := range []string{"probe_results", "process_samples", "system_metrics", "disk_metrics", "network_metrics", "script_metrics", "target_status", "collector_events", "eventlog_events", "inventory_snapshots", "inventory_changes"} {
		maxRowID := int64(-1)
		skipDelete := false
		if archiving {
			if bound, ok := rawArchivedMax[table]; ok {
				maxRowID = bound
				skipDelete = bound < 0
			} else if table == "probe_results" || table == "target_status" || table == "collector_events" || table == "eventlog_events" || table == "inventory_changes" {
				// Rollup dirty blocked archiving; cleanRawTableBounded will also observe dirty=true and skip.
				skipDelete = true
			}
		}
		if err := s.cleanRawTableBounded(ctx, table, rawCutoff, maxRowID, skipDelete); err != nil {
			return fmt.Errorf("清理 %s 原始数据失败：%w", table, err)
		}
	}
	if err := s.maintenanceBatches(ctx, `UPDATE rollup_state SET finalized=1 WHERE rowid IN
(SELECT s.rowid FROM rollup_state s JOIN raw_watermarks w ON w.table_name=s.source_table
WHERE s.level='minute' AND s.finalized=0 AND s.ts+60000<=w.raw_complete_from LIMIT 5000)`); err != nil {
		return err
	}
	if err := s.maintenanceBatches(ctx, `UPDATE rollup_state SET finalized=1 WHERE rowid IN
(SELECT s.rowid FROM rollup_state s WHERE s.level='hour' AND s.finalized=0 AND s.ts+3600000<=?
AND NOT EXISTS(SELECT 1 FROM rollup_dirty d WHERE d.source_table=s.source_table AND
((d.level='hour' AND d.ts=s.ts) OR (d.level='minute' AND d.ts>=s.ts AND d.ts<s.ts+3600000))) LIMIT 5000)`, minuteCutoff); err != nil {
		return err
	}
	if archiving {
		maxID, _, err := s.archiveExpiredForTable(ctx, "raw", "service_status", `s.ts<? AND s.ts<(SELECT max(l.ts) FROM service_status l WHERE l.collector_id=s.collector_id AND l.name=s.name)`, []any{rawCutoff}, now)
		if err != nil {
			return fmt.Errorf("归档 service_status 历史数据失败：%w", err)
		}
		if maxID >= 0 {
			if err := s.maintenanceBatches(ctx, `DELETE FROM service_status WHERE rowid IN
(SELECT s.rowid FROM service_status s WHERE s.rowid<=? AND s.ts<? AND s.ts<(SELECT max(l.ts) FROM service_status l WHERE l.collector_id=s.collector_id AND l.name=s.name) LIMIT 5000)`, maxID, rawCutoff); err != nil {
				return err
			}
		}
	} else {
		if err := s.maintenanceBatches(ctx, `DELETE FROM service_status WHERE rowid IN
(SELECT s.rowid FROM service_status s WHERE ts<? AND ts<(SELECT max(l.ts) FROM service_status l WHERE l.collector_id=s.collector_id AND l.name=s.name) LIMIT 5000)`, rawCutoff); err != nil {
			return err
		}
	}
	for _, table := range []string{"metric_minute", "probe_minute"} {
		source := "r.source_table"
		if table == "probe_minute" {
			source = "'probe_results'"
		}
		where := `r.ts<? AND EXISTS (SELECT 1 FROM rollup_state s WHERE s.source_table=` + source + ` AND s.level='hour' AND s.ts=r.ts/3600000*3600000 AND s.finalized=1)`
		if archiving {
			maxID, _, err := s.archiveExpiredForTable(ctx, "minute", table, where, []any{minuteCutoff}, now)
			if err != nil {
				return fmt.Errorf("归档 %s 历史数据失败：%w", table, err)
			}
			if maxID < 0 {
				continue
			}
			if err := s.maintenanceBatches(ctx, `DELETE FROM `+table+` WHERE rowid IN (SELECT r.rowid FROM `+table+` r WHERE r.rowid<=? AND `+where+` LIMIT 5000)`, maxID, minuteCutoff); err != nil {
				return err
			}
		} else {
			if err := s.maintenanceBatches(ctx, `DELETE FROM `+table+` WHERE rowid IN (SELECT r.rowid FROM `+table+` r WHERE `+where+` LIMIT 5000)`, minuteCutoff); err != nil {
				return err
			}
		}
	}
	if archiving {
		maxID, _, err := s.archiveExpiredForTable(ctx, "raw", "task_status", `ts<?`, []any{rawCutoff}, now)
		if err != nil {
			return fmt.Errorf("归档 task_status 历史数据失败：%w", err)
		}
		if maxID >= 0 {
			if err := s.maintenanceBatches(ctx, `DELETE FROM task_status WHERE rowid IN (SELECT rowid FROM task_status WHERE rowid<=? AND ts<? LIMIT 5000)`, maxID, rawCutoff); err != nil {
				return err
			}
		}
	} else {
		if err := s.maintenanceBatches(ctx, `DELETE FROM task_status WHERE rowid IN (SELECT rowid FROM task_status WHERE ts<? LIMIT 5000)`, rawCutoff); err != nil {
			return err
		}
	}
	for _, table := range []string{"metric_hour", "probe_hour", "status_hour"} {
		if err := s.maintenanceBatches(ctx, "DELETE FROM "+table+" WHERE rowid IN (SELECT rowid FROM "+table+" WHERE ts<? LIMIT 5000)", hourCutoff); err != nil {
			return err
		}
	}
	if err := s.maintenanceBatches(ctx, `DELETE FROM rollup_state WHERE rowid IN
(SELECT s.rowid FROM rollup_state s WHERE ts<? AND NOT EXISTS
(SELECT 1 FROM rollup_dirty d WHERE d.source_table=s.source_table AND d.level=s.level AND d.ts=s.ts) LIMIT 5000)`, hourCutoff); err != nil {
		return err
	}
	return s.cleanExpiredArchives(ctx, policy.Archive, now)
}

func (s *Store) effectiveRawCleanupCutoff(ctx context.Context, table string, cutoff int64) (int64, bool, error) {
	var dirty bool
	if err := s.maintenanceView.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM rollup_dirty WHERE source_table=? AND level='minute' AND ts<?)`, table, cutoff).Scan(&dirty); err != nil {
		return 0, false, err
	}
	if dirty {
		return 0, true, nil
	}
	var watermark int64
	if err := s.maintenanceView.db.QueryRowContext(ctx, `SELECT coalesce((SELECT raw_complete_from FROM raw_watermarks WHERE table_name=?), 0)`, table).Scan(&watermark); err != nil {
		return 0, false, err
	}
	if watermark > cutoff {
		return watermark, false, nil
	}
	return cutoff, false, nil
}

func (s *Store) cleanRawTable(ctx context.Context, table string, cutoff int64) error {
	return s.cleanRawTableBounded(ctx, table, cutoff, -1, false)
}

func (s *Store) cleanRawTableBounded(ctx context.Context, table string, cutoff, maxRowID int64, skipDelete bool) error {
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
			if skipDelete {
				return nil
			}
			var result sql.Result
			var err error
			if maxRowID >= 0 {
				result, err = tx.ExecContext(ctx, "DELETE FROM "+table+" WHERE rowid IN (SELECT rowid FROM "+table+" WHERE rowid<=? AND ts<(SELECT raw_complete_from FROM raw_watermarks WHERE table_name=?) LIMIT 5000)", maxRowID, table)
			} else {
				result, err = tx.ExecContext(ctx, "DELETE FROM "+table+" WHERE rowid IN (SELECT rowid FROM "+table+" WHERE ts<(SELECT raw_complete_from FROM raw_watermarks WHERE table_name=?) LIMIT 5000)", table)
			}
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
