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
	ID          string   `json:"id"`
	Title       string   `json:"title"`
	Description string   `json:"description"`
	Theme       string   `json:"theme"`
	Icon        string   `json:"icon"`
	NeedsTarget bool     `json:"needsTarget" jsonschema_description:"为 true 时 create_dashboard 必须提供 target。"`
	Panels      []string `json:"panels" jsonschema_description:"这页包含的卡片类型，便于预览。"`
	Example     string   `json:"example" jsonschema_description:"可以直接对 Agent 说的一句话。"`
}

// TemplateInput selects a template and its few options.
type TemplateInput struct {
	Template string `json:"template" jsonschema:"minLength=1" jsonschema_description:"模板 ID，先调用 list_dashboard_templates。"`
	ID       string `json:"id,omitempty" jsonschema:"pattern=^[a-z0-9-]+$" jsonschema_description:"面板 ID；省略时用模板 ID（带 target 时加上目标 ID）。同 ID 会被覆盖。"`
	Title    string `json:"title,omitempty" jsonschema_description:"显示标题；省略时用模板标题。"`
	Target   string `json:"target,omitempty" jsonschema:"pattern=^[a-z0-9-]+$" jsonschema_description:"监控目标 ID；needsTarget 的模板必填。"`
	Theme    string `json:"theme,omitempty" jsonschema:"enum=aurora,enum=midnight,enum=terminal,enum=sunset,enum=ocean,enum=forest,enum=paper" jsonschema_description:"配色风格；省略时用模板自带风格。"`
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

func catalog() []entry {
	return []entry{
		{Template{ID: "pc-overview", Title: "本机总览", Description: "CPU、内存、磁盘、网络、监控目标与告警，一页看完这台电脑。", Theme: "aurora", Icon: "pulse",
			Example: "用 pc-overview 模板给我做一页本机总览面板"}, func(string) []config.DashboardPanel {
			return []config.DashboardPanel{
				with(panel("cpu", "gauge", "CPU", 3, `SELECT round(cpu, 1) AS value FROM system_metrics WHERE `+hosts+` ORDER BY ts DESC LIMIT 1`), "percent", number(75), number(90)),
				with(panel("memory", "gauge", "内存", 3, `SELECT round(memory_percent, 1) AS value FROM system_metrics WHERE `+hosts+` ORDER BY ts DESC LIMIT 1`), "percent", number(80), number(92)),
				hint(panel("targets", "stat", "正常目标", 3, `SELECT COALESCE(SUM(state = 'up'), 0) || ' / ' || COUNT(*) AS value FROM v_target_current WHERE `+hosts), "正常数 / 目标总数"),
				with(panel("firing", "stat", "触发中告警", 3, `SELECT COUNT(*) AS value FROM v_alerts_active WHERE state = 'firing' AND `+hosts), "count", nil, number(1)),
				with(panel("load", "area", "CPU 与内存", 8, `SELECT `+bucket+` AS ts, 'CPU' AS series, round(avg(cpu), 1) AS value FROM system_metrics WHERE ts BETWEEN :from AND :to AND `+hosts+` GROUP BY 1
					UNION ALL SELECT `+bucket+` AS ts, '内存' AS series, round(avg(memory_percent), 1) AS value FROM system_metrics WHERE ts BETWEEN :from AND :to AND `+hosts+` GROUP BY 1 ORDER BY ts`), "percent", nil, nil),
				with(panel("disks", "toplist", "磁盘使用率", 4, `SELECT d.path AS label, round(d.used_percent, 1) AS value FROM disk_metrics d WHERE d.ts = (SELECT max(n.ts) FROM disk_metrics n WHERE n.collector_id = d.collector_id AND n.path = d.path) AND (:collector IS NULL OR d.collector_id = :collector) ORDER BY value DESC LIMIT 8`), "percent", number(80), number(92)),
				panel("target-grid", "status_grid", "监控目标", 6, `SELECT target_id AS label, CASE WHEN state IN ('up', 'down') THEN state ELSE 'unknown' END AS state FROM v_target_current WHERE `+hosts+` ORDER BY target_id`),
				with(panel("network", "area", "网络流量", 6, `SELECT `+bucket+` AS ts, '发送' AS series, round(sum(send_bytes_per_sec) / max(1, count(DISTINCT ts)), 0) AS value FROM network_metrics WHERE ts BETWEEN :from AND :to AND `+hosts+` GROUP BY 1
					UNION ALL SELECT `+bucket+` AS ts, '接收' AS series, round(sum(receive_bytes_per_sec) / max(1, count(DISTINCT ts)), 0) AS value FROM network_metrics WHERE ts BETWEEN :from AND :to AND `+hosts+` GROUP BY 1 ORDER BY ts`), "bytes_per_sec", nil, nil),
				panel("services", "pie", "Windows 服务状态", 4, `SELECT CASE s.state WHEN 'running' THEN '运行中' WHEN 'stopped' THEN '已停止' ELSE '其他' END AS label, COUNT(*) AS value FROM service_status s WHERE `+latestService+` AND (:collector IS NULL OR s.collector_id = :collector) GROUP BY 1 ORDER BY value DESC`),
				panel("alerts", "alerts", "当前告警", 8, `SELECT summary, severity, state, started_at FROM v_alerts_active WHERE `+hosts+` ORDER BY started_at DESC LIMIT 20`),
			}
		}},
		{Template{ID: "service-health", Title: "服务健康", Description: "一个监控目标的状态时间线、探测耗时、进程 CPU/内存和相关告警。", Theme: "midnight", Icon: "shield", NeedsTarget: true,
			Example: "用 service-health 模板给目标 my-api 做一页面板"}, func(target string) []config.DashboardPanel {
			own := "target_id = '" + target + "' AND " + hosts
			return []config.DashboardPanel{
				panel("state", "status_grid", "当前状态", 3, `SELECT collector_id AS label, CASE WHEN state IN ('up', 'down') THEN state ELSE 'unknown' END AS state FROM v_target_current WHERE `+own),
				with(panel("availability", "gauge", "可用率", 3, `SELECT round(100.0 * SUM(state = 'up') / COUNT(*), 2) AS value FROM target_status WHERE ts BETWEEN :from AND :to AND `+own), "percent", nil, nil),
				with(panel("latency", "trend", "探测耗时", 3, `SELECT `+bucket+` AS ts, round(avg(latency_ms), 1) AS value FROM probe_results WHERE ok = 1 AND ts BETWEEN :from AND :to AND `+own+` GROUP BY 1 ORDER BY 1`), "ms", nil, nil),
				with(panel("firing", "stat", "触发中告警", 3, `SELECT COUNT(*) AS value FROM v_alerts_active WHERE state = 'firing' AND `+own), "count", nil, number(1)),
				panel("timeline", "timeline", "状态时间线", 12, `SELECT ts, collector_id AS label, CASE WHEN state IN ('up', 'down') THEN state ELSE 'unknown' END AS state FROM target_status WHERE ts BETWEEN :from AND :to AND `+own+` ORDER BY ts LIMIT 5000`),
				with(panel("cpu", "area", "进程 CPU", 6, `SELECT `+bucket+` AS ts, round(sum(cpu) / max(1, count(DISTINCT ts)), 2) AS value FROM process_samples WHERE ts BETWEEN :from AND :to AND `+own+` GROUP BY 1 ORDER BY 1`), "percent", nil, nil),
				with(panel("memory", "area", "进程内存", 6, `SELECT `+bucket+` AS ts, round(sum(rss) / max(1, count(DISTINCT ts)), 0) AS value FROM process_samples WHERE ts BETWEEN :from AND :to AND `+own+` GROUP BY 1 ORDER BY 1`), "bytes", nil, nil),
				panel("failures", "log", "最近探测失败", 6, `SELECT ts, COALESCE(NULLIF(error, ''), NULLIF(message, ''), '探测失败') AS message, type AS level FROM probe_results WHERE ok = 0 AND ts BETWEEN :from AND :to AND `+own+` ORDER BY ts DESC LIMIT 50`),
				panel("alerts", "alerts", "告警记录", 6, `SELECT summary, severity, state, started_at FROM alerts WHERE started_at BETWEEN :from AND :to AND `+own+` ORDER BY started_at DESC LIMIT 30`),
			}
		}},
		{Template{ID: "website", Title: "网站可用性", Description: "HTTP 探测成功率、响应耗时、证书剩余天数和失败明细。", Theme: "ocean", Icon: "globe", NeedsTarget: true,
			Example: "用 website 模板给目标 my-blog 做一页面板"}, func(target string) []config.DashboardPanel {
			own := "target_id = '" + target + "' AND " + hosts
			return []config.DashboardPanel{
				with(panel("success", "gauge", "探测成功率", 4, `SELECT round(100.0 * SUM(ok) / COUNT(*), 2) AS value FROM probe_results WHERE ts BETWEEN :from AND :to AND `+own), "percent", nil, nil),
				with(panel("latency", "trend", "平均耗时", 4, `SELECT `+bucket+` AS ts, round(avg(latency_ms), 1) AS value FROM probe_results WHERE ok = 1 AND ts BETWEEN :from AND :to AND `+own+` GROUP BY 1 ORDER BY 1`), "ms", number(800), number(2000)),
				hint(panel("certificate", "stat", "证书剩余天数", 4, `SELECT COALESCE(CAST(MIN((tls_not_after - :to) / 86400000) AS INTEGER) || ' 天', '无 HTTPS') AS value FROM v_probe_certificates WHERE `+own), "最早到期的非根证书"),
				with(panel("latency-chart", "area", "响应耗时", 8, `SELECT `+bucket+` AS ts, '探测 ' || (probe + 1) AS series, round(avg(latency_ms), 1) AS value FROM probe_results WHERE ok = 1 AND ts BETWEEN :from AND :to AND `+own+` GROUP BY 1, probe ORDER BY 1`), "ms", nil, nil),
				panel("result", "pie", "探测结果", 4, `SELECT CASE ok WHEN 1 THEN '成功' ELSE '失败' END AS label, COUNT(*) AS value FROM probe_results WHERE ts BETWEEN :from AND :to AND `+own+` GROUP BY 1`),
				panel("timeline", "timeline", "可用性时间线", 12, `SELECT ts, '探测 ' || (probe + 1) AS label, CASE ok WHEN 1 THEN 'up' ELSE 'down' END AS state FROM probe_results WHERE ts BETWEEN :from AND :to AND `+own+` ORDER BY ts LIMIT 5000`),
				panel("failures", "log", "失败明细", 12, `SELECT ts, COALESCE(NULLIF(error, ''), NULLIF(message, ''), '探测失败') AS message, type AS level FROM probe_results WHERE ok = 0 AND ts BETWEEN :from AND :to AND `+own+` ORDER BY ts DESC LIMIT 100`),
			}
		}},
		{Template{ID: "alert-center", Title: "告警中心", Description: "触发中的告警、按严重度与规则的分布、最近告警与通知投递。", Theme: "sunset", Icon: "bell",
			Example: "用 alert-center 模板做一页告警面板"}, func(string) []config.DashboardPanel {
			return []config.DashboardPanel{
				with(panel("firing", "stat", "触发中", 3, `SELECT COUNT(*) AS value FROM v_alerts_active WHERE state = 'firing' AND `+hosts), "count", nil, number(1)),
				with(panel("critical", "stat", "严重", 3, `SELECT COUNT(*) AS value FROM v_alerts_active WHERE state = 'firing' AND severity = 'critical' AND `+hosts), "count", nil, number(1)),
				with(panel("opened", "stat", "时间范围内新增", 3, `SELECT COUNT(*) AS value FROM alerts WHERE started_at BETWEEN :from AND :to AND `+hosts), "count", nil, nil),
				with(panel("resolved", "stat", "时间范围内恢复", 3, `SELECT COUNT(*) AS value FROM alerts WHERE resolved_at BETWEEN :from AND :to AND `+hosts), "count", nil, nil),
				panel("severity", "pie", "按严重度", 4, `SELECT CASE severity WHEN 'critical' THEN '严重' WHEN 'warning' THEN '警告' ELSE '提示' END AS label, COUNT(*) AS value FROM alerts WHERE started_at BETWEEN :from AND :to AND `+hosts+` GROUP BY 1 ORDER BY value DESC`),
				with(panel("rules", "toplist", "最常触发的规则", 4, `SELECT rule_id AS label, COUNT(*) AS value FROM alerts WHERE started_at BETWEEN :from AND :to AND `+hosts+` GROUP BY 1 ORDER BY value DESC LIMIT 8`), "count", nil, nil),
				with(panel("targets", "toplist", "最常告警的目标", 4, `SELECT COALESCE(NULLIF(target_id, ''), '(主机)') AS label, COUNT(*) AS value FROM alerts WHERE started_at BETWEEN :from AND :to AND `+hosts+` GROUP BY 1 ORDER BY value DESC LIMIT 8`), "count", nil, nil),
				panel("active", "alerts", "当前告警", 7, `SELECT summary, severity, state, started_at FROM v_alerts_active WHERE `+hosts+` ORDER BY started_at DESC LIMIT 30`),
				panel("history", "log", "最近告警事件", 5, `SELECT e.ts AS ts, e.message AS message, e.kind AS level FROM alert_events e JOIN alerts a ON a.id = e.alert_id WHERE e.ts BETWEEN :from AND :to AND (:collector IS NULL OR a.collector_id = :collector) ORDER BY e.ts DESC LIMIT 60`),
			}
		}},
		{Template{ID: "windows-services", Title: "Windows 服务", Description: "服务运行分布、应当自动启动却停着的服务、最近的状态变化。", Theme: "forest", Icon: "layers",
			Example: "用 windows-services 模板做一页 Windows 服务面板"}, func(string) []config.DashboardPanel {
			scope := latestService + " AND (:collector IS NULL OR s.collector_id = :collector)"
			return []config.DashboardPanel{
				with(panel("running", "stat", "运行中", 3, `SELECT COUNT(*) AS value FROM service_status s WHERE s.state = 'running' AND `+scope), "count", nil, nil),
				with(panel("stopped-auto", "stat", "自动启动但已停止", 3, `SELECT COUNT(*) AS value FROM service_status s WHERE s.state = 'stopped' AND s.start_type IN ('automatic', 'auto') AND `+scope), "count", number(1), nil),
				with(panel("total", "stat", "服务总数", 3, `SELECT COUNT(*) AS value FROM service_status s WHERE `+scope), "count", nil, nil),
				with(panel("changes", "stat", "时间范围内状态变化", 3, `SELECT COUNT(*) AS value FROM service_status s WHERE s.ts BETWEEN :from AND :to AND EXISTS (SELECT 1 FROM service_status p WHERE p.collector_id = s.collector_id AND p.name = s.name AND p.ts < s.ts) AND (:collector IS NULL OR s.collector_id = :collector)`), "count", nil, nil),
				panel("states", "pie", "运行状态", 4, `SELECT CASE s.state WHEN 'running' THEN '运行中' WHEN 'stopped' THEN '已停止' ELSE s.state END AS label, COUNT(*) AS value FROM service_status s WHERE `+scope+` GROUP BY 1 ORDER BY value DESC`),
				panel("start-types", "pie", "启动类型", 4, `SELECT CASE s.start_type WHEN 'automatic' THEN '自动' WHEN 'automatic_delayed' THEN '自动（延迟）' WHEN 'manual' THEN '手动' WHEN 'disabled' THEN '已禁用' ELSE '其他' END AS label, COUNT(*) AS value FROM service_status s WHERE `+scope+` GROUP BY 1 ORDER BY value DESC`),
				panel("attention", "table", "自动启动但已停止", 4, `SELECT COALESCE(json_extract(s.detail, '$.displayName'), s.name) AS 服务, s.name AS 名称 FROM service_status s WHERE s.state = 'stopped' AND s.start_type IN ('automatic', 'auto') AND `+scope+` ORDER BY 1 LIMIT 50`),
				panel("recent", "log", "最近状态变化", 12, `SELECT s.ts AS ts, COALESCE(json_extract(s.detail, '$.displayName'), s.name) || ' → ' || s.state AS message, s.start_type AS level FROM service_status s WHERE s.ts BETWEEN :from AND :to AND EXISTS (SELECT 1 FROM service_status p WHERE p.collector_id = s.collector_id AND p.name = s.name AND p.ts < s.ts) AND (:collector IS NULL OR s.collector_id = :collector) ORDER BY s.ts DESC LIMIT 80`),
			}
		}},
		{Template{ID: "host-resources", Title: "主机资源", Description: "CPU、内存、磁盘与每块网卡的趋势，适合排查“电脑为什么慢”。", Theme: "terminal", Icon: "cpu",
			Example: "用 host-resources 模板做一页主机资源面板"}, func(string) []config.DashboardPanel {
			return []config.DashboardPanel{
				with(panel("cpu-now", "trend", "CPU", 4, `SELECT `+bucket+` AS ts, round(avg(cpu), 1) AS value FROM system_metrics WHERE ts BETWEEN :from AND :to AND `+hosts+` GROUP BY 1 ORDER BY 1`), "percent", number(75), number(90)),
				with(panel("memory-now", "trend", "内存", 4, `SELECT `+bucket+` AS ts, round(avg(memory_percent), 1) AS value FROM system_metrics WHERE ts BETWEEN :from AND :to AND `+hosts+` GROUP BY 1 ORDER BY 1`), "percent", number(80), number(92)),
				with(panel("uptime", "stat", "已运行", 4, `SELECT uptime AS value FROM system_metrics WHERE `+hosts+` ORDER BY ts DESC LIMIT 1`), "seconds", nil, nil),
				with(panel("cpu", "line", "CPU 使用率（各主机）", 6, `SELECT `+bucket+` AS ts, collector_id AS series, round(avg(cpu), 1) AS value FROM system_metrics WHERE ts BETWEEN :from AND :to AND `+hosts+` GROUP BY 1, 2 ORDER BY 1`), "percent", nil, nil),
				with(panel("memory", "area", "内存使用量", 6, `SELECT `+bucket+` AS ts, round(avg(memory_used), 0) AS value FROM system_metrics WHERE ts BETWEEN :from AND :to AND `+hosts+` GROUP BY 1 ORDER BY 1`), "bytes", nil, nil),
				with(panel("disks", "toplist", "磁盘使用率", 4, `SELECT d.path AS label, round(d.used_percent, 1) AS value FROM disk_metrics d WHERE d.ts = (SELECT max(n.ts) FROM disk_metrics n WHERE n.collector_id = d.collector_id AND n.path = d.path) AND (:collector IS NULL OR d.collector_id = :collector) ORDER BY value DESC LIMIT 8`), "percent", number(80), number(92)),
				with(panel("disk-free", "bar", "磁盘剩余空间", 4, `SELECT d.path AS label, d.free AS value FROM disk_metrics d WHERE d.ts = (SELECT max(n.ts) FROM disk_metrics n WHERE n.collector_id = d.collector_id AND n.path = d.path) AND (:collector IS NULL OR d.collector_id = :collector) ORDER BY d.path LIMIT 8`), "bytes", nil, nil),
				with(panel("nics", "toplist", "网卡流量（收+发）", 4, `SELECT name AS label, round(avg(send_bytes_per_sec + receive_bytes_per_sec), 0) AS value FROM network_metrics WHERE ts BETWEEN :from AND :to AND `+hosts+` GROUP BY 1 HAVING value > 0 ORDER BY value DESC LIMIT 6`), "bytes_per_sec", nil, nil),
				with(panel("network", "line", "网络收发", 12, `SELECT `+bucket+` AS ts, '发送' AS series, round(sum(send_bytes_per_sec) / max(1, count(DISTINCT ts)), 0) AS value FROM network_metrics WHERE ts BETWEEN :from AND :to AND `+hosts+` GROUP BY 1
					UNION ALL SELECT `+bucket+` AS ts, '接收' AS series, round(sum(receive_bytes_per_sec) / max(1, count(DISTINCT ts)), 0) AS value FROM network_metrics WHERE ts BETWEEN :from AND :to AND `+hosts+` GROUP BY 1 ORDER BY ts`), "bytes_per_sec", nil, nil),
			}
		}},
		{Template{ID: "startup-watch", Title: "启动项与计划任务", Description: "开机启动项、计划任务的数量与最近的增删改，留意不认识的新条目。", Theme: "paper", Icon: "rocket",
			Example: "用 startup-watch 模板做一页启动项面板"}, func(string) []config.DashboardPanel {
			return []config.DashboardPanel{
				with(panel("startup", "stat", "启动项", 4, `SELECT COUNT(*) AS value FROM inventory_current WHERE kind = 'startup' AND `+hosts), "count", nil, nil),
				with(panel("tasks", "stat", "计划任务", 4, `SELECT COUNT(*) AS value FROM inventory_current WHERE kind = 'tasks' AND `+hosts), "count", nil, nil),
				with(panel("changes", "stat", "时间范围内变化", 4, `SELECT COUNT(*) AS value FROM inventory_changes WHERE ts BETWEEN :from AND :to AND `+hosts), "count", number(1), nil),
				panel("change-kinds", "pie", "变化类型", 4, `SELECT CASE change WHEN 'added' THEN '新增' WHEN 'removed' THEN '删除' WHEN 'changed' THEN '修改' ELSE change END AS label, COUNT(*) AS value FROM inventory_changes WHERE ts BETWEEN :from AND :to AND `+hosts+` GROUP BY 1 ORDER BY value DESC`),
				panel("change-log", "log", "最近变化", 8, `SELECT ts, name || '（' || change || '）' AS message, kind AS level FROM inventory_changes WHERE ts BETWEEN :from AND :to AND `+hosts+` ORDER BY ts DESC LIMIT 80`),
				panel("startup-list", "table", "当前启动项", 12, `SELECT name AS 名称, scope AS 位置 FROM inventory_current WHERE kind = 'startup' AND `+hosts+` ORDER BY name LIMIT 200`),
			}
		}},
	}
}

// Templates lists the ready-made pages in display order.
func Templates() []Template {
	entries := catalog()
	result := make([]Template, 0, len(entries))
	for _, item := range entries {
		summary := item.Template
		for _, p := range item.build("example") {
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
	if chosen.NeedsTarget {
		if !idPattern.MatchString(input.Target) {
			return config.Dashboard{}, fmt.Errorf("模板 %s 需要 target（监控目标 ID），先调用 list_monitors 取得", chosen.ID)
		}
		if knownTarget != nil && !knownTarget(input.Target) {
			return config.Dashboard{}, fmt.Errorf("监控目标 %s 不存在，先调用 list_monitors 核对 ID，或用 add_monitor 创建", input.Target)
		}
	} else if input.Target != "" {
		return config.Dashboard{}, fmt.Errorf("模板 %s 不接受 target，请去掉这个参数", chosen.ID)
	}
	id := input.ID
	if id == "" {
		id = chosen.ID
		if chosen.NeedsTarget {
			id = input.Target
		}
	}
	if !idPattern.MatchString(id) {
		return config.Dashboard{}, fmt.Errorf("面板 ID 必须由小写字母、数字或横线组成")
	}
	title := strings.TrimSpace(input.Title)
	if title == "" {
		title = chosen.Title
		if chosen.NeedsTarget {
			title = chosen.Title + " · " + input.Target
		}
	}
	theme := input.Theme
	if theme == "" {
		theme = chosen.Theme
	}
	if !slices.Contains(config.DashboardThemes, theme) {
		return config.Dashboard{}, fmt.Errorf("theme 须为 %s 之一", strings.Join(config.DashboardThemes, "、"))
	}
	return config.Dashboard{ID: id, Title: title, Description: chosen.Description, Theme: theme, Icon: chosen.Icon, Refresh: "10s", DefaultRange: "1h", Panels: chosen.build(input.Target)}, nil
}
