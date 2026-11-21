// Copyright (c) 2026 LFen (https://lfen.ink) — lg-monitor

// Package blueprint turns short intents into complete, validated configuration:
// ready-made dashboard pages and one-call monitors.
package blueprint

import (
	"fmt"
	"regexp"
	"slices"
	"strings"

	"github.com/LFenX/lg-monitor/internal/config"
)

var idPattern = regexp.MustCompile(`^[a-z0-9-]+$`)

// Template describes one ready-made dashboard page an agent can create by name.
type Template struct {
	ID          string              `json:"id"`
	Title       string              `json:"title"`
	Description string              `json:"description"`
	Theme       string              `json:"theme,omitempty" jsonschema_description:"配色；省略时跟随网页的深浅色。"`
	Icon        string              `json:"icon"`
	Targeted    bool                `json:"targeted" jsonschema_description:"为 true 时可以用 target 只看一个监控。"`
	Panels      []string            `json:"panels" jsonschema_description:"这页包含的卡片类型，便于预览。"`
	Layout      []config.PanelShape `json:"layout" jsonschema_description:"每张卡片的类型和宽度，按顺序。"`
	Example     string              `json:"example" jsonschema_description:"可以直接对 Agent 说的一句话。"`
}

// TemplateInput selects a template and its few options.
type TemplateInput struct {
	Template string `json:"template" jsonschema:"minLength=1" jsonschema_description:"模板 ID，先调用 list_dashboard_templates。"`
	ID       string `json:"id,omitempty" jsonschema:"pattern=^[a-z0-9-]+$" jsonschema_description:"面板 ID；省略时用模板 ID（带 target 时加上目标 ID）。同 ID 会被覆盖。"`
	Title    string `json:"title,omitempty" jsonschema_description:"显示标题；省略时用模板标题。"`
	Target   string `json:"target,omitempty" jsonschema:"pattern=^[a-z0-9-]+$" jsonschema_description:"只看一个监控时填它的 ID，仅 services 和 websites 支持。"`
	Theme    string `json:"theme,omitempty" jsonschema:"enum=aurora,enum=midnight,enum=terminal,enum=sunset,enum=ocean,enum=forest,enum=paper" jsonschema_description:"配色风格；省略时跟随网页的深浅色，更适合日常查看。"`
}

type builder func(target string) []config.DashboardPanel

type entry struct {
	Template
	build builder
}

const hosts = "(:collector IS NULL OR collector_id = :collector)"

// bucket groups raw samples into at most about 240 points for any selected range.
const bucket = "CAST(ts / MAX(1000, (:to - :from) / 240) AS INTEGER) * MAX(1000, (:to - :from) / 240)"

func number(value float64) *float64 { return &value }

func panel(id, kind, title string, width int, query string) config.DashboardPanel {
	return config.DashboardPanel{ID: id, Type: kind, Title: title, Width: width, Query: strings.Join(strings.Fields(query), " ")}
}

func with(p config.DashboardPanel, unit string, warn, critical *float64) config.DashboardPanel {
	p.Unit, p.Warn, p.Critical = unit, warn, critical
	return p
}

func hint(p config.DashboardPanel, text string) config.DashboardPanel {
	p.Hint = text
	return p
}

const latestService = "NOT EXISTS (SELECT 1 FROM service_status n WHERE n.collector_id = s.collector_id AND n.name = s.name AND n.ts > s.ts)"

// latest keeps each series' newest sample from a periodic table. The outer rows are first narrowed to the
// last 15 minutes through the ts index: matching every stored row against its own series made these cards
// hit the 5-second query budget once a day of samples had built up. Service status is stored only on change,
// so it keeps latestService instead.
func latest(table, alias, key string) string {
	return alias + ".ts >= (SELECT max(ts) FROM " + table + ") - 900000 AND " + alias + ".ts = (SELECT max(n.ts) FROM " + table + " n WHERE n.collector_id = " + alias + ".collector_id AND n." + key + " = " + alias + "." + key + ")"
}

