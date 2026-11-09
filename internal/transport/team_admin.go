package transport

import (
	"context"
	"regexp"

	lgmonv1 "github.com/LFenX/lg-monitor/proto/lgmon/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func (r *Receiver) SetTeamAdmin(handler func(context.Context, *lgmonv1.TeamAdminRequest) ([]byte, error)) {
	r.teamAdmin = handler
}

func (s *PairService) TeamAdmin(request *lgmonv1.TeamAdminRequest, stream grpc.ServerStreamingServer[lgmonv1.TeamAdminResponse]) error {
	if err := s.admin(stream.Context()); err != nil {
		return err
	}
	if request == nil || !regexp.MustCompile(`^[a-z_]{1,80}$`).MatchString(request.Operation) || len(request.Params) > 8 || len(request.BodyJson) > 1024*1024 {
		return status.Error(codes.InvalidArgument, "团队管理请求格式无效")
	}
	for key, value := range request.Params {
		if len(key) > 32 || len(value) > 2048 {
			return status.Error(codes.InvalidArgument, "团队管理参数过长")
		}
	}
	if s.teamAdmin == nil {
		return status.Error(codes.Unimplemented, "监控器未提供团队管理，请升级后重试")
	}
	data, err := s.teamAdmin(stream.Context(), request)
	if err != nil {
		return err
	}
	if len(data) > TeamResponseLimit {
		return status.Error(codes.ResourceExhausted, "团队结果超过 16 MiB 上限")
	}
	// Chunking preserves the collector protocol's existing four-MiB frame budget.
	for len(data) > 0 {
		n := min(256*1024, len(data))
		if err := stream.Send(&lgmonv1.TeamAdminResponse{BodyJson: data[:n]}); err != nil {
			return err
		}
		data = data[n:]
	}
	return nil
}
