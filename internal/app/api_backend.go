package app

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/LFenX/lg-monitor/internal/api"
	"github.com/LFenX/lg-monitor/internal/config"
	"github.com/LFenX/lg-monitor/internal/feishu"
	"github.com/LFenX/lg-monitor/internal/feishu/setup"
	"github.com/LFenX/lg-monitor/internal/notify"
	"github.com/LFenX/lg-monitor/internal/notify/cards"
	"github.com/LFenX/lg-monitor/internal/rules"
	"github.com/LFenX/lg-monitor/internal/store"
)

type APIReader interface {
	Collector(context.Context, string) (store.CollectorRecord, bool, error)
	CurrentTargets(context.Context) ([]store.TargetRecord, error)
	AlertHistory(context.Context, string, int64) ([]rules.Alert, error)
	MetricHistory(context.Context, string, string, store.HistoryRange) (store.MetricHistoryResult, error)
	TargetHistory(context.Context, string, string, store.HistoryRange) (store.TargetHistoryResult, error)
	rules.ReplaySource
}

type SQLSource interface {
	Query(context.Context, string) (rules.QueryResult, error)
	Schema(context.Context) (store.DatabaseSchema, error)
}

type APIWriter interface {
	AcknowledgeAlert(context.Context, string) error
	AddSilence(context.Context, string, string, time.Duration, string) (store.Silence, error)
	RemoveSilence(context.Context, string) error
	notify.Recorder
}

type APIEditor interface {
	Replace(context.Context, string, []byte) (*config.Config, error)
	Update(context.Context, string, func([]byte) ([]byte, error)) (*config.Config, error)
	SaveTemplate(context.Context, string, []byte) (*config.Config, error)
	DeleteTemplate(context.Context, string) (*config.Config, error)
}

type APISecrets interface {
	Get(context.Context, string) ([]byte, error)
	Set(context.Context, string, []byte) error
}

type APIBackendOptions struct {
	Config         cards.TemplateSource
	Editor         APIEditor
	Read           APIReader
	SQL            SQLSource
	Write          APIWriter
	Collectors     rules.CollectorSource
	Secrets        APISecrets
	NewClient      ClientFactory
	NewWebhook     WebhookFactory
	NewJSONWebhook JSONWebhookFactory
	Entropy        io.Reader
	Version        string
	Waiter         notify.Waiter
	Toast          notify.ToastClient
	Now            func() time.Time
	TestNow        func() time.Time
	NewID          func() string
	Logger         *slog.Logger
}

type APIBackend struct {
	options APIBackendOptions
	testMu  sync.Mutex
	tests   map[string]time.Time
}
type apiSnapshot struct{ cfg *config.Config }

func (s apiSnapshot) Snapshot() *config.Config { return s.cfg }

func NewAPIBackend(opts APIBackendOptions) (*APIBackend, error) {
	if opts.Config == nil || opts.Editor == nil || opts.Read == nil || opts.SQL == nil || opts.Write == nil || opts.Collectors == nil || opts.Secrets == nil || opts.NewClient == nil || opts.Now == nil {
		return nil, fmt.Errorf("HTTP 业务接口缺少配置、读写存储、采集器、密钥、客户端或时钟依赖")
	}
	if opts.NewID == nil {
		opts.NewID = rand.Text
	}
	if opts.TestNow == nil {
		opts.TestNow = opts.Now
	}
	if opts.Entropy == nil {
		opts.Entropy = rand.Reader
	}
	if opts.Logger == nil {
		opts.Logger = slog.Default()
	}
	if opts.Toast == nil {
		opts.Toast = notify.NewToastClient(notify.ToastOptions{Logger: opts.Logger})
	}
	if opts.NewWebhook == nil {
		now := opts.Now
		opts.NewWebhook = func(endpoint, secret []byte) (feishu.WebhookClient, error) {
			return feishu.NewWebhookClient(endpoint, secret, feishu.WebhookOptions{Now: now})
		}
	}
	if opts.NewJSONWebhook == nil {
		now, version := opts.Now, opts.Version
		opts.NewJSONWebhook = func(endpoint, secret []byte) (notify.JSONWebhookClient, error) {
			return notify.NewJSONWebhookClient(endpoint, secret, notify.JSONWebhookOptions{Now: now, Version: version})
		}
	}
	return &APIBackend{options: opts, tests: make(map[string]time.Time)}, nil
}

