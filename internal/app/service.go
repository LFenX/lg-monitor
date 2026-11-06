package app

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"

	"github.com/LFenX/lg-monitor/internal/config"
	"github.com/LFenX/lg-monitor/internal/feishu"
	"github.com/LFenX/lg-monitor/internal/notify"
	"github.com/LFenX/lg-monitor/internal/notify/cards"
	lgmonv1 "github.com/LFenX/lg-monitor/proto/lgmon/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func serviceTestHandler(opts LocalOptions, manager *config.Manager, db Persistence, vault SecretReader) func(context.Context, *lgmonv1.ServiceTestRequest) (*lgmonv1.ServiceTestResponse, error) {
	return func(ctx context.Context, request *lgmonv1.ServiceTestRequest) (*lgmonv1.ServiceTestResponse, error) {
		if opts.ServiceIdentity != "LocalSystem" {
			return nil, status.Error(codes.FailedPrecondition, "请先以 LocalSystem 服务身份启动 monitor 或 both")
		}
		preflightFailure := func(message string) error {
			if err := db.RecordNotification(ctx, notify.Attempt{ID: rand.Text(), AlertID: request.TestId, State: "firing", NotifierID: request.NotifierId, Template: request.TemplateId, TS: opts.Now().UnixMilli(), Error: message}); err != nil {
				opts.Logger.Error("Service notification preflight audit failed")
			}
			return status.Error(codes.FailedPrecondition, message)
		}
		cfg := manager.Snapshot()
		var notifier config.Notifier
		for _, item := range cfg.Notifiers {
			if item.ID == request.NotifierId {
				notifier = item
				break
			}
		}
		if notifier.ID == "" {
			return nil, status.Error(codes.InvalidArgument, "指定通知渠道不存在，请先完成 feishu setup")
		}
		var application config.FeishuApp
		for _, item := range cfg.FeishuApps {
			if item.ID == notifier.App {
				application = item
				break
			}
		}
		secret, err := vault.Get(ctx, application.SecretRef)
		defer clear(secret)
		if err != nil {
			return nil, preflightFailure("服务无法读取飞书密钥，请检查机器 DPAPI 密钥库与文件访问权限")
		}
		client, err := opts.NewClient(application, secret)
		clear(secret)
		if err != nil {
			return nil, preflightFailure("服务创建飞书客户端失败，请检查应用配置")
		}
		defer client.Close()
		renderer, err := cards.New(manager, cards.Options{Now: opts.Now, Logger: opts.Logger})
		if err != nil {
			return nil, status.Error(codes.FailedPrecondition, "服务加载卡片模板失败，请检查配置")
		}
		selected := request.TemplateId
		if selected == "" {
			selected = notifier.Template
		}
		card := renderer.RenderTest(selected, request.TestId)
		sender, err := notify.NewSender(notify.Options{Client: client, Recorder: db, Now: opts.Now, Logger: opts.Logger})
		if err != nil {
			return nil, fmt.Errorf("创建服务身份通知发送器失败：%w", err)
		}
		sent, err := sender.Send(ctx, notify.Request{Message: feishu.CardMessage{AlertID: request.TestId, State: "firing", ChatID: notifier.ChatID, Card: card.JSON}, NotifierID: notifier.ID, Template: card.Template})
		response := &lgmonv1.ServiceTestResponse{Ok: err == nil, MessageId: sent.MessageID, ExecutionIdentity: opts.ServiceIdentity, Message: "测试卡片已由 LocalSystem 服务发送；请在群内确认实际收到"}
		if err != nil {
			response.Message = notify.UserMessage(err)
			var apiError *feishu.Error
			if errors.As(err, &apiError) {
				response.Message, response.Url = apiError.Message, apiError.URL
			}
		}
		if card.Warning != "" {
			response.Message += "；" + card.Warning
		}
		return response, nil
	}
}
