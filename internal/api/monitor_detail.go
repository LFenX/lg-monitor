package api

// GetMonitor is the single-service page: its history at two zoom levels, latency and events.
const GetMonitor Operation = "get_monitor"

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
	From   int64    `json:"from" jsonschema_description:"这一格的开始时间，UTC Unix 毫秒。"`
	State  string   `json:"state" jsonschema:"enum=up,enum=down,enum=unknown,enum=" jsonschema_description:"这一格里最差的状态；空字符串表示没有数据。"`
	Uptime *float64 `json:"uptime" jsonschema:"nullable" jsonschema_description:"这一格的正常比例，百分比。"`
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
	Rules         []MonitorRuleDetail `json:"rules"`
}

func monitorDetailRoutes() []Route {
	return []Route{
		{GetMonitor, "GET", "/api/v1/monitors/{id}", "一个服务的详情：24 小时与 30 天状态、可用率、响应时间、子服务、事件和告警规则", ScopeRead, nil, MonitorDetail{}, nil},
	}
}
