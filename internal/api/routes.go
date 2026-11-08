package api

import (
	"encoding/json"

	"github.com/LFenX/lg-monitor/internal/collector"
	"github.com/LFenX/lg-monitor/internal/config"
	"github.com/LFenX/lg-monitor/internal/feishu/setup"
	"github.com/LFenX/lg-monitor/internal/notify/cards"
	"github.com/LFenX/lg-monitor/internal/rules"
	"github.com/LFenX/lg-monitor/internal/store"
)

type Operation string

type alternativeModels []any

const (
	ListCollectors          Operation = "list_collectors"
	ListProcesses           Operation = "list_processes"
	TestTargetMatch         Operation = "test_target_match"
	ValidateConfig          Operation = "validate_config"
	ListTargets             Operation = "list_targets"
	TargetHistory           Operation = "target_history"
	Metrics                 Operation = "metrics"
	ListAlerts              Operation = "list_alerts"
	AckAlert                Operation = "ack_alert"
	CreateSilence           Operation = "create_silence"
	DeleteSilence           Operation = "delete_silence"
	GetTargetsConfig        Operation = "get_targets_config"
	PutTargetsConfig        Operation = "put_targets_config"
	GetRulesConfig          Operation = "get_rules_config"
	PutRulesConfig          Operation = "put_rules_config"
	GetNotifiersConfig      Operation = "get_notifiers_config"
	PutNotifiersConfig      Operation = "put_notifiers_config"
	GetSettingsConfig       Operation = "get_settings_config"
	PutSettingsConfig       Operation = "put_settings_config"
	TestRule                Operation = "test_rule"
	TestNotifier            Operation = "test_notifier"
	PutNotifierCredentials  Operation = "put_notifier_credentials"
	FeishuCheck             Operation = "feishu_check"
	FeishuChats             Operation = "feishu_chats"
	FeishuTest              Operation = "feishu_test_chat"
	FeishuSave              Operation = "feishu_save_chat"
	PutFeishuApp            Operation = "put_feishu_app"
	ListTemplates           Operation = "list_templates"
	GetTemplate             Operation = "get_template"
	PutTemplate             Operation = "put_template"
	DeleteTemplate          Operation = "delete_template"
	PreviewTemplate         Operation = "preview_template"
	Query                   Operation = "query"
	ListDashboards          Operation = "list_dashboards"
	GetDashboard            Operation = "get_dashboard"
	GetPanelData            Operation = "get_panel_data"
	Schema                  Operation = "schema"
	Stream                  Operation = "stream"
	OpenAPI                 Operation = "openapi"
	GetNotificationPause    Operation = "get_notification_pause"
	PutNotificationPause    Operation = "put_notification_pause"
	DeleteNotificationPause Operation = "delete_notification_pause"
)

type Parameter struct {
	Name        string
	Description string
}

type Route struct {
	ID                    Operation
	Method, Path, Summary string
	Scope                 Scope
	Request, Response     any
	Query                 []Parameter
}

type Confirmation struct {
	OK      bool   `json:"ok"`
	Message string `json:"message"`
}

type QueryInput struct {
	SQL    string             `json:"sql" jsonschema:"minLength=1,maxLength=262144"`
	Params config.QueryParams `json:"params,omitempty"`
}

type SilenceInput struct {
	Rule   string          `json:"rule,omitempty"`
	Target string          `json:"target,omitempty"`
	For    config.Duration `json:"for"`
	Reason string          `json:"reason"`
}

type PauseInput struct {
	For    config.Duration `json:"for"`
	Reason string          `json:"reason,omitempty" jsonschema:"maxLength=200"`
}

type SessionResult struct {
	Token string `json:"token"`
	Scope Scope  `json:"scope"`
}

type TargetMatchInput struct {
	Match config.Match `json:"match"`
}

type ReplayInput struct {
	Rule *config.Rule `json:"rule,omitempty"`
	From int64        `json:"from,omitempty"`
	To   int64        `json:"to,omitempty"`
}

type PreviewInput struct {
	Template *config.Template `json:"template,omitempty"`
	State    string           `json:"state,omitempty" jsonschema:"enum=firing,enum=resolved,enum=digest"`
}

