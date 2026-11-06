package setup

import (
	"context"
	"crypto/rand"
	"fmt"
	"slices"
	"strings"

	"github.com/LFenX/lg-monitor/internal/config"
	"github.com/LFenX/lg-monitor/internal/feishu"
	"github.com/LFenX/lg-monitor/internal/notify"
	"github.com/LFenX/lg-monitor/internal/notify/cards"
)

type Machine struct {
	opts   Options
	state  State
	client feishu.Client
}

func New(ctx context.Context, opts Options) (*Machine, error) {
	if opts.Checkpoints == nil || opts.Secrets == nil || opts.Configs == nil || opts.NewClient == nil || opts.NewDelivery == nil || opts.Templates == nil {
		return nil, fmt.Errorf("飞书向导缺少检查点、密钥、配置、客户端、发送器或模板依赖")
	}
	if opts.NewID == nil {
		opts.NewID = rand.Text
	}
	state, err := opts.Checkpoints.Load(ctx)
	if err != nil {
		return nil, fmt.Errorf("读取飞书向导进度失败：%w", err)
	}
	m := &Machine{opts: opts, state: state}
	if state.CredentialsOK {
		secret, err := opts.Secrets.Get(ctx, state.App.SecretRef)
		if err != nil {
			return nil, &privateError{message: "读取向导应用密钥失败，请重新保存该密钥", cause: err}
		}
		defer clear(secret)
		m.client, err = opts.NewClient(state.App, secret)
		if err != nil {
			return nil, &privateError{message: "恢复飞书客户端失败，请检查应用配置", cause: err}
		}
	}
	return m, nil
}

func (m *Machine) Close() error {
	if m.client != nil {
		return m.client.Close()
	}
	return nil
}

func (m *Machine) State() State {
	state := m.state
	if state.Chat.MemberCount != nil {
		count := *state.Chat.MemberCount
		state.Chat.MemberCount = &count
	}
	return state
}

func (m *Machine) commit(ctx context.Context, state State) error {
	state.Version = 1
	if err := m.opts.Checkpoints.Save(ctx, state); err != nil {
		return fmt.Errorf("保存飞书向导进度失败：%w", err)
	}
	m.state = state
	return nil
}

func (m *Machine) Credentials(ctx context.Context, app config.FeishuApp, secret []byte) (Step, error) {
	if !strings.HasPrefix(app.AppID, "cli_") || len(app.AppID) <= 4 || len(secret) == 0 || (app.Domain != "feishu" && app.Domain != "lark") || app.ID == "" || app.SecretRef == "" {
		return Step{Step: "credentials", Status: Error, Message: "App ID 须以 cli_ 开头，Secret 不能为空，域名类型须为 feishu 或 lark；" + CreateAppMessage, URL: appURL(app.Domain)}, nil
	}
	client, err := m.opts.NewClient(app, secret)
	if err != nil {
		return failure("credentials", err), nil
	}
	keep := false
	defer func() {
		if !keep {
			_ = client.Close()
		}
	}()
	if _, err := client.TenantToken(ctx); err != nil {
		return failure("credentials", err), nil
	}
	if err := m.opts.Secrets.Set(ctx, app.SecretRef, secret); err != nil {
		return Step{}, &privateError{message: "保存飞书应用密钥失败", cause: err}
	}
	if err := m.commit(ctx, State{App: app, CredentialsOK: true}); err != nil {
		return Step{}, err
	}
	if m.client != nil {
		_ = m.client.Close()
	}
	m.client, keep = client, true
	return Step{Step: "credentials", Status: OK, Message: "应用凭证有效，Secret 已加密保存"}, nil
}

func (m *Machine) Bot(ctx context.Context) (Step, error) {
	if m.client == nil {
		return Step{Step: "bot", Status: ActionRequired, Message: "请先填写应用凭证"}, nil
	}
	if m.state.BotOK {
		return Step{Step: "bot", Status: OK, Message: "已完成：机器人已启用「" + m.state.BotName + "」"}, nil
	}
	bot, err := m.client.BotInfo(ctx)
	if err != nil {
		return failure("bot", err), nil
	}
	if bot.ActivateStatus != 2 {
		return Step{Step: "bot", Status: ActionRequired, Message: BotMessage, URL: appURL(m.state.App.Domain)}, nil
	}
	state := m.state
	state.BotOK, state.BotName = true, bot.Name
	if err := m.commit(ctx, state); err != nil {
		return Step{}, err
	}
	return Step{Step: "bot", Status: OK, Message: "机器人已启用：「" + bot.Name + "」"}, nil
}