func catalog() []entry {
	return []entry{
		{Template{ID: "system", Title: "本机", Description: "CPU、内存、磁盘、网络和运行时长：这台电脑本身是否健康。", Icon: "cpu",
			Example: "用 system 模板做一页本机面板"}, func(string) []config.DashboardPanel {
			return []config.DashboardPanel{
				with(panel("cpu", "trend", "CPU", 2, `SELECT `+bucket+` AS ts, round(avg(cpu), 1) AS value FROM system_metrics WHERE ts BETWEEN :from AND :to AND `+hosts+` GROUP BY 1 ORDER BY 1`), "percent", number(75), number(90)),
				with(panel("memory", "trend", "内存", 2, `SELECT `+bucket+` AS ts, round(avg(memory_percent), 1) AS value FROM system_metrics WHERE ts BETWEEN :from AND :to AND `+hosts+` GROUP BY 1 ORDER BY 1`), "percent", number(80), number(92)),
				with(panel("disk-max", "stat", "磁盘最高占用", 2, `SELECT round(max(d.used_percent), 1) AS value FROM disk_metrics d WHERE `+latest("disk_metrics", "d", "path")+` AND (:collector IS NULL OR d.collector_id = :collector)`), "percent", number(80), number(92)),
				with(panel("traffic", "stat", "网络收发", 2, `SELECT round(sum(send_bytes_per_sec + receive_bytes_per_sec), 0) AS value FROM network_metrics x WHERE `+latest("network_metrics", "x", "name")+` AND (:collector IS NULL OR x.collector_id = :collector)`), "bytes_per_sec", nil, nil),
				with(panel("uptime", "stat", "已运行", 2, `SELECT uptime AS value FROM system_metrics WHERE `+hosts+` ORDER BY ts DESC LIMIT 1`), "seconds", nil, nil),
				with(panel("firing", "stat", "触发中告警", 2, `SELECT COUNT(*) AS value FROM v_alerts_active WHERE state = 'firing' AND `+hosts), "count", nil, number(1)),
				with(panel("load", "area", "CPU 与内存", 8, `SELECT `+bucket+` AS ts, 'CPU' AS series, round(avg(cpu), 1) AS value FROM system_metrics WHERE ts BETWEEN :from AND :to AND `+hosts+` GROUP BY 1
					UNION ALL SELECT `+bucket+` AS ts, '内存' AS series, round(avg(memory_percent), 1) AS value FROM system_metrics WHERE ts BETWEEN :from AND :to AND `+hosts+` GROUP BY 1 ORDER BY ts`), "percent", nil, nil),
				with(panel("disks", "toplist", "磁盘使用率", 4, `SELECT d.path AS label, round(d.used_percent, 1) AS value FROM disk_metrics d WHERE `+latest("disk_metrics", "d", "path")+` AND (:collector IS NULL OR d.collector_id = :collector) ORDER BY value DESC LIMIT 8`), "percent", number(80), number(92)),
				with(panel("network", "line", "网络收发", 8, `SELECT `+bucket+` AS ts, '发送' AS series, round(sum(send_bytes_per_sec) / max(1, count(DISTINCT ts)), 0) AS value FROM network_metrics WHERE ts BETWEEN :from AND :to AND `+hosts+` GROUP BY 1
					UNION ALL SELECT `+bucket+` AS ts, '接收' AS series, round(sum(receive_bytes_per_sec) / max(1, count(DISTINCT ts)), 0) AS value FROM network_metrics WHERE ts BETWEEN :from AND :to AND `+hosts+` GROUP BY 1 ORDER BY ts`), "bytes_per_sec", nil, nil),
				with(panel("nics", "toplist", "网卡流量", 4, `SELECT name AS label, round(avg(send_bytes_per_sec + receive_bytes_per_sec), 0) AS value FROM network_metrics WHERE ts BETWEEN :from AND :to AND `+hosts+` GROUP BY 1 HAVING value > 0 ORDER BY value DESC LIMIT 6`), "bytes_per_sec", nil, nil),
				with(panel("disk-free", "bar", "磁盘剩余空间", 6, `SELECT d.path AS label, d.free AS value FROM disk_metrics d WHERE `+latest("disk_metrics", "d", "path")+` AND (:collector IS NULL OR d.collector_id = :collector) ORDER BY d.path LIMIT 8`), "bytes", nil, nil),
				panel("alerts", "alerts", "当前告警", 6, `SELECT summary, severity, state, started_at FROM v_alerts_active WHERE `+hosts+` ORDER BY started_at DESC LIMIT 20`),
			}
		}},
		{Template{ID: "services", Title: "服务", Description: "全部监控对象的状态、可用率、响应耗时和资源占用；可以只看一个监控。", Icon: "shield",
			Example: "用 services 模板做一页服务面板"}, func(target string) []config.DashboardPanel {
			own := hosts
			if target != "" {
				own = "target_id = '" + target + "' AND " + hosts
			}
			return []config.DashboardPanel{
				hint(with(panel("up", "stat", "正常", 3, `SELECT COALESCE(SUM(state = 'up'), 0) AS value FROM v_target_current WHERE `+own), "count", nil, nil), "当前处于正常状态的监控"),
				hint(with(panel("down", "stat", "异常", 3, `SELECT COALESCE(SUM(state = 'down'), 0) AS value FROM v_target_current WHERE `+own), "count", nil, number(1)), "需要马上处理"),
				hint(with(panel("unknown", "stat", "未知", 3, `SELECT COALESCE(SUM(state NOT IN ('up', 'down')), 0) AS value FROM v_target_current WHERE `+own), "count", number(1), nil), "还没采到或无法判断"),
				hint(with(panel("availability", "stat", "可用率", 3, `SELECT round(100.0 * SUM(state = 'up') / MAX(1, SUM(state IN ('up', 'down'))), 2) AS value FROM target_status WHERE ts BETWEEN :from AND :to AND `+own), "percent", nil, nil), "所选时间范围内"),
				panel("grid", "status_grid", "全部监控", 12, `SELECT target_id AS label, CASE WHEN state IN ('up', 'down') THEN state ELSE 'unknown' END AS state FROM v_target_current WHERE `+own+` ORDER BY CASE state WHEN 'down' THEN 0 WHEN 'up' THEN 2 ELSE 1 END, target_id`),
				panel("timeline", "timeline", "状态时间线", 12, `SELECT ts, target_id AS label, CASE WHEN state IN ('up', 'down') THEN state ELSE 'unknown' END AS state FROM target_status WHERE ts BETWEEN :from AND :to AND `+own+` ORDER BY ts LIMIT 8000`),
				with(panel("latency", "line", "探测耗时", 6, `SELECT `+bucket+` AS ts, target_id AS series, round(avg(latency_ms), 1) AS value FROM probe_results WHERE ok = 1 AND ts BETWEEN :from AND :to AND `+own+` GROUP BY 1, 2 ORDER BY 1`), "ms", nil, nil),
				with(panel("cpu", "line", "进程 CPU", 6, `SELECT `+bucket+` AS ts, target_id AS series, round(sum(cpu) / max(1, count(DISTINCT ts)), 2) AS value FROM process_samples WHERE ts BETWEEN :from AND :to AND `+own+` GROUP BY 1, 2 ORDER BY 1`), "percent", nil, nil),
				with(panel("memory", "area", "进程内存", 8, `SELECT `+bucket+` AS ts, target_id AS series, round(sum(rss) / max(1, count(DISTINCT ts)), 0) AS value FROM process_samples WHERE ts BETWEEN :from AND :to AND `+own+` GROUP BY 1, 2 ORDER BY 1`), "bytes", nil, nil),
				with(panel("memory-top", "toplist", "内存占用", 4, `SELECT p.target_id AS label, SUM(p.rss) AS value FROM process_samples p WHERE `+latest("process_samples", "p", "target_id")+` AND (:collector IS NULL OR p.collector_id = :collector)`+targetFilter(target, "p")+` GROUP BY 1 ORDER BY value DESC LIMIT 8`), "bytes", nil, nil),
				panel("failures", "log", "最近探测失败", 6, `SELECT ts, target_id || '：' || COALESCE(NULLIF(error, ''), NULLIF(message, ''), '探测失败') AS message, type AS level FROM probe_results WHERE ok = 0 AND ts BETWEEN :from AND :to AND `+own+` ORDER BY ts DESC LIMIT 60`),
				panel("alerts", "alerts", "告警记录", 6, `SELECT summary, severity, state, started_at FROM alerts WHERE started_at BETWEEN :from AND :to AND `+own+` ORDER BY started_at DESC LIMIT 30`),
			}
		}},
		{Template{ID: "websites", Title: "网站", Description: "所有网址探测的成功率、响应耗时、证书剩余天数和失败明细；可以只看一个网址监控。", Icon: "globe",
			Example: "用 websites 模板做一页网站面板"}, func(target string) []config.DashboardPanel {
			own := "type = 'http' AND " + hosts
			if target != "" {
				own = "target_id = '" + target + "' AND " + own
			}
			certificates := hosts + targetFilter(target, "")
			return []config.DashboardPanel{
				with(panel("success", "stat", "成功率", 3, `SELECT round(100.0 * SUM(ok) / MAX(1, COUNT(*)), 2) AS value FROM probe_results WHERE ts BETWEEN :from AND :to AND `+own), "percent", nil, nil),
				with(panel("latency-now", "trend", "平均耗时", 3, `SELECT `+bucket+` AS ts, round(avg(latency_ms), 1) AS value FROM probe_results WHERE ok = 1 AND ts BETWEEN :from AND :to AND `+own+` GROUP BY 1 ORDER BY 1`), "ms", number(800), number(2000)),
				with(panel("slowest", "stat", "最慢一次", 3, `SELECT round(max(latency_ms), 0) AS value FROM probe_results WHERE ok = 1 AND ts BETWEEN :from AND :to AND `+own), "ms", number(2000), number(5000)),
				hint(panel("certificate", "stat", "证书最早到期", 3, `SELECT COALESCE(CAST(MIN((tls_not_after - :to) / 86400000) AS INTEGER) || ' 天', '无 HTTPS') AS value FROM v_probe_certificates WHERE `+certificates), "最早到期的非根证书"),
				with(panel("latency", "area", "响应耗时", 8, `SELECT `+bucket+` AS ts, target_id AS series, round(avg(latency_ms), 1) AS value FROM probe_results WHERE ok = 1 AND ts BETWEEN :from AND :to AND `+own+` GROUP BY 1, 2 ORDER BY 1`), "ms", nil, nil),
				panel("result", "pie", "探测结果", 4, `SELECT CASE ok WHEN 1 THEN '成功' ELSE '失败' END AS label, COUNT(*) AS value FROM probe_results WHERE ts BETWEEN :from AND :to AND `+own+` GROUP BY 1`),
				panel("timeline", "timeline", "可用性时间线", 12, `SELECT ts, target_id AS label, CASE ok WHEN 1 THEN 'up' ELSE 'down' END AS state FROM probe_results WHERE ts BETWEEN :from AND :to AND `+own+` ORDER BY ts LIMIT 8000`),
				panel("certificates", "table", "HTTPS 证书", 5, `SELECT target_id AS 监控, tls_subject AS 证书, CAST((tls_not_after - :to) / 86400000 AS INTEGER) AS 剩余天数 FROM v_probe_certificates WHERE `+certificates+` ORDER BY tls_not_after LIMIT 50`),
				panel("failures", "log", "失败明细", 7, `SELECT ts, target_id || '：' || COALESCE(NULLIF(error, ''), NULLIF(message, ''), '探测失败') AS message, 'http' AS level FROM probe_results WHERE ok = 0 AND ts BETWEEN :from AND :to AND `+own+` ORDER BY ts DESC LIMIT 100`),
			}
		}},
		{Template{ID: "alerts", Title: "告警", Description: "触发中的告警、按严重度与规则的分布、最常出问题的对象和最近事件。", Icon: "bell",
			Example: "用 alerts 模板做一页告警面板"}, func(string) []config.DashboardPanel {
			return []config.DashboardPanel{
				with(panel("firing", "stat", "触发中", 3, `SELECT COUNT(*) AS value FROM v_alerts_active WHERE state = 'firing' AND `+hosts), "count", nil, number(1)),
				with(panel("critical", "stat", "严重", 3, `SELECT COUNT(*) AS value FROM v_alerts_active WHERE state = 'firing' AND severity = 'critical' AND `+hosts), "count", nil, number(1)),
				with(panel("opened", "stat", "新增", 3, `SELECT COUNT(*) AS value FROM alerts WHERE started_at BETWEEN :from AND :to AND `+hosts), "count", nil, nil),
				with(panel("resolved", "stat", "已恢复", 3, `SELECT COUNT(*) AS value FROM alerts WHERE resolved_at BETWEEN :from AND :to AND `+hosts), "count", nil, nil),
				panel("active", "alerts", "当前告警", 8, `SELECT summary, severity, state, started_at FROM v_alerts_active WHERE `+hosts+` ORDER BY started_at DESC LIMIT 30`),
				panel("severity", "pie", "按严重度", 4, `SELECT CASE severity WHEN 'critical' THEN '严重' WHEN 'warning' THEN '警告' ELSE '提示' END AS label, COUNT(*) AS value FROM alerts WHERE started_at BETWEEN :from AND :to AND `+hosts+` GROUP BY 1 ORDER BY value DESC`),
				with(panel("rules", "toplist", "最常触发的规则", 4, `SELECT rule_id AS label, COUNT(*) AS value FROM alerts WHERE started_at BETWEEN :from AND :to AND `+hosts+` GROUP BY 1 ORDER BY value DESC LIMIT 8`), "count", nil, nil),
				with(panel("targets", "toplist", "最常出问题的对象", 4, `SELECT COALESCE(NULLIF(target_id, ''), '(主机)') AS label, COUNT(*) AS value FROM alerts WHERE started_at BETWEEN :from AND :to AND `+hosts+` GROUP BY 1 ORDER BY value DESC LIMIT 8`), "count", nil, nil),
				panel("history", "log", "最近告警事件", 4, `SELECT e.ts AS ts, e.message AS message, e.kind AS level FROM alert_events e JOIN alerts a ON a.id = e.alert_id WHERE e.ts BETWEEN :from AND :to AND (:collector IS NULL OR a.collector_id = :collector) ORDER BY e.ts DESC LIMIT 60`),
			}
		}},
		{Template{ID: "windows", Title: "Windows 系统", Description: "Windows 服务、开机启动项和计划任务：数量、分布，以及最近的变化。", Icon: "layers",
			Example: "用 windows 模板做一页 Windows 系统面板"}, func(string) []config.DashboardPanel {
			scope := latestService + " AND (:collector IS NULL OR s.collector_id = :collector)"
			changed := `s.ts BETWEEN :from AND :to AND EXISTS (SELECT 1 FROM service_status p WHERE p.collector_id = s.collector_id AND p.name = s.name AND p.ts < s.ts) AND (:collector IS NULL OR s.collector_id = :collector)`
			return []config.DashboardPanel{
				with(panel("running", "stat", "运行中服务", 2, `SELECT COUNT(*) AS value FROM service_status s WHERE s.state = 'running' AND `+scope), "count", nil, nil),
				hint(with(panel("stopped-auto", "stat", "该启动却停着", 2, `SELECT COUNT(*) AS value FROM service_status s WHERE s.state = 'stopped' AND s.start_type IN ('automatic', 'auto') AND `+scope), "count", number(1), nil), "启动类型为自动"),
				with(panel("total", "stat", "服务总数", 2, `SELECT COUNT(*) AS value FROM service_status s WHERE `+scope), "count", nil, nil),
				with(panel("startup", "stat", "启动项", 2, `SELECT COUNT(*) AS value FROM inventory_current WHERE kind = 'startup' AND `+hosts), "count", nil, nil),
				with(panel("tasks", "stat", "计划任务", 2, `SELECT COUNT(*) AS value FROM inventory_current WHERE kind = 'tasks' AND `+hosts), "count", nil, nil),
				with(panel("changes", "stat", "新增与变化", 2, `SELECT (SELECT COUNT(*) FROM inventory_changes WHERE ts BETWEEN :from AND :to AND `+hosts+`) + (SELECT COUNT(*) FROM service_status s WHERE `+changed+`) AS value`), "count", number(1), nil),
				panel("states", "pie", "服务运行状态", 4, `SELECT CASE s.state WHEN 'running' THEN '运行中' WHEN 'stopped' THEN '已停止' ELSE s.state END AS label, COUNT(*) AS value FROM service_status s WHERE `+scope+` GROUP BY 1 ORDER BY value DESC`),
				panel("start-types", "pie", "启动类型", 4, `SELECT CASE s.start_type WHEN 'automatic' THEN '自动' WHEN 'automatic_delayed' THEN '自动（延迟）' WHEN 'manual' THEN '手动' WHEN 'disabled' THEN '已禁用' ELSE '其他' END AS label, COUNT(*) AS value FROM service_status s WHERE `+scope+` GROUP BY 1 ORDER BY value DESC`),
				panel("attention", "table", "该启动却停着", 4, `SELECT COALESCE(json_extract(s.detail, '$.displayName'), s.name) AS 服务, s.name AS 名称 FROM service_status s WHERE s.state = 'stopped' AND s.start_type IN ('automatic', 'auto') AND `+scope+` ORDER BY 1 LIMIT 50`),
				panel("service-log", "log", "服务状态变化", 6, `SELECT s.ts AS ts, COALESCE(json_extract(s.detail, '$.displayName'), s.name) || ' → ' || s.state AS message, s.start_type AS level FROM service_status s WHERE `+changed+` ORDER BY s.ts DESC LIMIT 80`),
				panel("startup-log", "log", "启动项与计划任务变化", 6, `SELECT ts, name || '（' || change || '）' AS message, kind AS level FROM inventory_changes WHERE ts BETWEEN :from AND :to AND `+hosts+` ORDER BY ts DESC LIMIT 80`),
				panel("startup-list", "table", "当前启动项", 12, `SELECT name AS 名称, scope AS 位置 FROM inventory_current WHERE kind = 'startup' AND `+hosts+` ORDER BY name LIMIT 200`),
			}
		}},
	}
}

