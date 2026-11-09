package app

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"slices"
	"sync"
	"sync/atomic"
	"time"

	"github.com/LFenX/lg-monitor/internal/api"
	"github.com/LFenX/lg-monitor/internal/config"
	"github.com/LFenX/lg-monitor/internal/feishu"
	"github.com/LFenX/lg-monitor/internal/notify/cards"
	"github.com/LFenX/lg-monitor/internal/rules"
	"github.com/LFenX/lg-monitor/internal/store"
)

const cardActionKeyName = "lgmon-card-actions-hmac"

type CardActionRepository interface {
	CardNotification(context.Context, string, string, string) (store.CardNotification, error)
	CardActionAlert(context.Context, string) (rules.Alert, error)
	ApplyCardAction(context.Context, store.CardActionInput) (store.CardActionResult, error)
	Collector(context.Context, string) (store.CollectorRecord, bool, error)
}
type CardActionOptions struct {
	Config    *config.Manager
	Store     CardActionRepository
	Keys      APISecrets
	NewStream feishu.ActionStreamFactory
	Now       func() time.Time
	Random    io.Reader
	Logger    *slog.Logger
	OnAction  func(store.CardActionResult)
}
type CardActions struct {
	options CardActionOptions
	keyMu   sync.Mutex
	key     []byte
	closed  bool
	queries chan struct{}
	running atomic.Bool
	stateMu sync.Mutex
	states  map[string]api.CardActionsStatus
}

func NewCardActions(options CardActionOptions) (*CardActions, error) {
	if options.Config == nil || options.Store == nil || options.Keys == nil || options.Now == nil {
		return nil, fmt.Errorf("卡片动作需要配置、存储、受保护密钥库和时钟")
	}
	if options.Random == nil {
		options.Random = rand.Reader
	}
	if options.Logger == nil {
		options.Logger = slog.Default()
	}
	if options.NewStream == nil {
		options.NewStream = func(app config.FeishuApp, secret []byte) (feishu.ActionStream, error) {
			return feishu.NewActionStream(app, secret, feishu.ActionOptions{Logger: options.Logger})
		}
	}
	return &CardActions{options: options, queries: make(chan struct{}, 16), states: map[string]api.CardActionsStatus{}}, nil
}

func (m *CardActions) signingKey(ctx context.Context) ([]byte, error) {
	m.keyMu.Lock()
	defer m.keyMu.Unlock()
	if m.closed {
		return nil, fmt.Errorf("卡片动作已关闭")
	}
	if len(m.key) == 32 {
		return append([]byte(nil), m.key...), nil
	}
	key, err := m.options.Keys.Get(ctx, cardActionKeyName)
	if errors.Is(err, fs.ErrNotExist) {
		key = make([]byte, 32)
		if _, err = io.ReadFull(m.options.Random, key); err != nil {
			clear(key)
			return nil, fmt.Errorf("生成卡片动作签名密钥失败")
		}
		err = m.options.Keys.Set(ctx, cardActionKeyName, key)
	}
	if err != nil {
		clear(key)
		return nil, fmt.Errorf("读取或保存卡片动作签名密钥失败")
	}
	if len(key) != 32 {
		clear(key)
		return nil, fmt.Errorf("卡片动作签名密钥长度无效，请由管理员检查密钥库")
	}
	m.key = append([]byte(nil), key...)
	clear(key)
	return append([]byte(nil), m.key...), nil
}

func actionChannel(cfg *config.Config, id string) (config.Notifier, config.FeishuApp, bool) {
	for _, channel := range cfg.Notifiers {
		if channel.ID != id || channel.Type != "feishu_app" || !channel.CardActions {
			continue
		}
		for _, app := range cfg.FeishuApps {
			if app.ID == channel.App {
				return channel, app, true
			}
		}
	}
	return config.Notifier{}, config.FeishuApp{}, false
}
func sameActionApp(first, second config.FeishuApp) bool {
	return first.ID == second.ID && first.AppID == second.AppID && first.Domain == second.Domain
}

func (m *CardActions) routeEligible(ctx context.Context, cfg *config.Config, channel config.Notifier, alert rules.Alert) bool {
	eligible := slices.ContainsFunc(cfg.Rules, func(rule config.Rule) bool {
		return rule.ID == alert.RuleID && slices.Contains(rule.Notify, channel.ID)
	})
	if alert.TargetID == rules.CollectorTargetID {
		eligible = slices.ContainsFunc(config.OfflineRules(cfg), func(rule config.Rule) bool {
			return rule.ID == alert.RuleID && slices.Contains(rule.Notify, channel.ID)
		})
	}
	if !eligible {
		return false
	}
	var tags []string
	for _, target := range cfg.Targets {
		if target.ID == alert.TargetID {
			tags = target.Tags
			break
		}
	}
	host := ""
	if metadata, found, err := m.options.Store.Collector(ctx, alert.CollectorID); err == nil && found {
		host = metadata.Hostname
	} else if err != nil {
		return false
	}
	return channel.Filter.Match(alert.Severity, alert.CollectorID, host, tags)
}

