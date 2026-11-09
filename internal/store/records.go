package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"math"
	"strconv"

	"github.com/LFenX/lg-monitor/internal/pipeline"
)

type record struct {
	kind                         pipeline.Kind
	collector, target            string
	ts                           int64
	state, detail, probe         string
	ok                           bool
	latency                      int64
	message                      string
	pid                          int32
	cpu                          float64
	rss                          int64
	createdAt                    int64
	intervalMillis               int64
	seq, gapFrom, gapTo, dropped int64
	operations                   []recordOperation
	probeType, probeMessage      string
	tlsNotAfter                  int64
	tlsSubject                   string
	dropPrimary                  bool
}

func (s *Store) Write(ctx context.Context, message pipeline.Message) error {
	row, err := encodeRecord(message)
	if err != nil {
		return err
	}
	return s.submit(ctx, request{record: &row})
}

func encodeRecord(message pipeline.Message) (record, error) {
	if err := message.Validate(); err != nil {
		return record{}, err
	}
	row := record{kind: message.Kind, collector: message.CollectorID, ts: message.TS}
	operations, err := encodeCollection(message)
	if err != nil {
		return row, err
	}
	row.operations = operations
	switch message.Kind {
	case pipeline.KindInventory:
		data, err := json.Marshal(message.Inventory)
		if err != nil {
			return record{}, fmt.Errorf("编码启动项与计划任务失败：%w", err)
		}
		row.detail, row.seq = string(data), int64(message.Seq)
	case pipeline.KindTargetStatus:
		status := message.TargetStatus
		data, err := json.Marshal(status)
		if err != nil {
			return record{}, fmt.Errorf("序列化目标状态失败：%w", err)
		}
		row.target, row.state, row.detail = status.TargetID, status.State, string(data)
	case pipeline.KindProbeResult:
		probe := message.ProbeResult
		row.target, row.probe, row.ok, row.latency, row.message = probe.TargetID, strconv.Itoa(probe.Probe), probe.OK, probe.LatencyMS, probe.Error
		row.probeType, row.probeMessage = probe.Type, probe.Message
		row.tlsNotAfter, row.tlsSubject = probe.TLSNotAfter, probe.TLSSubject
	case pipeline.KindProcessSample:
		process := message.ProcessSample
		if math.IsNaN(process.CPUPercent) || math.IsInf(process.CPUPercent, 0) || process.CPUPercent < 0 || process.CPUPercent > 100 || process.RSS > math.MaxInt64 {
			return record{}, fmt.Errorf("进程 CPU 必须在 0～100%% 之间，RSS 不能超过有符号 64 位整数范围")
		}
		row.target, row.pid, row.cpu, row.rss = process.TargetID, process.PID, process.CPUPercent, int64(process.RSS)
		row.createdAt = process.CreatedAt
		row.intervalMillis = process.IntervalMillis
		if row.intervalMillis == 0 {
			row.intervalMillis = 2000
		}
	case pipeline.KindDataGap:
		gap := message.DataGap
		row.seq, row.gapFrom, row.gapTo, row.dropped, row.message = int64(message.Seq), int64(gap.FromSeq), int64(gap.ToSeq), int64(gap.Dropped), gap.Reason
	}
	// Copy into immutable scalar fields before returning; producers may reuse their buffers.
	return row, nil
}

func (s *Store) appendBatch(ctx context.Context, batch []record) error {
	return s.transaction(ctx, func(ctx context.Context, tx *sql.Tx) error { return appendRecords(ctx, tx, batch, s.now().UnixMilli()) })
}

