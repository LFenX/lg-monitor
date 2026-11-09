package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"slices"
	"strings"
	"time"

	"github.com/LFenX/lg-monitor/internal/collector"
	"github.com/LFenX/lg-monitor/internal/config"
	"github.com/LFenX/lg-monitor/internal/pipeline"
)

const MaxExportPoints = 100000

type ExportMetricPoint struct {
	Labels map[string]string `json:"labels"`
	Value  float64           `json:"value"`
	TS     int64             `json:"ts"`
}

type ExportMetricFamily struct {
	Name   string              `json:"name"`
	Help   string              `json:"help"`
	Unit   string              `json:"unit"`
	Points []ExportMetricPoint `json:"points"`
}

type ExportMetricSnapshot struct {
	TS       int64                `json:"ts"`
	Families []ExportMetricFamily `json:"families"`
}

type metricDefinition struct{ name, help, unit string }

var exportDefinitions = []metricDefinition{
	{"collector_info", "Registered collector identity; value is one.", "1"},
	{"collector_last_seen_timestamp_seconds", "Last persisted heartbeat receipt; zero means never received.", "s"},
	{"system_cpu_ratio", "Latest whole-machine CPU utilization.", "1"},
	{"system_memory_total_bytes", "Physical memory capacity.", "By"},
	{"system_memory_used_bytes", "Used physical memory.", "By"},
	{"system_memory_available_bytes", "Available physical memory.", "By"},
	{"system_memory_used_ratio", "Used physical memory ratio.", "1"},
	{"system_uptime_seconds", "Latest system uptime.", "s"},
	{"system_observed_timestamp_seconds", "System sample observation time.", "s"},
	{"disk_total_bytes", "Mounted volume capacity.", "By"},
	{"disk_used_bytes", "Mounted volume used capacity.", "By"},
	{"disk_free_bytes", "Mounted volume free capacity.", "By"},
	{"disk_used_ratio", "Mounted volume used ratio.", "1"},
	{"disk_observed_timestamp_seconds", "Mounted volume observation time.", "s"},
	{"network_send_bytes_per_second", "Latest outgoing interface rate.", "By/s"},
	{"network_receive_bytes_per_second", "Latest incoming interface rate.", "By/s"},
	{"network_observed_timestamp_seconds", "Interface observation time.", "s"},
	{"target_state", "Latest target state: up=1, down=0, unknown=-1.", "1"},
	{"target_process_count", "Process count in the latest known up or down target observation.", "1"},
	{"target_observed_timestamp_seconds", "Target state observation time.", "s"},
	{"target_cpu_ratio", "Whole-machine CPU ratio summed over matched processes.", "1"},
	{"target_rss_bytes", "Resident memory summed over matched processes.", "By"},
	{"target_process_observed_timestamp_seconds", "Matched process sample observation time.", "s"},
	{"probe_success", "Latest probe result: success=1, failure=0.", "1"},
	{"probe_latency_seconds", "Latest probe latency.", "s"},
	{"probe_observed_timestamp_seconds", "Probe observation time.", "s"},
	{"probe_tls_not_after_timestamp_seconds", "Observed leaf or intermediate certificate expiry.", "s"},
	{"script_value", "Latest numeric script metric in its original undeclared unit; metric label carries its name.", ""},
	{"script_observed_timestamp_seconds", "Script metric observation time.", "s"},
	{"service_state", "Latest service state: running=1, stopped/paused/deleted=0, unknown/pending=-1.", "1"},
	{"service_observed_timestamp_seconds", "Service state observation time.", "s"},
	{"collector_alerts_active", "Number of currently firing persisted alerts for a registered collector by severity.", "1"},
	{"alerts_active", "Number of all currently firing persisted alerts, including global rules, by severity.", "1"},
}

type metricBuilder struct {
	snapshot ExportMetricSnapshot
	families map[string]int
	seen     map[string]bool
	count    int
	err      error
}

func newMetricBuilder(ts int64) *metricBuilder {
	b := &metricBuilder{snapshot: ExportMetricSnapshot{TS: ts, Families: []ExportMetricFamily{}}, families: map[string]int{}, seen: map[string]bool{}}
	for _, d := range exportDefinitions {
		b.families[d.name] = len(b.snapshot.Families)
		b.snapshot.Families = append(b.snapshot.Families, ExportMetricFamily{Name: "lgmon_" + d.name, Help: d.help, Unit: d.unit, Points: []ExportMetricPoint{}})
	}
	return b
}

