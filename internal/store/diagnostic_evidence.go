package store

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/LFenX/lg-monitor/internal/pipeline"
)

func (d diagnosticRead) evidence() error {
	p := d.value
	id := p.Alert.CollectorID
	offset := "0"
	if exists, err := d.column("collectors", "clock_offset_ms"); err != nil {
		return err
	} else if exists {
		offset = "clock_offset_ms"
	}
	host := DiagnosticHost{ID: id}
	err := d.tx.QueryRowContext(d.ctx, `SELECT coalesce(hostname,''),coalesce(last_seen,0),`+offset+` FROM collectors WHERE id=?`, id).Scan(&host.Name, &host.LastSeen, &host.ClockOffsetMS)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("读取诊断采集器失败：%w", err)
	}
	if err == nil {
		host.Name = diagnosticText(host.Name, 512)
		p.Host = &host
	}
	concrete := p.Alert.TargetID != "" && !strings.HasPrefix(p.Alert.TargetID, "@")
	if concrete {
		args := []any{id, p.Alert.TargetID, id, p.Alert.TargetID, p.Window.Anchor, id, p.Alert.TargetID, id, p.Alert.TargetID}
		query := `SELECT 'incident',ts,state,detail FROM target_status WHERE collector_id=? AND target_id=? AND ts=(SELECT max(ts) FROM target_status WHERE collector_id=? AND target_id=? AND ts<=?)
UNION ALL SELECT 'latest',ts,state,detail FROM target_status WHERE collector_id=? AND target_id=? AND ts=(SELECT max(ts) FROM target_status WHERE collector_id=? AND target_id=?) ORDER BY 1`
		if err := d.read("targetSnapshots", "target_status", 2, query, args, func(rows *sql.Rows) error {
			var snapshot DiagnosticSnapshot
			var detail string
			if err := rows.Scan(&snapshot.Role, &snapshot.TS, &snapshot.State, &detail); err != nil {
				return err
			}
			var status pipeline.TargetStatus
			if err := json.Unmarshal([]byte(detail), &status); err != nil {
				return err
			}
			snapshot.Process, snapshot.Port, snapshot.Service = diagnosticText(status.Process, 32), diagnosticText(status.Port, 32), diagnosticText(status.Service, 32)
			snapshot.Processes = []DiagnosticProcess{}
			snapshot.ProcessesTruncated = len(status.Processes) > 50
			for _, ref := range status.Processes[:min(50, len(status.Processes))] {
				snapshot.Processes = append(snapshot.Processes, DiagnosticProcess{PID: ref.PID, ParentPID: ref.ParentPID, Name: diagnosticText(ref.Name, 256), Exe: diagnosticText(ref.Exe, 2048), CreatedAt: ref.CreatedAt, CPUPercent: ref.CPUPercent, RSS: ref.RSS, Threads: ref.Threads})
			}
			p.Snapshots = append(p.Snapshots, snapshot)
			return nil
		}); err != nil {
			return err
		}
		if len(p.Snapshots) == 0 || p.Snapshots[0].Role != "incident" {
			p.Notes = append(p.Notes, "没有故障点之前的目标快照；最近状态不能替代触发时的进程证据。")
		}
		if len(p.Snapshots) > 0 && p.Snapshots[0].Role == "incident" && len(p.Snapshots[0].Processes) == 0 {
			var at int64
			var state, detail string
			err := d.tx.QueryRowContext(d.ctx, `SELECT ts,state,detail FROM target_status WHERE collector_id=? AND target_id=? AND ts>=? AND ts<? AND json_array_length(detail,'$.processes')>0 ORDER BY ts DESC LIMIT 1`, id, p.Alert.TargetID, p.Window.From, p.Window.Anchor).Scan(&at, &state, &detail)
			if err != nil && !errors.Is(err, sql.ErrNoRows) {
				return fmt.Errorf("读取故障前匹配进程失败：%w", err)
			}
			if err == nil {
				var status pipeline.TargetStatus
				if err := json.Unmarshal([]byte(detail), &status); err != nil {
					return err
				}
				snapshot := DiagnosticSnapshot{Role: "previousMatch", TS: at, State: state, Process: diagnosticText(status.Process, 32), Port: diagnosticText(status.Port, 32), Service: diagnosticText(status.Service, 32), Processes: []DiagnosticProcess{}, ProcessesTruncated: len(status.Processes) > 50}
				for _, ref := range status.Processes[:min(50, len(status.Processes))] {
					snapshot.Processes = append(snapshot.Processes, DiagnosticProcess{PID: ref.PID, ParentPID: ref.ParentPID, Name: diagnosticText(ref.Name, 256), Exe: diagnosticText(ref.Exe, 2048), CreatedAt: ref.CreatedAt, CPUPercent: ref.CPUPercent, RSS: ref.RSS, Threads: ref.Threads})
				}
				p.Snapshots = append(p.Snapshots, snapshot)
				p.Coverage[0].Rows++
				p.Coverage[0].Limit = 3
			}
		}
		kind, tls := "'unknown'", "0"
		for _, field := range []struct {
			name string
			out  *string
		}{{"type", &kind}, {"tls_not_after", &tls}} {
			if exists, err := d.column("probe_results", field.name); err != nil {
				return err
			} else if exists {
				if field.name == "type" {
					*field.out = "coalesce(type,'unknown')"
				} else {
					*field.out = "coalesce(" + field.name + ",0)"
				}
			}
		}
		if err := d.read("probeResults", "probe_results", 50, `SELECT ts,probe,`+kind+`,ok,latency_ms,`+tls+` FROM probe_results WHERE collector_id=? AND target_id=? AND ts>=? AND ts<=? ORDER BY ts DESC,probe`, []any{id, p.Alert.TargetID, p.Window.From, p.Window.To}, func(rows *sql.Rows) error {
			var probe DiagnosticProbe
			if err := rows.Scan(&probe.TS, &probe.Probe, &probe.Type, &probe.OK, &probe.LatencyMS, &probe.TLSNotAfter); err != nil {
				return err
			}
			probe.Type = diagnosticText(probe.Type, 32)
			probe.Probe = diagnosticText(probe.Probe, 32)
			p.Probes = append(p.Probes, probe)
			return nil
		}); err != nil {
			return err
		}
	} else {
		d.unavailable("targetSnapshots", 2, "告警没有具体目标，不枚举整机进程")
		d.unavailable("probeResults", 50, "告警没有具体目标，不扩展探测范围")
	}
	if err := d.read("systemMetrics", "system_metrics", 10, `SELECT ts,cpu,memory_percent,uptime FROM system_metrics WHERE collector_id=? AND ts>=? AND ts<=? ORDER BY ts DESC`, []any{id, p.Window.From, p.Window.To}, func(rows *sql.Rows) error {
		var metric DiagnosticMetric
		if err := rows.Scan(&metric.TS, &metric.CPUPercent, &metric.MemoryPercent, &metric.UptimeSeconds); err != nil {
			return err
		}
		p.Metrics = append(p.Metrics, metric)
		return nil
	}); err != nil {
		return err
	}
	if err := d.events(concrete); err != nil {
		return err
	}
	if err := d.read("startupChanges", "inventory_changes", 50, `SELECT ts,scope,name,change FROM inventory_changes WHERE collector_id=? AND kind='startup' AND ts>=? AND ts<=? ORDER BY ts DESC,seq DESC,id`, []any{id, p.Window.From, p.Window.To}, func(rows *sql.Rows) error {
		var change DiagnosticChange
		if err := rows.Scan(&change.TS, &change.Scope, &change.Name, &change.Change); err != nil {
			return err
		}
		change.Scope, change.Name = diagnosticText(change.Scope, 512), diagnosticText(change.Name, 512)
		change.Change = diagnosticText(change.Change, 32)
		p.Changes = append(p.Changes, change)
		return nil
	}); err != nil {
		return err
	}
	source := "''"
	if exists, err := d.column("collector_events", "source_table"); err != nil {
		return err
	} else if exists {
		source = "source_table"
	}
	if err := d.read("collectorEvents", "collector_events", 50, `SELECT ts,kind,dropped,`+source+` FROM collector_events WHERE collector_id=? AND ts>=? AND ts<=? ORDER BY ts DESC,seq DESC`, []any{id, p.Window.From, p.Window.To}, func(rows *sql.Rows) error {
		var event DiagnosticCollectorEvent
		if err := rows.Scan(&event.TS, &event.Kind, &event.Dropped, &event.SourceTable); err != nil {
			return err
		}
		event.Kind, event.SourceTable = diagnosticText(event.Kind, 64), diagnosticText(event.SourceTable, 64)
		p.CollectorEvents = append(p.CollectorEvents, event)
		return nil
	}); err != nil {
		return err
	}
	return nil
}