func appendFilteredRecords(ctx context.Context, tx *sql.Tx, batch []record, receivedAt int64) error {
	queries := map[pipeline.Kind]string{
		pipeline.KindTargetStatus:  `INSERT INTO target_status(collector_id,target_id,ts,state,detail) VALUES(?,?,?,?,?) ON CONFLICT(collector_id,target_id,ts) DO UPDATE SET state=excluded.state, detail=excluded.detail`,
		pipeline.KindProbeResult:   `INSERT INTO probe_results(collector_id,target_id,probe,ts,ok,latency_ms,error,type,message,tls_not_after,tls_subject) VALUES(?,?,?,?,?,?,?,?,?,?,?)`,
		pipeline.KindProcessSample: `INSERT INTO process_samples(collector_id,target_id,ts,pid,cpu,rss,created_at,interval_ms) VALUES(?,?,?,?,?,?,?,?)`,
		pipeline.KindDataGap:       `INSERT INTO collector_events(collector_id,seq,ts,kind,message,gap_from,gap_to,dropped) VALUES(?,?,?,'data_gap',?,?,?,?) ON CONFLICT(collector_id,seq) DO NOTHING`,
	}
	statements := make(map[pipeline.Kind]*sql.Stmt)
	defer func() {
		for _, statement := range statements {
			statement.Close()
		}
	}()
	collectors := make(map[string]bool)
	type sourceBucket struct {
		table string
		ts    int64
	}
	dirty := make(map[sourceBucket]bool)
	for _, row := range batch {
		collectors[row.collector] = true
		if row.kind == pipeline.KindInventory {
			if err := appendInventory(ctx, tx, row, receivedAt); err != nil {
				return err
			}
			continue
		}
		if !row.dropPrimary && (row.kind == pipeline.KindProcessSample || row.kind == pipeline.KindProbeResult) {
			dirty[sourceBucket{primaryTable(row.kind), floorBucket(row.ts, minuteMS)}] = true
		}
		for _, operation := range row.operations {
			if table := operation.table(); table != "service_status" && table != "eventlog_events" && table != "eventlog_matches" && table != "collector_events" {
				dirty[sourceBucket{table, floorBucket(row.ts, minuteMS)}] = true
			}
		}
		if row.kind == pipeline.KindSystemMetrics || row.kind == pipeline.KindServiceStatus || row.kind == pipeline.KindEventLog || row.kind == pipeline.KindEventLogNotice {
			if err := appendCollection(ctx, tx, row); err != nil {
				return err
			}
			continue
		}
		if row.dropPrimary {
			if err := appendCollection(ctx, tx, row); err != nil {
				return err
			}
			continue
		}
		statement := statements[row.kind]
		if statement == nil {
			var err error
			statement, err = tx.PrepareContext(ctx, queries[row.kind])
			if err != nil {
				return fmt.Errorf("准备采集记录写入失败：%w", err)
			}
			statements[row.kind] = statement
		}
		var args []any
		switch row.kind {
		case pipeline.KindTargetStatus:
			args = []any{row.collector, row.target, row.ts, row.state, row.detail}
		case pipeline.KindProbeResult:
			var notAfter any
			if row.tlsNotAfter != 0 {
				notAfter = row.tlsNotAfter
			}
			args = []any{row.collector, row.target, row.probe, row.ts, row.ok, row.latency, row.message, row.probeType, row.probeMessage, notAfter, row.tlsSubject}
		case pipeline.KindProcessSample:
			args = []any{row.collector, row.target, row.ts, row.pid, row.cpu, row.rss, row.createdAt, row.intervalMillis}
		case pipeline.KindDataGap:
			args = []any{row.collector, row.seq, row.ts, row.message, row.gapFrom, row.gapTo, row.dropped}
		}
		if _, err := statement.ExecContext(ctx, args...); err != nil {
			return fmt.Errorf("写入采集记录失败：%w", err)
		}
		if err := appendCollection(ctx, tx, row); err != nil {
			return err
		}
	}
	for bucket := range dirty {
		if _, err := tx.ExecContext(ctx, `INSERT INTO rollup_dirty(source_table,level,ts,version) VALUES(?,'minute',?,1) ON CONFLICT(source_table,level,ts) DO UPDATE SET version=version+1`, bucket.table, bucket.ts); err != nil {
			return fmt.Errorf("标记分钟汇总更新失败：%w", err)
		}
	}
	for id := range collectors {
		// Buffered sample timestamps describe observations, not a live connection to the monitor.
		if _, err := tx.ExecContext(ctx, `INSERT INTO collectors(id,first_seen,last_seen) VALUES(?,?,?) ON CONFLICT(id) DO NOTHING`, id, receivedAt, receivedAt); err != nil {
			return fmt.Errorf("更新采集器时间失败：%w", err)
		}
	}
	return nil
}

type CollectorRecord struct {
	ID            string               `json:"id"`
	Hostname      string               `json:"hostname"`
	FirstSeen     int64                `json:"firstSeen"`
	LastSeen      int64                `json:"lastSeen"`
	Version       string               `json:"version"`
	ClockOffsetMS int64                `json:"clockOffsetMs"`
	Cache         *pipeline.CacheUsage `json:"cache" jsonschema:"nullable"`
}

func (s *Store) RegisterCollector(ctx context.Context, collector CollectorRecord) error {
	if collector.ID == "" {
		return fmt.Errorf("采集器 ID 不能为空")
	}
	return s.operation(ctx, func(ctx context.Context, tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `INSERT INTO collectors(id,hostname,first_seen,last_seen,version) VALUES(?,?,?,?,?) ON CONFLICT(id) DO UPDATE SET hostname=excluded.hostname,version=excluded.version,first_seen=min(coalesce(first_seen,excluded.first_seen),excluded.first_seen),last_seen=excluded.last_seen,cache_bytes=NULL,cache_max_bytes=NULL,cache_bytes_per_hour=NULL`, collector.ID, collector.Hostname, collector.FirstSeen, collector.LastSeen, collector.Version)
		if err != nil {
			return fmt.Errorf("保存采集器信息失败：%w", err)
		}
		return nil
	})
}
