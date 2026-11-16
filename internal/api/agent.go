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
	EditMonitor            Operation = "edit_monitor"
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
	Dashboards   int    `json:"dashboards" jsonschema_description:"自定义视图（声明式卡片页）的数量。"`
	Boards       int    `json:"boards" jsonschema_description:"面板数量。"`
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
	Modules      []MCPModule      `json:"modules" jsonschema_description:"全部工具按模块分组：每个模块做什么、什么时候用、有哪些工具。"`
}

type ExploreSuggestion struct {
	Name    string `json:"name"`
	Port    *int   `json:"port,omitempty"`
	Process string `json:"process,omitempty"`
	Service string `json:"service,omitempty"`
	Task    string `json:"task,omitempty"`
}

type ExploreItem struct {
	Kind        string             `json:"kind" jsonschema:"enum=port,enum=service,enum=process,enum=task"`
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
	Task        string             `json:"task,omitempty" jsonschema_description:"计划任务的完整路径（小写）。"`
	LastRun     int64              `json:"lastRun,omitempty" jsonschema_description:"计划任务上次运行时间，UTC Unix 毫秒。"`
	Result      string             `json:"result,omitempty" jsonschema_description:"计划任务上次运行结果的人话说明。"`
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
	BeatSlots []StatusSlot  `json:"beatSlots,omitempty"`
	Uptime    *float64      `json:"uptime" jsonschema:"nullable" jsonschema_description:"最近 24 小时的正常比例，百分比。"`
	LatencyMS *float64      `json:"latencyMs" jsonschema:"nullable" jsonschema_description:"最近 5 分钟成功探测的平均耗时。"`
	Kind      string        `json:"kind" jsonschema:"enum=port,enum=url,enum=service,enum=process,enum=task,enum=other"`
	Dashboard string        `json:"dashboard,omitempty"`
	URL       string        `json:"url,omitempty"`
	Parent    string        `json:"parent,omitempty" jsonschema_description:"主服务的 ID；为空表示自己是主服务。"`
	Children  []string      `json:"children" jsonschema_description:"直接子服务的 ID。"`
	Depth     int           `json:"depth" jsonschema_description:"在面板服务树里的层级，主服务为 0。"`
	Boards    []string      `json:"boards" jsonschema_description:"直接列在哪些面板上；子服务随父服务显示时为空。"`
	Overall   string        `json:"overall" jsonschema:"enum=up,enum=down,enum=unknown" jsonschema_description:"自身和全部子服务里最差的状态，面板上显示的是它；state 只是自身。"`
	Issue     string        `json:"issue,omitempty" jsonschema_description:"整体不正常时的人话原因，例如“子服务 Tunnel 异常”。"`
	// Group figures cover the service and every descendant, so a failing part shows on the parent's row.
	GroupBeats     []string     `json:"groupBeats,omitempty" jsonschema_description:"有子服务时：整组每格取最差状态的心跳。"`
	GroupBeatSlots []StatusSlot `json:"groupBeatSlots,omitempty"`
	GroupUptime    *float64     `json:"groupUptime,omitempty" jsonschema_description:"有子服务时：整组里最低的 24 小时可用率。"`
	MonitorURL     string       `json:"monitorUrl" jsonschema_description:"服务详情页地址。"`
	// Card figures only come with board views, for services whose card style shows them.
	Uptime7d  *float64       `json:"uptime7d,omitempty" jsonschema_description:"卡片用：7 天可用率。"`
	Uptime30d *float64       `json:"uptime30d,omitempty" jsonschema_description:"卡片用：30 天可用率。"`
	Days      []StatusSlot   `json:"days,omitempty" jsonschema_description:"卡片用：最近 30 天每天一格。"`
	Latency   []LatencyPoint `json:"latency,omitempty" jsonschema_description:"卡片用：最近 24 小时响应时间，每 15 分钟一点。"`
}