func (b *APIBackend) Actions() map[api.Operation]api.Action {
	result := map[api.Operation]api.Action{}
	for _, route := range api.Routes() {
		if route.ID == api.Stream || route.ID == api.OpenAPI {
			continue
		}
		result[route.ID] = func(ctx context.Context, request api.Request) (any, error) {
			if route.ID != api.TestRule {
				var cancel context.CancelFunc
				budget := 15 * time.Second
				if route.ID == api.TestNotifier || route.ID == api.FeishuTest {
					budget = 90 * time.Second
				}
				ctx, cancel = context.WithTimeout(ctx, budget)
				defer cancel()
			}
			value, err := b.call(ctx, route.ID, request)
			if err != nil {
				var issues config.Errors
				if errors.As(err, &issues) {
					return nil, &api.Fault{Status: 422, Code: "invalid_config", Message: err.Error(), Cause: err}
				}
				if errors.Is(err, fs.ErrNotExist) || errors.Is(err, sql.ErrNoRows) {
					return nil, &api.Fault{Status: 404, Code: "not_found", Message: "请求的记录或配置不存在", Cause: err}
				}
				if errors.Is(err, store.ErrHistoryTooLarge) {
					return nil, &api.Fault{Status: 400, Code: "history_too_large", Message: err.Error(), Cause: err}
				}
			}
			return value, err
		}
	}
	return result
}

func invalidAPI(message string) error {
	return &api.Fault{Status: 400, Code: "invalid_request", Message: message}
}
func missingAPI(message string) error {
	return &api.Fault{Status: 404, Code: "not_found", Message: message}
}

