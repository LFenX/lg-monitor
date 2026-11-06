package store

import (
	"context"
	"database/sql"
	"fmt"
)

const minuteMS int64 = 60000
const hourMS int64 = 3600000

func floorBucket(ts, width int64) int64 {
	remainder := ts % width
	if remainder < 0 {
		remainder += width
	}
	return ts - remainder
}

type aggregateMetric struct {
	collector, target, name, resource, probe string
	samples                                  int64
	sum, maximum                             float64
}
type aggregateProbe struct {
	collector, target, probe string
	samples, successes       int64
	sum                      float64
}
type aggregateBucket struct {
	level     string
	source    string
	ts        int64
	version   int64
	finalized bool
	metrics   []aggregateMetric
	probes    []aggregateProbe
}
type aggregateSource interface {
	Next(context.Context, string, int64) (aggregateBucket, bool, error)
}

type sqliteAggregates struct{ db *sql.DB }

func (s sqliteAggregates) Next(ctx context.Context, level string, end int64) (aggregateBucket, bool, error) {
	bucket := aggregateBucket{level: level}
	width := minuteMS
	if level == "hour" {
		width = hourMS
	}
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return bucket, false, fmt.Errorf("开始汇总只读快照失败：%w", err)
	}
	defer tx.Rollback()
	err = tx.QueryRowContext(ctx, `SELECT d.source_table,d.ts,d.version,coalesce(s.finalized,0) FROM rollup_dirty d
LEFT JOIN rollup_state s ON s.source_table=d.source_table AND s.level=d.level AND s.ts=d.ts
WHERE d.level=? AND d.ts<=?-?-30000 ORDER BY d.ts,d.source_table LIMIT 1`, level, end, width).Scan(&bucket.source, &bucket.ts, &bucket.version, &bucket.finalized)
	if err == sql.ErrNoRows {
		return bucket, false, nil
	}
	if err != nil {
		return bucket, false, fmt.Errorf("读取待汇总时间桶失败：%w", err)
	}
	if !bucket.finalized {
		if level == "minute" {
			err = readMinute(ctx, tx, &bucket)
		} else {
			err = readHour(ctx, tx, &bucket)
		}
	}
	return bucket, true, err
}

func readMetricRows(ctx context.Context, tx *sql.Tx, bucket *aggregateBucket, query string, args ...any) error {
	rows, err := tx.QueryContext(ctx, query, args...)
	if err != nil {
		return fmt.Errorf("读取指标汇总失败：%w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var value aggregateMetric
		if err := rows.Scan(&value.collector, &value.target, &value.name, &value.resource, &value.probe, &value.samples, &value.sum, &value.maximum); err != nil {
			return fmt.Errorf("读取指标汇总字段失败：%w", err)
		}
		bucket.metrics = append(bucket.metrics, value)
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("遍历指标汇总失败：%w", err)
	}
	return nil
}

func readProbeRows(ctx context.Context, tx *sql.Tx, bucket *aggregateBucket, query string, args ...any) error {
	rows, err := tx.QueryContext(ctx, query, args...)
	if err != nil {
		return fmt.Errorf("读取探测汇总失败：%w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var value aggregateProbe
		if err := rows.Scan(&value.collector, &value.target, &value.probe, &value.samples, &value.successes, &value.sum); err != nil {
			return fmt.Errorf("读取探测汇总字段失败：%w", err)
		}
		bucket.probes = append(bucket.probes, value)
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("遍历探测汇总失败：%w", err)
	}
	return nil
}

func readMinute(ctx context.Context, tx *sql.Tx, bucket *aggregateBucket) error {
	if bucket.source == "probe_results" {
		return readProbeRows(ctx, tx, bucket, `SELECT collector_id,target_id,probe,count(*),sum(CASE WHEN ok THEN 1 ELSE 0 END),total(latency_ms)
FROM probe_results WHERE ts>=? AND ts<? GROUP BY collector_id,target_id,probe`, bucket.ts, bucket.ts+minuteMS)
	}
	sources := map[string]string{
		"system_metrics": `SELECT s.collector_id,'',j.key,'','',j.value FROM system_metrics s,
json_each(json_object('system.cpu_percent',cpu,'system.memory_total_bytes',memory_total,'system.memory_used_bytes',memory_used,
'system.memory_available_bytes',memory_available,'system.memory_percent',memory_percent,'system.uptime_seconds',uptime)) j WHERE ts>=? AND ts<?`,
		"disk_metrics": `SELECT d.collector_id,'',j.key,d.path,'',j.value FROM disk_metrics d,
json_each(json_object('disk.total_bytes',total,'disk.used_bytes',used,'disk.free_bytes',free,'disk.used_percent',used_percent)) j WHERE ts>=? AND ts<?`,
		"network_metrics": `SELECT n.collector_id,'',j.key,n.name,'',j.value FROM network_metrics n,
json_each(json_object('network.bytes_sent',bytes_sent,'network.bytes_received',bytes_received,'network.send_bytes_per_sec',send_bytes_per_sec,'network.receive_bytes_per_sec',receive_bytes_per_sec)) j WHERE ts>=? AND ts<?`,
		"script_metrics": `SELECT collector_id,target_id,'script.'||name,'',probe,value FROM script_metrics WHERE ts>=? AND ts<?`,
		"process_samples": `SELECT p.collector_id,p.target_id,j.key,'','',j.value FROM
(SELECT collector_id,target_id,ts,total(cpu) AS cpu,total(rss) AS rss FROM process_samples WHERE ts>=? AND ts<? GROUP BY collector_id,target_id,ts) p,
json_each(json_object('process.cpu_percent',cpu,'process.rss_bytes',rss)) j`,
	}
	query, ok := sources[bucket.source]
	if !ok {
		return fmt.Errorf("未知的汇总来源表 %s", bucket.source)
	}
	return readMetricRows(ctx, tx, bucket, `WITH values_by_metric(collector_id,target_id,name,resource,probe,value) AS (`+query+`)
SELECT collector_id,target_id,name,resource,probe,count(*),total(value),max(value)
FROM values_by_metric GROUP BY collector_id,target_id,name,resource,probe`, bucket.ts, bucket.ts+minuteMS)
}

func readHour(ctx context.Context, tx *sql.Tx, bucket *aggregateBucket) error {
	if bucket.source == "probe_results" {
		return readProbeRows(ctx, tx, bucket, `SELECT collector_id,target_id,probe,sum(samples),sum(successes),total(latency_sum)
FROM probe_minute WHERE ts>=? AND ts<? GROUP BY collector_id,target_id,probe`, bucket.ts, bucket.ts+hourMS)
	}
	return readMetricRows(ctx, tx, bucket, `SELECT collector_id,target_id,name,resource,probe,sum(samples),total(sum_value),max(max_value)
FROM metric_minute WHERE source_table=? AND ts>=? AND ts<? GROUP BY collector_id,target_id,name,resource,probe`, bucket.source, bucket.ts, bucket.ts+hourMS)
}
