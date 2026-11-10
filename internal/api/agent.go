package api

import (
	"github.com/LFenX/lg-monitor/internal/blueprint"
	"github.com/LFenX/lg-monitor/internal/config"
)

// Intent-level operations: each one completes a whole task in a single call,
// so an agent does not have to assemble targets, rules and pages by hand.
const (
	GetGuide               Operation = "get_guide"
	Explore                Operation = "explore"
	ListMonitors           Operation = "list_monitors"
	AddMonitor             Operation = "add_monitor"
	RemoveMonitor          Operation = "remove_monitor"
	ListDashboardTemplates Operation = "list_dashboard_templates"
	CreateDashboard        Operation = "create_dashboard"
	PutDashboard           Operation = "put_dashboard"
	DeleteDashboard        Operation = "delete_dashboard"
	GetAssistant           Operation = "get_assistant"
	ConfigureAssistant     Operation = "configure_assistant"
	AssistantChat          Operation = "assistant_chat"
)

type GuideStep struct {
	ID    string `json:"id"`
	Title string `json:"title"`
	Done  bool   `json:"done"`
	How   string `json:"how" jsonschema_description:"完成这一步的具体做法；Agent 可直接照做。"`
	Tool  string `json:"tool,omitempty" jsonschema_description:"完成这一步的 MCP 工具。"`
}

type GuideInstall struct {
	Executable string `json:"executable" jsonschema_description:"监控器程序的绝对路径。"`
	Home       string `json:"home" jsonschema_description:"监控器实际使用的数据目录；命令行的 --home 必须与它一致。"`
	Personal   bool   `json:"personal" jsonschema_description:"为 true 表示以普通用户身份运行，命令不需要管理员终端。"`
	URL        string `json:"url" jsonschema_description:"网页与 HTTP API 的基础地址。"`
}

type GuideState struct {
	Collectors   int    `json:"collectors"`
	Online       int    `json:"online"`
	Monitors     int    `json:"monitors"`
	Healthy      int    `json:"healthy"`
	FiringAlerts int    `json:"firingAlerts"`
	Dashboards   int    `json:"dashboards"`
	Notifiers    int    `json:"notifiers"`
	AgentSeenAt  int64  `json:"agentSeenAt" jsonschema_description:"最近一次 MCP 调用的 UTC Unix 毫秒；0 表示本次运行还没有 Agent 连接过。"`
	AgentName    string `json:"agentName,omitempty"`
}

type GuideCollector struct {
	ID       string `json:"id"`
	Hostname string `json:"hostname"`
	Online   bool   `json:"online"`
}

// Guide tells a newly connected agent or the web page where things stand and what to do next.
type Guide struct {
	Product      string           `json:"product"`
	Version      string           `json:"version"`
	Scope        Scope            `json:"scope" jsonschema_description:"当前令牌的权限：read 只能看，config 可以添加监控和面板。"`
	CanConfigure bool             `json:"canConfigure"`
	Install      GuideInstall     `json:"install"`
	State        GuideState       `json:"state"`
	Collectors   []GuideCollector `json:"collectors"`
	Steps        []GuideStep      `json:"steps"`
	Workflow     []string         `json:"workflow" jsonschema_description:"推荐的工具调用顺序。"`
	Rules        []string         `json:"rules" jsonschema_description:"必须遵守的边界。"`
}

type ExploreSuggestion struct {
	Name    string `json:"name"`
	Port    *int   `json:"port,omitempty"`
	Process string `json:"process,omitempty"`
	Service string `json:"service,omitempty"`
}

