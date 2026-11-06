package notify

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/LFenX/lg-monitor/internal/feishu"
)

type CardSender interface {
	SendCard(context.Context, feishu.CardMessage) (feishu.SendResult, error)
}
type Attempt struct {
	ID         string `json:"id"`
	AlertID    string `json:"alertId"`
	NotifierID string `json:"notifierId"`
	Template   string `json:"template"`
	TS         int64  `json:"ts"`
	OK         bool   `json:"ok"`
	Error      string `json:"error"`
	MessageID  string `json:"messageId"`
}
type Recorder interface {
	RecordNotification(context.Context, Attempt) error
}
type Waiter interface {
	Wait(context.Context, time.Duration) error
}
type nativeWaiter struct{}

func (nativeWaiter) Wait(ctx context.Context, delay time.Duration) error {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

type Request struct {
	Message    feishu.CardMessage
	NotifierID string
	Template   string
}
type Options struct {
	Client   CardSender
	Recorder Recorder
	Waiter   Waiter
	Now      func() time.Time
	NewID    func() string
	Logger   *slog.Logger
}
type Sender struct{ options Options }
type deliveryError struct {
	message string
	cause   error
}

func (e *deliveryError) Error() string { return e.message }
func (e *deliveryError) Unwrap() error { return e.cause }

func UserMessage(err error) string {
	var delivery *deliveryError
	if errors.As(err, &delivery) {
		return delivery.message
	}
	var api *feishu.Error
	if errors.As(err, &api) {
		return api.Message
	}
	return "飞书卡片发送失败，请检查网络和应用配置"
}

func NewSender(options Options) (*Sender, error) {
	if options.Client == nil || options.Recorder == nil {
		return nil, fmt.Errorf("通知发送需要飞书客户端和发送记录存储")
	}
	if options.Waiter == nil {
		options.Waiter = nativeWaiter{}
	}
	if options.Now == nil {
		options.Now = time.Now
	}
	if options.NewID == nil {
		options.NewID = rand.Text
	}
	if options.Logger == nil {
		options.Logger = slog.Default()
	}
	return &Sender{options: options}, nil
}

func (s *Sender) Send(ctx context.Context, request Request) (feishu.SendResult, error) {
	if request.NotifierID == "" {
		return feishu.SendResult{}, fmt.Errorf("通知渠道 ID 不能为空")
	}
	if request.Template == "" {
		request.Template = "standard"
	}
	delays := []time.Duration{2 * time.Second, 5 * time.Second, 15 * time.Second}
	for attempt := 0; attempt <= len(delays); attempt++ {
		if err := ctx.Err(); err != nil {
			return feishu.SendResult{}, &deliveryError{message: "通知发送已取消", cause: err}
		}
		id := s.options.NewID()
		if id == "" {
			return feishu.SendResult{}, fmt.Errorf("通知记录 ID 生成失败")
		}
		result, err := s.options.Client.SendCard(ctx, request.Message)
		record := Attempt{ID: id, AlertID: request.Message.AlertID, NotifierID: request.NotifierID, Template: request.Template, TS: s.options.Now().UTC().UnixMilli(), OK: err == nil, MessageID: result.MessageID}
		var failure *feishu.Error
		if err != nil {
			record.Error = "飞书卡片发送失败，请检查网络和应用配置"
			if errors.As(err, &failure) {
				record.Error = failure.Message
			}
		}
		// A cancelled network request still needs an audit record; this context cannot send another message.
		auditCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		auditErr := s.options.Recorder.RecordNotification(auditCtx, record)
		cancel()
		if auditErr != nil {
			return result, &deliveryError{message: "记录通知发送结果失败，请检查数据库和磁盘状态", cause: auditErr}
		}
		if err == nil {
			return result, nil
		}
		kind, code := feishu.API, 0
		if failure != nil {
			kind, code = failure.Kind, failure.Code
		}
		s.options.Logger.Warn("Notification attempt failed", "attempt", attempt+1, "kind", kind, "code", code)
		if attempt == len(delays) || !feishu.Retryable(err) {
			return feishu.SendResult{}, &deliveryError{message: record.Error, cause: err}
		}
		if waitErr := s.options.Waiter.Wait(ctx, delays[attempt]); waitErr != nil {
			return feishu.SendResult{}, &deliveryError{message: "等待通知重试已取消", cause: waitErr}
		}
	}
	return feishu.SendResult{}, fmt.Errorf("通知发送未完成")
}
