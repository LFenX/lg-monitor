package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"time"

	"github.com/LFenX/lg-monitor/internal/api"
	"github.com/LFenX/lg-monitor/internal/config"
	"github.com/LFenX/lg-monitor/internal/feishu"
	"github.com/LFenX/lg-monitor/internal/feishu/setup"
	"github.com/LFenX/lg-monitor/internal/notify"
)

func (b *APIBackend) allowTest(key string) error {
	b.testMu.Lock()
	defer b.testMu.Unlock()
	now := b.options.TestNow()
	if last, found := b.tests[key]; found && now.Sub(last) < 10*time.Second {
		return &api.Fault{Status: 429, Code: "notification_rate_limited", Message: "同一个目的地每十秒只能发送一次测试，请稍后重试"}
	}
	for key, last := range b.tests {
		if now.Sub(last) >= 10*time.Second {
			delete(b.tests, key)
		}
	}
	b.tests[key] = now
	return nil
}

func chatDestination(app config.FeishuApp, chat string) string {
	return "feishu\x00" + app.Domain + "\x00" + app.AppID + "\x00" + chat
}

func validateTestFields(body []byte) error {
	var fields map[string]json.RawMessage
	if len(bytes.TrimSpace(body)) == 0 {
		return nil
	}
	if err := json.Unmarshal(body, &fields); err != nil {
		return invalidAPI("测试请求必须是 JSON 对象")
	}
	_, hasTemplate := fields["template"]
	if raw, present := fields["draft"]; present {
		if hasTemplate {
			return invalidAPI("template 和 draft 只能选择一个")
		}
		if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
			return invalidAPI("draft 必须是有效的模板定义，不能为 null")
		}
	}
	return nil
}

func (b *APIBackend) sendTest(ctx context.Context, cfg *config.Config, client notify.CardSender, app config.FeishuApp, chat feishu.Chat, notifier, key string, input api.FeishuTestInput) (any, error) {
	renderer, err := b.renderer(cfg)
	if err != nil {
		return nil, err
	}
	sent, err := setup.SendTest(ctx, setup.TestOptions{
		Client: client, App: app, Chat: chat, Templates: renderer, Draft: input.Draft,
		Template: input.Template, State: input.State, TestID: b.options.NewID(), NotifierID: notifier,
		BeforeSend: func() error { return b.allowTest(key) },
		NewDelivery: func(client notify.CardSender) (setup.Delivery, error) {
			return notify.NewSender(notify.Options{Client: client, Recorder: b.options.Write, Now: b.options.Now, Waiter: b.options.Waiter, Logger: b.options.Logger})
		},
	})
	if err != nil {
		var fault *api.Fault
		var rendering *setup.TemplateError
		if errors.As(err, &fault) {
			return nil, err
		}
		if errors.As(err, &rendering) {
			var issues config.Errors
			if errors.As(err, &issues) {
				return nil, err
			}
			return nil, invalidAPI(err.Error())
		}
		if app.ID == "" {
			return api.Confirmation{OK: false, Message: notify.UserMessage(err)}, nil
		}
		return api.Confirmation{OK: false, Message: apiNotificationFailure(err)}, nil
	}
	message := "测试卡片已发送，请在飞书群中确认实际收到"
	if sent.Card.Warning != "" {
		message += "；" + sent.Card.Warning
	}
	return api.Confirmation{OK: true, Message: message}, nil
}

func (b *APIBackend) testFeishuChat(ctx context.Context, cfg *config.Config, request api.Request) (any, error) {
	var input api.FeishuTestInput
	if err := api.DecodeBody(request.Body, &input); err != nil {
		return nil, err
	}
	if err := validateTestFields(request.Body); err != nil {
		return nil, err
	}
	if input.ChatID == "" {
		return nil, invalidAPI("请选择接收测试卡片的群")
	}
	if input.Template != "" && input.Draft != nil {
		return nil, invalidAPI("template 和 draft 只能选择一个")
	}
	if input.State != "" && input.State != "firing" && input.State != "resolved" {
		return nil, invalidAPI("state 必须是 firing 或 resolved")
	}
	app, client, err := b.client(ctx, cfg, input.App)
	if err != nil {
		return nil, err
	}
	defer client.Close()
	return b.sendTest(ctx, cfg, client, app, feishu.Chat{ID: input.ChatID}, "test:"+app.ID+":"+input.ChatID, chatDestination(app, input.ChatID), input)
}