type ExploreItem struct {
	Kind        string             `json:"kind" jsonschema:"enum=port,enum=service,enum=process"`
	Name        string             `json:"name"`
	DisplayName string             `json:"displayName,omitempty"`
	State       string             `json:"state,omitempty"`
	StartType   string             `json:"startType,omitempty"`
	PID         int32              `json:"pid,omitempty"`
	Port        int                `json:"port,omitempty"`
	Address     string             `json:"address,omitempty"`
	Process     string             `json:"process,omitempty"`
	Executable  string             `json:"executable,omitempty"`
	CPUPercent  float64            `json:"cpuPercent,omitempty"`
	RSS         uint64             `json:"rss,omitempty"`
	Services    []string           `json:"services,omitempty"`
	MonitoredBy string             `json:"monitoredBy,omitempty" jsonschema_description:"已经监控它的目标 ID；为空表示尚未监控。"`
	Suggest     *ExploreSuggestion `json:"suggest,omitempty" jsonschema_description:"可以原样传给 add_monitor 的参数。"`
}

type ExploreResult struct {
	Collector   GuideCollector `json:"collector"`
	Kind        string         `json:"kind"`
	CollectedAt int64          `json:"collectedAt"`
	Total       int            `json:"total" jsonschema_description:"筛选前的总数。"`
	Matched     int            `json:"matched" jsonschema_description:"符合 q/state 的数量；items 只是其中一页。"`
	Counts      map[string]int `json:"counts"`
	Items       []ExploreItem  `json:"items"`
	Offset      int            `json:"offset"`
	Limit       int            `json:"limit"`
	Notes       []string       `json:"notes,omitempty"`
}

type MonitorHost struct {
	CollectorID string `json:"collectorId"`
	Hostname    string `json:"hostname"`
	State       string `json:"state"`
	Reason      string `json:"reason,omitempty"`
	TS          int64  `json:"ts"`
}

type MonitorRule struct {
	ID       string `json:"id"`
	Type     string `json:"type"`
	Severity string `json:"severity"`
}

type Monitor struct {
	ID        string        `json:"id"`
	Name      string        `json:"name"`
	Collector string        `json:"collector,omitempty"`
	Watching  []string      `json:"watching" jsonschema_description:"用人话描述监控的对象。"`
	State     string        `json:"state" jsonschema:"enum=up,enum=down,enum=unknown"`
	Hosts     []MonitorHost `json:"hosts"`
	Rules     []MonitorRule `json:"rules"`
	Firing    int           `json:"firing"`
	Beats     []string      `json:"beats" jsonschema_description:"最近 24 小时每 30 分钟一格的最差状态：up、down、unknown，空字符串表示没有数据；最后一格是现在。"`
	Uptime    *float64      `json:"uptime" jsonschema:"nullable" jsonschema_description:"最近 24 小时的正常比例，百分比。"`
	LatencyMS *float64      `json:"latencyMs" jsonschema:"nullable" jsonschema_description:"最近 5 分钟成功探测的平均耗时。"`
	Kind      string        `json:"kind" jsonschema:"enum=port,enum=url,enum=service,enum=process,enum=other"`
	Dashboard string        `json:"dashboard,omitempty"`
	URL       string        `json:"url,omitempty"`
}

type MonitorResult struct {
	Monitor         Monitor  `json:"monitor"`
	Created         bool     `json:"created" jsonschema_description:"false 表示更新了已有监控。"`
	NotifierCreated string   `json:"notifierCreated,omitempty" jsonschema_description:"没有任何通知渠道时自动创建的本机通知渠道 ID。"`
	Dashboard       string   `json:"dashboard,omitempty" jsonschema_description:"这个监控所属的分类面板 ID。"`
	DashboardURL    string   `json:"dashboardUrl,omitempty"`
	MonitorURL      string   `json:"monitorUrl" jsonschema_description:"监控详情页，交给用户查看这一个监控。"`
	Next            []string `json:"next" jsonschema_description:"建议接着做的事，可直接转述给用户。"`
}

type DashboardResult struct {
	ID      string `json:"id"`
	Title   string `json:"title"`
	Created bool   `json:"created"`
	Panels  int    `json:"panels"`
	URL     string `json:"url" jsonschema_description:"把这个地址交给用户，在浏览器打开查看。"`
}