func (b *metricBuilder) add(name string, labels map[string]string, value float64, ts int64) {
	if b.err != nil {
		return
	}
	index, exists := b.families[name]
	if !exists || math.IsNaN(value) || math.IsInf(value, 0) || ts <= 0 {
		b.err = fmt.Errorf("指标导出包含无效名称、数值或观测时间")
		return
	}
	keys := make([]string, 0, len(labels))
	copyLabels := make(map[string]string, len(labels))
	for k, v := range labels {
		keys = append(keys, k)
		copyLabels[k] = v
	}
	slices.Sort(keys)
	identity := name
	for _, key := range keys {
		identity += fmt.Sprintf("|%d:%s=%d:%s", len(key), key, len(labels[key]), labels[key])
	}
	if b.seen[identity] {
		b.err = fmt.Errorf("指标导出出现重复的名称和标签集")
		return
	}
	if b.count >= MaxExportPoints {
		b.err = fmt.Errorf("指标导出超过 %d 个点，请减少配置范围", MaxExportPoints)
		return
	}
	b.count++
	b.seen[identity] = true
	b.snapshot.Families[index].Points = append(b.snapshot.Families[index].Points, ExportMetricPoint{Labels: copyLabels, Value: value, TS: ts})
}

func (b *metricBuilder) observed(name string, labels map[string]string, ts int64) {
	b.add(name, labels, float64(ts)/1000, ts)
}

// ExportMetrics reads one consistent WAL snapshot without touching collectors or the writer.
func (v *View) ExportMetrics(ctx context.Context, targets []config.Target) (ExportMetricSnapshot, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	tx, err := v.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return ExportMetricSnapshot{}, fmt.Errorf("开始只读指标快照失败：%w", err)
	}
	defer tx.Rollback()
	rows, err := tx.QueryContext(ctx, `SELECT id,coalesce(hostname,''),coalesce(version,''),coalesce(last_seen,0) FROM collectors ORDER BY id`)
	if err != nil {
		return ExportMetricSnapshot{}, fmt.Errorf("读取指标主机失败：%w", err)
	}
	type identity struct {
		id, hostname, version string
		lastSeen              int64
	}
	var identities []identity
	for rows.Next() {
		var c identity
		if err := rows.Scan(&c.id, &c.hostname, &c.version, &c.lastSeen); err != nil {
			rows.Close()
			return ExportMetricSnapshot{}, fmt.Errorf("读取指标主机身份失败：%w", err)
		}
		identities = append(identities, c)
		if len(identities) > MaxExportPoints {
			rows.Close()
			return ExportMetricSnapshot{}, fmt.Errorf("指标主机数量超过导出界限")
		}
	}
	err = errors.Join(rows.Err(), rows.Close())
	if err != nil {
		return ExportMetricSnapshot{}, fmt.Errorf("读取指标主机集合失败：%w", err)
	}
	b := newMetricBuilder(v.now().UnixMilli())
	for _, c := range identities {
		labels := map[string]string{"collector_id": c.id}
		b.add("collector_info", map[string]string{"collector_id": c.id, "hostname": c.hostname, "version": c.version}, 1, b.snapshot.TS)
		b.add("collector_last_seen_timestamp_seconds", labels, float64(c.lastSeen)/1000, b.snapshot.TS)
		if err := exportSystem(ctx, tx, b, labels); err != nil {
			return ExportMetricSnapshot{}, err
		}
		if err := exportServices(ctx, tx, b, labels); err != nil {
			return ExportMetricSnapshot{}, err
		}
		for _, target := range targets {
			if target.AppliesTo(c.id, c.hostname) {
				if err := exportTarget(ctx, tx, b, c.id, target); err != nil {
					return ExportMetricSnapshot{}, err
				}
			}
		}
		for _, severity := range []string{"critical", "warning", "info"} {
			var count int64
			if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM alerts WHERE state='firing' AND collector_id=? AND severity=?`, c.id, severity).Scan(&count); err != nil {
				return ExportMetricSnapshot{}, fmt.Errorf("读取活动告警指标失败：%w", err)
			}
			b.add("collector_alerts_active", map[string]string{"collector_id": c.id, "severity": severity}, float64(count), b.snapshot.TS)
		}
		if b.err != nil {
			return ExportMetricSnapshot{}, b.err
		}
	}
	for _, severity := range []string{"critical", "warning", "info"} {
		var count int64
		if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM alerts WHERE state='firing' AND severity=?`, severity).Scan(&count); err != nil {
			return ExportMetricSnapshot{}, fmt.Errorf("读取全局活动告警指标失败：%w", err)
		}
		b.add("alerts_active", map[string]string{"severity": severity}, float64(count), b.snapshot.TS)
	}
	if b.err != nil {
		return ExportMetricSnapshot{}, b.err
	}
	if err := tx.Commit(); err != nil {
		return ExportMetricSnapshot{}, fmt.Errorf("结束只读指标快照失败：%w", err)
	}
	families := b.snapshot.Families[:0]
	for _, family := range b.snapshot.Families {
		if len(family.Points) > 0 {
			families = append(families, family)
		}
	}
	b.snapshot.Families = families
	return b.snapshot, b.err
}