type MonitorResult struct {
	Monitor         Monitor  `json:"monitor"`
	Created         bool     `json:"created" jsonschema_description:"false 表示更新了已有监控。"`
	NotifierCreated string   `json:"notifierCreated,omitempty" jsonschema_description:"没有任何通知渠道时自动创建的本机通知渠道 ID。"`
	Board           string   `json:"board,omitempty" jsonschema_description:"显示这个监控的面板 ID（子服务为父服务所在的面板）。"`
	BoardCreated    bool     `json:"boardCreated,omitempty" jsonschema_description:"true 表示这次自动新建了面板。"`
	BoardURL        string   `json:"boardUrl,omitempty"`
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

// AssistantStatus is what 波西米亚 needs and has; keys are reported only as saved or not.
type AssistantStatus struct {
	Name           string   `json:"name"`
	Configured     bool     `json:"configured" jsonschema_description:"DeepSeek 和百炼的密钥都已保存，可以提问。"`
	DeepSeekKey    bool     `json:"deepseekKey" jsonschema_description:"已保存 DeepSeek 的 API Key。"`
	Model          string   `json:"model" jsonschema_description:"DeepSeek 对话模型。"`
	Models         []string `json:"models" jsonschema_description:"这个 DeepSeek 账号可用的模型；没有密钥或读不到时为空。"`
	DashScopeKey   bool     `json:"dashscopeKey" jsonschema_description:"已保存百炼的 API Key。"`
	Region         string   `json:"region" jsonschema:"enum=beijing,enum=singapore"`
	EmbeddingModel string   `json:"embeddingModel"`
	RerankModel    string   `json:"rerankModel"`
	Docs           int      `json:"docs" jsonschema_description:"已建好向量索引的说明段落数；0 表示第一次需要查说明时再建。"`
	Problem        string   `json:"problem,omitempty" jsonschema_description:"还不能用时差什么。"`
	Setup          string   `json:"setup" jsonschema_description:"也可以在监控器电脑上运行这条命令配置（按提示粘贴密钥，输入不显示）。"`
}

// AssistantConfigInput changes only what it names; each part is checked with a small request before saving.
type AssistantConfigInput struct {
	DeepSeekKey    string  `json:"deepseekKey,omitempty" jsonschema:"writeOnly=true,maxLength=8192" jsonschema_description:"DeepSeek 的 API Key；省略时沿用已保存的。只写不读。"`
	Model          *string `json:"model,omitempty" jsonschema:"maxLength=200" jsonschema_description:"DeepSeek 对话模型，须在账号的模型列表里；空字符串表示默认。"`
	DashScopeKey   string  `json:"dashscopeKey,omitempty" jsonschema:"writeOnly=true,maxLength=8192" jsonschema_description:"百炼的 API Key；省略时沿用已保存的。只写不读。"`
	Region         *string `json:"region,omitempty" jsonschema:"enum=beijing,enum=singapore,enum=" jsonschema_description:"百炼地域。"`
	EmbeddingModel *string `json:"embeddingModel,omitempty" jsonschema:"maxLength=200" jsonschema_description:"百炼向量模型；空字符串表示默认。"`
	RerankModel    *string `json:"rerankModel,omitempty" jsonschema:"maxLength=200" jsonschema_description:"百炼 rerank 模型；空字符串表示默认。"`
}
type AssistantMessage struct {
	Role string `json:"role" jsonschema:"enum=user,enum=assistant"`
	Text string `json:"text" jsonschema:"maxLength=8000"`
}

// MonitorEdit changes what a person sees of a monitor without touching what it checks.
type MonitorEdit struct {
	Name   *string `json:"name,omitempty" jsonschema:"minLength=1,maxLength=80" jsonschema_description:"新名字。"`
	Parent *string `json:"parent,omitempty" jsonschema_description:"挂在哪个服务下面；空字符串表示改为主服务。"`
}

type AssistantInput struct {
	Messages []AssistantMessage `json:"messages" jsonschema:"minItems=1,maxItems=40"`
}

func agentRoutes() []Route {
	return []Route{
		{GetGuide, "GET", "/api/v1/guide", "当前安装、状态和下一步；Agent 接入后先读它", ScopeRead, nil, Guide{}, nil},
		{Explore, "GET", "/api/v1/explore", "搜索这台电脑上的监听端口、Windows 服务、进程和计划任务，并给出可直接添加监控的参数", ScopeRead, nil, ExploreResult{}, []Parameter{{"collector", "采集器 ID；只有一台电脑时省略"}, {"kind", "ports、services、processes 或 tasks；默认 ports"}, {"q", "名称关键词，不区分大小写"}, {"state", "services：running、stopped 或 attention（自动启动但已停止）；tasks：attention（上次运行失败）"}, {"limit", "每页条数，1～200，默认 50"}, {"offset", "跳过的条数，默认 0"}}},
		{ListMonitors, "GET", "/api/v1/monitors", "全部监控及其当前状态、规则、告警数和面板", ScopeRead, nil, []Monitor{}, nil},
		{AddMonitor, "POST", "/api/v1/monitors", "一次调用创建或更新监控：目标、探测、告警规则、通知渠道和面板", ScopeConfig, blueprint.MonitorInput{}, MonitorResult{}, nil},
		{EditMonitor, "PUT", "/api/v1/monitors/{id}", "改名或改挂在哪个服务下面；检查方式和告警不变", ScopeConfig, MonitorEdit{}, Monitor{}, nil},
		{RemoveMonitor, "DELETE", "/api/v1/monitors/{id}", "删除监控目标及其规则，并从所有面板移除；有子服务时需要 children=true 整组删除", ScopeConfig, nil, Confirmation{}, []Parameter{{"children", "true 时连同全部子服务一起删除"}}},
		{ListDashboardTemplates, "GET", "/api/v1/dashboard-templates", "可直接创建的面板模板", ScopeRead, nil, []blueprint.Template{}, nil},
		{CreateDashboard, "POST", "/api/v1/dashboards", "按模板创建面板", ScopeConfig, blueprint.TemplateInput{}, DashboardResult{}, nil},
		{PutDashboard, "PUT", "/api/v1/dashboards/{id}", "校验并保存完整的声明式面板定义", ScopeConfig, config.Dashboard{}, DashboardResult{}, nil},
		{DeleteDashboard, "DELETE", "/api/v1/dashboards/{id}", "删除声明式面板", ScopeConfig, nil, Confirmation{}, nil},
		{GetAssistant, "GET", "/api/v1/assistant", "波西米亚（面板内助手）的配置情况：两把密钥是否已保存、模型、地域和还差什么", ScopeRead, nil, AssistantStatus{}, nil},
		{ConfigureAssistant, "PUT", "/api/v1/assistant", "配置波西米亚：DeepSeek 与百炼的密钥和模型；每部分先用一次小请求验证再保存，密钥只写不读", ScopeConfig, AssistantConfigInput{}, AssistantStatus{}, nil},
		{AssistantChat, "POST", "/api/v1/assistant/chat", "与波西米亚对话；SSE 返回 text、tool、done 或 error 事件，它只能读取监控数据和 lgmon 的说明", ScopeRead, AssistantInput{}, "", nil},
	}
}