func (d diagnosticRead) events(concrete bool) error {
	p := d.value
	query := `SELECT e.ts,e.channel,e.provider,e.event_id,e.level,e.record_id,e.subject FROM eventlog_events e WHERE e.collector_id=? AND e.ts>=? AND e.ts<=?`
	args := []any{p.Alert.CollectorID, p.Window.From, p.Window.To}
	if concrete {
		if exists, err := d.table("eventlog_matches"); err != nil {
			return err
		} else if !exists {
			d.unavailable("eventLogs", 50, "旧版数据没有事件目标归属，不扩展到其他目标")
			return nil
		}
		query += ` AND EXISTS(SELECT 1 FROM eventlog_matches m WHERE m.collector_id=e.collector_id AND m.channel=e.channel AND m.record_id=e.record_id AND m.ts=e.ts AND m.name=? AND m.kind IN ('subject','subscription'))`
		args = append(args, p.Alert.TargetID)
	}
	query += ` ORDER BY e.ts DESC,e.channel DESC,e.record_id DESC`
	if err := d.read("eventLogs", "eventlog_events", 50, query, args, func(rows *sql.Rows) error {
		var event DiagnosticEvent
		var recordID int64
		if err := rows.Scan(&event.TS, &event.Channel, &event.Provider, &event.EventID, &event.Level, &recordID, &event.Subject); err != nil {
			return err
		}
		event.RecordID = strconv.FormatInt(recordID, 10)
		event.Provider, event.Subject = diagnosticText(event.Provider, 256), diagnosticText(event.Subject, 512)
		event.Summary = fmt.Sprintf("Windows 事件 %d；原始正文未导出", event.EventID)
		switch {
		case event.Channel == "Application" && strings.EqualFold(event.Provider, "Application Error") && event.EventID == 1000:
			event.Summary = "应用程序错误事件；需结合进程与时间核对"
		case event.Channel == "Application" && strings.EqualFold(event.Provider, "Application Hang") && event.EventID == 1002:
			event.Summary = "应用程序无响应事件；需结合进程与时间核对"
		case event.Channel == "System" && strings.EqualFold(event.Provider, "Service Control Manager") && (event.EventID == 7031 || event.EventID == 7034):
			event.Summary = "服务意外停止事件；未据此推断停止原因"
		case event.Channel == "System" && ((strings.EqualFold(event.Provider, "EventLog") && event.EventID == 6008) || (strings.EqualFold(event.Provider, "Microsoft-Windows-Kernel-Power") && event.EventID == 41)):
			event.Summary = "非正常关机相关事件；需核对机器实际操作时间"
		}
		p.Events = append(p.Events, event)
		return nil
	}); err != nil {
		return err
	}
	slices.Reverse(p.Events)
	return nil
}