func exportSystem(ctx context.Context, tx *sql.Tx, b *metricBuilder, labels map[string]string) error {
	var ts int64
	var detail string
	err := tx.QueryRowContext(ctx, `SELECT ts,detail FROM system_metrics WHERE collector_id=? ORDER BY ts DESC LIMIT 1`, labels["collector_id"]).Scan(&ts, &detail)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("读取系统指标快照失败：%w", err)
	}
	var s collector.SystemMetrics
	if err := json.Unmarshal([]byte(detail), &s); err != nil {
		return fmt.Errorf("解码系统指标快照失败：%w", err)
	}
	for name, value := range map[string]float64{"system_cpu_ratio": s.CPUPercent / 100, "system_memory_total_bytes": float64(s.MemoryTotal), "system_memory_used_bytes": float64(s.MemoryUsed), "system_memory_available_bytes": float64(s.MemoryAvailable), "system_memory_used_ratio": s.MemoryPercent / 100, "system_uptime_seconds": float64(s.UptimeSeconds)} {
		b.add(name, labels, value, ts)
	}
	b.observed("system_observed_timestamp_seconds", labels, ts)
	for _, d := range s.Disks {
		l := map[string]string{"collector_id": labels["collector_id"], "volume": d.Path}
		for name, value := range map[string]float64{"disk_total_bytes": float64(d.Total), "disk_used_bytes": float64(d.Used), "disk_free_bytes": float64(d.Free), "disk_used_ratio": d.UsedPercent / 100} {
			b.add(name, l, value, ts)
		}
		b.observed("disk_observed_timestamp_seconds", l, ts)
	}
	for _, n := range s.Networks {
		l := map[string]string{"collector_id": labels["collector_id"], "interface": n.Name}
		b.add("network_send_bytes_per_second", l, n.SendBytesPerSec, ts)
		b.add("network_receive_bytes_per_second", l, n.ReceiveBytesPerSec, ts)
		b.observed("network_observed_timestamp_seconds", l, ts)
	}
	return b.err
}

