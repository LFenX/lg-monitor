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
	kind                 pipeline.Kind
	collector, target    string
	ts                   int64
	state, detail, probe string
	ok                   bool
	latency              int64
	message              string
	pid                  int32
	cpu                  float64
	rss                  int64
}

func (s *Store) Write(ctx context.Context, message pipeline.Message) error {
	if err := message.Validate(); err != nil {
		return err
	}
	row := record{kind: message.Kind, collector: message.CollectorID, ts: message.TS}
	switch message.Kind {
	case pipeline.KindTargetStatus:
		status := message.TargetStatus
		data, err := json.Marshal(status)
		if err != nil {
			return fmt.Errorf("序列化目标状态失败：%w", err)
		}
		row.target, row.state, row.detail = status.TargetID, status.State, string(data)
	case pipeline.KindProbeResult:
		probe := message.ProbeResult
		row.target, row.probe, row.ok, row.latency, row.message = probe.TargetID, strconv.Itoa(probe.Probe), probe.OK, probe.LatencyMS, probe.Error
	case pipeline.KindProcessSample:
		process := message.ProcessSample
		if math.IsNaN(process.CPUPercent) || math.IsInf(process.CPUPercent, 0) || process.CPUPercent < 0 || process.CPUPercent > 100 || process.RSS > math.MaxInt64 {
			return fmt.Errorf("进程 CPU 必须在 0～100%% 之间，RSS 不能超过有符号 64 位整数范围")
		}
		row.target, row.pid, row.cpu, row.rss = process.TargetID, process.PID, process.CPUPercent, int64(process.RSS)
	}
	// Copy into immutable scalar fields before returning; producers may reuse their buffers.
	return s.submit(ctx, request{record: &row})
}

func (s *Store) appendBatch(ctx context.Context, batch []record) error {
	return s.transaction(ctx, func(ctx context.Context, tx *sql.Tx) error {
		queries := map[pipeline.Kind]string{
			pipeline.KindTargetStatus:  `INSERT INTO target_status(collector_id,target_id,ts,state,detail) VALUES(?,?,?,?,?) ON CONFLICT(collector_id,target_id,ts) DO UPDATE SET state=excluded.state, detail=excluded.detail`,
			pipeline.KindProbeResult:   `INSERT INTO probe_results(collector_id,target_id,probe,ts,ok,latency_ms,error) VALUES(?,?,?,?,?,?,?)`,
			pipeline.KindProcessSample: `INSERT INTO process_samples(collector_id,target_id,ts,pid,cpu,rss) VALUES(?,?,?,?,?,?)`,
		}
		statements := make(map[pipeline.Kind]*sql.Stmt)
		defer func() {
			for _, statement := range statements {
				statement.Close()
			}
		}()
		type seen struct{ first, last int64 }
		collectors := make(map[string]seen)
		for _, row := range batch {
			times, exists := collectors[row.collector]
			if !exists {
				times.first, times.last = row.ts, row.ts
			} else {
				times.first, times.last = min(times.first, row.ts), max(times.last, row.ts)
			}
			collectors[row.collector] = times
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
				args = []any{row.collector, row.target, row.probe, row.ts, row.ok, row.latency, row.message}
			case pipeline.KindProcessSample:
				args = []any{row.collector, row.target, row.ts, row.pid, row.cpu, row.rss}
			}
			if _, err := statement.ExecContext(ctx, args...); err != nil {
				return fmt.Errorf("写入采集记录失败：%w", err)
			}
		}
		for id, times := range collectors {
			if _, err := tx.ExecContext(ctx, `INSERT INTO collectors(id,first_seen,last_seen) VALUES(?,?,?) ON CONFLICT(id) DO UPDATE SET first_seen=min(coalesce(first_seen,excluded.first_seen),excluded.first_seen), last_seen=max(coalesce(last_seen,excluded.last_seen),excluded.last_seen)`, id, times.first, times.last); err != nil {
				return fmt.Errorf("更新采集器时间失败：%w", err)
			}
		}
		return nil
	})
}

type CollectorRecord struct {
	ID        string `json:"id"`
	Hostname  string `json:"hostname"`
	FirstSeen int64  `json:"firstSeen"`
	LastSeen  int64  `json:"lastSeen"`
	Version   string `json:"version"`
}

func (s *Store) RegisterCollector(ctx context.Context, collector CollectorRecord) error {
	if collector.ID == "" {
		return fmt.Errorf("采集器 ID 不能为空")
	}
	return s.operation(ctx, func(ctx context.Context, tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `INSERT INTO collectors(id,hostname,first_seen,last_seen,version) VALUES(?,?,?,?,?) ON CONFLICT(id) DO UPDATE SET hostname=excluded.hostname,version=excluded.version,first_seen=min(coalesce(first_seen,excluded.first_seen),excluded.first_seen),last_seen=max(coalesce(last_seen,excluded.last_seen),excluded.last_seen)`, collector.ID, collector.Hostname, collector.FirstSeen, collector.LastSeen, collector.Version)
		if err != nil {
			return fmt.Errorf("保存采集器信息失败：%w", err)
		}
		return nil
	})
}
