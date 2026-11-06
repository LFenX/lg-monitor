package transport

import (
	"context"
	"regexp"

	lgmonv1 "github.com/LFenX/lg-monitor/proto/lgmon/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func (r *Receiver) SetServiceTest(handler func(context.Context, *lgmonv1.ServiceTestRequest) (*lgmonv1.ServiceTestResponse, error)) {
	r.serviceTest = handler
}

func (s *PairService) TestFeishu(ctx context.Context, request *lgmonv1.ServiceTestRequest) (*lgmonv1.ServiceTestResponse, error) {
	if err := s.admin(ctx); err != nil {
		return nil, err
	}
	identifier := regexp.MustCompile(`^[a-z0-9-]{1,128}$`)
	if request == nil || !identifier.MatchString(request.NotifierId) || (request.TemplateId != "" && !identifier.MatchString(request.TemplateId)) || !regexp.MustCompile(`^test-[A-Za-z0-9_-]{1,128}$`).MatchString(request.TestId) {
		return nil, status.Error(codes.InvalidArgument, "请指定有效的已有通知渠道、模板和测试标识")
	}
	if s.serviceTest == nil {
		return nil, status.Error(codes.FailedPrecondition, "未配置服务身份发送入口")
	}
	return s.serviceTest(ctx, request)
}
