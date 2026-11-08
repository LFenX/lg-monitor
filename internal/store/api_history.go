package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
)

const MaxHistoryPoints = 10000

var ErrHistoryTooLarge = errors.New("历史结果超过 10000 条，请缩小时间范围、指定采集器或选择更大的 step")

type HistoryRange struct {
	From int64  `json:"from"`
	To   int64  `json:"to"`
	Step string `json:"step" jsonschema:"enum=raw,enum=1m,enum=1h"`
}

func (r HistoryRange) Validate() error {
	if r.From < 0 || r.To < r.From {
		return fmt.Errorf("历史时间须为非负 UTC Unix 毫秒，且 from 不能晚于 to")
	}
	if r.Step != "raw" && r.Step != "1m" && r.Step != "1h" {
		return fmt.Errorf("step 必须为 raw、1m 或 1h")
	}
	return nil
}

type MetricPoint struct {
	CollectorID string  `json:"collectorId"`
	TargetID    string  `json:"targetId,omitempty"`
	Name        string  `json:"name"`
	Resource    string  `json:"resource,omitempty"`
	Probe       string  `json:"probe,omitempty"`
	TS          int64   `json:"ts"`
	Samples     int64   `json:"samples"`
	Sum         float64 `json:"sum"`
	Max         float64 `json:"max"`
	Average     float64 `json:"average"`
}

type ProbePoint struct {
	Probe            int     `json:"probe"`
	TS               int64   `json:"ts"`
	Samples          int64   `json:"samples"`
	Successes        int64   `json:"successes"`
	LatencySum       float64 `json:"latencySum"`
	SuccessRate      float64 `json:"successRate"`
	AverageLatencyMS float64 `json:"averageLatencyMs"`
	Type             string  `json:"type,omitempty"`
	Error            string  `json:"error,omitempty"`
	Message          string  `json:"message,omitempty"`
	TLSNotAfter      *int64  `json:"tlsNotAfter" jsonschema:"nullable"`
	TLSSubject       string  `json:"tlsSubject"`
}

type ProbeCertificate struct {
	Probe       int    `json:"probe"`
	TS          int64  `json:"ts"`
	TLSNotAfter int64  `json:"tlsNotAfter"`
	TLSSubject  string `json:"tlsSubject"`
}

type TargetHistoryResult struct {
	HistoryRange
	CollectorID  string             `json:"collectorId"`
	TargetID     string             `json:"targetId"`
	States       []TargetRecord     `json:"states"`
	Probes       []ProbePoint       `json:"probes"`
	Processes    []MetricPoint      `json:"processes"`
	Notes        []string           `json:"notes"`
	Certificates []ProbeCertificate `json:"certificates"`
}

type MetricHistoryResult struct {
	HistoryRange
	Metrics []MetricPoint `json:"metrics"`
}

func (v *View) MetricHistory(ctx context.Context, collectorID, name string, period HistoryRange) (MetricHistoryResult, error) {
	result := MetricHistoryResult{HistoryRange: period, Metrics: []MetricPoint{}}
	if err := period.Validate(); err != nil {
		return result, err
	}
	points, err := v.metricHistory(ctx, collectorID, "", name, period)
	result.Metrics = points
	return result, err
}

func (v *View) TargetHistory(ctx context.Context, collectorID, targetID string, period HistoryRange) (TargetHistoryResult, error) {
	result := TargetHistoryResult{HistoryRange: period, CollectorID: collectorID, TargetID: targetID, States: []TargetRecord{}, Probes: []ProbePoint{}, Processes: []MetricPoint{}, Notes: []string{}, Certificates: []ProbeCertificate{}}
	if err := period.Validate(); err != nil {
		return result, err
	}
	if collectorID == "" || targetID == "" {
		return result, fmt.Errorf("目标历史必须指定采集器与目标 ID")
	}
	rows, err := v.db.QueryContext(ctx, `SELECT collector_id,target_id,ts,state,detail FROM target_status WHERE collector_id=? AND target_id=? AND ts>=? AND ts<=? ORDER BY ts LIMIT ?`, collectorID, targetID, period.From, period.To, MaxHistoryPoints+1)
	if err != nil {
		return result, fmt.Errorf("读取目标状态历史失败：%w", err)
	}
	for rows.Next() {
		var row TargetRecord
		var detail string
		if err := rows.Scan(&row.CollectorID, &row.TargetID, &row.TS, &row.State, &detail); err != nil {
			rows.Close()
			return result, fmt.Errorf("读取状态历史字段失败：%w", err)
		}
		row.Detail = []byte(detail)
		result.States = append(result.States, row)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return result, fmt.Errorf("遍历状态历史失败：%w", err)
	}
	if len(result.States) > MaxHistoryPoints {
		return result, ErrHistoryTooLarge
	}
	result.Processes, err = v.metricHistory(ctx, collectorID, targetID, "", period)
	if err != nil {
		return result, err
	}
	result.Probes, err = v.probeHistory(ctx, collectorID, targetID, period)
	if err != nil {
		return result, err
	}
	result.Certificates, err = v.probeCertificates(ctx, collectorID, targetID)
	if period.Step != "raw" {
		result.Notes = append(result.Notes, "探测与进程指标使用完整时间桶；状态仍为原始采样，超出原始保留期的状态不可恢复")
	}
	return result, err
}

