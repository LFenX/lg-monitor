package notify

import (
	"context"
	"crypto/rand"
	"fmt"
	"time"
)

type ToastRequest struct {
	NotifierID, AlertID, State, EventKey string
	Value                                Toast
}

type ToastSenderOptions struct {
	Client   ToastClient
	Recorder Recorder
	Now      func() time.Time
	NewID    func() string
	Allow    func(context.Context) (bool, error)
}

type ToastSender struct{ options ToastSenderOptions }

func NewToastSender(options ToastSenderOptions) (*ToastSender, error) {
	if options.Client == nil || options.Recorder == nil {
		return nil, fmt.Errorf("Windows 通知发送需要显示接口和记录存储")
	}
	if options.Now == nil {
		options.Now = time.Now
	}
	if options.NewID == nil {
		options.NewID = rand.Text
	}
	return &ToastSender{options: options}, nil
}

func (s *ToastSender) Send(ctx context.Context, request ToastRequest) (ToastResult, error) {
	if request.NotifierID == "" || request.AlertID == "" {
		return ToastResult{}, fmt.Errorf("Windows 通知缺少渠道或告警编号")
	}
	if err := ctx.Err(); err != nil {
		return ToastResult{}, &deliveryError{message: "Windows 通知已取消", cause: err}
	}
	if s.options.Allow != nil {
		allowed, err := s.options.Allow(ctx)
		if err != nil {
			return ToastResult{}, err
		}
		if !allowed {
			return ToastResult{}, nil
		}
	}
	id := s.options.NewID()
	if id == "" {
		return ToastResult{}, fmt.Errorf("Windows 通知记录编号生成失败")
	}
	result, err := s.options.Client.Show(ctx, request.Value)
	attempt := Attempt{ID: id, AlertID: request.AlertID, State: request.State, NotifierID: request.NotifierID, EventKey: request.EventKey, TS: s.options.Now().UTC().UnixMilli(), OK: err == nil && !result.Skipped, Skipped: err == nil && result.Skipped}
	if err != nil {
		attempt.Error = "Windows 通知提交失败，请检查登录会话和通知设置"
	} else if result.Skipped {
		attempt.Error = result.Reason
	}
	auditCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	if auditErr := s.options.Recorder.RecordNotification(auditCtx, attempt); auditErr != nil {
		return result, &deliveryError{message: "记录 Windows 通知结果失败，请检查数据库和磁盘状态", cause: auditErr}
	}
	if err != nil {
		return result, &deliveryError{message: attempt.Error, cause: err}
	}
	return result, nil
}