func (b *APIBackend) call(ctx context.Context, op api.Operation, r api.Request) (any, error) {
	cfg := b.options.Config.Snapshot()
	switch op {
	case api.Schema:
		return b.options.SQL.Schema(ctx)
	case api.Query:
		var body api.QueryInput
		if err := api.DecodeBody(r.Body, &body); err != nil {
			return nil, err
		}
		result, err := b.options.SQL.Query(ctx, body.SQL)
		if err != nil {
			if errors.Is(err, context.DeadlineExceeded) {
				return nil, &api.Fault{Status: 408, Code: "query_timeout", Message: "只读 SQL 查询达到时间上限（最多 5 秒），请缩小查询", Cause: err}
			}
			if errors.Is(err, context.Canceled) {
				return nil, &api.Fault{Status: 408, Code: "query_cancelled", Message: "只读 SQL 查询已取消", Cause: err}
			}
			return nil, &api.Fault{Status: 400, Code: "invalid_query", Message: err.Error(), Cause: err}
		}
		return result, nil
	case api.ListCollectors:
		presences, err := b.options.Collectors.Collectors(ctx)
		if err != nil {
			return nil, err
		}
		result := []api.Collector{}
		for _, item := range presences {
			metadata, _, err := b.options.Read.Collector(ctx, item.ID)
			if err != nil {
				return nil, err
			}
			result = append(result, api.Collector{ID: item.ID, Hostname: item.Hostname, Version: metadata.Version, LastSeen: item.LastSeen, Connected: item.Connected, Online: item.Connected && b.options.Now().UnixMilli()-item.LastSeen <= 30000})
		}
		slices.SortFunc(result, func(a, c api.Collector) int { return strings.Compare(a.ID, c.ID) })
		return result, nil
	case api.ListTargets:
		return b.targets(ctx, cfg)
	case api.TargetHistory, api.Metrics:
		period, err := b.period(r)
		if err != nil {
			return nil, err
		}
		if op == api.TargetHistory {
			return b.options.Read.TargetHistory(ctx, r.Params["collector"], r.Params["target"], period)
		}
		return b.options.Read.MetricHistory(ctx, r.Query.Get("collector"), r.Query.Get("name"), period)
	case api.ListAlerts:
		state := r.Query.Get("state")
		if state == "" {
			state = rules.StateFiring
		}
		if !slices.Contains([]string{rules.StateFiring, rules.StateResolved, rules.StatePending, "all"}, state) {
			return nil, invalidAPI("state 必须为 firing、resolved、pending 或 all")
		}
		since, err := apiTimestamp(r.Query.Get("since"))
		if err != nil {
			return nil, err
		}
		return b.options.Read.AlertHistory(ctx, state, since)
	case api.AckAlert:
		if err := b.options.Write.AcknowledgeAlert(ctx, r.Params["id"]); err != nil {
			if errors.Is(err, store.ErrAlertUnavailable) {
				return nil, missingAPI("告警不存在或已恢复，请重新列出触发中告警")
			}
			return nil, err
		}
		return api.Confirmation{OK: true, Message: "告警已确认"}, nil
	case api.CreateSilence:
		var body api.SilenceInput
		if err := api.DecodeBody(r.Body, &body); err != nil {
			return nil, err
		}
		duration, err := body.For.Parse()
		if err != nil || duration <= 0 || (strings.TrimSpace(body.Rule) == "") == (strings.TrimSpace(body.Target) == "") || strings.TrimSpace(body.Reason) == "" || len(body.Reason) > 4096 {
			return nil, invalidAPI("静音须指定一个 rule 或 target、正数 for 和不超过 4096 字节的非空 reason")
		}
		if body.Rule != "" && !slices.ContainsFunc(cfg.Rules, func(item config.Rule) bool { return item.ID == body.Rule }) {
			return nil, missingAPI("指定的规则不存在")
		}
		if body.Target != "" && !slices.ContainsFunc(cfg.Targets, func(item config.Target) bool { return item.ID == body.Target }) {
			return nil, missingAPI("指定的目标不存在")
		}
		return b.options.Write.AddSilence(ctx, body.Rule, body.Target, duration, body.Reason)
	case api.DeleteSilence:
		if err := b.options.Write.RemoveSilence(ctx, r.Params["id"]); err != nil {
			return nil, err
		}
		return api.Confirmation{OK: true, Message: "静音已删除"}, nil
	case api.GetTargetsConfig:
		return config.TargetsFile{Targets: cfg.Targets}, nil
	case api.GetRulesConfig:
		return config.RulesFile{Rules: cfg.Rules}, nil
	case api.GetNotifiersConfig:
		return config.NotifiersFile{FeishuApps: cfg.FeishuApps, Notifiers: cfg.Notifiers}, nil
	case api.GetSettingsConfig:
		return cfg.Settings, nil
	case api.PutTargetsConfig, api.PutRulesConfig, api.PutNotifiersConfig, api.PutSettingsConfig:
		kind := map[api.Operation]string{api.PutTargetsConfig: "targets", api.PutRulesConfig: "rules", api.PutNotifiersConfig: "notifiers", api.PutSettingsConfig: "settings"}[op]
		if op == api.PutNotifiersConfig {
			principal, _ := api.CurrentPrincipal(ctx)
			if !principal.Scope.Allows(api.ScopeAdmin) {
				body, err := config.ParseNotifiers(r.Body)
				if err != nil {
					return nil, err
				}
				refs := map[string]bool{}
				for _, app := range cfg.FeishuApps {
					refs[app.SecretRef] = true
				}
				for _, app := range body.FeishuApps {
					if !refs[app.SecretRef] {
						return nil, &api.Fault{Status: 403, Code: "insufficient_scope", Message: "添加或更换密钥引用需要 admin 权限，请以管理员身份运行 lgmon token create --scope admin，并通过飞书应用接口保存凭证"}
					}
				}
				for _, channel := range body.Notifiers {
					if channel.Type != "feishu_webhook" && channel.Type != "webhook" {
						continue
					}
					found := false
					for _, old := range cfg.Notifiers {
						if old.ID == channel.ID && old.Type == channel.Type && old.URLRef == channel.URLRef && old.SecretRef == channel.SecretRef {
							found = true
						}
					}
					if !found {
						return nil, &api.Fault{Status: 403, Code: "insufficient_scope", Message: "添加或更换 Webhook 凭据引用需要 admin 权限，请通过渠道凭据接口保存地址和签名密钥"}
					}
				}
			}
		}
		updated, err := b.options.Editor.Replace(ctx, kind+".yaml", r.Body)
		if err != nil {
			return nil, err
		}
		switch op {
		case api.PutTargetsConfig:
			return config.TargetsFile{Targets: updated.Targets}, nil
		case api.PutRulesConfig:
			return config.RulesFile{Rules: updated.Rules}, nil
		case api.PutNotifiersConfig:
			return config.NotifiersFile{FeishuApps: updated.FeishuApps, Notifiers: updated.Notifiers}, nil
		default:
			return updated.Settings, nil
		}
	case api.TestRule:
		return b.replay(ctx, cfg, r)
	case api.TestNotifier:
		return b.testNotifier(ctx, cfg, r)
	case api.FeishuTest:
		return b.testFeishuChat(ctx, cfg, r)
	case api.FeishuSave:
		return b.saveFeishuChat(ctx, cfg, r)
	case api.FeishuCheck, api.FeishuChats:
		app, client, err := b.client(ctx, cfg, r.Query.Get("app"))
		if err != nil {
			return nil, err
		}
		defer client.Close()
		if op == api.FeishuCheck {
			return setup.Inspect(ctx, app, client), nil
		}
		return setup.QueryChats(ctx, client, "应用机器人"), nil
	case api.PutFeishuApp:
		return b.saveApp(ctx, cfg, r)
	case api.PutNotifierCredentials:
		return b.saveWebhook(ctx, cfg, r)
	case api.ListTemplates, api.GetTemplate, api.PutTemplate, api.DeleteTemplate, api.PreviewTemplate:
		return b.template(ctx, op, cfg, r)
	}
	return nil, fmt.Errorf("HTTP 业务操作 %s 未实现", op)
}

