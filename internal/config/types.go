// Copyright (c) 2026 LFen (https://lfen.ink) — lg-monitor

package config

import (
	"encoding/json"
	"fmt"
	"strconv"
	"time"

	"github.com/invopop/jsonschema"
	"gopkg.in/yaml.v3"
)

type Config struct {
	Targets         []Target         `json:"targets,omitempty"`
	Rules           []Rule           `json:"rules,omitempty"`
	FeishuApps      []FeishuApp      `json:"feishuApps,omitempty"`
	Notifiers       []Notifier       `json:"notifiers,omitempty"`
	Templates       []Template       `json:"templates,omitempty"`
	Dashboards      []Dashboard      `json:"dashboards,omitempty"`
	DashboardStates []DashboardState `json:"dashboardStates,omitempty"`
	Boards          []Board          `json:"boards,omitempty"`
	Settings        Settings         `json:"settings,omitempty"`
	// Docs are the services' Markdown documents (docs/<target ID>.md), read through the doc API only.
	Docs map[string]string `json:"-"`

	revision       uint64
	sources        map[string]*yaml.Node
	templateFiles  []string
	dashboardFiles []string
}

type TargetsFile struct {
	Targets []Target `yaml:"targets,omitempty" json:"targets,omitempty"`
}

type RulesFile struct {
	Rules []Rule `yaml:"rules,omitempty" json:"rules,omitempty"`
}

type BoardsFile struct {
	Boards []Board `yaml:"boards,omitempty" json:"boards,omitempty"`
}

// Board groups monitors that are looked at together. It holds no queries: what a board
// shows comes from the monitors themselves, so adding a monitor is the only thing to do.
type Board struct {
	ID          string   `yaml:"id" json:"id" jsonschema:"minLength=1,maxLength=48,pattern=^[a-z0-9-]+$"`
	Name        string   `yaml:"name" json:"name" jsonschema:"minLength=1,maxLength=40" jsonschema_description:"面板名称，例如“我的服务”。"`
	Description string   `yaml:"description,omitempty" json:"description,omitempty" jsonschema:"maxLength=200" jsonschema_description:"一句话介绍；打开内网可见时，局域网里的人能看到名称和这句介绍。"`
	Monitors    []string `yaml:"monitors,omitempty" json:"monitors,omitempty" jsonschema:"uniqueItems=true" jsonschema_description:"面板上的监控目标 ID，按显示顺序。子服务随父服务显示，不必列出。"`
	CardStyle   string   `yaml:"cardStyle,omitempty" json:"cardStyle,omitempty" jsonschema:"enum=pulse,enum=minimal,enum=ring,enum=latency,enum=days,enum=number" jsonschema_description:"卡片视图里服务的默认样式：pulse 心跳（默认）、minimal 极简、ring 可用率环、latency 响应曲线、days 30 天、number 大数字。"`
	// Cards picks a style for single services; it wins over CardStyle.
	Cards map[string]string `yaml:"cards,omitempty" json:"cards,omitempty" jsonschema_description:"单独指定某些服务的卡片样式：服务 ID → 样式，取值同 cardStyle。"`
}

type NotifiersFile struct {
	FeishuApps []FeishuApp `yaml:"feishuApps,omitempty" json:"feishuApps,omitempty"`
	Notifiers  []Notifier  `yaml:"notifiers,omitempty" json:"notifiers,omitempty"`
}

