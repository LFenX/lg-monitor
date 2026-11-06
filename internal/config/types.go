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
	Targets    []Target    `json:"targets,omitempty"`
	Rules      []Rule      `json:"rules,omitempty"`
	FeishuApps []FeishuApp `json:"feishuApps,omitempty"`
	Notifiers  []Notifier  `json:"notifiers,omitempty"`
	Templates  []Template  `json:"templates,omitempty"`
	Settings   Settings    `json:"settings,omitempty"`

	sources       map[string]*yaml.Node
	templateFiles []string
}

type TargetsFile struct {
	Targets []Target `yaml:"targets,omitempty" json:"targets,omitempty"`
}

type RulesFile struct {
	Rules []Rule `yaml:"rules,omitempty" json:"rules,omitempty"`
}

type NotifiersFile struct {
	FeishuApps []FeishuApp `yaml:"feishuApps,omitempty" json:"feishuApps,omitempty"`
	Notifiers  []Notifier  `yaml:"notifiers,omitempty" json:"notifiers,omitempty"`
}

type Target struct {
	ID        string   `yaml:"id" json:"id" jsonschema:"minLength=1,pattern=^[a-z0-9-]+$"`
	Name      string   `yaml:"name,omitempty" json:"name,omitempty"`
	Collector string   `yaml:"collector,omitempty" json:"collector,omitempty" jsonschema:"minLength=1,maxLength=255,pattern=^(\\*|[^\\s*/\\\\:]+)$" jsonschema_description:"采集器 ID 或主机名；* 或省略表示全部采集器，主机名不区分大小写。"`
	Match     Match    `yaml:"match" json:"match"`
	Probes    []Probe  `yaml:"probes,omitempty" json:"probes,omitempty"`
	Tags      []string `yaml:"tags,omitempty" json:"tags,omitempty"`
}

type Match struct {
	Process string `yaml:"process,omitempty" json:"process,omitempty" jsonschema:"minLength=1"`
	Cmdline string `yaml:"cmdline,omitempty" json:"cmdline,omitempty" jsonschema:"minLength=1" jsonschema_description:"命令行正则默认不区分大小写；可用 (?-i) 显式区分大小写。"`
	Port    *int   `yaml:"port,omitempty" json:"port,omitempty" jsonschema:"minimum=1,maximum=65535"`
	Service string `yaml:"service,omitempty" json:"service,omitempty" jsonschema:"minLength=1"`
}

type Probe struct {
	Type         string   `yaml:"type" json:"type" jsonschema:"enum=http,enum=tcp,enum=script"`
	Run          string   `yaml:"run,omitempty" json:"run,omitempty" jsonschema:"minLength=1"`
	URL          string   `yaml:"url,omitempty" json:"url,omitempty" jsonschema:"minLength=1,pattern=^https?://"`
	Address      string   `yaml:"address,omitempty" json:"address,omitempty" jsonschema:"minLength=1"`
	Interval     Duration `yaml:"interval" json:"interval"`
	Timeout      Duration `yaml:"timeout" json:"timeout"`
	ExpectStatus Status   `yaml:"expectStatus,omitempty" json:"expectStatus,omitempty"`
}

type Rule struct {
	ID        string   `yaml:"id" json:"id" jsonschema:"minLength=1,pattern=^[a-z0-9-]+$"`
	Name      string   `yaml:"name,omitempty" json:"name,omitempty"`
	Type      string   `yaml:"type" json:"type" jsonschema:"enum=process_absent,enum=port_closed,enum=probe_failed,enum=collector_offline,enum=metric_threshold,enum=service_stopped,enum=script_failed,enum=sql"`
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
}

type FeishuApp struct {
	ID        string `yaml:"id" json:"id" jsonschema:"minLength=1,pattern=^[a-z0-9-]+$"`
	AppID     string `yaml:"appId" json:"appId" jsonschema:"minLength=1"`
	SecretRef string `yaml:"secretRef" json:"secretRef" jsonschema:"minLength=1"`
	Domain    string `yaml:"domain" json:"domain" jsonschema:"enum=feishu,enum=lark"`
	Proxy     string `yaml:"proxy,omitempty" json:"proxy,omitempty"`
}

type Notifier struct {
	ID       string `yaml:"id" json:"id" jsonschema:"minLength=1,pattern=^[a-z0-9-]+$"`
	Type     string `yaml:"type" json:"type" jsonschema:"enum=feishu_app"`
	App      string `yaml:"app" json:"app" jsonschema:"minLength=1"`
	ChatID   string `yaml:"chatId" json:"chatId" jsonschema:"minLength=1"`
	ChatName string `yaml:"chatName,omitempty" json:"chatName,omitempty"`
	Template string `yaml:"template,omitempty" json:"template,omitempty"`
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
