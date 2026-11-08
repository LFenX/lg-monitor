package store

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/LFenX/lg-monitor/internal/collector"
	"github.com/LFenX/lg-monitor/internal/config"
	"github.com/LFenX/lg-monitor/internal/pipeline"
	"github.com/LFenX/lg-monitor/internal/rules"
)

func (v *View) LoadReplay(ctx context.Context, rule config.Rule, since, until int64) (rules.ReplayData, error) {
	data := rules.ReplayData{Messages: []pipeline.Message{}, Hostnames: map[string]string{}, Warnings: []string{}}
	appendMessage := func(message pipeline.Message) error {
		if len(data.Messages) >= rules.MaxReplayRecords {
			return fmt.Errorf("历史采样超过 %d 条，请缩小回放范围", rules.MaxReplayRecords)
		}
		data.Messages = append(data.Messages, message)
		return nil
	}
	rows, err := v.db.QueryContext(ctx, `SELECT id,coalesce(hostname,'') FROM collectors`)
	if err != nil {
		return data, fmt.Errorf("读取回放主机选择信息失败：%w", err)
	}
	for rows.Next() {
		var id, name string
		if err := rows.Scan(&id, &name); err != nil {
			rows.Close()
			return data, err
		}
		data.Hostnames[id] = name
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return data, err
	}
	if rule.Type == "eventlog" {
		return v.loadEventLogReplay(ctx, rule, since, until, data)
	}
	system := rule.Type == "metric_threshold" && !strings.HasPrefix(rule.Metric, "process.") && !strings.HasPrefix(rule.Metric, "script.")
	if system {
		rows, err := v.db.QueryContext(ctx, `SELECT collector_id,ts,detail FROM system_metrics s WHERE ts<=? AND (ts>=? OR ts=(SELECT max(p.ts) FROM system_metrics p WHERE p.collector_id=s.collector_id AND p.ts<?)) ORDER BY ts,collector_id LIMIT ?`, until, since, since, rules.MaxReplayRecords+1)
		if err != nil {
			return data, fmt.Errorf("读取系统指标历史失败：%w", err)
		}
		defer rows.Close()
		for rows.Next() {
			var message pipeline.Message
			var detail string
			var metrics collector.SystemMetrics
			if err := rows.Scan(&message.CollectorID, &message.TS, &detail); err != nil {
				return data, err
			}
			if err := json.Unmarshal([]byte(detail), &metrics); err != nil {
				return data, fmt.Errorf("解码系统指标历史失败：%w", err)
			}
			message.Kind, message.SystemMetrics = pipeline.KindSystemMetrics, &metrics
			if err := appendMessage(message); err != nil {
				return data, err
			}
		}
		return data, rows.Err()
	}
	rows, err = v.db.QueryContext(ctx, `SELECT collector_id,ts,detail FROM target_status s WHERE target_id=? AND ts<=? AND (ts>=? OR ts=(SELECT max(p.ts) FROM target_status p WHERE p.collector_id=s.collector_id AND p.target_id=s.target_id AND p.ts<?)) ORDER BY ts,collector_id LIMIT ?`, rule.Target, until, since, since, rules.MaxReplayRecords+1)
	if err != nil {
		return data, fmt.Errorf("读取目标状态历史失败：%w", err)
	}
	for rows.Next() {
		var message pipeline.Message
		var detail string
		var status pipeline.TargetStatus
		if err := rows.Scan(&message.CollectorID, &message.TS, &detail); err != nil {
			rows.Close()
			return data, err
		}
		if err := json.Unmarshal([]byte(detail), &status); err != nil {
			rows.Close()
			return data, fmt.Errorf("解码目标历史失败：%w", err)
		}
		message.Kind, message.TargetStatus = pipeline.KindTargetStatus, &status
		if err := appendMessage(message); err != nil {
			rows.Close()
			return data, err
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return data, err
	}
	if rule.Type == "probe_failed" || rule.Type == "script_failed" || rule.Type == "cert_expiry" || strings.HasPrefix(rule.Metric, "script.") {
		rows, err = v.db.QueryContext(ctx, `SELECT collector_id,ts,probe,ok,latency_ms,coalesce(error,''),type,message,coalesce(tls_not_after,0),tls_subject FROM probe_results s WHERE target_id=? AND ts<=? AND (ts>=? OR ts=(SELECT max(p.ts) FROM probe_results p WHERE p.collector_id=s.collector_id AND p.target_id=s.target_id AND p.probe=s.probe AND p.ts<?)) ORDER BY ts,rowid LIMIT ?`, rule.Target, until, since, since, rules.MaxReplayRecords+1)
		if err != nil {
			return data, fmt.Errorf("读取探测历史失败：%w", err)
		}
		start := len(data.Messages)
		for rows.Next() {
			var message pipeline.Message
			var probe collector.ProbeResult
			var index string
			probe.TargetID = rule.Target
			if err := rows.Scan(&message.CollectorID, &message.TS, &index, &probe.OK, &probe.LatencyMS, &probe.Error, &probe.Type, &probe.Message, &probe.TLSNotAfter, &probe.TLSSubject); err != nil {
				rows.Close()
				return data, err
			}
			probe.Probe, err = strconv.Atoi(index)
			if err != nil {
				rows.Close()
				return data, fmt.Errorf("探测历史编号无效：%w", err)
			}
			probe.TS = message.TS
			message.Kind, message.ProbeResult = pipeline.KindProbeResult, &probe
			if err := appendMessage(message); err != nil {
				rows.Close()
				return data, err
			}
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return data, err
		}
		for i := start; i < len(data.Messages); i++ {
			message := &data.Messages[i]
			if message.ProbeResult.Type != "script" {
				continue
			}
			metrics, err := v.db.QueryContext(ctx, `SELECT name,value FROM script_metrics WHERE collector_id=? AND target_id=? AND probe=? AND ts=? ORDER BY name`, message.CollectorID, rule.Target, fmt.Sprint(message.ProbeResult.Probe), message.TS)
			if err != nil {
				return data, fmt.Errorf("读取脚本指标历史失败：%w", err)
			}
			message.ProbeResult.Metrics = map[string]float64{}
			for metrics.Next() {
				var name string
				var value float64
				if err := metrics.Scan(&name, &value); err != nil {
					metrics.Close()
					return data, err
				}
				message.ProbeResult.Metrics[name] = value
			}
			err = metrics.Err()
			metrics.Close()
			if err != nil {
				return data, err
			}
		}
	}
	if strings.HasPrefix(rule.Metric, "process.") {
		rows, err = v.db.QueryContext(ctx, `SELECT collector_id,ts,pid,cpu,rss,created_at FROM process_samples s WHERE target_id=? AND ts<=? AND (ts>=? OR ts=(SELECT max(p.ts) FROM process_samples p WHERE p.collector_id=s.collector_id AND p.target_id=s.target_id AND p.pid=s.pid AND p.ts<?)) ORDER BY ts,rowid LIMIT ?`, rule.Target, until, since, since, rules.MaxReplayRecords+1)
		if err != nil {
			return data, fmt.Errorf("读取进程指标历史失败：%w", err)
		}
		start := len(data.Messages)
		for rows.Next() {
			var message pipeline.Message
			var sample pipeline.ProcessSample
			sample.TargetID = rule.Target
			if err := rows.Scan(&message.CollectorID, &message.TS, &sample.PID, &sample.CPUPercent, &sample.RSS, &sample.CreatedAt); err != nil {
				rows.Close()
				return data, err
			}
			message.Kind, message.ProcessSample = pipeline.KindProcessSample, &sample
			if err := appendMessage(message); err != nil {
				rows.Close()
				return data, err
			}
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return data, err
		}
		skipped := 0
		for i := start; i < len(data.Messages); i++ {
			message := &data.Messages[i]
			sample := message.ProcessSample
			if sample.CreatedAt != 0 {
				continue
			}
			var detail string
			err := v.db.QueryRowContext(ctx, `SELECT detail FROM target_status WHERE collector_id=? AND target_id=? AND ts<=? ORDER BY ts DESC LIMIT 1`, message.CollectorID, rule.Target, message.TS).Scan(&detail)
			if err == nil {
				var status pipeline.TargetStatus
				if json.Unmarshal([]byte(detail), &status) == nil {
					for _, process := range status.Processes {
						if process.PID == sample.PID && process.CreatedAt <= message.TS {
							sample.CreatedAt = process.CreatedAt
							break
						}
					}
				}
			}
			if ctx.Err() != nil {
				return data, ctx.Err()
			}
			if sample.CreatedAt == 0 {
				skipped++
			}
		}
		if skipped > 0 {
			filtered := data.Messages[:0]
			for _, message := range data.Messages {
				if message.ProcessSample == nil || message.ProcessSample.CreatedAt > 0 {
					filtered = append(filtered, message)
				}
			}
			data.Messages = filtered
			data.Warnings = append(data.Warnings, fmt.Sprintf("已跳过 %d 条无法确认创建时间的旧进程采样，未伪造 PID 身份", skipped))
		}
	}
	return data, nil
}
