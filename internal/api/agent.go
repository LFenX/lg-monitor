// Copyright (c) 2026 LFen (https://lfen.ink) — lg-monitor

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
	GetKnowledge           Operation = "assistant_knowledge"
	PutKnowledge           Operation = "assistant_knowledge_update"
	AssistantConfirmStep   Operation = "assistant_confirm"
	ListAgents             Operation = "list_agents"
	AgentPresence          Operation = "agent_presence"
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
	AgentName    string `json:"agentName,omitempty" jsonschema_description:"最近一次调用的 Agent，例如 Claude Code；认不出时是它的令牌名。"`
}

// AgentClient is who is on the other end of an MCP call, as the MCP server running for that agent reported it.
type AgentClient struct {
	// Kind is a known agent ID such as claude-code or codex; empty when the agent was not recognized.
	Kind string
	// Client and Version are the clientInfo the agent sent in MCP initialize.
	Client, Version string
	// Session is one MCP connection: a stdio MCP server process, or one agent's token over HTTP.
	Session string
}

// AgentPresenceInput is the heartbeat a running MCP server sends for its agent.
type AgentPresenceInput struct {
	State    string `json:"state,omitempty" jsonschema:"enum=open,enum=alive,enum=closed" jsonschema_description:"open：刚连上；alive：仍连着（默认）；closed：已断开。"`
	Interval int    `json:"interval,omitempty" jsonschema:"minimum=0,maximum=600" jsonschema_description:"之后每隔多少秒再报一次；0 表示不保证，按最近一次调用判断是否还连着。"`
}

// AgentSession is one agent connection the web panel shows.
type AgentSession struct {
	ID         string `json:"id"`
	Kind       string `json:"kind,omitempty" jsonschema_description:"认出的 Agent，例如 claude-code、codex、cursor、opencode、kimi、dsh、zcode；空表示没认出。"`
	Name       string `json:"name" jsonschema_description:"给人看的名称。"`
	Client     string `json:"client,omitempty" jsonschema_description:"Agent 在 MCP initialize 里报告的客户端名。"`
	Version    string `json:"version,omitempty"`
	Token      string `json:"token,omitempty" jsonschema_description:"这个连接使用的令牌名称。"`
	Connected  bool   `json:"connected" jsonschema_description:"现在是否连着：MCP 服务按时报到，或者不报到的连接 10 分钟内调用过。"`
	Since      int64  `json:"since" jsonschema_description:"这次连接开始的 UTC Unix 毫秒。"`
	LastSeenAt int64  `json:"lastSeenAt" jsonschema_description:"最近一次报到或调用的 UTC Unix 毫秒。"`
	Calls      int    `json:"calls" jsonschema_description:"这次连接调用了多少次工具。"`
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
	Ports        *GuidePorts      `json:"ports,omitempty" jsonschema_description:"只在 Windows 这次开机保留了 lgmon 的端口时出现：lgmon 临时改用了哪些端口，以及管理员怎样立即恢复。"`
}

// GuidePorts says which ports Windows kept from lgmon this time.
type GuidePorts struct {
	Moved []GuidePort `json:"moved"`
	Fix   []string    `json:"fix" jsonschema_description:"在管理员 PowerShell 里依次运行；会短暂中断 WSL 和 Docker 的网络。"`
}

