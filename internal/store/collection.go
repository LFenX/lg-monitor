package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"slices"

	"github.com/LFenX/lg-monitor/internal/pipeline"
)

type recordOperation struct {
	query string
	args  []any
}

func encodeCollection(message pipeline.Message) ([]recordOperation, error) {
	if message.SystemMetrics != nil {
		m := message.SystemMetrics
		data, err := json.Marshal(m)
		if err != nil {
			return nil, fmt.Errorf("编码系统指标失败：%w", err)
		}
		operations := []recordOperation{{`INSERT INTO system_metrics(collector_id,ts,cpu,memory_total,memory_used,memory_available,memory_percent,uptime,detail) VALUES(?,?,?,?,?,?,?,?,?) ON CONFLICT(collector_id,ts) DO UPDATE SET cpu=excluded.cpu,memory_total=excluded.memory_total,memory_used=excluded.memory_used,memory_available=excluded.memory_available,memory_percent=excluded.memory_percent,uptime=excluded.uptime,detail=excluded.detail`, []any{message.CollectorID, message.TS, m.CPUPercent, int64(m.MemoryTotal), int64(m.MemoryUsed), int64(m.MemoryAvailable), m.MemoryPercent, int64(m.UptimeSeconds), string(data)}}}
		for _, d := range m.Disks {
			operations = append(operations, recordOperation{`INSERT INTO disk_metrics(collector_id,path,ts,total,used,free,used_percent) VALUES(?,?,?,?,?,?,?) ON CONFLICT(collector_id,path,ts) DO UPDATE SET total=excluded.total,used=excluded.used,free=excluded.free,used_percent=excluded.used_percent`, []any{message.CollectorID, d.Path, message.TS, int64(d.Total), int64(d.Used), int64(d.Free), d.UsedPercent}})
		}
		for _, n := range m.Networks {
			operations = append(operations, recordOperation{`INSERT INTO network_metrics(collector_id,name,ts,bytes_sent,bytes_received,send_bytes_per_sec,receive_bytes_per_sec) VALUES(?,?,?,?,?,?,?) ON CONFLICT(collector_id,name,ts) DO UPDATE SET bytes_sent=excluded.bytes_sent,bytes_received=excluded.bytes_received,send_bytes_per_sec=excluded.send_bytes_per_sec,receive_bytes_per_sec=excluded.receive_bytes_per_sec`, []any{message.CollectorID, n.Name, message.TS, int64(n.BytesSent), int64(n.BytesReceived), n.SendBytesPerSec, n.ReceiveBytesPerSec}})
		}
		return operations, nil
	}
	if s := message.ServiceStatus; s != nil {
		data, err := json.Marshal(s)
		if err != nil {
			return nil, fmt.Errorf("编码服务状态失败：%w", err)
		}
		return []recordOperation{{`INSERT INTO service_status(collector_id,name,ts,state,start_type,pid,detail) VALUES(?,?,?,?,?,?,?) ON CONFLICT(collector_id,name,ts) DO UPDATE SET state=excluded.state,start_type=excluded.start_type,pid=excluded.pid,detail=excluded.detail`, []any{message.CollectorID, s.Name, message.TS, s.State, s.StartType, s.PID, string(data)}}}, nil
	}
	var operations []recordOperation
	if p := message.ProbeResult; p != nil && len(p.Metrics) != 0 {
		names := make([]string, 0, len(p.Metrics))
		for name := range p.Metrics {
			names = append(names, name)
		}
		slices.Sort(names)
		for _, name := range names {
			operations = append(operations, recordOperation{`INSERT INTO script_metrics(collector_id,target_id,probe,ts,name,value) VALUES(?,?,?,?,?,?) ON CONFLICT(collector_id,target_id,probe,ts,name) DO UPDATE SET value=excluded.value`, []any{message.CollectorID, p.TargetID, fmt.Sprint(p.Probe), message.TS, name, p.Metrics[name]}})
		}
	}
	return operations, nil
}

func appendCollection(ctx context.Context, tx *sql.Tx, row record) error {
	for _, operation := range row.operations {
		if _, err := tx.ExecContext(ctx, operation.query, operation.args...); err != nil {
			return fmt.Errorf("写入系统、服务或脚本指标失败：%w", err)
		}
	}
	return nil
}