// targetFilter narrows a query to one monitor; alias names the table when the query joins.
func targetFilter(target, alias string) string {
	if target == "" {
		return ""
	}
	if alias != "" {
		alias += "."
	}
	return " AND " + alias + "target_id = '" + target + "'"
}

// Templates lists the ready-made pages in display order.
func Templates() []Template {
	entries := catalog()
	result := make([]Template, 0, len(entries))
	for _, item := range entries {
		summary := item.Template
		summary.Targeted = item.ID == "services" || item.ID == "websites"
		for _, p := range item.build("") {
			summary.Layout = append(summary.Layout, config.PanelShape{Type: p.Type, Width: p.Width})
			if !slices.Contains(summary.Panels, p.Type) {
				summary.Panels = append(summary.Panels, p.Type)
			}
		}
		result = append(result, summary)
	}
	return result
}

// Build renders a template into a complete dashboard definition.
// knownTarget reports whether a target ID exists in the current configuration.
func Build(input TemplateInput, knownTarget func(string) bool) (config.Dashboard, error) {
	var chosen *entry
	entries := catalog()
	for index := range entries {
		if entries[index].ID == input.Template {
			chosen = &entries[index]
		}
	}
	if chosen == nil {
		names := make([]string, 0, len(entries))
		for _, item := range entries {
			names = append(names, item.ID)
		}
		return config.Dashboard{}, fmt.Errorf("没有名为 %s 的面板模板，可用模板：%s", input.Template, strings.Join(names, "、"))
	}
	targeted := chosen.ID == "services" || chosen.ID == "websites"
	if input.Target != "" {
		if !targeted {
			return config.Dashboard{}, fmt.Errorf("模板 %s 覆盖整类内容，不接受 target；只看一个监控请用 services 或 websites", chosen.ID)
		}
		if !idPattern.MatchString(input.Target) {
			return config.Dashboard{}, fmt.Errorf("target 必须是监控 ID，先调用 list_monitors 取得")
		}
		if knownTarget != nil && !knownTarget(input.Target) {
			return config.Dashboard{}, fmt.Errorf("监控目标 %s 不存在，先调用 list_monitors 核对 ID，或用 add_monitor 创建", input.Target)
		}
	}
	id := input.ID
	if id == "" {
		id = chosen.ID
		if input.Target != "" {
			id = chosen.ID + "-" + input.Target
		}
	}
	if !idPattern.MatchString(id) {
		return config.Dashboard{}, fmt.Errorf("面板 ID 必须由小写字母、数字或横线组成")
	}
	title := strings.TrimSpace(input.Title)
	if title == "" {
		title = chosen.Title
		if input.Target != "" {
			title = chosen.Title + " · " + input.Target
		}
	}
	theme := input.Theme
	if theme != "" && !slices.Contains(config.DashboardThemes, theme) {
		return config.Dashboard{}, fmt.Errorf("theme 须为 %s 之一，或省略以跟随网页配色", strings.Join(config.DashboardThemes, "、"))
	}
	return config.Dashboard{ID: id, Title: title, Description: chosen.Description, Theme: theme, Icon: chosen.Icon, Refresh: "10s", DefaultRange: "1h", Panels: chosen.build(input.Target)}, nil
}