type GuidePort struct {
	Configured int `json:"configured"`
	Actual     int `json:"actual"`
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
	// Offline marks a host whose collector stopped reporting, so TS is the last good sample, not a fresh one.
	Offline bool `json:"offline,omitempty"`
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
	Kind      string        `json:"kind" jsonschema:"enum=port,enum=url,enum=service,enum=process,enum=task,enum=job,enum=other"`
	Dashboard string        `json:"dashboard,omitempty"`
	URL       string        `json:"url,omitempty"`
	Parent    string        `json:"parent,omitempty" jsonschema_description:"主服务的 ID；为空表示自己是主服务。"`
	Children  []string      `json:"children" jsonschema_description:"直接子服务的 ID。"`
	Depth     int           `json:"depth" jsonschema_description:"在面板服务树里的层级，主服务为 0。"`
	Boards    []string      `json:"boards" jsonschema_description:"直接列在哪些面板上；子服务随父服务显示时为空。"`
	Overall   string        `json:"overall" jsonschema:"enum=up,enum=down,enum=unknown" jsonschema_description:"该服务自身的探测状态，与 state 相同。"`
	// Role, Description and the dependency lists are for people: the list, the dependency view and the docs.
	Role         string              `json:"role" jsonschema_description:"服务角色，决定依赖关系图里的图标：web、api、app、database、cache、queue、storage、tunnel、gateway、worker、scheduler、ai、mcp、external。"`
	RoleGuessed  bool                `json:"roleGuessed,omitempty" jsonschema_description:"true 表示没有设置角色，这是按监控类型和名字推断的。"`
	Description  string              `json:"description,omitempty" jsonschema_description:"一两句话的说明。"`
	Entry        string              `json:"entry,omitempty" jsonschema_description:"访问入口：点“打开”去的地址；网址和网页类端口服务没有设置时给出推断的地址。"`
	EntryGuessed bool                `json:"entryGuessed,omitempty" jsonschema_description:"true 表示没有设置访问入口，这是按网址或端口推断的。"`
	Depends      []MonitorDependency `json:"depends,omitempty" jsonschema_description:"这个服务用到的服务。"`
	Dependents   []MonitorDependency `json:"dependents,omitempty" jsonschema_description:"用到这个服务的服务（反向）。"`
	// External lists what this service uses on the account's other computers; they never count toward its state.
	External []MonitorDependency `json:"external,omitempty" jsonschema_description:"这个服务用到的、同一云账号其他设备上的服务（外部依赖，device 是那台设备的 ID）。只用于展示，不参与本机告警和状态。"`
	HasDoc   bool                `json:"hasDoc,omitempty" jsonschema_description:"true 表示写了文档，用 get_monitor_doc 读。"`
	// Job is a batch job's latest meaningful day; its daily records come from get_job_day and list_job_days.
	Job   *JobBrief `json:"job,omitempty" jsonschema_description:"批处理任务：最近一天的状态（pending、running、success、partial、failed、missed、unknown）和说明。"`
	Issue string    `json:"issue,omitempty" jsonschema_description:"该服务自身的问题说明。"`
	// Group history remains available to clients that explicitly request a subtree summary.
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

// MonitorDependency is one edge of the dependency view, seen from either end.
type MonitorDependency struct {
	RestrictedName string `json:"restrictedName,omitempty"`
	ID             string `json:"id" jsonschema_description:"另一端的服务 ID。"`
	Relation       string `json:"relation" jsonschema:"enum=uses,enum=calls,enum=data,enum=reads,enum=writes,enum=via,enum=forwards,enum=runs,enum=sends,enum=consumes,enum=auth,enum=syncs,enum=replicates,enum=backup,enum=fallback,enum=monitors"`
	Optional       bool   `json:"optional,omitempty"`
	Note           string `json:"note,omitempty"`
	Device         string `json:"device,omitempty" jsonschema_description:"外部依赖：服务所在的另一台设备的 ID。"`
	DeviceName     string `json:"deviceName,omitempty" jsonschema_description:"外部依赖：那台设备的名称。"`
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
	// SharedBy is set when this computer has no DeepSeek key and talks through a colleague's shared model.
	SharedBy *AssistantShare `json:"sharedBy,omitempty" jsonschema_description:"正在用哪位同事共享的模型：昵称、头像和模型。"`
	Setup    string          `json:"setup" jsonschema_description:"也可以在监控器电脑上运行这条命令配置（按提示粘贴密钥，输入不显示）。"`
}

// AssistantShare names a colleague whose model 波西米亚 uses here.
type AssistantShare struct {
	Name   string `json:"name"`
	Avatar string `json:"avatar,omitempty"`
	Model  string `json:"model"`
}

// AssistantKnowledge is what 波西米亚 can look things up in, and how much of it is embedded.
type AssistantKnowledge struct {
	Asked           bool           `json:"asked" jsonschema_description:"是否已经问过本人要不要向量化服务文档。"`
	Guides          int            `json:"guides" jsonschema_description:"lgmon 使用说明的段落数。"`
	GuidesIndexed   int            `json:"guidesIndexed"`
	Services        int            `json:"services" jsonschema_description:"服务资料（每个服务一段）的段落数，配置好百炼后自动向量化。"`
	ServicesIndexed int            `json:"servicesIndexed"`
	Docs            []KnowledgeDoc `json:"docs"`
}

type KnowledgeDoc struct {
	ID        string `json:"id" jsonschema_description:"服务 ID。"`
	Name      string `json:"name"`
	Vectorize bool   `json:"vectorize" jsonschema_description:"true 表示纳入向量检索；false 只按关键词检索。"`
	Passages  int    `json:"passages"`
	Indexed   int    `json:"indexed" jsonschema_description:"已向量化的段落数。"`
}

// AssistantKnowledgeInput chooses which documents are embedded; documents not named keep their choice.
type AssistantKnowledgeInput struct {
	All  *bool           `json:"all,omitempty" jsonschema_description:"true 全部纳入，false 全部不纳入。"`
	Docs map[string]bool `json:"docs,omitempty" jsonschema_description:"服务 ID 到 true/false。"`
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
	Name        *string              `json:"name,omitempty" jsonschema:"minLength=1,maxLength=80" jsonschema_description:"新名字。"`
	Parent      *string              `json:"parent,omitempty" jsonschema_description:"挂在哪个服务下面；空字符串表示改为主服务。"`
	Description *string              `json:"description,omitempty" jsonschema:"maxLength=300" jsonschema_description:"一两句话说明这个服务做什么、谁在用；空字符串表示清除。"`
	Role        *string              `json:"role,omitempty" jsonschema:"maxLength=16" jsonschema_description:"服务角色（决定图标）：web、api、app、database、cache、queue、storage、tunnel、gateway、worker、scheduler、ai、mcp、external；空字符串表示按监控类型推断。"`
	Depends     *[]config.Dependency `json:"depends,omitempty" jsonschema:"maxItems=20" jsonschema_description:"整个替换这个服务用到的服务（可以跨面板）；[] 表示清空。同一云账号其他设备上的服务写 {target, device}（从 list_external_services 找），会显示在面板的外部依赖区。只用于展示依赖关系，不影响告警。"`
	Entry       *string              `json:"entry,omitempty" jsonschema:"maxLength=500" jsonschema_description:"访问入口：http:// 或 https:// 开头、点“打开”去的地址，例如对外的域名；空字符串表示清除，改回按网址或端口推断。"`
}

type AssistantInput struct {
	Messages []AssistantMessage `json:"messages" jsonschema:"minItems=1,maxItems=40"`
	Page     *AssistantPage     `json:"page,omitempty" jsonschema_description:"提问时用户正在看的网页，让波西米亚知道人在哪里。"`
}

// AssistantPage is where in the web panel the question was asked.
type AssistantPage struct {
	Path  string `json:"path" jsonschema:"maxLength=300" jsonschema_description:"网页地址 # 之后的部分，例如 #/boards/main。"`
	Title string `json:"title,omitempty" jsonschema:"maxLength=200" jsonschema_description:"这一页给人看的名称，例如“面板 · 我的服务”。"`
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
		{GetKnowledge, "GET", "/api/v1/assistant/knowledge", "波西米亚的知识库：使用说明、服务资料和服务文档各有多少段、已向量化多少；每篇文档是否纳入向量检索", ScopeRead, nil, AssistantKnowledge{}, nil},
		{PutKnowledge, "PUT", "/api/v1/assistant/knowledge", "选择哪些服务文档纳入向量检索（默认只按关键词检索）；改动后只重新向量化改了的段落", ScopeConfig, AssistantKnowledgeInput{}, AssistantKnowledge{}, nil},
		{AssistantChat, "POST", "/api/v1/assistant/chat", "与波西米亚对话；SSE 返回 text、tool、change、confirm、done 或 error 事件。它用 MCP 的工具查看和修改已有的服务（不能添加新服务），权限与发起请求的会话相同", ScopeRead, AssistantInput{}, "", nil},
		{ListAgents, "GET", "/api/v1/agents", "接入过的 Agent（Claude Code、Codex、Cursor 等）：现在连着的在前，最近 24 小时断开的在后", ScopeRead, nil, []AgentSession{}, nil},
		{AgentPresence, "POST", "/api/v1/agents/presence", "lgmon mcp 为它服务的 Agent 报到：连上、仍连着或断开；身份来自请求头，不由 Agent 调用", ScopeRead, AgentPresenceInput{}, Confirmation{}, nil},
		{AssistantConfirmStep, "POST", "/api/v1/assistant/confirm", "回答波西米亚在对话里提出、需要本人确认的一步（confirm 事件）；同意后它才执行", ScopeRead, AssistantDecision{}, Confirmation{}, nil},
	}
}