type NotifierTestInput struct {
	Template string           `json:"template,omitempty"`
	Draft    *config.Template `json:"draft,omitempty"`
}

type FeishuTestInput struct {
	App      string           `json:"app,omitempty"`
	ChatID   string           `json:"chatId"`
	Template string           `json:"template,omitempty"`
	Draft    *config.Template `json:"draft,omitempty"`
	State    string           `json:"state,omitempty" jsonschema:"enum=firing,enum=resolved"`
}

type FeishuSaveInput struct {
	App      string                 `json:"app,omitempty"`
	ChatID   string                 `json:"chatId"`
	Template *string                `json:"template,omitempty"`
	Filter   *config.NotifierFilter `json:"filter,omitempty"`
	Rules    []string               `json:"rules,omitempty"`
}

type FeishuAppInput struct {
	ID     string `json:"id"`
	AppID  string `json:"appId"`
	Domain string `json:"domain" jsonschema:"enum=feishu,enum=lark"`
	Secret string `json:"secret" jsonschema:"writeOnly=true,minLength=1"`
}

type NotifierCredentialsInput struct {
	Type          string  `json:"type,omitempty" jsonschema:"enum=feishu_webhook,enum=webhook"`
	URL           string  `json:"url" jsonschema:"writeOnly=true,minLength=1,maxLength=4096"`
	SigningSecret *string `json:"signingSecret,omitempty" jsonschema:"nullable,writeOnly=true,maxLength=65536" jsonschema_description:"省略或 null 保留当前签名密钥；新建通用 Webhook 时生成密钥并只显示一次；空字符串取消签名。"`
}

type NotifierCredentialsResult struct {
	OK            bool   `json:"ok"`
	Message       string `json:"message"`
	SigningSecret string `json:"signingSecret,omitempty" jsonschema_description:"仅首次生成的 Standard Webhooks 密钥显示一次，已有密钥不返回。"`
}

type Collector struct {
	ID            string               `json:"id"`
	Hostname      string               `json:"hostname"`
	Version       string               `json:"version"`
	LastSeen      int64                `json:"lastSeen"`
	Connected     bool                 `json:"connected"`
	Online        bool                 `json:"online"`
	ClockOffsetMS int64                `json:"clockOffsetMs" jsonschema_description:"监控器时间减采集器时间的估计毫秒数；正数表示采集器慢，负数表示快。"`
	Config        CollectorConfigState `json:"config" jsonschema_description:"最近一次配置下发/回执；未连接或未有回执时保守为 pending，version 是目标内容哈希。"`
}

type CollectorConfigState struct {
	State   string `json:"state" jsonschema:"enum=applied,enum=pending,enum=rejected,enum=outdated"`
	Version string `json:"version"`
	Message string `json:"message"`
}

type MatchedProcess struct {
	collector.ProcessInfo
	WorkDir string `json:"workDir,omitempty" jsonschema_description:"仅草稿包含 workDir 条件且读取成功时返回；普通进程列表不提供。"`
}

type TargetMatchResult struct {
	Collector   collector.ProcessCollector `json:"collector"`
	CollectedAt int64                      `json:"collectedAt"`
	Processes   []MatchedProcess           `json:"processes"`
	Truncated   bool                       `json:"truncated"`
	Reason      *string                    `json:"reason" jsonschema:"nullable"`
}