type AssistantStatus struct {
	Configured   bool                       `json:"configured"`
	Provider     string                     `json:"provider"`
	ProviderName string                     `json:"providerName"`
	Model        string                     `json:"model"`
	BaseURL      string                     `json:"baseUrl"`
	Problem      string                     `json:"problem,omitempty" jsonschema_description:"尚不可用的原因。"`
	Setup        string                     `json:"setup,omitempty" jsonschema_description:"尚未配置时，在监控器电脑上运行的命令。"`
	Providers    []config.AssistantProvider `json:"providers"`
}

type AssistantConfigInput struct {
	Provider string `json:"provider" jsonschema:"minLength=1"`
	Model    string `json:"model,omitempty" jsonschema:"maxLength=200"`
	BaseURL  string `json:"baseUrl,omitempty" jsonschema:"maxLength=2048"`
	APIKey   string `json:"apiKey,omitempty" jsonschema:"writeOnly=true,maxLength=8192" jsonschema_description:"省略时沿用已保存的密钥。"`
}
type AssistantMessage struct {
	Role string `json:"role" jsonschema:"enum=user,enum=assistant"`
	Text string `json:"text" jsonschema:"maxLength=8000"`
}

type AssistantInput struct {
	Messages []AssistantMessage `json:"messages" jsonschema:"minItems=1,maxItems=40"`
}

func agentRoutes() []Route {
	return []Route{
		{GetGuide, "GET", "/api/v1/guide", "当前安装、状态和下一步；Agent 接入后先读它", ScopeRead, nil, Guide{}, nil},
		{Explore, "GET", "/api/v1/explore", "搜索这台电脑上的监听端口、Windows 服务和进程，并给出可直接添加监控的参数", ScopeRead, nil, ExploreResult{}, []Parameter{{"collector", "采集器 ID；只有一台电脑时省略"}, {"kind", "ports、services 或 processes；默认 ports"}, {"q", "名称关键词，不区分大小写"}, {"state", "仅 services：running、stopped 或 attention（自动启动但已停止）"}, {"limit", "每页条数，1～200，默认 50"}, {"offset", "跳过的条数，默认 0"}}},
		{ListMonitors, "GET", "/api/v1/monitors", "全部监控及其当前状态、规则、告警数和面板", ScopeRead, nil, []Monitor{}, nil},
		{AddMonitor, "POST", "/api/v1/monitors", "一次调用创建或更新监控：目标、探测、告警规则、通知渠道和面板", ScopeConfig, blueprint.MonitorInput{}, MonitorResult{}, nil},
		{RemoveMonitor, "DELETE", "/api/v1/monitors/{id}", "删除监控目标及其规则，以及只看它的面板", ScopeConfig, nil, Confirmation{}, nil},
		{ListDashboardTemplates, "GET", "/api/v1/dashboard-templates", "可直接创建的面板模板", ScopeRead, nil, []blueprint.Template{}, nil},
		{CreateDashboard, "POST", "/api/v1/dashboards", "按模板创建面板", ScopeConfig, blueprint.TemplateInput{}, DashboardResult{}, nil},
		{PutDashboard, "PUT", "/api/v1/dashboards/{id}", "校验并保存完整的声明式面板定义", ScopeConfig, config.Dashboard{}, DashboardResult{}, nil},
		{DeleteDashboard, "DELETE", "/api/v1/dashboards/{id}", "删除声明式面板", ScopeConfig, nil, Confirmation{}, nil},
		{GetAssistant, "GET", "/api/v1/assistant", "面板内 AI 助手的供应商、模型和是否可用", ScopeRead, nil, AssistantStatus{}, nil},
		{ConfigureAssistant, "PUT", "/api/v1/assistant", "先用一次简短请求验证，再保存助手的供应商、模型、地址和 API Key", ScopeAdmin, AssistantConfigInput{}, AssistantStatus{}, nil},
		{AssistantChat, "POST", "/api/v1/assistant/chat", "与 AI 助手对话；SSE 返回 text、tool、done 或 error 事件，助手只能读取监控数据", ScopeRead, AssistantInput{}, "", nil},
	}
}
