package main

import (
	"context"
	"fmt"
	"time"

	"github.com/LFenX/lg-monitor/internal/feishu/setup"
	lgmonv1 "github.com/LFenX/lg-monitor/proto/lgmon/v1"
	"google.golang.org/grpc/status"
)

func nativeServiceTest(parent context.Context, home, notifier, template, testID string) (setup.Step, error) {
	ctx, cancel := context.WithTimeout(parent, 2*time.Minute)
	defer cancel()
	admin, closeAdmin, err := defaultPairDependencies().Admin(ctx, home)
	if err != nil {
		return setup.Step{}, fmt.Errorf("连接服务身份发送入口失败，请确认 monitor/both 服务已启动：%w", err)
	}
	defer closeAdmin()
	result, err := admin.TestFeishu(ctx, &lgmonv1.ServiceTestRequest{NotifierId: notifier, TemplateId: template, TestId: testID})
	if err != nil {
		return setup.Step{}, fmt.Errorf("服务身份测试失败：%s", status.Convert(err).Message())
	}
	if result.ExecutionIdentity != "LocalSystem" {
		return setup.Step{}, fmt.Errorf("发送端未证明 LocalSystem 服务身份")
	}
	step := setup.Step{Step: "test", Status: setup.OK, Message: result.Message, MessageID: result.MessageId, URL: result.Url}
	if !result.Ok {
		step.Status = setup.Error
	}
	return step, nil
}