func (b *APIBackend) targets(ctx context.Context, cfg *config.Config) ([]store.TargetRecord, error) {
	items, err := b.options.Read.CurrentTargets(ctx)
	if err != nil {
		return nil, err
	}
	presences, err := b.options.Collectors.Collectors(ctx)
	if err != nil {
		return nil, err
	}
	byID := map[[2]string]store.TargetRecord{}
	for _, item := range items {
		byID[[2]string{item.CollectorID, item.TargetID}] = item
	}
	result := []store.TargetRecord{}
	for _, host := range presences {
		for _, target := range cfg.Targets {
			if !target.AppliesTo(host.ID, host.Hostname) {
				continue
			}
			item, ok := byID[[2]string{host.ID, target.ID}]
			if !ok {
				item = store.TargetRecord{CollectorID: host.ID, TargetID: target.ID, State: "unknown", Detail: json.RawMessage(`{"reason":"尚未收到目标采样"}`)}
			}
			result = append(result, item)
		}
	}
	slices.SortFunc(result, func(a, c store.TargetRecord) int {
		if a.CollectorID != c.CollectorID {
			return strings.Compare(a.CollectorID, c.CollectorID)
		}
		return strings.Compare(a.TargetID, c.TargetID)
	})
	return result, nil
}

func apiTimestamp(text string) (int64, error) {
	if text == "" {
		return 0, nil
	}
	value, err := strconv.ParseInt(text, 10, 64)
	if err != nil || value < 0 {
		return 0, invalidAPI("时间须为非负 UTC Unix 毫秒")
	}
	return value, nil
}

func (b *APIBackend) period(r api.Request) (store.HistoryRange, error) {
	result := store.HistoryRange{From: b.options.Now().Add(-time.Hour).UnixMilli(), To: b.options.Now().UnixMilli(), Step: r.Query.Get("step")}
	for _, field := range []struct {
		name        string
		destination *int64
	}{{"from", &result.From}, {"to", &result.To}} {
		if value := r.Query.Get(field.name); value != "" {
			parsed, err := apiTimestamp(value)
			if err != nil {
				return result, err
			}
			*field.destination = parsed
		}
	}
	if result.Step == "" {
		result.Step = "raw"
		if result.To-result.From > 24*time.Hour.Milliseconds() {
			result.Step = "1m"
		}
		if result.To-result.From > 14*24*time.Hour.Milliseconds() {
			result.Step = "1h"
		}
	}
	if err := result.Validate(); err != nil {
		return result, invalidAPI(err.Error())
	}
	return result, nil
}

