package app

import (
	"context"
	"errors"
	"strings"

	"github.com/LFenX/lg-monitor/internal/api"
	"github.com/LFenX/lg-monitor/internal/config"
	"github.com/LFenX/lg-monitor/internal/feishu"
	"github.com/LFenX/lg-monitor/internal/notify"
)

func (b *APIBackend) saveWebhook(ctx context.Context, cfg *config.Config, request api.Request) (any, error) {
	var body api.NotifierCredentialsInput
	if err := api.DecodeBody(request.Body, &body); err != nil {
		return nil, err
	}
	id := request.Params["id"]
	kind := body.Type
	for _, channel := range cfg.Notifiers {
		if channel.ID == id && kind == "" {
			kind = channel.Type
		}
	}
	if kind == "webhook" {
		return b.saveGenericWebhook(ctx, id, body)
	}
	if !apiID.MatchString(id) || (body.Type != "" && body.Type != "feishu_webhook") || len(body.URL) > 4096 || strings.TrimSpace(body.URL) == "" {
		return nil, invalidAPI("请填写合法渠道编号、feishu_webhook 类型和完整的机器人地址")
	}
	for _, channel := range cfg.Notifiers {
		if channel.ID == id && channel.Type != "feishu_webhook" {
			return nil, invalidAPI("指定渠道不是飞书 Webhook，请使用新的渠道编号")
		}
	}
	endpoint := []byte(body.URL)
	body.URL = ""
	defer clear(endpoint)
	if err := feishu.ValidateWebhookURL(endpoint); err != nil {
		return nil, invalidAPI(err.Error())
	}
	if body.SigningSecret != nil && len(*body.SigningSecret) > 65536 {
		return nil, invalidAPI("Webhook 签名密钥不能超过 64 KiB")
	}
	suffix := b.options.NewID()
	if suffix == "" {
		return nil, &api.Fault{Status: 500, Code: "secret_save_failed", Message: "生成 Webhook 凭据引用失败，请稍后重试"}
	}
	urlRef := "webhook-url-" + id + "-" + suffix
	if err := b.options.Secrets.Set(ctx, urlRef, endpoint); err != nil {
		return nil, &api.Fault{Status: 500, Code: "secret_save_failed", Message: "加密保存 Webhook 地址失败，请检查服务日志", Cause: err}
	}
	var secretRef *string
	if body.SigningSecret != nil {
		value := []byte(*body.SigningSecret)
		*body.SigningSecret = ""
		defer clear(value)
		name := ""
		secretRef = &name
		if len(value) > 0 {
			name = "webhook-sign-" + id + "-" + suffix
			if err := b.options.Secrets.Set(ctx, name, value); err != nil {
				return nil, &api.Fault{Status: 500, Code: "secret_save_failed", Message: "加密保存 Webhook 签名密钥失败，请检查服务日志", Cause: err}
			}
		}
	}
	_, err := b.options.Editor.Update(ctx, "notifiers.yaml", func(data []byte) ([]byte, error) {
		updated, err := config.WebhookConfig(data, id, "feishu_webhook", urlRef, secretRef)
		if err != nil {
			var issues config.Errors
			if errors.As(err, &issues) {
				return nil, err
			}
			return nil, invalidAPI(err.Error())
		}
		if err := guardCardActionGrants(data, updated); err != nil {
			return nil, err
		}
		return updated, nil
	})
	if err != nil {
		return nil, err
	}
	return api.Confirmation{OK: true, Message: "Webhook 凭据已加密保存，请发送测试通知确认接入"}, nil
}

func (b *APIBackend) saveGenericWebhook(ctx context.Context, id string, body api.NotifierCredentialsInput) (any, error) {
	if !apiID.MatchString(id) || len(body.URL) > 4096 {
		return nil, invalidAPI("Webhook 渠道编号或地址无效")
	}
	endpoint := []byte(body.URL)
	body.URL = ""
	defer clear(endpoint)
	if err := notify.ValidateWebhookURL(endpoint); err != nil {
		return nil, invalidAPI(err.Error())
	}
	var supplied []byte
	if body.SigningSecret != nil {
		supplied = []byte(*body.SigningSecret)
		*body.SigningSecret = ""
		defer clear(supplied)
		if len(supplied) > 0 {
			key, err := notify.DecodeWebhookSecret(supplied)
			clear(key)
			if err != nil {
				return nil, invalidAPI(err.Error())
			}
		}
	}
	generated := ""
	_, err := b.options.Editor.Update(ctx, "notifiers.yaml", func(data []byte) ([]byte, error) {
		current, err := config.ParseNotifiers(data)
		if err != nil {
			return nil, err
		}
		exists := false
		for _, channel := range current.Notifiers {
			if channel.ID != id {
				continue
			}
			if channel.Type != "webhook" {
				return nil, invalidAPI("渠道类型不一致，请使用新的通用 Webhook 渠道编号")
			}
			exists = true
		}
		value := supplied
		if body.SigningSecret == nil && !exists {
			generated, err = notify.GenerateWebhookSecret(b.options.Entropy)
			if err != nil {
				return nil, &api.Fault{Status: 500, Code: "secret_generate_failed", Message: "生成 Webhook 签名密钥失败，请重试", Cause: err}
			}
			value = []byte(generated)
			defer clear(value)
		}
		suffix := b.options.NewID()
		if suffix == "" {
			return nil, &api.Fault{Status: 500, Code: "secret_save_failed", Message: "生成 Webhook 凭据引用失败"}
		}
		urlRef := "webhook-url-" + id + "-" + suffix
		if err := b.options.Secrets.Set(ctx, urlRef, endpoint); err != nil {
			return nil, &api.Fault{Status: 500, Code: "secret_save_failed", Message: "加密保存 Webhook 地址失败", Cause: err}
		}
		var secretRef *string
		if body.SigningSecret != nil || generated != "" {
			name := ""
			secretRef = &name
			if len(value) > 0 {
				name = "webhook-sign-" + id + "-" + suffix
				if err := b.options.Secrets.Set(ctx, name, value); err != nil {
					return nil, &api.Fault{Status: 500, Code: "secret_save_failed", Message: "加密保存 Webhook 签名密钥失败", Cause: err}
				}
			}
		}
		updated, err := config.WebhookConfig(data, id, "webhook", urlRef, secretRef)
		if err != nil {
			return nil, err
		}
		if err := guardCardActionGrants(data, updated); err != nil {
			return nil, err
		}
		return updated, nil
	})
	if err != nil {
		return nil, err
	}
	return api.NotifierCredentialsResult{OK: true, Message: "Webhook 凭据已加密保存；新生成的签名密钥请立即保存给接收方", SigningSecret: generated}, nil
}
