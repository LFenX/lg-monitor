package app

import (
	"context"

	"github.com/LFenX/lg-monitor/internal/api"
	"github.com/LFenX/lg-monitor/internal/config"
)

func guardCardActionGrants(old, updated []byte) error {
	before, err := config.ParseNotifiers(old)
	if err != nil {
		return err
	}
	after, err := config.ParseNotifiers(updated)
	if err != nil {
		return err
	}
	if config.CardActionGrantsAdded(before, after) {
		return &api.Fault{Status: 403, Code: "card_action_grant_required", Message: "新增或变更卡片动作授权需要管理员通过通知群的卡片动作开关确认；请先关闭旧授权再更换应用或群"}
	}
	return nil
}

func (b *APIBackend) cardActions(ctx context.Context, cfg *config.Config, op api.Operation, r api.Request) (any, error) {
	id := r.Params["id"]
	found := false
	for _, channel := range cfg.Notifiers {
		if channel.ID == id {
			if channel.Type != "feishu_app" {
				return nil, invalidAPI("只有飞书应用渠道支持卡片动作")
			}
			found = true
		}
	}
	if !found {
		return nil, missingAPI("通知渠道不存在")
	}
	if op == api.PutCardActions {
		var input api.CardActionsInput
		if err := api.DecodeBody(r.Body, &input); err != nil {
			return nil, err
		}
		updated, err := b.options.Editor.Update(ctx, "notifiers.yaml", func(old []byte) ([]byte, error) { return config.CardActionsConfig(old, id, input.Enabled) })
		if err != nil {
			return nil, err
		}
		cfg = updated
	}
	if b.options.CardActions != nil {
		return b.options.CardActions.Status(id)
	}
	for _, channel := range cfg.Notifiers {
		if channel.ID == id {
			state := "disabled"
			if channel.CardActions {
				state = "waiting"
			}
			return api.CardActionsStatus{Notifier: id, App: channel.App, Enabled: channel.CardActions, State: state}, nil
		}
	}
	return nil, missingAPI("通知渠道不存在")
}