func (m *Machine) Permissions(ctx context.Context) (Step, error) {
	if m.client == nil || !m.state.BotOK {
		return Step{Step: "permissions", Status: ActionRequired, Message: "请先完成凭证和机器人检查"}, nil
	}
	if m.state.PermissionsOK {
		return Step{Step: "permissions", Status: OK, Message: "已完成：应用权限检查"}, nil
	}
	result := CheckPermissions(ctx, m.client, m.state.App)
	if result.Status == OK {
		state := m.state
		state.PermissionsOK = true
		if err := m.commit(ctx, state); err != nil {
			return Step{}, err
		}
	}
	return result, nil
}

func (m *Machine) Chats(ctx context.Context) Step {
	if m.client == nil || !m.state.PermissionsOK {
		return Step{Step: "chat", Status: ActionRequired, Message: "请先完成应用权限检查"}
	}
	return QueryChats(ctx, m.client, m.state.BotName)
}

func (m *Machine) Select(ctx context.Context, chatID, templateID string) (Step, error) {
	if !m.state.PermissionsOK {
		return Step{Step: "chat", Status: ActionRequired, Message: "请先完成应用权限检查"}, nil
	}
	chats := m.Chats(ctx)
	if chats.Status != OK {
		return chats, nil
	}
	index := slices.IndexFunc(chats.Chats, func(chat feishu.Chat) bool { return chat.ID == chatID })
	if index < 0 {
		return Step{Step: "chat", Status: ActionRequired, Message: "所选群不在机器人所在的群列表里，请先拉机器人入群"}, nil
	}
	if !slices.ContainsFunc(m.opts.Templates.List(), func(entry cards.Entry) bool { return entry.ID == templateID }) {
		return Step{Step: "template", Status: Error, Message: "所选模板不存在，请运行 template list 查看"}, nil
	}
	state := m.state
	if state.Chat.ID != chatID || state.Template != templateID {
		state.TestID, state.MessageID, state.Complete = "", "", false
	}
	state.Chat, state.Template = chats.Chats[index], templateID
	if err := m.commit(ctx, state); err != nil {
		return Step{}, err
	}
	return Step{Step: "chat", Status: OK, Message: "已选择告警群「" + state.Chat.Name + "」和模板 " + templateID}, nil
}

func (m *Machine) SendTest(ctx context.Context) (Step, error) {
	if m.client == nil || m.state.Chat.ID == "" || m.state.Template == "" {
		return Step{Step: "test", Status: ActionRequired, Message: "请先完成群和模板选择"}, nil
	}
	if m.state.MessageID != "" {
		return Step{Step: "test", Status: OK, Message: "已完成：测试卡片已发送，请确认群内是否收到", MessageID: m.state.MessageID}, nil
	}
	state := m.state
	if state.TestID == "" {
		state.TestID = "test-" + m.opts.NewID()
		if err := m.commit(ctx, state); err != nil {
			return Step{}, err
		}
	}
	delivery, err := m.opts.NewDelivery(m.client)
	if err != nil {
		return Step{}, &privateError{message: "准备测试消息发送器失败", cause: err}
	}
	card := m.opts.Templates.RenderTest(state.Template, state.TestID)
	sent, err := delivery.Send(ctx, notify.Request{
		Message:    feishu.CardMessage{AlertID: state.TestID, State: "firing", ChatID: state.Chat.ID, Card: card.JSON},
		NotifierID: NotifierID(state.App.ID), Template: card.Template,
	})
	if err != nil {
		step := failure("test", err)
		step.Message = notify.UserMessage(err)
		return step, nil
	}
	if sent.MessageID == "" {
		return Step{Step: "test", Status: Error, Message: "飞书发送响应没有消息 ID，尚不能确认成功"}, nil
	}
	state.MessageID = sent.MessageID
	if err := m.commit(ctx, state); err != nil {
		return Step{}, err
	}
	message := "测试卡片发送成功，请确认群内是否收到"
	if card.Warning != "" {
		message += "；" + card.Warning
	}
	return Step{Step: "test", Status: OK, Message: message, MessageID: sent.MessageID}, nil
}

func (m *Machine) Confirm(ctx context.Context, received bool) (Step, error) {
	if m.state.MessageID == "" || !received {
		return Step{Step: "test", Status: ActionRequired, Message: "尚未确认收到。请检查选群、机器人能力和应用发布状态，保留消息 ID 排查；可运行 feishu test 显式重发测试。", MessageID: m.state.MessageID}, nil
	}
	state := m.state
	notifier := config.Notifier{ID: NotifierID(state.App.ID), Type: "feishu_app", App: state.App.ID, ChatID: state.Chat.ID, ChatName: state.Chat.Name, Template: state.Template}
	if err := m.opts.Configs.SaveNotifier(ctx, state.App, notifier); err != nil {
		return Step{}, fmt.Errorf("保存飞书通知配置失败：%w", err)
	}
	state.Complete = true
	if err := m.commit(ctx, state); err != nil {
		return Step{}, err
	}
	return Step{Step: "complete", Status: OK, Message: "飞书通知配置已保存", MessageID: state.MessageID}, nil
}