type Target struct {
	ID        string                `yaml:"id" json:"id" jsonschema:"minLength=1,pattern=^[a-z0-9-]+$"`
	Name      string                `yaml:"name,omitempty" json:"name,omitempty"`
	Collector string                `yaml:"collector,omitempty" json:"collector,omitempty" jsonschema:"minLength=1,maxLength=255,pattern=^(\\*|[^\\s*/\\\\:]+)$" jsonschema_description:"采集器 ID 或主机名；* 或省略表示全部采集器，主机名不区分大小写。"`
	Match     Match                 `yaml:"match,omitempty" json:"match,omitempty"`
	EventLog  *EventLogSubscription `yaml:"eventlog,omitempty" json:"eventlog,omitempty"`
	Probes    []Probe               `yaml:"probes,omitempty" json:"probes,omitempty"`
	Tags      []string              `yaml:"tags,omitempty" json:"tags,omitempty"`
	Source    *TargetSource         `yaml:"source,omitempty" json:"source,omitempty"`
	Parent    string                `yaml:"parent,omitempty" json:"parent,omitempty" jsonschema:"maxLength=48,pattern=^[a-z0-9-]+$" jsonschema_description:"主服务的目标 ID。只定义服务树的层级，各服务独立探测。只在监控器使用，不下发采集器。"`
	Task      string                `yaml:"task,omitempty" json:"task,omitempty" jsonschema:"minLength=2,maxLength=1024,pattern=^\\\\" jsonschema_description:"Windows 计划任务的完整路径，例如 \\Backup\\Nightly。状态取自采集器的计划任务清单（上次运行结果、是否禁用）；只在监控器使用，不下发采集器。"`
	// Role, Description and Depends describe the service for people and stay on the monitor, like Parent.
	Role        string       `yaml:"role,omitempty" json:"role,omitempty" jsonschema:"enum=web,enum=api,enum=app,enum=database,enum=cache,enum=queue,enum=storage,enum=tunnel,enum=gateway,enum=worker,enum=scheduler,enum=ai,enum=mcp,enum=external" jsonschema_description:"服务角色，决定依赖关系图里的图标：web 网站、api 接口、app 应用、database 数据库、cache 缓存、queue 消息队列、storage 文件与存储、tunnel 隧道、gateway 网关与代理、worker 后台程序、scheduler 定时任务、ai 模型服务、mcp MCP 服务、external 外部服务。省略时按监控类型推断。"`
	Description string       `yaml:"description,omitempty" json:"description,omitempty" jsonschema:"maxLength=300" jsonschema_description:"一两句话说明这个服务是做什么的、谁在用；显示在列表和详情里。"`
	Entry       string       `yaml:"entry,omitempty" json:"entry,omitempty" jsonschema:"maxLength=500,pattern=^https?://" jsonschema_description:"访问入口：点“打开”去的地址，例如 https://blog.example.com 或 http://127.0.0.1:3000。省略时网址监控用它的网址，网页类的端口监控用 http://127.0.0.1:端口。"`
	Depends     []Dependency `yaml:"depends,omitempty" json:"depends,omitempty" jsonschema:"maxItems=20" jsonschema_description:"这个服务用到的其他服务（可以跨面板）。只用于展示依赖关系，不影响告警；父子关系另用 parent。"`
	// Job makes this target a batch job watched through its business events; it then has no match or probes.
	Job *Job `yaml:"job,omitempty" json:"job,omitempty" jsonschema_description:"批处理任务：状态只来自任务自己上报的业务事件（每日记录、阶段、小时分区），不看进程或端口。"`
}

// Dependency says this service uses another one, and how.
type Dependency struct {
	Target   string `yaml:"target" json:"target" jsonschema:"minLength=1,maxLength=48,pattern=^[a-z0-9-]+$" jsonschema_description:"被依赖的服务 ID。"`
	Relation string `yaml:"relation,omitempty" json:"relation,omitempty" jsonschema:"enum=uses,enum=calls,enum=data,enum=reads,enum=writes,enum=via,enum=forwards,enum=runs,enum=sends,enum=consumes,enum=auth,enum=syncs,enum=replicates,enum=backup,enum=fallback,enum=monitors" jsonschema_description:"uses 依赖（默认）、calls 调用它的接口、data 读写它的数据、reads 只读它的数据、writes 只往它写数据、via 经由它对外（隧道、网关、代理）、forwards 把请求转发给它（网关、反向代理指向后端）、runs 由它启动或触发、sends 向它发送消息、consumes 从它消费消息、auth 用它登录或鉴权、syncs 与它双向同步、replicates 把数据复制给它、backup 备份到它、fallback 出故障时切换到它、monitors 检查它的健康。"`
	Optional bool   `yaml:"optional,omitempty" json:"optional,omitempty" jsonschema_description:"可选依赖：它出问题时这个服务还能降级运行。图里画成细虚线。"`
	Note     string `yaml:"note,omitempty" json:"note,omitempty" jsonschema:"maxLength=80" jsonschema_description:"一句补充，例如“订单表”“每天 01:00”。"`
}

type Match struct {
	Process string `yaml:"process,omitempty" json:"process,omitempty" jsonschema:"minLength=1"`
	Cmdline string `yaml:"cmdline,omitempty" json:"cmdline,omitempty" jsonschema:"minLength=1" jsonschema_description:"命令行正则默认不区分大小写；可用 (?-i) 显式区分大小写。"`
	ExeDir  string `yaml:"exeDir,omitempty" json:"exeDir,omitempty" jsonschema:"minLength=1" jsonschema_description:"程序所在目录，Windows 绝对盘符或 UNC 路径；匹配自身及子目录，不区分大小写。"`
	WorkDir string `yaml:"workDir,omitempty" json:"workDir,omitempty" jsonschema:"minLength=1" jsonschema_description:"当前工作目录，需同时配置 process、cmdline 或 exeDir；只对候选读取，十秒内复用缓存。"`
	Port    *int   `yaml:"port,omitempty" json:"port,omitempty" jsonschema:"minimum=1,maximum=65535"`
	Service string `yaml:"service,omitempty" json:"service,omitempty" jsonschema:"minLength=1"`
}

