package setup

import (
	"context"
	"errors"
	"strings"

	"github.com/LFenX/lg-monitor/internal/config"
	"github.com/LFenX/lg-monitor/internal/feishu"
	"github.com/LFenX/lg-monitor/internal/notify"
	"github.com/LFenX/lg-monitor/internal/notify/cards"
)

const (
	OK             = "ok"
	ActionRequired = "action_required"
	Error          = "error"
)

const CreateAppMessage = "打开 https://open.feishu.cn/app → 创建企业自建应用 → 填写名称和描述 → 凭证与基础信息 → 复制 App ID 和 App Secret"
const BotMessage = "开发者后台 → 应用能力 → 添加应用能力 → 机器人；启用后自动继续"
const PublishMessage = "新开通的权限通常要发布新版本才生效：开发者后台 → 版本管理与发布 → 创建版本 → 申请发布；企业开启审核时还需等待管理员审批。实际界面请在原型验收中核对。"

type Permission struct {
	Name        string `json:"name"`
	Required    bool   `json:"required"`
	Description string `json:"description"`
	Granted     bool   `json:"granted"`
}

func RequiredPermissions() []Permission {
	return []Permission{
		{Name: "im:message:send_as_bot", Required: true, Description: "以机器人身份往群里发告警卡片"},
		{Name: "im:chat:read", Required: true, Description: "读取机器人所在的群，这样不用你手动查群 ID"},
		{Name: "im:chat:create", Required: false, Description: "让机器人帮你新建一个告警群；当前向导只提供拉机器人入群"},
	}
}

type Step struct {
	Step        string        `json:"step"`
	Status      string        `json:"status"`
	Message     string        `json:"message"`
	URL         string        `json:"url,omitempty"`
	Permissions []Permission  `json:"permissions,omitempty"`
	Chats       []feishu.Chat `json:"chats,omitempty"`
	MessageID   string        `json:"messageId,omitempty"`
}

type Report struct {
	Steps   []Step `json:"steps"`
	BotName string `json:"botName,omitempty"`
}

type State struct {
	Version       int              `json:"version"`
	App           config.FeishuApp `json:"app"`
	BotName       string           `json:"botName"`
	Chat          feishu.Chat      `json:"chat"`
	Template      string           `json:"template"`
	CredentialsOK bool             `json:"credentialsOk"`
	BotOK         bool             `json:"botOk"`
	PermissionsOK bool             `json:"permissionsOk"`
	TestID        string           `json:"testId"`
	MessageID     string           `json:"messageId"`
	Complete      bool             `json:"complete"`
}

type Checkpoints interface {
	Load(context.Context) (State, error)
	Save(context.Context, State) error
}

type SecretStore interface {
	Set(context.Context, string, []byte) error
	Get(context.Context, string) ([]byte, error)
}

type ConfigWriter interface {
	SaveNotifier(context.Context, config.FeishuApp, config.Notifier) error
}

type ClientFactory func(config.FeishuApp, []byte) (feishu.Client, error)

type Delivery interface {
	Send(context.Context, notify.Request) (feishu.SendResult, error)
}

type DeliveryFactory func(feishu.Client) (Delivery, error)

type Templates interface {
	List() []cards.Entry
	RenderTestState(string, string, string, *config.Template) (cards.Result, error)
}

type Options struct {
	Checkpoints Checkpoints
	Secrets     SecretStore
	Configs     ConfigWriter
	NewClient   ClientFactory
	NewDelivery DeliveryFactory
	Templates   Templates
	NewID       func() string
}

type privateError struct {
	message string
	cause   error
}

func (e *privateError) Error() string { return e.message }
func (e *privateError) Unwrap() error { return e.cause }

func failure(step string, err error) Step {
	result := Step{Step: step, Status: Error, Message: "飞书检查失败，请检查网络、代理和应用配置"}
	var apiError *feishu.Error
	if errors.As(err, &apiError) {
		result.Message, result.URL = apiError.Message, apiError.URL
		if apiError.Kind == feishu.MissingScopes || apiError.Kind == feishu.BotDisabled || apiError.Kind == feishu.NotInChat {
			result.Status = ActionRequired
		}
	}
	return result
}

func applyURL(app config.FeishuApp, scopes []string) string {
	url := feishu.ApplyURL(app.AppID, scopes)
	if app.Domain == "lark" {
		url = strings.Replace(url, "https://open.feishu.cn", "https://open.larksuite.com", 1)
	}
	return url
}

func appURL(domain string) string {
	if domain == "lark" {
		return "https://open.larksuite.com/app"
	}
	return "https://open.feishu.cn/app"
}

func NotifierID(app string) string {
	if app == "default" {
		return "feishu-alerts"
	}
	return "feishu-" + app
}