type chatConfigWriter func(context.Context, config.FeishuApp, config.Notifier) error

func (f chatConfigWriter) SaveNotifier(ctx context.Context, app config.FeishuApp, channel config.Notifier) error {
	return f(ctx, app, channel)
}

func (b *APIBackend) saveFeishuChat(ctx context.Context, cfg *config.Config, request api.Request) (any, error) {
	var input api.FeishuSaveInput
	if err := api.DecodeBody(request.Body, &input); err != nil {
		return nil, err
	}
	var fields map[string]json.RawMessage
	_ = json.Unmarshal(request.Body, &fields)
	if raw, found := fields["filter"]; found && bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return nil, invalidAPI("filter 不能为 null，请省略或填写有效筛选条件")
	}
	if input.ChatID == "" {
		return nil, invalidAPI("请选择要保存的群")
	}
	for _, id := range input.Rules {
		if !slices.ContainsFunc(cfg.Rules, func(rule config.Rule) bool { return rule.ID == id }) {
			return nil, invalidAPI("所选规则已不存在，请刷新后重试")
		}
	}
	app, client, err := b.client(ctx, cfg, input.App)
	if err != nil {
		return nil, err
	}
	defer client.Close()
	chats, err := client.ListChats(ctx)
	if err != nil {
		return nil, &api.Fault{Status: 502, Code: "feishu_lookup_failed", Message: apiNotificationFailure(err), Cause: err}
	}
	index := slices.IndexFunc(chats, func(chat feishu.Chat) bool { return chat.ID == input.ChatID })
	if index < 0 {
		return nil, &api.Fault{Status: 409, Code: "chat_changed", Message: "机器人已不在这个群中，请重新检查并选择群"}
	}
	chat := chats[index]
	if detail, err := client.ChatInfo(ctx, chat.ID); err == nil && detail.Name != "" {
		chat.Name = detail.Name
	}
	var saved config.Notifier
	writer := chatConfigWriter(func(ctx context.Context, app config.FeishuApp, _ config.Notifier) error {
		_, err := b.options.Editor.Update(ctx, "notifiers.yaml", func(current []byte) ([]byte, error) {
			file, err := config.ParseNotifiers(current)
			if err != nil {
				return nil, err
			}
			latest := slices.IndexFunc(file.FeishuApps, func(value config.FeishuApp) bool { return value.ID == app.ID })
			if latest < 0 || file.FeishuApps[latest] != app {
				return nil, &api.Fault{Status: 409, Code: "app_changed", Message: "应用配置已变化，请重新检查群后保存"}
			}
			saved = config.Notifier{Type: "feishu_app", App: app.ID, ChatID: chat.ID, ChatName: chat.Name, Template: "standard"}
			found := false
			for _, channel := range file.Notifiers {
				if channel.Type == "feishu_app" && channel.App == app.ID && channel.ChatID == chat.ID {
					saved = channel
					found = true
					break
				}
			}
			if !found {
				base := setup.NotifierID(app.ID)
				for suffix := 1; ; suffix++ {
					saved.ID = base
					if suffix > 1 {
						saved.ID += "-" + strconv.Itoa(suffix)
					}
					if !slices.ContainsFunc(file.Notifiers, func(channel config.Notifier) bool { return channel.ID == saved.ID }) {
						break
					}
				}
			}
			saved.ChatName = chat.Name
			if input.Template != nil {
				saved.Template = *input.Template
			}
			if input.Filter != nil {
				saved.Filter = input.Filter
			}
			updated, err := setup.NotifierConfig(current, app, saved)
			if err != nil {
				return nil, err
			}
			if err := guardCardActionGrants(current, updated); err != nil {
				return nil, err
			}
			return updated, nil
		})
		return err
	})
	if err := setup.SaveChat(ctx, writer, app, chat, config.Notifier{}); err != nil {
		return nil, err
	}
	if len(input.Rules) > 0 {
		if _, err := b.options.Editor.Update(ctx, "rules.yaml", func(data []byte) ([]byte, error) { return config.AddRuleNotifier(data, input.Rules, saved.ID) }); err != nil {
			return api.Confirmation{OK: false, Message: fmt.Sprintf("群已保存，但没能加进规则：%s。请在规则通知设置中手动添加这个群", err)}, nil
		}
	}
	return api.Confirmation{OK: true, Message: "群通知渠道已保存"}, nil
}
