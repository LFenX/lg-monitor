package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/LFenX/lg-monitor/internal/api"
	"github.com/LFenX/lg-monitor/internal/collector"
	"github.com/LFenX/lg-monitor/internal/config"
	"github.com/LFenX/lg-monitor/internal/store"
	"github.com/LFenX/lg-monitor/internal/transport"
)

type ServiceOperationSource interface {
	OperationPolicy(context.Context, string) (collector.OperationPolicy, error)
	ExecuteOperation(context.Context, string, collector.OperationCommand) (collector.OperationResult, error)
	OperationResult(context.Context, string, string) (collector.OperationResult, error)
}
type OperationRepository interface {
	OperationRequest(context.Context, string, string) (store.OperationRequest, error)
	ClaimOperation(context.Context, store.OperationRequest, string) (store.OperationRequest, bool, error)
	FinishOperation(context.Context, string, collector.OperationResult) (store.OperationRequest, error)
	RecoverOperations(context.Context) error
	Collector(context.Context, string) (store.CollectorRecord, bool, error)
}
type OperationManager struct {
	repository OperationRepository
	source     ServiceOperationSource
}

func NewOperationManager(ctx context.Context, repository OperationRepository, source ServiceOperationSource) (*OperationManager, error) {
	if repository == nil || source == nil {
		return nil, fmt.Errorf("服务操作需要持久监控账本和认证采集器通道")
	}
	if err := repository.RecoverOperations(ctx); err != nil {
		return nil, err
	}
	return &OperationManager{repository: repository, source: source}, nil
}
func operationRequestHash(id string, input api.ServiceOperationInput) string {
	data, _ := json.Marshal(struct {
		Collector string
		Input     api.ServiceOperationInput
	}{id, input})
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}
func operationFault(value store.OperationRequest) error {
	if value.State == "succeeded" {
		return nil
	}
	status, code := 409, "operation_"+value.State
	if value.State == "denied" {
		status = 403
	}
	return &api.Fault{Status: status, Code: code, Message: fmt.Sprintf("请求 %s：%s；请查询原请求记录，不要自动重新发起", value.RequestID, value.Message)}
}
func operationSourceFault(err error) error {
	if errors.Is(err, transport.ErrCollectorOffline) {
		return &api.Fault{Status: 503, Code: "collector_offline", Message: "采集器离线，未提供可执行操作", Cause: err}
	}
	if errors.Is(err, transport.ErrCollectorOutdated) {
		return &api.Fault{Status: 409, Code: "collector_outdated", Message: "采集器尚未支持受限服务操作，请升级后重试", Cause: err}
	}
	return &api.Fault{Status: 503, Code: "operations_unavailable", Message: "采集器服务操作查询不可用，请检查连接、时钟和本机配置", Cause: err}
}
func (m *OperationManager) Policy(ctx context.Context, id string) (collector.OperationPolicy, error) {
	if _, found, err := m.repository.Collector(ctx, id); err != nil {
		return collector.OperationPolicy{}, err
	} else if !found {
		return collector.OperationPolicy{}, missingAPI("采集器不存在")
	}
	value, err := m.source.OperationPolicy(ctx, id)
	if err != nil {
		return collector.OperationPolicy{}, operationSourceFault(err)
	}
	if value.Enabled {
		policy := config.RemoteOperations{Enabled: true, Services: value.Services}
		if err := policy.Validate(); err != nil || policy.Version() != value.Version {
			return collector.OperationPolicy{}, operationSourceFault(fmt.Errorf("采集器策略版本或服务清单无效"))
		}
	}
	return value, nil
}
func (m *OperationManager) Execute(ctx context.Context, id, actor string, input api.ServiceOperationInput) (store.OperationRequest, error) {
	var empty store.OperationRequest
	if !collector.ValidOperationRequestID(input.RequestID) || !collector.ValidOperationVersion(input.Version) || len(input.GrantID) > 64 || !config.ValidID(input.GrantID) || !input.Confirmed || !utf8.ValidString(input.Reason) || utf8.RuneCountInString(input.Reason) > 200 || strings.TrimSpace(input.Reason) == "" {
		return empty, invalidAPI("请提供 requestId（32 位小写十六进制）、已查看的授权及策略版本、1～200 字原因，并明确 confirmed=true")
	}
	hash := operationRequestHash(id, input)
	if previous, err := m.repository.OperationRequest(ctx, input.RequestID, hash); err == nil {
		return previous, operationFault(previous)
	} else if errors.Is(err, collector.ErrOperationConflict) {
		return empty, &api.Fault{Status: 409, Code: "operation_conflict", Message: "相同请求 ID 的内容不一致，未重新执行"}
	} else if !errors.Is(err, collector.ErrOperationMissing) {
		return empty, err
	}
	policy, err := m.Policy(ctx, id)
	if err != nil {
		return empty, err
	}
	index := slices.IndexFunc(policy.Services, func(grant config.ServiceGrant) bool { return grant.ID == input.GrantID })
	if !policy.Enabled || index < 0 {
		return empty, &api.Fault{Status: 403, Code: "operation_not_authorized", Message: "采集器本机没有启用这一项精确服务授权"}
	}
	if policy.Version != input.Version {
		return empty, &api.Fault{Status: 409, Code: "operation_policy_changed", Message: "采集器操作授权已变化，请重新查看并由本人核对后确认"}
	}
	request, fresh, err := m.repository.ClaimOperation(ctx, store.OperationRequest{RequestID: input.RequestID, CollectorID: id, GrantID: input.GrantID, Version: input.Version, Service: policy.Services[index].Service, Actor: actor, Reason: input.Reason}, hash)
	if errors.Is(err, collector.ErrOperationConflict) {
		return empty, &api.Fault{Status: 409, Code: "operation_conflict", Message: "相同请求 ID 已用于不同内容，未重新执行"}
	}
	if err != nil {
		return empty, &api.Fault{Status: 503, Code: "operation_intent_failed", Message: "操作意图未能持久保存，没有下发服务操作", Cause: err}
	}
	if !fresh {
		return request, operationFault(request)
	}
	result, dispatchErr := m.source.ExecuteOperation(ctx, id, collector.OperationCommand{RequestID: input.RequestID, GrantID: input.GrantID, Version: input.Version})
	if dispatchErr != nil {
		result = collector.OperationResult{RequestID: input.RequestID, GrantID: input.GrantID, Version: input.Version, Service: request.Service, State: "unknown", Message: "下发后的执行结果未知，请只读查询原请求或人工核对"}
		var dispatch *transport.OperationDispatchError
		if errors.As(dispatchErr, &dispatch) && !dispatch.Sent {
			result.State, result.Message = "failed", "操作没有下发，请检查采集器连接、能力和时钟"
		}
	}
	finishCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Second)
	defer cancel()
	completed, err := m.repository.FinishOperation(finishCtx, request.RequestID, result)
	if err != nil {
		request.State, request.Message = "unknown", "执行结果尚未持久确认，请查询原请求，不得自动重试"
		return request, &api.Fault{Status: 503, Code: "operation_audit_unknown", Message: fmt.Sprintf("请求 %s：%s", request.RequestID, request.Message), Cause: err}
	}
	return completed, operationFault(completed)
}
func (m *OperationManager) Get(ctx context.Context, id string) (store.OperationRequest, error) {
	if !collector.ValidOperationRequestID(id) {
		return store.OperationRequest{}, invalidAPI("操作请求 ID 必须为 32 位小写十六进制")
	}
	value, err := m.repository.OperationRequest(ctx, id, "")
	if errors.Is(err, collector.ErrOperationMissing) {
		return value, missingAPI("操作请求不存在")
	}
	if err != nil {
		return value, err
	}
	if value.State != "running" && value.State != "unknown" {
		return value, nil
	}
	result, err := m.source.OperationResult(ctx, value.CollectorID, id)
	if err != nil || result.State == "running" {
		return value, nil
	}
	updated, err := m.repository.FinishOperation(ctx, id, result)
	if err != nil {
		value.State, value.Message = "unknown", "操作终态尚未持久确认，请在采集器本机核对"
		return value, nil
	}
	return updated, nil
}