func (b *APIBackend) replay(ctx context.Context, cfg *config.Config, r api.Request) (any, error) {
	var body api.ReplayInput
	if len(strings.TrimSpace(string(r.Body))) > 0 {
		if bareDraft(r.Body) {
			definition, err := config.ParseRule(r.Body, cfg)
			if err != nil {
				return nil, err
			}
			body.Rule = &definition
		} else if err := api.DecodeBody(r.Body, &body); err != nil {
			return nil, err
		}
	}
	var definition config.Rule
	if body.Rule != nil {
		data, err := json.Marshal(body.Rule)
		if err != nil {
			return nil, invalidAPI("规则定义无法编码")
		}
		definition, err = config.ParseRule(data, cfg)
		if err != nil {
			return nil, err
		}
	} else {
		for _, item := range cfg.Rules {
			if item.ID == r.Params["id"] {
				definition = item
				break
			}
		}
	}
	if definition.ID == "" {
		return nil, missingAPI("规则不存在，请提供已有规则 ID 或临时规则定义")
	}
	until := b.options.Now()
	if body.To != 0 {
		until = time.UnixMilli(body.To)
	}
	since := until.Add(-24 * time.Hour)
	if body.From != 0 {
		since = time.UnixMilli(body.From)
	}
	if body.From < 0 || body.To < 0 || !until.After(since) {
		return nil, invalidAPI("回放起点必须早于终点，且时间不能为负数")
	}
	result, err := rules.Replay(ctx, rules.ReplayOptions{Config: cfg, Rule: definition, Since: since, Until: until, Now: b.options.Now, Source: b.options.Read})
	if err != nil {
		return nil, invalidAPI(err.Error())
	}
	return result, nil
}

func bareDraft(body []byte) bool {
	var fields map[string]json.RawMessage
	return json.Unmarshal(body, &fields) == nil && fields["id"] != nil
}

func (b *APIBackend) renderer(cfg *config.Config) (*cards.Renderer, error) {
	return cards.New(apiSnapshot{cfg}, cards.Options{Now: b.options.Now, Logger: b.options.Logger})
}

func (b *APIBackend) template(ctx context.Context, op api.Operation, cfg *config.Config, r api.Request) (any, error) {
	renderer, err := b.renderer(cfg)
	if err != nil {
		return nil, err
	}
	id := r.Params["id"]
	if op != api.ListTemplates && !apiID.MatchString(id) {
		return nil, invalidAPI("模板 ID 必须由小写字母、数字或横线组成")
	}
	switch op {
	case api.ListTemplates:
		return renderer.List(), nil
	case api.GetTemplate:
		spec, err := renderer.Definition(id)
		if err != nil {
			return nil, missingAPI("指定的卡片模板不存在")
		}
		return spec, nil
	case api.DeleteTemplate:
		if slices.Contains([]string{"standard", "compact", "detailed", "digest"}, id) {
			return nil, invalidAPI("预设模板不能删除，请新建自定义模板")
		}
		if _, err := b.options.Editor.DeleteTemplate(ctx, id); err != nil {
			return nil, err
		}
		return api.Confirmation{OK: true, Message: "模板已删除"}, nil
	case api.PutTemplate:
		spec, draft, err := config.ParseTemplate(r.Body, cfg)
		if err != nil {
			return nil, err
		}
		if spec.ID != id {
			return nil, invalidAPI("模板 ID 必须与接口路径一致")
		}
		renderer, err = b.renderer(draft)
		if err != nil {
			return nil, err
		}
		if err := renderer.Check(id); err != nil {
			return nil, invalidAPI(err.Error())
		}
		if _, err := b.options.Editor.SaveTemplate(ctx, id, r.Body); err != nil {
			return nil, err
		}
		return spec, nil
	case api.PreviewTemplate:
		body := api.PreviewInput{State: rules.StateFiring}
		if len(strings.TrimSpace(string(r.Body))) > 0 {
			if bareDraft(r.Body) {
				spec, _, err := config.ParseTemplate(r.Body, cfg)
				if err != nil {
					return nil, err
				}
				body.Template = &spec
			} else if err := api.DecodeBody(r.Body, &body); err != nil {
				return nil, err
			}
		}
		if !slices.Contains([]string{rules.StateFiring, rules.StateResolved, "digest"}, body.State) {
			return nil, invalidAPI("预览 state 必须为 firing、resolved 或 digest")
		}
		if body.Template != nil {
			data, err := json.Marshal(body.Template)
			if err != nil {
				return nil, invalidAPI("模板草稿无法编码")
			}
			spec, draft, err := config.ParseTemplate(data, cfg)
			if err != nil {
				return nil, err
			}
			id = spec.ID
			renderer, err = b.renderer(draft)
			if err != nil {
				return nil, err
			}
		}
		if err := renderer.Check(id); err != nil {
			return nil, invalidAPI(err.Error())
		}
		input := cards.Sample(body.State)
		input.DashboardURL = cfg.Settings.PublicBaseURL
		return renderer.Render(id, input), nil
	}
	return nil, fmt.Errorf("未知模板操作")
}

