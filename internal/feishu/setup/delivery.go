package setup

import (
	"context"
	"fmt"

	"github.com/LFenX/lg-monitor/internal/config"
	"github.com/LFenX/lg-monitor/internal/feishu"
	"github.com/LFenX/lg-monitor/internal/notify"
	"github.com/LFenX/lg-monitor/internal/notify/cards"
)

type TestRenderer interface {
	RenderTestState(string, string, string, *config.Template) (cards.Result, error)
}

type TestOptions struct {
	Client                              notify.CardSender
	NewDelivery                         func(notify.CardSender) (Delivery, error)
	Templates                           TestRenderer
	App                                 config.FeishuApp
	Chat                                feishu.Chat
	Template, State, TestID, NotifierID string
	Draft                               *config.Template
	BeforeSend                          func() error
}

type TestResult struct {
	MessageID string
	Card      cards.Result
}

type TemplateError struct{ Err error }

func (e *TemplateError) Error() string { return "测试模板校验失败：" + e.Err.Error() }
func (e *TemplateError) Unwrap() error { return e.Err }

func SendTest(ctx context.Context, opts TestOptions) (TestResult, error) {
	if opts.Client == nil || opts.NewDelivery == nil || opts.Templates == nil || (opts.App.ID != "" && opts.Chat.ID == "") || opts.TestID == "" {
		return TestResult{}, fmt.Errorf("测试发送缺少客户端、发送器、模板、群或测试标识")
	}
	card, err := opts.Templates.RenderTestState(opts.Template, opts.TestID, opts.State, opts.Draft)
	if err != nil {
		return TestResult{}, &TemplateError{Err: err}
	}
	if opts.BeforeSend != nil {
		if err := opts.BeforeSend(); err != nil {
			return TestResult{}, err
		}
	}
	delivery, err := opts.NewDelivery(opts.Client)
	if err != nil {
		return TestResult{}, fmt.Errorf("准备测试发送器失败：%w", err)
	}
	state := opts.State
	if state == "" {
		state = "firing"
	}
	sent, err := delivery.Send(ctx, notify.Request{
		Message:    feishu.CardMessage{AlertID: opts.TestID, State: state, ChatID: opts.Chat.ID, Card: card.JSON},
		NotifierID: opts.NotifierID, Template: card.Template,
	})
	if err != nil {
		return TestResult{}, err
	}
	if opts.App.ID != "" && sent.MessageID == "" {
		return TestResult{}, fmt.Errorf("飞书发送响应没有消息 ID，尚不能确认成功")
	}
	return TestResult{MessageID: sent.MessageID, Card: card}, nil
}

func SaveChat(ctx context.Context, writer ConfigWriter, app config.FeishuApp, chat feishu.Chat, settings config.Notifier) error {
	if writer == nil || app.ID == "" || chat.ID == "" {
		return fmt.Errorf("保存群渠道缺少应用、群或配置写入器")
	}
	if settings.ID == "" {
		settings.ID = NotifierID(app.ID)
	}
	settings.Type, settings.App, settings.ChatID, settings.ChatName = "feishu_app", app.ID, chat.ID, chat.Name
	return writer.SaveNotifier(ctx, app, settings)
}