func (v *View) metricHistory(ctx context.Context, collectorID, targetID, name string, period HistoryRange) ([]MetricPoint, error) {
	var query string
	args := []any{}
	if period.Step == "raw" {
		query = `WITH bounds(first,last) AS (VALUES(?,?)), metric_points(collector_id,target_id,name,resource,probe,ts,samples,sum_value,max_value) AS (` + rawMetricPoints + `)
SELECT collector_id,target_id,name,resource,probe,ts,samples,sum_value,max_value FROM metric_points WHERE ts>=? AND ts<=?`
		args = append(args, period.From, period.To, period.From, period.To)
	} else {
		table, width := "metric_minute", minuteMS
		if period.Step == "1h" {
			table, width = "metric_hour", hourMS
		}
		query = `SELECT collector_id,target_id,name,resource,probe,ts,samples,sum_value,max_value FROM ` + table + ` WHERE ts>=? AND ts<=? AND ts<=?-?`
		args = append(args, period.From, period.To, v.now().UnixMilli(), width)
	}
	if collectorID != "" {
		query += ` AND collector_id=?`
		args = append(args, collectorID)
	}
	if targetID != "" {
		query += ` AND target_id=? AND name IN ('process.cpu_percent','process.rss_bytes')`
		args = append(args, targetID)
	}
	if name != "" {
		query += ` AND name=?`
		args = append(args, name)
	}
	query += ` ORDER BY ts,collector_id,target_id,name,resource,probe LIMIT ?`
	args = append(args, MaxHistoryPoints+1)
	rows, err := v.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("查询指标历史失败：%w", err)
	}
	defer rows.Close()
	result := []MetricPoint{}
	for rows.Next() {
		var row MetricPoint
		if err := rows.Scan(&row.CollectorID, &row.TargetID, &row.Name, &row.Resource, &row.Probe, &row.TS, &row.Samples, &row.Sum, &row.Max); err != nil {
			return nil, fmt.Errorf("读取指标历史字段失败：%w", err)
		}
		if row.Samples > 0 {
			row.Average = row.Sum / float64(row.Samples)
		}
		result = append(result, row)
		if len(result) > MaxHistoryPoints {
			return nil, ErrHistoryTooLarge
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("遍历指标历史失败：%w", err)
	}
	return result, nil
}

const rawMetricPoints = `
SELECT s.collector_id,'',j.key,'','',s.ts,1,j.value,j.value FROM system_metrics s,
json_each(json_object('system.cpu_percent',cpu,'system.memory_total_bytes',memory_total,'system.memory_used_bytes',memory_used,
'system.memory_available_bytes',memory_available,'system.memory_percent',memory_percent,'system.uptime_seconds',uptime)) j
WHERE s.ts>=(SELECT first FROM bounds) AND s.ts<=(SELECT last FROM bounds)
UNION ALL SELECT d.collector_id,'',j.key,d.path,'',d.ts,1,j.value,j.value FROM disk_metrics d,
json_each(json_object('disk.total_bytes',total,'disk.used_bytes',used,'disk.free_bytes',free,'disk.used_percent',used_percent)) j
WHERE d.ts>=(SELECT first FROM bounds) AND d.ts<=(SELECT last FROM bounds)
UNION ALL SELECT n.collector_id,'',j.key,n.name,'',n.ts,1,j.value,j.value FROM network_metrics n,
json_each(json_object('network.bytes_sent',bytes_sent,'network.bytes_received',bytes_received,'network.send_bytes_per_sec',send_bytes_per_sec,'network.receive_bytes_per_sec',receive_bytes_per_sec)) j
WHERE n.ts>=(SELECT first FROM bounds) AND n.ts<=(SELECT last FROM bounds)
UNION ALL SELECT collector_id,target_id,'script.'||name,'',probe,ts,1,value,value FROM script_metrics
WHERE ts>=(SELECT first FROM bounds) AND ts<=(SELECT last FROM bounds)
UNION ALL SELECT p.collector_id,p.target_id,j.key,'','',p.ts,1,j.value,j.value FROM
(SELECT collector_id,target_id,ts,total(cpu) AS cpu,total(rss) AS rss FROM process_samples
WHERE ts>=(SELECT first FROM bounds) AND ts<=(SELECT last FROM bounds) GROUP BY collector_id,target_id,ts) p,
json_each(json_object('process.cpu_percent',cpu,'process.rss_bytes',rss)) j`