func (b *APIBackend) client(ctx context.Context, cfg *config.Config, id string) (config.FeishuApp, feishu.Client, error) {
	var app config.FeishuApp
	for _, item := range cfg.FeishuApps {
		if item.ID == id || (id == "" && len(cfg.FeishuApps) == 1) {
			app = item
			break
		}
	}
	if app.ID == "" {
		return app, nil, invalidAPI("请选择已配置的飞书应用，多个应用时需要 app 参数")
	}
	secret, err := b.options.Secrets.Get(ctx, app.SecretRef)
	defer clear(secret)
	if err != nil {
		return app, nil, invalidAPI("无法读取飞书应用密钥，请由管理员重新保存凭证")
	}
	client, err := b.options.NewClient(app, secret)
	if err != nil {
		return app, nil, invalidAPI("创建飞书客户端失败，请检查应用配置")
	}
	return app, client, nil
}

func (b *APIBackend) testNotifier(ctx context.Context, cfg *config.Config, r api.Request) (any, error) {
	var body api.NotifierTestInput
	if len(strings.TrimSpace(string(r.Body))) > 0 {
		if err := api.DecodeBody(r.Body, &body); err != nil {
			return nil, err
		}
	}
	if err := validateTestFields(r.Body); err != nil {
		return nil, err
	}
	var channel config.Notifier
	for _, item := range cfg.Notifiers {
		if item.ID == r.Params["id"] {
			channel = item
			break
		}
	}
	if channel.ID == "" {
		return nil, missingAPI("指定的通知渠道不存在")
	}
	if body.Template != "" && body.Draft != nil {
		return nil, invalidAPI("template 和 draft 只能选择一个")
	}
	if channel.Type == "webhook" {
		if body.Template != "" || body.Draft != nil {
			return nil, invalidAPI("通用 Webhook 不使用卡片模板")
		}
		if err := b.allowTest("channel:" + channel.ID); err != nil {
			return nil, err
		}
		client, err := openJSONWebhook(ctx, b.options.Secrets, b.options.NewJSONWebhook, channel)
		if err != nil {
			return api.Confirmation{OK: false, Message: "无法读取通用 Webhook 凭据，请由管理员检查"}, nil
		}
		defer client.Close()
		input := cards.Sample("firing")
		input.Alert.ID, input.Alert.NotificationAt, input.DashboardURL = b.options.NewID(), b.options.Now().UnixMilli(), cfg.Settings.PublicBaseURL
		payload, err := json.Marshal(notify.WebhookPayload(input, b.options.Now(), true))
		if err != nil {
			return nil, err
		}
		sender, err := notify.NewWebhookSender(notify.WebhookSenderOptions{Client: client, Recorder: b.options.Write, Now: b.options.Now, Waiter: b.options.Waiter, Logger: b.options.Logger})
		if err != nil {
			return nil, err
		}
		err = sender.Send(ctx, notify.WebhookRequest{AlertID: input.Alert.ID, State: input.Alert.State, EventKey: "test:" + input.Alert.ID, NotifierID: channel.ID, Body: payload})
		if err != nil {
			return api.Confirmation{OK: false, Message: notify.UserMessage(err)}, nil
		}
		return api.Confirmation{OK: true, Message: "测试 Webhook 已提交，请在接收方确认实际收到"}, nil
	}
	if channel.Type == "toast" {
		if body.Template != "" || body.Draft != nil {
			return nil, invalidAPI("Windows 通知不使用飞书卡片模板")
		}
		if err := b.allowTest("channel:" + channel.ID); err != nil {
			return nil, err
		}
		sender, err := notify.NewToastSender(notify.ToastSenderOptions{Client: b.options.Toast, Recorder: b.options.Write, Now: b.options.Now})
		if err != nil {
			return nil, err
		}
		result, err := sender.Send(ctx, notify.ToastRequest{NotifierID: channel.ID, AlertID: b.options.NewID(), State: rules.StateFiring, Value: notify.Toast{Title: "lg-monitor 测试通知", Message: "这是一条测试通知，请确认 Windows 通知已显示。"}})
		if err != nil {
			return api.Confirmation{OK: false, Message: notify.UserMessage(err)}, nil
		}
		if result.Skipped {
			return api.Confirmation{OK: false, Message: "Windows 通知已跳过：" + result.Reason}, nil
		}
		return api.Confirmation{OK: true, Message: "Windows 通知已提交，请在当前登录会话中确认实际显示"}, nil
	}
	var client notify.CardSender
	var application config.FeishuApp
	var closeClient func() error
	var err error
	if channel.Type == "feishu_webhook" {
		var hook feishu.WebhookClient
		hook, err = openWebhook(ctx, b.options.Secrets, b.options.NewWebhook, channel)
		if err == nil {
			client, closeClient = hook, hook.Close
		}
	} else {
		var appClient feishu.Client
		application, appClient, err = b.client(ctx, cfg, channel.App)
		if err == nil {
			client, closeClient = appClient, appClient.Close
		}
	}
	if err != nil {
		return api.Confirmation{OK: false, Message: "无法读取通知渠道的凭据或配置，请由管理员检查"}, nil
	}
	defer closeClient()
	selected := body.Template
	if selected == "" && body.Draft == nil {
		selected = channel.Template
	}
	key := "channel:" + channel.ID
	if channel.Type == "feishu_app" {
		key = chatDestination(application, channel.ChatID)
	}
	return b.sendTest(ctx, cfg, client, application, feishu.Chat{ID: channel.ChatID}, channel.ID, key, api.FeishuTestInput{Template: selected, Draft: body.Draft})
}