func (m *CardActions) CardsFor(ctx context.Context, app config.FeishuApp, channel config.Notifier, alert rules.Alert) ([]cards.CallbackAction, error) {
	if m == nil || !channel.CardActions || channel.Type != "feishu_app" || alert.State != rules.StateFiring {
		return nil, nil
	}
	cfg := m.options.Config.Snapshot()
	current, currentApp, enabled := actionChannel(cfg, channel.ID)
	if !enabled || !sameActionApp(app, currentApp) || current.ChatID != channel.ChatID || current.App != channel.App {
		return nil, nil
	}
	stored, err := m.options.Store.CardActionAlert(ctx, alert.ID)
	if err != nil || stored.State != rules.StateFiring || !m.routeEligible(ctx, cfg, current, stored) {
		return nil, nil
	}
	key, err := m.signingKey(ctx)
	if err != nil {
		return nil, err
	}
	defer clear(key)
	values := make([]cards.CallbackAction, 0, 2)
	for _, action := range []struct{ operation, text string }{{"ack", "确认"}, {"silence_1h", "静音 1 小时"}} {
		value, err := feishu.SignCardAction(key, currentApp, current, alert.ID, alert.NotificationKey, action.operation)
		if err != nil {
			return nil, err
		}
		data, err := json.Marshal(value)
		if err != nil {
			return nil, fmt.Errorf("编码卡片动作失败：%w", err)
		}
		values = append(values, cards.CallbackAction{Text: action.text, Value: data})
	}
	return values, nil
}

func (m *CardActions) Handle(parent context.Context, source config.FeishuApp, event feishu.CardAction) feishu.CardActionReply {
	deny := func() feishu.CardActionReply {
		return feishu.CardActionReply{Type: "error", Message: "无权操作这条卡片，请从当前已启用通知群的原消息重试"}
	}
	if event.AppID != source.AppID || event.EventID == "" || event.OperatorID == "" || event.ChatID == "" || event.MessageID == "" {
		return deny()
	}
	ctx, cancel := context.WithTimeout(parent, 2*time.Second)
	defer cancel()
	select {
	case m.queries <- struct{}{}:
		defer func() { <-m.queries }()
	default:
		return feishu.CardActionReply{Type: "warning", Message: "卡片动作繁忙，请稍后重试"}
	}
	value, err := feishu.ParseCardActionValue(event.Value)
	if err != nil {
		return deny()
	}
	binding, err := m.options.Store.CardNotification(ctx, event.MessageID, value.AlertID, value.EventKey)
	if err != nil {
		return deny()
	}
	cfg := m.options.Config.Snapshot()
	channel, app, enabled := actionChannel(cfg, binding.NotifierID)
	if !enabled || source.AppID != app.AppID || source.Domain != app.Domain || channel.ChatID != event.ChatID {
		return deny()
	}
	alert, err := m.options.Store.CardActionAlert(ctx, value.AlertID)
	if err != nil {
		return feishu.CardActionReply{Type: "info", Message: "告警已不存在，请查看最新告警状态"}
	}
	if !m.routeEligible(ctx, cfg, channel, alert) {
		return deny()
	}
	key, err := m.signingKey(ctx)
	if err != nil {
		return feishu.CardActionReply{Type: "error", Message: "动作签名密钥不可用，请联系监控器管理员"}
	}
	defer clear(key)
	if !feishu.VerifyCardAction(key, app, channel, value) {
		return deny()
	}
	result, err := m.options.Store.ApplyCardAction(ctx, store.CardActionInput{AppID: app.ID, EventID: event.EventID, OperatorHash: feishu.CardOperatorHash(key, app.ID, event.OperatorID), Operation: value.Operation, Notification: binding})
	if errors.Is(err, store.ErrCardActionBinding) {
		return deny()
	}
	if errors.Is(err, store.ErrAlertUnavailable) {
		return feishu.CardActionReply{Type: "info", Message: "告警已恢复或已关闭，本次没有改变状态"}
	}
	if err != nil {
		return feishu.CardActionReply{Type: "error", Message: "动作尚未确认完成，请刷新告警状态后重试"}
	}
	if result.Duplicate {
		return feishu.CardActionReply{Type: "info", Message: "这次操作已经处理，请以最新告警状态为准"}
	}
	if m.options.OnAction != nil {
		m.options.OnAction(result)
	}
	if value.Operation == "ack" {
		return feishu.CardActionReply{Type: "success", Message: "已确认该告警，恢复时仍会通知"}
	}
	return feishu.CardActionReply{Type: "success", Message: "已将该告警组静音 1 小时，其他主机、目标和规则不受影响"}
}

func (m *CardActions) closeSigningKey() {
	m.keyMu.Lock()
	defer m.keyMu.Unlock()
	m.closed = true
	clear(m.key)
	m.key = nil
}
