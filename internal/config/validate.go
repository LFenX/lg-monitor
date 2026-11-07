package config

import (
	"encoding/json"
	"fmt"
	"net"
	"net/url"
	"path"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

var idPattern = regexp.MustCompile(`^[a-z0-9-]+$`)
var statusPattern = regexp.MustCompile(`^([1-5][0-9]{2}|[1-5]xx|[<>]=?[1-5][0-9]{2})$`)
var collectorPattern = regexp.MustCompile(`^(\*|[^\s*/\\:]+)$`)

var presets = map[string]bool{"standard": true, "compact": true, "detailed": true, "digest": true}

type location struct {
	file string
	node *yaml.Node
}

func (l location) at(parts ...any) location {
	if node := nodeAt(l.node, parts...); node != nil {
		return location{file: l.file, node: node}
	}
	return l
}

func (l location) add(issues *Errors, format string, args ...any) {
	issues.add(l.file, l.node, fmt.Sprintf(format, args...), nil)
}

func Validate(cfg *Config) error {
	if cfg == nil {
		return fmt.Errorf("配置不能为空")
	}
	var issues Errors
	targets, apps, notifiers, templates := map[string]bool{}, map[string]bool{}, map[string]bool{}, map[string]bool{}
	targetSpecs := make(map[string]Target, len(cfg.Targets))
	for id := range presets {
		templates[id] = true
	}
	seen := make(map[string]bool)
	register := func(kind, id string, loc location, index map[string]bool) {
		if !idPattern.MatchString(id) {
			loc.at("id").add(&issues, "%s ID 必须由小写字母、数字或横线组成且不能为空", kind)
		}
		key := kind + ":" + id
		if seen[key] || (kind == "模板" && presets[id]) {
			loc.at("id").add(&issues, "%s %s 的 ID 重复，请修改 ID", kind, id)
		}
		seen[key], index[id] = true, true
	}
	root := func(file string) location { return location{file: file, node: cfg.sources[file]} }
	for _, issue := range cfg.Settings.validateHTTP() {
		root("settings.yaml").at(issue.field).add(&issues, "%s", issue.message)
	}
	if _, err := cfg.Settings.Retention.Effective(); err != nil {
		field := "raw"
		if strings.Contains(err.Error(), "分钟级") {
			field = "minute"
		}
		if strings.HasPrefix(err.Error(), "小时级") || strings.Contains(err.Error(), "retention.hour") {
			field = "hour"
		}
		if strings.Contains(err.Error(), "retention.minute") {
			field = "minute"
		}
		root("settings.yaml").at("retention", field).add(&issues, "%s", err)
	}
	templateLocation := func(i int) location {
		file := "card-templates/" + cfg.Templates[i].ID + ".yaml"
		if i < len(cfg.templateFiles) {
			file = cfg.templateFiles[i]
		}
		return root(file)
	}
	for i, target := range cfg.Targets {
		register("目标", target.ID, root("targets.yaml").at("targets", i), targets)
		targetSpecs[target.ID] = target
	}
	for i, app := range cfg.FeishuApps {
		register("飞书应用", app.ID, root("notifiers.yaml").at("feishuApps", i), apps)
	}
	for i, notifier := range cfg.Notifiers {
		register("通知渠道", notifier.ID, root("notifiers.yaml").at("notifiers", i), notifiers)
	}
	for i, template := range cfg.Templates {
		register("模板", template.ID, templateLocation(i), templates)
	}
	for i, target := range cfg.Targets {
		loc := root("targets.yaml").at("targets", i)
		if target.Collector != "" && (len(target.Collector) > 255 || !collectorPattern.MatchString(target.Collector)) {
			loc.at("collector").add(&issues, "目标 %s 的 collector 必须是采集器 ID、主机名或 *，最长 255 字节", target.ID)
		}
		match := target.Match
		if strings.TrimSpace(match.Process) == "" && strings.TrimSpace(match.Cmdline) == "" && match.Port == nil && strings.TrimSpace(match.Service) == "" {
			loc.at("match").add(&issues, "目标 %s 至少需要一个非空匹配条件：process、cmdline、port 或 service", target.ID)
		}
		if match.Cmdline != "" {
			if _, err := regexp.Compile("(?i)" + match.Cmdline); err != nil {
				issues.add(loc.file, loc.at("match", "cmdline").node, "命令行正则表达式无效，请检查括号和转义", err)
			}
		}
		if match.Port != nil && (*match.Port < 1 || *match.Port > 65535) {
			loc.at("match", "port").add(&issues, "目标 %s 的端口必须在 1 到 65535 之间", target.ID)
		}
		for j, probe := range target.Probes {
			probeLoc := loc.at("probes", j)
			switch probe.Type {
			case "http":
				if probe.Script != "" || len(probe.Args) != 0 {
					probeLoc.add(&issues, "HTTP 探测不接受 script 或 args")
				}
				if !validHTTPURL(probe.URL) {
					probeLoc.at("url").add(&issues, "HTTP 探测需要完整的 http 或 https URL")
				}
				if probe.Address != "" {
					probeLoc.at("address").add(&issues, "HTTP 探测请使用 url，不接受 address")
				}
				if probe.ExpectStatus != "" && !statusPattern.MatchString(string(probe.ExpectStatus)) {
					probeLoc.at("expectStatus").add(&issues, "expectStatus 必须为 100 到 599、2xx 等状态组或 <500 等比较条件")
				}
			case "tcp":
				if probe.Script != "" || len(probe.Args) != 0 {
					probeLoc.add(&issues, "TCP 探测不接受 script 或 args")
				}
				if !validTCPAddress(probe.Address) {
					probeLoc.at("address").add(&issues, "TCP 探测需要 address，格式为 主机:端口，IPv6 地址须使用方括号")
				}
				if probe.URL != "" || probe.ExpectStatus != "" {
					probeLoc.add(&issues, "TCP 探测不接受 url 或 expectStatus")
				}
			case "script":
				if err := scriptNameError(probe.Script); err != nil {
					probeLoc.at("script").add(&issues, "%s", err)
				}
				for k, arg := range probe.Args {
					if !scriptArgument.MatchString(arg) {
						probeLoc.at("args", k).add(&issues, "参数只允许字母、数字、空格和 _ . , : = / @ + -")
					}
				}
				if probe.URL != "" || probe.Address != "" || probe.ExpectStatus != "" {
					probeLoc.add(&issues, "脚本探测只接受 script 和 args，不接受 url、address 或 expectStatus")
				}
			default:
				probeLoc.at("type").add(&issues, "探测类型只支持 http、tcp 或 script")
			}
			checkDuration(&issues, probeLoc.at("interval"), probe.Interval, "探测 interval", true)
			checkDuration(&issues, probeLoc.at("timeout"), probe.Timeout, "探测 timeout", true)
		}
	}
	for i, rule := range cfg.Rules {
		loc := root("rules.yaml").at("rules", i)
		register("规则", rule.ID, loc, map[string]bool{})
		if rule.ID == DefaultOfflineRuleID && rule.Type != "collector_offline" {
			loc.at("id").add(&issues, "规则 ID collector-offline 保留给 collector_offline 类型")
		}
		if !oneOf(rule.Type, "process_absent", "port_closed", "probe_failed", "collector_offline", "metric_threshold", "service_stopped", "script_failed", "sql") {
			loc.at("type").add(&issues, "规则 %s 的类型只支持 process_absent、port_closed、probe_failed、collector_offline、metric_threshold、service_stopped、script_failed 或 sql", rule.ID)
		}
		if rule.Type == "collector_offline" {
			if rule.Target != "" || nodeAt(cfg.sources["rules.yaml"], "rules", i, "target") != nil {
				loc.at("target").add(&issues, "collector_offline 自动适用全部有效配对采集器，请删除 target")
			}
		} else if (rule.Target != "" || rule.NeedsTarget()) && !targets[rule.Target] {
			loc.at("target").add(&issues, "规则 %s 引用的目标 %s 不存在", rule.ID, rule.Target)
		} else {
			target := targetSpecs[rule.Target]
			if rule.Type == "process_absent" && target.Match.Process == "" && target.Match.Cmdline == "" {
				loc.at("type").add(&issues, "规则 %s 的类型是 process_absent，但目标 %s 没有配置 match.process 或 match.cmdline", rule.ID, rule.Target)
			}
			if rule.Type == "port_closed" && target.Match.Port == nil {
				loc.at("type").add(&issues, "规则 %s 的类型是 port_closed，但目标 %s 没有配置 match.port", rule.ID, rule.Target)
			}
			if rule.Type == "probe_failed" && len(target.Probes) == 0 {
				loc.at("type").add(&issues, "规则 %s 的类型是 probe_failed，但目标 %s 没有配置 probes", rule.ID, rule.Target)
			}
		}
		if err := rule.ValidateCondition(targetSpecs[rule.Target]); err != nil {
			loc.add(&issues, "规则 %s：%s", rule.ID, err)
		}
		if rule.For != "" || nodeAt(cfg.sources["rules.yaml"], "rules", i, "for") != nil {
			checkDuration(&issues, loc.at("for"), rule.For, "规则 for", false)
		}
		if !oneOf(rule.Severity, "critical", "warning", "info") {
			loc.at("severity").add(&issues, "规则 %s 的 severity 必须为 critical、warning 或 info", rule.ID)
		}
		if len(rule.Notify) == 0 {
			loc.at("notify").add(&issues, "规则 %s 至少需要一个通知渠道", rule.ID)
		}
		used := make(map[string]bool)
		for j, id := range rule.Notify {
			if !notifiers[id] {
				loc.at("notify", j).add(&issues, "规则 %s 引用的通知渠道 %s 不存在", rule.ID, id)
			}
			if used[id] {
				loc.at("notify", j).add(&issues, "规则 %s 重复引用通知渠道 %s", rule.ID, id)
			}
			used[id] = true
		}
		if rule.Template != "" && !templates[rule.Template] {
			loc.at("template").add(&issues, "规则 %s 引用的模板 %s 不存在", rule.ID, rule.Template)
		}
	}
	for i, app := range cfg.FeishuApps {
		loc := root("notifiers.yaml").at("feishuApps", i)
		if strings.TrimSpace(app.AppID) == "" {
			loc.at("appId").add(&issues, "飞书应用 %s 缺少 appId", app.ID)
		}
		if strings.TrimSpace(app.SecretRef) == "" {
			loc.at("secretRef").add(&issues, "飞书应用 %s 缺少 secretRef，请引用密钥库中的名称", app.ID)
		}
		if !oneOf(app.Domain, "feishu", "lark") {
			loc.at("domain").add(&issues, "飞书应用 %s 的 domain 必须为 feishu 或 lark", app.ID)
		}
		if app.Proxy != "" && !oneOf(app.Proxy, "auto", "none") && !validHTTPURL(app.Proxy) {
			loc.at("proxy").add(&issues, "代理必须为 auto、none 或完整的 HTTP 代理地址")
		}
	}
	for i, notifier := range cfg.Notifiers {
		loc := root("notifiers.yaml").at("notifiers", i)
		present := func(name string, value string) bool { return value != "" || nodeAt(loc.node, name) != nil }
		if !oneOf(notifier.Type, "feishu_app", "feishu_webhook", "webhook", "toast") {
			loc.at("type").add(&issues, "通知类型只支持 feishu_app、feishu_webhook、webhook 或 toast")
		}
		if notifier.Type == "feishu_app" && !apps[notifier.App] {
			loc.at("app").add(&issues, "通知渠道 %s 引用的飞书应用 %s 不存在", notifier.ID, notifier.App)
		}
		if notifier.Type == "feishu_app" && strings.TrimSpace(notifier.ChatID) == "" {
			loc.at("chatId").add(&issues, "通知渠道 %s 缺少 chatId，请通过飞书开通向导选择告警群", notifier.ID)
		}
		if notifier.Template != "" && !templates[notifier.Template] {
			loc.at("template").add(&issues, "通知渠道 %s 引用的模板 %s 不存在", notifier.ID, notifier.Template)
		}
		if notifier.Type == "toast" && (present("app", notifier.App) || present("chatId", notifier.ChatID) || present("chatName", notifier.ChatName) || present("template", notifier.Template)) {
			loc.add(&issues, "Windows 通知不使用飞书应用、群或卡片模板，请删除 app、chatId、chatName 和 template")
		}
		if oneOf(notifier.Type, "feishu_webhook", "webhook") {
			if strings.TrimSpace(notifier.URLRef) == "" {
				loc.at("urlRef").add(&issues, "Webhook 渠道 %s 缺少密钥库地址引用 urlRef，请由管理员保存凭据", notifier.ID)
			}
			if present("secretRef", notifier.SecretRef) && strings.TrimSpace(notifier.SecretRef) == "" {
				loc.at("secretRef").add(&issues, "签名密钥引用不能为空白字符")
			}
			if present("app", notifier.App) || present("chatId", notifier.ChatID) || present("chatName", notifier.ChatName) {
				loc.add(&issues, "自定义机器人不使用飞书应用或群标识，请删除 app、chatId 和 chatName")
			}
			if notifier.Type == "webhook" && present("template", notifier.Template) {
				loc.at("template").add(&issues, "通用 Webhook 发送告警 JSON，不使用卡片模板")
			}
			if strings.Contains(notifier.URLRef, "://") || strings.Contains(notifier.SecretRef, "://") {
				loc.add(&issues, "urlRef 和 secretRef 只保存密钥库引用，请勿填写明文 Webhook 地址")
			}
		} else if present("urlRef", notifier.URLRef) || present("secretRef", notifier.SecretRef) {
			loc.add(&issues, "只有 Webhook 渠道可以设置 urlRef 或 secretRef")
		}
		validateNotifierFilter(notifier.Filter, loc.at("filter"), &issues)
	}
	for i, rule := range cfg.Rules {
		for _, id := range rule.Notify {
			for _, notifier := range cfg.Notifiers {
				if notifier.ID == id && notifier.Filter != nil && notifier.Filter.Severity != nil && !slices.Contains(notifier.Filter.Severity, rule.Severity) {
					root("rules.yaml").at("rules", i, "notify").add(&issues, "规则 %s 的严重程度是 %s，但通知渠道 %s 只接收 %s，这条规则永远不会发到该渠道", rule.ID, rule.Severity, id, strings.Join(notifier.Filter.Severity, "、"))
				}
			}
		}
	}
	for i, template := range cfg.Templates {
		loc := templateLocation(i)
		for _, text := range templateTexts(template) {
			if err := validateTemplateText(text.value); err != nil {
				loc.at(text.path...).add(&issues, "%s", err)
			}
		}
		if template.ID != strings.TrimSuffix(path.Base(loc.file), path.Ext(loc.file)) {
			loc.at("id").add(&issues, "模板 ID 必须与文件名一致，请使用 %s.yaml", template.ID)
		}
		if (template.Extends == "") == (template.Raw == nil) {
			loc.add(&issues, "模板必须且只能选择 extends 或 raw 一种写法")
		}
		if template.Extends != "" && !presets[template.Extends] {
			loc.at("extends").add(&issues, "extends 必须引用 standard、compact、detailed 或 digest 预设")
		}
		if template.Raw != nil {
			switch raw := template.Raw.(type) {
			case string:
				if strings.TrimSpace(raw) == "" {
					loc.at("raw").add(&issues, "raw 不能为空")
				}
			case map[string]any:
			default:
				loc.at("raw").add(&issues, "raw 必须为卡片对象、状态对象或 JSON 模板字符串")
			}
			if _, err := json.Marshal(template.Raw); err != nil {
				issues.add(loc.file, loc.at("raw").node, "raw 包含无法转换为 JSON 的值", err)
			}
		}
		for severity, mention := range template.Mention {
			if !oneOf(severity, "critical", "warning", "info", "resolved") || !validMention(mention) {
				loc.at("mention", severity).add(&issues, "mention 的状态须为 critical、warning、info 或 resolved，值须为 all 或用户标识列表")
			}
		}
	}
	if len(issues) > 0 {
		return issues
	}
	return nil
}

func checkDuration(issues *Errors, loc location, value Duration, label string, positive bool) {
	parsed, err := value.Parse()
	if value == "" || err != nil || parsed < 0 || (positive && parsed == 0) {
		message := label + " 必须为非负合法时长，例如 0s 或 10s"
		if positive {
			message = label + " 必须为正数合法时长，例如 2s 或 5m"
		}
		issues.add(loc.file, loc.node, message, err)
	}
}

func validHTTPURL(text string) bool {
	u, err := url.Parse(text)
	if err != nil || !oneOf(u.Scheme, "http", "https") || u.Hostname() == "" || strings.ContainsAny(u.Host, " \t\r\n") {
		return false
	}
	if port := u.Port(); port != "" {
		number, err := strconv.Atoi(port)
		return err == nil && number >= 1 && number <= 65535
	}
	return true
}

func validTCPAddress(address string) bool {
	host, port, err := net.SplitHostPort(address)
	number, numberErr := strconv.Atoi(port)
	return err == nil && numberErr == nil && strings.TrimSpace(host) != "" && !strings.ContainsAny(host, " \t\r\n") && number >= 1 && number <= 65535
}

func oneOf(value string, options ...string) bool {
	for _, option := range options {
		if value == option {
			return true
		}
	}
	return false
}

func validMention(value any) bool {
	if text, ok := value.(string); ok {
		return text == "all"
	}
	values, ok := value.([]any)
	if !ok {
		return false
	}
	for _, item := range values {
		if text, ok := item.(string); !ok || strings.TrimSpace(text) == "" {
			return false
		}
	}
	return true
}