func apiNotificationFailure(err error) string {
	var failure *feishu.Error
	if errors.As(err, &failure) {
		switch failure.Kind {
		case feishu.Credentials:
			return "飞书凭证无效，请由管理员重新保存"
		case feishu.MissingScopes:
			return "飞书应用缺少发送权限，请检查接入状态"
		case feishu.NotInChat:
			return "机器人不在接收群中，请先拉入群聊"
		case feishu.RateLimited:
			return "飞书请求过于频繁，请稍后重试"
		case feishu.CardRejected:
			return "飞书拒绝了测试卡片，请检查模板"
		}
	}
	return "飞书测试发送失败，请检查网络、应用配置和服务日志"
}

var apiID = regexp.MustCompile(`^[a-z0-9-]+$`)

func (b *APIBackend) saveApp(ctx context.Context, cfg *config.Config, r api.Request) (any, error) {
	var body api.FeishuAppInput
	if err := api.DecodeBody(r.Body, &body); err != nil {
		return nil, err
	}
	if !apiID.MatchString(body.ID) || !strings.HasPrefix(body.AppID, "cli_") || len(body.AppID) <= 4 || body.Secret == "" || !slices.Contains([]string{"feishu", "lark"}, body.Domain) {
		return nil, invalidAPI("请填写合法应用 ID、以 cli_ 开头的 App ID、非空 Secret，以及 feishu 或 lark 域名类型")
	}
	app := config.FeishuApp{ID: body.ID, AppID: body.AppID, Domain: body.Domain, SecretRef: "feishu-app-" + body.ID + "-" + b.options.NewID()}
	for _, old := range cfg.FeishuApps {
		if old.ID == app.ID {
			app.Proxy = old.Proxy
		}
	}
	secret := []byte(body.Secret)
	body.Secret = ""
	defer clear(secret)
	client, err := b.options.NewClient(app, secret)
	if err != nil {
		return api.Confirmation{OK: false, Message: "创建飞书客户端失败，请检查应用配置"}, nil
	}
	defer client.Close()
	if _, err := client.TenantToken(ctx); err != nil {
		return api.Confirmation{OK: false, Message: apiNotificationFailure(err)}, nil
	}
	if err := b.options.Secrets.Set(ctx, app.SecretRef, secret); err != nil {
		return nil, &api.Fault{Status: 500, Code: "secret_save_failed", Message: "加密保存应用密钥失败，请检查服务日志", Cause: err}
	}
	if _, err := b.options.Editor.Update(ctx, "notifiers.yaml", func(data []byte) ([]byte, error) { return setup.ApplicationConfig(data, app) }); err != nil {
		return nil, err
	}
	return api.Confirmation{OK: true, Message: "应用凭证已验证并加密保存"}, nil
}