func (v *View) probeHistory(ctx context.Context, collectorID, targetID string, period HistoryRange) ([]ProbePoint, error) {
	query := `SELECT probe,ts,1,CASE WHEN ok THEN 1 ELSE 0 END,latency_ms,type,error,message,tls_not_after,tls_subject FROM probe_results WHERE collector_id=? AND target_id=? AND ts>=? AND ts<=?`
	args := []any{collectorID, targetID, period.From, period.To}
	if period.Step != "raw" {
		table, width := "probe_minute", minuteMS
		if period.Step == "1h" {
			table, width = "probe_hour", hourMS
		}
		query = `SELECT probe,ts,samples,successes,latency_sum,'','','',NULL,'' FROM ` + table + ` WHERE collector_id=? AND target_id=? AND ts>=? AND ts<=? AND ts<=?-?`
		args = append(args, v.now().UnixMilli(), width)
	}
	query += ` ORDER BY ts,probe LIMIT ?`
	args = append(args, MaxHistoryPoints+1)
	rows, err := v.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("读取探测历史失败：%w", err)
	}
	defer rows.Close()
	result := []ProbePoint{}
	for rows.Next() {
		var row ProbePoint
		if err := rows.Scan(&row.Probe, &row.TS, &row.Samples, &row.Successes, &row.LatencySum, &row.Type, &row.Error, &row.Message, &row.TLSNotAfter, &row.TLSSubject); err != nil {
			return nil, fmt.Errorf("读取探测历史字段失败：%w", err)
		}
		if row.Samples > 0 {
			row.SuccessRate = float64(row.Successes) / float64(row.Samples)
			row.AverageLatencyMS = row.LatencySum / float64(row.Samples)
		}
		result = append(result, row)
		if len(result) > MaxHistoryPoints {
			return nil, ErrHistoryTooLarge
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("遍历探测历史失败：%w", err)
	}
	return result, nil
}

func (v *View) probeCertificates(ctx context.Context, collectorID, targetID string) ([]ProbeCertificate, error) {
	rows, err := v.db.QueryContext(ctx, `SELECT probe,ts,tls_not_after,tls_subject FROM v_probe_certificates WHERE collector_id=? AND target_id=? ORDER BY CAST(probe AS INTEGER)`, collectorID, targetID)
	if err != nil {
		return nil, fmt.Errorf("读取最新证书信息失败：%w", err)
	}
	defer rows.Close()
	result := []ProbeCertificate{}
	for rows.Next() {
		var row ProbeCertificate
		if err := rows.Scan(&row.Probe, &row.TS, &row.TLSNotAfter, &row.TLSSubject); err != nil {
			return nil, fmt.Errorf("读取最新证书字段失败：%w", err)
		}
		result = append(result, row)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("遍历最新证书信息失败：%w", err)
	}
	return result, nil
}

func (s *Store) RemoveSilence(ctx context.Context, id string) error {
	if strings.TrimSpace(id) == "" {
		return fmt.Errorf("删除静音需要静音 ID")
	}
	return s.operation(ctx, func(ctx context.Context, tx *sql.Tx) error {
		result, err := tx.ExecContext(ctx, `DELETE FROM silences WHERE id=?`, id)
		if err != nil {
			return fmt.Errorf("删除静音失败：%w", err)
		}
		changed, err := result.RowsAffected()
		if err != nil {
			return fmt.Errorf("读取静音删除结果失败：%w", err)
		}
		if changed == 0 {
			return fmt.Errorf("静音不存在或已删除：%w", sql.ErrNoRows)
		}
		return nil
	})
}