type Probe struct {
	Type          string   `yaml:"type" json:"type" jsonschema:"enum=http,enum=tcp,enum=script"`
	Script        string   `yaml:"script,omitempty" json:"script,omitempty" jsonschema:"minLength=1"`
	Args          []string `yaml:"args,omitempty" json:"args,omitempty"`
	URL           string   `yaml:"url,omitempty" json:"url,omitempty" jsonschema:"minLength=1,pattern=^https?://"`
	Address       string   `yaml:"address,omitempty" json:"address,omitempty" jsonschema:"minLength=1"`
	Interval      Duration `yaml:"interval" json:"interval"`
	Timeout       Duration `yaml:"timeout" json:"timeout"`
	ExpectStatus  Status   `yaml:"expectStatus,omitempty" json:"expectStatus,omitempty"`
	Keyword       string   `yaml:"keyword,omitempty" json:"keyword,omitempty" jsonschema:"minLength=1,maxLength=256" jsonschema_description:"区分大小写的 UTF-8 子串，1～256 字节；只检查解压正文的前 1 MiB。"`
	KeywordAbsent *bool    `yaml:"keywordAbsent,omitempty" json:"keywordAbsent,omitempty" jsonschema_description:"为 true 时要求关键字不出现；必须同时配置 keyword，仅限 HTTP。"`
}

type Rule struct {
	ID        string   `yaml:"id" json:"id" jsonschema:"minLength=1,pattern=^[a-z0-9-]+$"`
	Name      string   `yaml:"name,omitempty" json:"name,omitempty"`
	Type      string   `yaml:"type" json:"type" jsonschema:"enum=process_absent,enum=port_closed,enum=probe_failed,enum=collector_offline,enum=metric_threshold,enum=service_stopped,enum=script_failed,enum=sql,enum=cert_expiry,enum=eventlog"`
	Target    string   `yaml:"target,omitempty" json:"target,omitempty" jsonschema:"minLength=1"`
	For       Duration `yaml:"for,omitempty" json:"for,omitempty"`
	Severity  string   `yaml:"severity" json:"severity" jsonschema:"enum=critical,enum=warning,enum=info"`
	Notify    []string `yaml:"notify" json:"notify" jsonschema:"minItems=1,uniqueItems=true"`
	Template  string   `yaml:"template,omitempty" json:"template,omitempty"`
	Renotify  Duration `yaml:"renotify,omitempty" json:"renotify,omitempty"`
	Metric    string   `yaml:"metric,omitempty" json:"metric,omitempty"`
	Op        string   `yaml:"op,omitempty" json:"op,omitempty" jsonschema:"enum=gt,enum=gte,enum=lt,enum=lte,enum=eq,enum=ne"`
	Threshold *float64 `yaml:"threshold,omitempty" json:"threshold,omitempty"`
	Resource  string   `yaml:"resource,omitempty" json:"resource,omitempty"`
	Probe     *int     `yaml:"probe,omitempty" json:"probe,omitempty" jsonschema:"minimum=0"`
	Query     string   `yaml:"query,omitempty" json:"query,omitempty"`
	Interval  Duration `yaml:"interval,omitempty" json:"interval,omitempty"`
	Within    Duration `yaml:"within,omitempty" json:"within,omitempty" jsonschema_description:"cert_expiry 的剩余时长阈值，默认 14d，范围 1h～365d。"`
	Preset    string   `yaml:"preset,omitempty" json:"preset,omitempty" jsonschema:"enum=app_crash,enum=service_crash,enum=unexpected_shutdown"`
	Count     *int     `yaml:"count,omitempty" json:"count,omitempty" jsonschema:"minimum=1,maximum=8640000"`
	Window    Duration `yaml:"window,omitempty" json:"window,omitempty" jsonschema_description:"eventlog 的计数窗口，默认 10m，范围 1s～24h。"`
}

type FeishuApp struct {
	ID        string `yaml:"id" json:"id" jsonschema:"minLength=1,pattern=^[a-z0-9-]+$"`
	AppID     string `yaml:"appId" json:"appId" jsonschema:"minLength=1"`
	SecretRef string `yaml:"secretRef" json:"secretRef" jsonschema:"minLength=1"`
	Domain    string `yaml:"domain" json:"domain" jsonschema:"enum=feishu,enum=lark"`
	Proxy     string `yaml:"proxy,omitempty" json:"proxy,omitempty"`
}