func exportServices(ctx context.Context, tx *sql.Tx, b *metricBuilder, labels map[string]string) error {
	rows, err := tx.QueryContext(ctx, `SELECT s.name,s.ts,s.state FROM service_status s JOIN (SELECT name,max(ts) ts FROM service_status WHERE collector_id=? GROUP BY name) latest ON s.name=latest.name AND s.ts=latest.ts WHERE s.collector_id=? ORDER BY s.name`, labels["collector_id"], labels["collector_id"])
	if err != nil {
		return fmt.Errorf("读取服务指标失败：%w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var name, state string
		var ts int64
		if err := rows.Scan(&name, &ts, &state); err != nil {
			return fmt.Errorf("读取服务状态指标失败：%w", err)
		}
		value := -1.0
		switch strings.ToLower(state) {
		case "running":
			value = 1
		case "stopped", "paused", "deleted":
			value = 0
		}
		l := map[string]string{"collector_id": labels["collector_id"], "service": name}
		b.add("service_state", l, value, ts)
		b.observed("service_observed_timestamp_seconds", l, ts)
		if b.err != nil {
			return b.err
		}
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("结束服务指标查询失败：%w", err)
	}
	return nil
}

func exportTarget(ctx context.Context, tx *sql.Tx, b *metricBuilder, cid string, target config.Target) error {
	l := map[string]string{"collector_id": cid, "target_id": target.ID}
	var ts int64
	var state, detail string
	err := tx.QueryRowContext(ctx, `SELECT ts,state,detail FROM target_status WHERE collector_id=? AND target_id=? ORDER BY ts DESC LIMIT 1`, cid, target.ID).Scan(&ts, &state, &detail)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("读取目标指标失败：%w", err)
	}
	if err == nil {
		value := -1.0
		switch state {
		case "up":
			value = 1
		case "down":
			value = 0
		}
		b.add("target_state", l, value, ts)
		b.observed("target_observed_timestamp_seconds", l, ts)
		if state == "down" {
			b.add("target_process_count", l, 0, ts)
		}
		if state == "up" {
			var status pipeline.TargetStatus
			if err := json.Unmarshal([]byte(detail), &status); err != nil {
				return fmt.Errorf("解码目标指标失败：%w", err)
			}
			b.add("target_process_count", l, float64(len(status.Processes)), ts)
			if err := exportTargetProcesses(ctx, tx, b, l, ts, status.Processes); err != nil {
				return err
			}
		}
	}
	for index, probe := range target.Probes {
		p := fmt.Sprint(index)
		labels := map[string]string{"collector_id": cid, "target_id": target.ID, "probe": p}
		var pts, ok, latency int64
		var expiry sql.NullInt64
		err := tx.QueryRowContext(ctx, `SELECT ts,ok,latency_ms,tls_not_after FROM probe_results WHERE collector_id=? AND target_id=? AND probe=? ORDER BY ts DESC LIMIT 1`, cid, target.ID, p).Scan(&pts, &ok, &latency, &expiry)
		if errors.Is(err, sql.ErrNoRows) {
			continue
		}
		if err != nil {
			return fmt.Errorf("读取探测指标失败：%w", err)
		}
		b.add("probe_success", labels, float64(ok), pts)
		b.add("probe_latency_seconds", labels, float64(latency)/1000, pts)
		b.observed("probe_observed_timestamp_seconds", labels, pts)
		if expiry.Valid && expiry.Int64 > 0 {
			b.add("probe_tls_not_after_timestamp_seconds", labels, float64(expiry.Int64)/1000, pts)
		}
		if probe.Type == "script" {
			rows, err := tx.QueryContext(ctx, `SELECT name,value FROM script_metrics WHERE collector_id=? AND target_id=? AND probe=? AND ts=? ORDER BY name`, cid, target.ID, p, pts)
			if err != nil {
				return fmt.Errorf("读取脚本指标失败：%w", err)
			}
			for rows.Next() {
				var name string
				var value float64
				if err := rows.Scan(&name, &value); err != nil {
					rows.Close()
					return fmt.Errorf("读取脚本指标值失败：%w", err)
				}
				labels["metric"] = name
				b.add("script_value", labels, value, pts)
				b.observed("script_observed_timestamp_seconds", labels, pts)
				if b.err != nil {
					rows.Close()
					return b.err
				}
			}
			err = errors.Join(rows.Err(), rows.Close())
			if err != nil {
				return fmt.Errorf("结束脚本指标查询失败：%w", err)
			}
		}
	}
	return b.err
}

// Event time alone cannot prove that a late sample belongs to the current PID incarnation.
func exportTargetProcesses(ctx context.Context, tx *sql.Tx, b *metricBuilder, labels map[string]string, stateTS int64, refs []pipeline.ProcessRef) error {
	type identity struct {
		pid     int32
		created int64
	}
	expected := map[identity]bool{}
	for _, ref := range refs {
		if ref.CreatedAt <= 0 {
			return nil
		}
		expected[identity{ref.PID, ref.CreatedAt}] = true
	}
	if len(expected) == 0 {
		return nil
	}
	rows, err := tx.QueryContext(ctx, `SELECT ts,pid,created_at,cpu,rss FROM process_samples WHERE collector_id=? AND target_id=? AND ts=(SELECT ts FROM process_samples WHERE collector_id=? AND target_id=? ORDER BY ts DESC LIMIT 1)`, labels["collector_id"], labels["target_id"], labels["collector_id"], labels["target_id"])
	if err != nil {
		return fmt.Errorf("读取目标进程指标失败：%w", err)
	}
	defer rows.Close()
	seen := map[identity]bool{}
	var cpu, rss float64
	var sampleTS int64
	coherent := true
	for rows.Next() {
		var pid int32
		var created int64
		var processCPU, processRSS float64
		if err := rows.Scan(&sampleTS, &pid, &created, &processCPU, &processRSS); err != nil {
			return fmt.Errorf("读取进程指标身份失败：%w", err)
		}
		key := identity{pid, created}
		if sampleTS < stateTS || !expected[key] || seen[key] {
			coherent = false
		}
		seen[key] = true
		cpu += processCPU
		rss += processRSS
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("结束进程指标查询失败：%w", err)
	}
	if coherent && len(seen) == len(expected) {
		b.add("target_cpu_ratio", labels, cpu/100, sampleTS)
		b.add("target_rss_bytes", labels, rss, sampleTS)
		b.observed("target_process_observed_timestamp_seconds", labels, sampleTS)
	}
	return b.err
}
