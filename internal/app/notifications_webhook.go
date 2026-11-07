package app

import (
	"context"

	"github.com/LFenX/lg-monitor/internal/config"
	"github.com/LFenX/lg-monitor/internal/feishu"
	"github.com/LFenX/lg-monitor/internal/notify"
)

type WebhookFactory func([]byte, []byte) (feishu.WebhookClient, error)
type JSONWebhookFactory func([]byte, []byte) (notify.JSONWebhookClient, error)

func openJSONWebhook(ctx context.Context, vault SecretReader, factory JSONWebhookFactory, channel config.Notifier) (notify.JSONWebhookClient, error) {
	if factory == nil {
		return nil, &runtimeError{message: "缺少通用 Webhook 客户端接口"}
	}
	endpoint, err := vault.Get(ctx, channel.URLRef)
	defer clear(endpoint)
	if err != nil {
		return nil, &runtimeError{message: "读取 Webhook 地址失败，请由管理员保存凭据", cause: err}
	}
	if err := notify.ValidateWebhookURL(endpoint); err != nil {
		return nil, &runtimeError{message: "密钥库中的 Webhook 地址无效", cause: err}
	}
	var secret []byte
	if channel.SecretRef != "" {
		secret, err = vault.Get(ctx, channel.SecretRef)
		defer clear(secret)
		if err != nil {
			return nil, &runtimeError{message: "读取 Webhook 签名密钥失败", cause: err}
		}
		key, err := notify.DecodeWebhookSecret(secret)
		clear(key)
		if err != nil {
			return nil, &runtimeError{message: "Webhook 签名密钥无效，请由管理员重新保存", cause: err}
		}
	}
	client, err := factory(endpoint, secret)
	if err != nil || client == nil {
		return nil, &runtimeError{message: "创建通用 Webhook 客户端失败", cause: err}
	}
	return client, nil
}

func openWebhook(ctx context.Context, vault SecretReader, factory WebhookFactory, channel config.Notifier) (feishu.WebhookClient, error) {
	if factory == nil {
		return nil, &runtimeError{message: "缺少 Webhook 客户端接口"}
	}
	endpoint, err := vault.Get(ctx, channel.URLRef)
	defer clear(endpoint)
	if err != nil {
		return nil, &runtimeError{message: "读取 Webhook 地址失败，请由管理员重新保存凭据", cause: err}
	}
	if err := feishu.ValidateWebhookURL(endpoint); err != nil {
		return nil, &runtimeError{message: "密钥库中的飞书 Webhook 地址无效，请由管理员重新保存", cause: err}
	}
	var signingSecret []byte
	if channel.SecretRef != "" {
		signingSecret, err = vault.Get(ctx, channel.SecretRef)
		defer clear(signingSecret)
		if err != nil {
			return nil, &runtimeError{message: "读取 Webhook 签名密钥失败，请由管理员重新保存凭据", cause: err}
		}
		if len(signingSecret) == 0 {
			return nil, &runtimeError{message: "Webhook 签名密钥为空，请由管理员重新保存凭据"}
		}
	}
	client, err := factory(endpoint, signingSecret)
	if err != nil {
		return nil, &runtimeError{message: "创建 Webhook 客户端失败，请检查渠道配置", cause: err}
	}
	if client == nil {
		return nil, &runtimeError{message: "Webhook 客户端接口未创建客户端"}
	}
	return client, nil
}