type Notifier struct {
	ID          string          `yaml:"id" json:"id" jsonschema:"minLength=1,pattern=^[a-z0-9-]+$"`
	Type        string          `yaml:"type" json:"type" jsonschema:"enum=feishu_app,enum=feishu_webhook,enum=webhook,enum=toast"`
	App         string          `yaml:"app,omitempty" json:"app,omitempty" jsonschema:"minLength=1"`
	ChatID      string          `yaml:"chatId,omitempty" json:"chatId,omitempty" jsonschema:"minLength=1"`
	OpenID      string          `yaml:"openId,omitempty" json:"openId,omitempty" jsonschema:"minLength=4,maxLength=256,pattern=^ou_[A-Za-z0-9_-]+$" jsonschema_description:"飞书应用的个人收件人 open_id，与 chatId 二选一；须由本人确认目的地，不能启用群卡片动作。"`
	ChatName    string          `yaml:"chatName,omitempty" json:"chatName,omitempty"`
	Template    string          `yaml:"template,omitempty" json:"template,omitempty"`
	URLRef      string          `yaml:"urlRef,omitempty" json:"urlRef,omitempty" jsonschema:"minLength=1" jsonschema_description:"Webhook 地址在密钥库中的引用，不能填写明文地址。"`
	SecretRef   string          `yaml:"secretRef,omitempty" json:"secretRef,omitempty" jsonschema:"minLength=1" jsonschema_description:"可选签名密钥的引用，不是密钥内容。"`
	Filter      *NotifierFilter `yaml:"filter,omitempty" json:"filter,omitempty"`
	CardActions bool            `yaml:"cardActions,omitempty" json:"cardActions,omitempty" jsonschema_description:"仅飞书应用：管理员明确授权该群成员确认消息对应告警或静音该告警组一小时，默认关闭。"`
}

type NotifierFilter struct {
	Severity   []string `yaml:"severity,omitempty" json:"severity,omitempty" jsonschema:"minItems=1,uniqueItems=true"`
	Tags       []string `yaml:"tags,omitempty" json:"tags,omitempty" jsonschema:"minItems=1,uniqueItems=true"`
	Collectors []string `yaml:"collectors,omitempty" json:"collectors,omitempty" jsonschema:"minItems=1,uniqueItems=true"`
}

type Template struct {
	ID          string            `yaml:"id" json:"id" jsonschema:"minLength=1,pattern=^[a-z0-9-]+$"`
	Name        string            `yaml:"name,omitempty" json:"name,omitempty"`
	Extends     string            `yaml:"extends,omitempty" json:"extends,omitempty" jsonschema:"enum=standard,enum=compact,enum=detailed,enum=digest"`
	Title       string            `yaml:"title,omitempty" json:"title,omitempty"`
	Colors      map[string]string `yaml:"colors,omitempty" json:"colors,omitempty"`
	Fields      []string          `yaml:"fields,omitempty" json:"fields,omitempty"`
	ExtraFields []TemplateField   `yaml:"extraFields,omitempty" json:"extraFields,omitempty"`
	Mention     map[string]any    `yaml:"mention,omitempty" json:"mention,omitempty"`
	Buttons     []TemplateButton  `yaml:"buttons,omitempty" json:"buttons,omitempty"`
	Note        string            `yaml:"note,omitempty" json:"note,omitempty"`
	Raw         any               `yaml:"raw,omitempty" json:"raw,omitempty" jsonschema:"oneof_type=string;object"`
}

type TemplateField struct {
	Label string `yaml:"label" json:"label"`
	Value string `yaml:"value" json:"value"`
}

type TemplateButton struct {
	Text string `yaml:"text" json:"text"`
	URL  string `yaml:"url" json:"url"`
}

type Duration string

func (d Duration) Parse() (time.Duration, error) {
	if d == "" {
		return 0, nil
	}
	value, err := parseDuration(string(d))
	if err != nil {
		return 0, fmt.Errorf("时长格式无效，请使用 10s、5m、14d 等格式：%w", err)
	}
	return value, nil
}

func (Duration) JSONSchema() *jsonschema.Schema {
	return &jsonschema.Schema{Type: "string", Pattern: `^\+?(0|(([0-9]+(\.[0-9]*)?|\.[0-9]+)(ns|us|µs|μs|ms|s|m|h|d))+)$`}
}

type Status string

func (s *Status) UnmarshalYAML(node *yaml.Node) error {
	if node.Kind != yaml.ScalarNode || (node.Tag != "!!str" && node.Tag != "!!int") {
		return fmt.Errorf("第 %d 行：expectStatus 必须为状态码或字符串", node.Line)
	}
	*s = Status(node.Value)
	if node.Tag == "!!int" {
		var code int
		if err := node.Decode(&code); err != nil {
			return fmt.Errorf("第 %d 行：解析 HTTP 状态码失败：%w", node.Line, err)
		}
		*s = Status(strconv.Itoa(code))
	}
	return nil
}

func (Status) JSONSchema() *jsonschema.Schema {
	return &jsonschema.Schema{OneOf: []*jsonschema.Schema{
		{Type: "integer", Minimum: json.Number("100"), Maximum: json.Number("599")},
		{Type: "string", Pattern: `^([1-5][0-9]{2}|[1-5]xx|[<>]=?[1-5][0-9]{2})$`},
	}}
}