// Routes is shared by HTTP registration, OpenAPI, and later MCP authorization.
func Routes() []Route {
	period := []Parameter{{"from", "开始时间，UTC Unix 毫秒；默认一小时前"}, {"to", "结束时间，UTC Unix 毫秒；默认现在"}, {"step", "raw、1m 或 1h；省略时按时间范围选择"}}
	return []Route{
		{ListCollectors, "GET", "/api/v1/collectors", "采集器列表与在线状态", ScopeRead, nil, []Collector{}, nil},
		{ListProcesses, "GET", "/api/v1/collectors/{id}/processes", "从在线采集器最近快照列出进程，不含命令行，同一采集器两秒限一次", ScopeRead, nil, collector.ProcessList{}, []Parameter{{"name", "进程名包含关键词，不区分大小写；省略表示全部"}}},
		{TestTargetMatch, "POST", "/api/v1/collectors/{id}/processes/match", "在采集器本机用草稿 match 试匹配，不返回命令行，同一采集器两秒限一次", ScopeConfig, TargetMatchInput{}, TargetMatchResult{}, nil},
		{ValidateConfig, "POST", "/api/v1/config/validate", "只读校验磁盘配置或内存草稿覆盖后的整套配置，不写入或发布", ScopeRead, config.ValidationInput{}, config.ValidationResult{}, nil},
		{ListTargets, "GET", "/api/v1/targets", "目标当前状态", ScopeRead, nil, []store.TargetRecord{}, nil},
		{TargetHistory, "GET", "/api/v1/targets/{collector}/{target}/history", "目标状态、探测和进程指标历史", ScopeRead, nil, store.TargetHistoryResult{}, period},
		{Metrics, "GET", "/api/v1/metrics", "系统与脚本指标历史", ScopeRead, nil, store.MetricHistoryResult{}, append([]Parameter{{"collector", "采集器 ID；省略时查询全部"}, {"name", "指标名称；省略时查询全部"}}, period...)},
		{ListAlerts, "GET", "/api/v1/alerts", "告警列表", ScopeRead, nil, []rules.Alert{}, []Parameter{{"state", "firing、resolved、pending 或 all；默认 firing"}, {"since", "开始时间，UTC Unix 毫秒"}}},
		{GetNotificationPause, "GET", "/api/v1/notifications/pause", "读取全局通知暂停状态", ScopeRead, nil, store.NotificationPause{}, nil},
		{PutNotificationPause, "PUT", "/api/v1/notifications/pause", "暂停全部通知或修改结束时间，1 分钟到 24 小时", ScopeConfig, PauseInput{}, store.NotificationPause{}, nil},
		{DeleteNotificationPause, "DELETE", "/api/v1/notifications/pause", "恢复全部通知并登记持久化补发", ScopeConfig, nil, store.NotificationPause{}, nil},
		{AckAlert, "POST", "/api/v1/alerts/{id}/ack", "确认告警", ScopeConfig, nil, Confirmation{}, nil},
		{CreateSilence, "POST", "/api/v1/silences", "创建规则或目标静音", ScopeConfig, SilenceInput{}, store.Silence{}, nil},
		{DeleteSilence, "DELETE", "/api/v1/silences/{id}", "删除静音", ScopeConfig, nil, Confirmation{}, nil},
		{GetTargetsConfig, "GET", "/api/v1/config/targets", "读取目标配置", ScopeRead, nil, config.TargetsFile{}, nil},
		{PutTargetsConfig, "PUT", "/api/v1/config/targets", "校验并整体替换目标配置", ScopeConfig, config.TargetsFile{}, config.TargetsFile{}, nil},
		{GetRulesConfig, "GET", "/api/v1/config/rules", "读取规则配置", ScopeRead, nil, config.RulesFile{}, nil},
		{PutRulesConfig, "PUT", "/api/v1/config/rules", "校验并整体替换规则配置", ScopeConfig, config.RulesFile{}, config.RulesFile{}, nil},
		{GetNotifiersConfig, "GET", "/api/v1/config/notifiers", "读取通知配置，不含密钥", ScopeRead, nil, config.NotifiersFile{}, nil},
		{PutNotifiersConfig, "PUT", "/api/v1/config/notifiers", "校验并整体替换通知配置", ScopeConfig, config.NotifiersFile{}, config.NotifiersFile{}, nil},
		{GetSettingsConfig, "GET", "/api/v1/config/settings", "读取运行设置", ScopeRead, nil, config.Settings{}, nil},
		{PutSettingsConfig, "PUT", "/api/v1/config/settings", "校验并整体替换运行设置", ScopeAdmin, config.Settings{}, config.Settings{}, nil},
		{TestRule, "POST", "/api/v1/rules/{id}/test", "回放已保存或临时规则，同一时间只允许一个", ScopeRead, alternativeModels{ReplayInput{}, config.Rule{}}, rules.ReplayResult{}, nil},
		{TestNotifier, "POST", "/api/v1/notifiers/{id}/test", "发送测试通知，每个渠道间隔至少十秒", ScopeConfig, NotifierTestInput{}, Confirmation{}, nil},
		{PutNotifierCredentials, "POST", "/api/v1/notifiers/{id}/credentials", "创建或更新 Webhook 渠道凭据，新生成的签名密钥只显示一次", ScopeAdmin, NotifierCredentialsInput{}, NotifierCredentialsResult{}, nil},
		{FeishuCheck, "GET", "/api/v1/feishu/check", "只读检查飞书接入", ScopeConfig, nil, setup.Report{}, []Parameter{{"app", "配置中的应用 ID；仅一个应用时可省略"}}},
		{FeishuChats, "GET", "/api/v1/feishu/chats", "查询机器人所在的群", ScopeConfig, nil, setup.Step{}, []Parameter{{"app", "配置中的应用 ID；仅一个应用时可省略"}}},
		{FeishuTest, "POST", "/api/v1/feishu/test", "给机器人所在的任意群发送已保存或草稿测试卡片，同一目的地十秒限一次", ScopeConfig, FeishuTestInput{}, Confirmation{}, nil},
		{FeishuSave, "POST", "/api/v1/feishu/notifiers", "确认收件后合并保存群渠道，并可加入规则；服务端不维护收件确认状态", ScopeConfig, FeishuSaveInput{}, Confirmation{}, nil},
		{PutFeishuApp, "POST", "/api/v1/feishu/apps", "验证应用凭证并加密保存，Secret 只写", ScopeAdmin, FeishuAppInput{}, Confirmation{}, nil},
		{ListTemplates, "GET", "/api/v1/templates", "列出预设和自定义模板", ScopeRead, nil, []cards.Entry{}, nil},
		{GetTemplate, "GET", "/api/v1/templates/{id}", "读取模板", ScopeRead, nil, config.Template{}, nil},
		{PutTemplate, "PUT", "/api/v1/templates/{id}", "校验并保存自定义模板", ScopeConfig, config.Template{}, config.Template{}, nil},
		{DeleteTemplate, "DELETE", "/api/v1/templates/{id}", "删除自定义模板", ScopeConfig, nil, Confirmation{}, nil},
		{PreviewTemplate, "POST", "/api/v1/templates/{id}/preview", "渲染已保存或未保存模板，不发送", ScopeRead, alternativeModels{PreviewInput{}, config.Template{}}, cards.Result{}, nil},
		{Query, "POST", "/api/v1/query", "执行单条只读 SQL，最多五秒及一万行", ScopeRead, QueryInput{}, QueryResult{}, nil},
		{ListDashboards, "GET", "/api/v1/dashboards", "按 ID 列出本地声明式面板及加载错误", ScopeRead, nil, []DashboardSummary{}, nil},
		{GetDashboard, "GET", "/api/v1/dashboards/{id}", "读取带版本及各卡片错误的面板定义", ScopeRead, nil, DashboardDefinition{}, nil},
		{GetPanelData, "GET", "/api/v1/dashboards/{id}/panels/{panel}/data", "执行已加载卡片的只读查询，返回实际参数及版本", ScopeRead, nil, PanelData{}, []Parameter{{"range", "相对时长，例如 1h；以服务器时间计算"}, {"from", "固定开始时间，UTC Unix 毫秒"}, {"to", "固定结束时间，UTC Unix 毫秒"}, {"collector", "已登记采集器 ID；省略表示全部主机"}}},
		{Schema, "GET", "/api/v1/schema", "监控数据库实际表列及迁移注释说明", ScopeRead, nil, store.DatabaseSchema{}, nil},
		{Stream, "GET", "/api/v1/stream", "SSE 目标变化、告警和采集数据；断线后重新读取快照", ScopeRead, nil, json.RawMessage{}, nil},
		{OpenAPI, "GET", "/api/v1/openapi.json", "OpenAPI 3.1 接口说明", ScopeRead, nil, map[string]any{}, nil},
	}
}
