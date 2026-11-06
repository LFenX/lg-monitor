package api

import (
	"encoding/json"

	"github.com/LFenX/lg-monitor/internal/config"
	"github.com/LFenX/lg-monitor/internal/feishu/setup"
	"github.com/LFenX/lg-monitor/internal/notify/cards"
	"github.com/LFenX/lg-monitor/internal/rules"
	"github.com/LFenX/lg-monitor/internal/store"
)

type Operation string

type alternativeModels []any

const (
	ListCollectors     Operation = "list_collectors"
	ListTargets        Operation = "list_targets"
	TargetHistory      Operation = "target_history"
	Metrics            Operation = "metrics"
	ListAlerts         Operation = "list_alerts"
	AckAlert           Operation = "ack_alert"
	CreateSilence      Operation = "create_silence"
	DeleteSilence      Operation = "delete_silence"
	GetTargetsConfig   Operation = "get_targets_config"
	PutTargetsConfig   Operation = "put_targets_config"
	GetRulesConfig     Operation = "get_rules_config"
	PutRulesConfig     Operation = "put_rules_config"
	GetNotifiersConfig Operation = "get_notifiers_config"
	PutNotifiersConfig Operation = "put_notifiers_config"
	GetSettingsConfig  Operation = "get_settings_config"
	PutSettingsConfig  Operation = "put_settings_config"
	TestRule           Operation = "test_rule"
	TestNotifier       Operation = "test_notifier"
	FeishuCheck        Operation = "feishu_check"
	FeishuChats        Operation = "feishu_chats"
	PutFeishuApp       Operation = "put_feishu_app"
	ListTemplates      Operation = "list_templates"
	GetTemplate        Operation = "get_template"
	PutTemplate        Operation = "put_template"
	DeleteTemplate     Operation = "delete_template"
	PreviewTemplate    Operation = "preview_template"
	Query              Operation = "query"
	Schema             Operation = "schema"
	Stream             Operation = "stream"
	OpenAPI            Operation = "openapi"
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
	SQL string `json:"sql" jsonschema:"minLength=1,maxLength=262144"`
}

type SilenceInput struct {
	Rule   string          `json:"rule,omitempty"`
	Target string          `json:"target,omitempty"`
	For    config.Duration `json:"for"`
	Reason string          `json:"reason"`
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
	Template string `json:"template,omitempty"`
}

type FeishuAppInput struct {
	ID     string `json:"id"`
	AppID  string `json:"appId"`
	Domain string `json:"domain" jsonschema:"enum=feishu,enum=lark"`
	Secret string `json:"secret" jsonschema:"writeOnly=true,minLength=1"`
}

type Collector struct {
	ID        string `json:"id"`
	Hostname  string `json:"hostname"`
	Version   string `json:"version"`
	LastSeen  int64  `json:"lastSeen"`
	Connected bool   `json:"connected"`
	Online    bool   `json:"online"`
}

// Routes is shared by HTTP registration, OpenAPI, and later MCP authorization.
func Routes() []Route {
	period := []Parameter{{"from", "开始时间，UTC Unix 毫秒；默认一小时前"}, {"to", "结束时间，UTC Unix 毫秒；默认现在"}, {"step", "raw、1m 或 1h；省略时按时间范围选择"}}
	return []Route{
		{ListCollectors, "GET", "/api/v1/collectors", "采集器列表与在线状态", ScopeRead, nil, []Collector{}, nil},
		{ListTargets, "GET", "/api/v1/targets", "目标当前状态", ScopeRead, nil, []store.TargetRecord{}, nil},
		{TargetHistory, "GET", "/api/v1/targets/{collector}/{target}/history", "目标状态、探测和进程指标历史", ScopeRead, nil, store.TargetHistoryResult{}, period},
		{Metrics, "GET", "/api/v1/metrics", "系统与脚本指标历史", ScopeRead, nil, store.MetricHistoryResult{}, append([]Parameter{{"collector", "采集器 ID；省略时查询全部"}, {"name", "指标名称；省略时查询全部"}}, period...)},
		{ListAlerts, "GET", "/api/v1/alerts", "告警列表", ScopeRead, nil, []rules.Alert{}, []Parameter{{"state", "firing、resolved、pending 或 all；默认 firing"}, {"since", "开始时间，UTC Unix 毫秒"}}},
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
		{FeishuCheck, "GET", "/api/v1/feishu/check", "只读检查飞书接入", ScopeConfig, nil, setup.Report{}, []Parameter{{"app", "配置中的应用 ID；仅一个应用时可省略"}}},
		{FeishuChats, "GET", "/api/v1/feishu/chats", "查询机器人所在的群", ScopeConfig, nil, setup.Step{}, []Parameter{{"app", "配置中的应用 ID；仅一个应用时可省略"}}},
		{PutFeishuApp, "POST", "/api/v1/feishu/apps", "验证应用凭证并加密保存，Secret 只写", ScopeAdmin, FeishuAppInput{}, Confirmation{}, nil},
		{ListTemplates, "GET", "/api/v1/templates", "列出预设和自定义模板", ScopeRead, nil, []cards.Entry{}, nil},
		{GetTemplate, "GET", "/api/v1/templates/{id}", "读取模板", ScopeRead, nil, config.Template{}, nil},
		{PutTemplate, "PUT", "/api/v1/templates/{id}", "校验并保存自定义模板", ScopeConfig, config.Template{}, config.Template{}, nil},
		{DeleteTemplate, "DELETE", "/api/v1/templates/{id}", "删除自定义模板", ScopeConfig, nil, Confirmation{}, nil},
		{PreviewTemplate, "POST", "/api/v1/templates/{id}/preview", "渲染已保存或未保存模板，不发送", ScopeRead, alternativeModels{PreviewInput{}, config.Template{}}, cards.Result{}, nil},
		{Query, "POST", "/api/v1/query", "执行单条只读 SQL，最多五秒及一万行", ScopeRead, QueryInput{}, rules.QueryResult{}, nil},
		{Schema, "GET", "/api/v1/schema", "监控数据库实际表列及迁移注释说明", ScopeRead, nil, store.DatabaseSchema{}, nil},
		{Stream, "GET", "/api/v1/stream", "SSE 目标变化、告警和采集数据；断线后重新读取快照", ScopeRead, nil, json.RawMessage{}, nil},
		{OpenAPI, "GET", "/api/v1/openapi.json", "OpenAPI 3.1 接口说明", ScopeRead, nil, map[string]any{}, nil},
	}
}
