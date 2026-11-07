package notify

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"time"
)

func GenerateWebhookSecret(entropy io.Reader) (string, error) {
	key := make([]byte, 32)
	defer clear(key)
	if _, err := io.ReadFull(entropy, key); err != nil {
		return "", &deliveryError{message: "生成 Webhook 签名密钥失败，请重试", cause: err}
	}
	return "whsec_" + base64.StdEncoding.EncodeToString(key), nil
}

type WebhookRequest struct {
	AlertID, State, EventKey, NotifierID string
	Body                                 json.RawMessage
}
type WebhookSenderOptions struct {
	Client   JSONWebhookClient
	Recorder Recorder
	Now      func() time.Time
	NewID    func() string
	Waiter   Waiter
	Logger   *slog.Logger
	Allow    func(context.Context) (bool, error)
}
type WebhookSender struct{ options WebhookSenderOptions }

func NewWebhookSender(options WebhookSenderOptions) (*WebhookSender, error) {
	if options.Client == nil || options.Recorder == nil {
		return nil, fmt.Errorf("Webhook 发送需要客户端与发送记录存储")
	}
	if options.Now == nil {
		options.Now = time.Now
	}
	if options.NewID == nil {
		options.NewID = rand.Text
	}
	if options.Waiter == nil {
		options.Waiter = nativeWaiter{}
	}
	if options.Logger == nil {
		options.Logger = slog.Default()
	}
	return &WebhookSender{options}, nil
}

func (s *WebhookSender) Send(ctx context.Context, request WebhookRequest) error {
	if request.AlertID == "" || request.State == "" || request.NotifierID == "" || !json.Valid(request.Body) {
		return fmt.Errorf("Webhook 通知缺少告警、状态、渠道或合法 JSON")
	}
	id := WebhookID(request.AlertID, request.State, request.EventKey, request.NotifierID)
	delays := []time.Duration{2 * time.Second, 5 * time.Second, 15 * time.Second}
	for i := 0; i <= len(delays); i++ {
		if err := ctx.Err(); err != nil {
			return &deliveryError{message: "Webhook 通知已取消", cause: err}
		}
		if s.options.Allow != nil {
			allowed, err := s.options.Allow(ctx)
			if err != nil {
				return err
			}
			if !allowed {
				return nil
			}
		}
		attemptID := s.options.NewID()
		if attemptID == "" {
			return fmt.Errorf("Webhook 通知记录编号生成失败")
		}
		status, err := s.options.Client.Send(ctx, id, request.Body)
		record := Attempt{ID: attemptID, AlertID: request.AlertID, State: request.State, NotifierID: request.NotifierID, EventKey: request.EventKey, TS: s.options.Now().UTC().UnixMilli(), OK: err == nil, StatusCode: status}
		var failure *WebhookError
		if err != nil {
			record.Error = "Webhook 发送失败，请检查接收方和网络"
			if errors.As(err, &failure) {
				record.Error = failure.Message
			}
		}
		auditCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		auditErr := s.options.Recorder.RecordNotification(auditCtx, record)
		cancel()
		if auditErr != nil {
			return &deliveryError{message: "记录 Webhook 发送结果失败，请检查数据库和磁盘状态", cause: auditErr}
		}
		if err == nil {
			return nil
		}
		s.options.Logger.Warn("Webhook delivery attempt failed", "attempt", i+1, "status", status)
		if i == len(delays) || failure == nil || !failure.retry {
			return &deliveryError{message: record.Error, cause: err}
		}
		if err := s.options.Waiter.Wait(ctx, delays[i]); err != nil {
			return &deliveryError{message: "等待 Webhook 重试已取消", cause: err}
		}
	}
	return fmt.Errorf("Webhook 发送未完成")
}
