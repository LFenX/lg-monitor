// Copyright (c) 2026 LFen (https://lfen.ink) — lg-monitor

package api

// GetMonitor is the single-service page: its history at two zoom levels, latency and events.
const GetMonitor Operation = "get_monitor"

// A service's document is Markdown kept beside the configuration; empty means it has none.
const (
	GetMonitorDoc Operation = "get_monitor_doc"
	PutMonitorDoc Operation = "put_monitor_doc"
)

// MonitorDoc is one service's Markdown document.
type MonitorDoc struct {
	ID       string `json:"id"`
	Markdown string `json:"markdown" jsonschema_description:"文档全文（Markdown）；空字符串表示还没有文档。"`
}

// MonitorDocInput replaces a service's document.
type MonitorDocInput struct {
	Markdown string `json:"markdown" jsonschema:"maxLength=262144" jsonschema_description:"整篇文档（Markdown，最多 256 KiB），会整个替换原文档；空字符串表示删除文档。可以写部署位置、启动方式、依赖说明、排障步骤。[[服务ID]] 会显示成带状态的服务链接；代码块语言写 deps 会画出依赖关系图（块里不写内容画这个服务的上下游，写几个服务 ID 则画它们之间的关系）。"`
}

type MonitorRef struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Overall string `json:"overall"`
}

type BoardRef struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type StatusSlot struct {
	From     int64           `json:"from" jsonschema_description:"这一格的开始时间，UTC Unix 毫秒。"`
	State    string          `json:"state" jsonschema:"enum=up,enum=down,enum=unknown,enum=" jsonschema_description:"这一格里最差的状态；空字符串表示没有数据。"`
	Uptime   *float64        `json:"uptime" jsonschema:"nullable" jsonschema_description:"这一格的正常比例，百分比。"`
	Segments []StatusSegment `json:"segments,omitempty" jsonschema_description:"仍保留原始采样时，按实际观测时间定位的状态区间；不含未采集或已过期的时间。同一天里原始采样已过期、只剩小时汇总的时段，用 summary 区间按整点补上。"`
}

type StatusSegment struct {
	From    int64    `json:"from"`
	To      int64    `json:"to"`
	State   string   `json:"state" jsonschema:"enum=up,enum=down,enum=unknown"`
	Summary bool     `json:"summary,omitempty" jsonschema_description:"true 表示这段只剩小时汇总：范围按整点，异常在小时内的具体时点未保留。"`
	Uptime  *float64 `json:"uptime,omitempty" jsonschema_description:"summary 区间的正常比例，百分比。"`
}

type LatencyPoint struct {
	TS int64   `json:"ts"`
	MS float64 `json:"ms"`
}

type MonitorEvent struct {
	TS   int64  `json:"ts"`
	Kind string `json:"kind" jsonschema:"enum=up,enum=down,enum=unknown,enum=alert,enum=resolved"`
	Text string `json:"text"`
	Host string `json:"host,omitempty"`
}

// MonitorIncident is one stretch of trouble: a confirmed outage, or an alert that fired on its own.
type MonitorIncident struct {
	MonitorID   string          `json:"monitorId"`
	MonitorName string          `json:"monitorName"`
	Depth       int             `json:"depth" jsonschema_description:"相对这个服务的层级，0 是它自己。"`
	Kind        string          `json:"kind" jsonschema:"enum=down,enum=alert" jsonschema_description:"down 是异常时段；alert 是不伴随异常时段的告警（例如证书快到期）。"`
	From        int64           `json:"from"`
	To          int64           `json:"to" jsonschema_description:"结束时间；0 表示仍在持续。"`
	DurationMS  int64           `json:"durationMs" jsonschema_description:"持续时长；仍在持续时算到现在。"`
	Reason      string          `json:"reason,omitempty" jsonschema_description:"开始时记录的原因，或告警摘要。"`
	Host        string          `json:"host,omitempty"`
	Alerts      []IncidentAlert `json:"alerts" jsonschema_description:"这段时间里触发的告警。"`
}

type IncidentAlert struct {
	StartedAt  int64  `json:"startedAt"`
	ResolvedAt int64  `json:"resolvedAt,omitempty"`
	Severity   string `json:"severity"`
	Summary    string `json:"summary"`
}

type MonitorRuleDetail struct {
	ID       string   `json:"id"`
	Type     string   `json:"type"`
	Severity string   `json:"severity"`
	Text     string   `json:"text" jsonschema_description:"什么情况下告警，用人话说。"`
	Notify   []string `json:"notify" jsonschema_description:"通知到哪里。"`
}

type MonitorDetail struct {
	Monitor       Monitor             `json:"monitor"`
	Lineage       []MonitorRef        `json:"lineage" jsonschema_description:"从最上层主服务到直接父服务。"`
	Children      []Monitor           `json:"children" jsonschema_description:"全部子服务，按显示顺序，depth 相对这个服务。"`
	Boards        []BoardRef          `json:"boards" jsonschema_description:"显示这个服务的面板，含随父服务显示的。"`
	Slots         []StatusSlot        `json:"slots" jsonschema_description:"最近 24 小时，96 格，每格 15 分钟，最后一格是现在。"`
	Days          []StatusSlot        `json:"days" jsonschema_description:"最近 30 天，每天一格（监控器本地零点对齐），最后一格是今天。"`
	Uptime24h     *float64            `json:"uptime24h" jsonschema:"nullable"`
	Uptime7d      *float64            `json:"uptime7d" jsonschema:"nullable"`
	Uptime30d     *float64            `json:"uptime30d" jsonschema:"nullable"`
	Latency       []LatencyPoint      `json:"latency" jsonschema_description:"最近 24 小时成功探测的平均耗时，每 15 分钟一点；没有探测时为空。"`
	LatencyNow    *float64            `json:"latencyNow" jsonschema:"nullable"`
	LatencyAvg    *float64            `json:"latencyAvg" jsonschema:"nullable" jsonschema_description:"最近 24 小时成功探测的平均耗时。"`
	CertExpiresAt int64               `json:"certExpiresAt,omitempty" jsonschema_description:"HTTPS 证书最早到期时间，UTC Unix 毫秒。"`
	Events        []MonitorEvent      `json:"events" jsonschema_description:"最近的状态变化和告警，新的在前，最多 50 条。"`
	Incidents     []MonitorIncident   `json:"incidents" jsonschema_description:"这个服务和全部子服务最近 7 天的异常时段（恢复后只算持续满 1 分钟的，正在持续的立即出现）及单独的告警，新的在前，最多 200 条。"`
	Rules         []MonitorRuleDetail `json:"rules"`
	HistoryNotice string              `json:"historyNotice,omitempty"`
}

func monitorDetailRoutes() []Route {
	return []Route{
		{GetMonitor, "GET", "/api/v1/monitors/{id}", "一个服务的详情：24 小时与 30 天状态、可用率、响应时间、子服务、事件和告警规则", ScopeRead, nil, MonitorDetail{}, nil},
		{GetMonitorDoc, "GET", "/api/v1/monitors/{id}/doc", "一个服务的 Markdown 文档；没有文档时 markdown 为空", ScopeRead, nil, MonitorDoc{}, nil},
		{PutMonitorDoc, "PUT", "/api/v1/monitors/{id}/doc", "整个替换一个服务的 Markdown 文档；空字符串删除文档", ScopeConfig, MonitorDocInput{}, MonitorDoc{}, nil},
	}
}
