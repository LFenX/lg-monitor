package store

import (
	"context"
	"database/sql"
	"fmt"
	"slices"
	"strings"

	"github.com/LFenX/lg-monitor/internal/pipeline"
)

func primaryTable(kind pipeline.Kind) string {
	switch kind {
	case pipeline.KindTargetStatus:
		return "target_status"
	case pipeline.KindProbeResult:
		return "probe_results"
	case pipeline.KindProcessSample:
		return "process_samples"
	case pipeline.KindSystemMetrics:
		return "system_metrics"
	case pipeline.KindServiceStatus:
		return "service_status"
	case pipeline.KindEventLog:
		return "eventlog_events"
	default:
		return "collector_events"
	}
}

func (o recordOperation) table() string {
	for _, name := range []string{"system_metrics", "disk_metrics", "network_metrics", "service_status", "script_metrics", "eventlog_events", "eventlog_matches", "collector_events"} {
		if strings.HasPrefix(o.query, "INSERT INTO "+name+"(") {
			return name
		}
	}
	return ""
}

type lateGroup struct{ collector, table string }
type lateCount struct{ count, first, last int64 }

func appendRecords(ctx context.Context, tx *sql.Tx, batch []record, receivedAt int64) error {
	_, err := appendRecordsChecked(ctx, tx, batch, receivedAt)
	return err
}

func appendRecordsChecked(ctx context.Context, tx *sql.Tx, batch []record, receivedAt int64) ([]bool, error) {
	watermarks := map[string]int64{}
	rows, err := tx.QueryContext(ctx, `SELECT table_name,raw_complete_from FROM raw_watermarks`)
	if err != nil {
		return nil, fmt.Errorf("读取迟到采样完整起点失败：%w", err)
	}
	for rows.Next() {
		var table string
		var cutoff int64
		if err := rows.Scan(&table, &cutoff); err != nil {
			rows.Close()
			return nil, fmt.Errorf("读取完整起点字段失败：%w", err)
		}
		watermarks[table] = cutoff
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, fmt.Errorf("遍历完整起点失败：%w", err)
	}
	dropped := map[lateGroup]lateCount{}
	keep := func(row record, table string) bool {
		cutoff, known := watermarks[table]
		if !known || table == "service_status" || row.ts >= cutoff {
			return true
		}
		key := lateGroup{row.collector, table}
		value := dropped[key]
		if value.count == 0 {
			value.first, value.last = row.ts, row.ts
		}
		value.count++
		value.first, value.last = min(value.first, row.ts), max(value.last, row.ts)
		dropped[key] = value
		return false
	}
	filtered := slices.Clone(batch)
	accepted := make([]bool, len(batch))
	for i, row := range batch {
		table := primaryTable(row.kind)
		cutoff, known := watermarks[table]
		accepted[i] = !known || table == "service_status" || row.ts >= cutoff
		filtered[i].dropPrimary = !accepted[i]
		if row.kind == pipeline.KindEventLog || row.kind == pipeline.KindEventLogNotice {
			keep(row, table)
			if !accepted[i] {
				filtered[i].operations = nil
			}
			continue
		}
		if row.kind != pipeline.KindSystemMetrics && row.kind != pipeline.KindServiceStatus {
			keep(row, table)
		}
		filtered[i].operations = nil
		for _, operation := range row.operations {
			if operation.table() == "" {
				return nil, fmt.Errorf("采集写入没有登记来源表")
			}
			if keep(row, operation.table()) {
				filtered[i].operations = append(filtered[i].operations, operation)
			}
		}
	}
	if err := appendFilteredRecords(ctx, tx, filtered, receivedAt); err != nil {
		return nil, err
	}
	keys := make([]lateGroup, 0, len(dropped))
	for key := range dropped {
		keys = append(keys, key)
	}
	slices.SortFunc(keys, func(a, b lateGroup) int {
		if a.collector != b.collector {
			return strings.Compare(a.collector, b.collector)
		}
		return strings.Compare(a.table, b.table)
	})
	for _, key := range keys {
		value := dropped[key]
		// Negative audit IDs cannot collide with positive transport sequence numbers.
		_, err := tx.ExecContext(ctx, `INSERT INTO collector_events(collector_id,seq,ts,kind,message,gap_from,gap_to,dropped,source_table,first_ts,last_ts)
VALUES(?,(SELECT min(coalesce(min(seq),0),0)-1 FROM collector_events WHERE collector_id=?),?,'late_dropped',?,0,0,?,?,?,?)`,
			key.collector, key.collector, receivedAt, fmt.Sprintf("原始数据已清理，丢弃 %d 条 %s 迟到采样；时间范围 %d～%d（UTC 毫秒）", value.count, key.table, value.first, value.last), value.count, key.table, value.first, value.last)
		if err != nil {
			return nil, fmt.Errorf("保存迟到采样丢弃事件失败：%w", err)
		}
	}
	return accepted, nil
}
