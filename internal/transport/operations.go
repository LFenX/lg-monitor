package transport

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"slices"
	"strconv"
	"time"

	"github.com/LFenX/lg-monitor/internal/collector"
	"github.com/LFenX/lg-monitor/internal/config"
	lgmonv1 "github.com/LFenX/lg-monitor/proto/lgmon/v1"
)

const ServiceOperationsCapability = "service_operations_v1"
const operationResponseLimit = 512 * 1024

type OperationResponder interface {
	Policy(context.Context) (collector.OperationPolicy, error)
	Execute(context.Context, collector.OperationCommand) collector.OperationResult
	Result(context.Context, string) (collector.OperationResult, error)
}

type OperationDispatchError struct {
	Sent  bool
	Cause error
}

func (e *OperationDispatchError) Error() string {
	if e.Sent {
		return "操作下发后的结果未知，请查询原请求，不得自动重试"
	}
	return "操作未下发，请检查采集器连接、能力和时钟"
}
func (e *OperationDispatchError) Unwrap() error { return e.Cause }

func decodeOperationJSON(data []byte, destination any) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return fmt.Errorf("操作消息格式无效：%w", err)
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return fmt.Errorf("操作消息只能有一个 JSON 对象")
	}
	return nil
}

func answerOperation(ctx context.Context, source OperationResponder, query *lgmonv1.OperationQuery) *lgmonv1.OperationReply {
	result := &lgmonv1.OperationReply{RequestId: query.RequestId}
	if source == nil || query.RequestId == "" || len(query.RequestId) > 64 || len(query.CommandJson) > 4096 {
		result.ErrorCode, result.Error = "invalid", "操作请求格式无效或采集器未提供该能力"
		return result
	}
	var value any
	var err error
	switch query.Kind {
	case "policy":
		if len(query.CommandJson) != 0 {
			err = fmt.Errorf("策略查询不能带命令")
			break
		}
		value, err = source.Policy(ctx)
	case "execute":
		var command collector.OperationCommand
		err = decodeOperationJSON(query.CommandJson, &command)
		if err == nil {
			value = source.Execute(ctx, command)
		}
	case "result":
		var input struct {
			RequestID string `json:"requestId"`
		}
		err = decodeOperationJSON(query.CommandJson, &input)
		if err == nil {
			value, err = source.Result(ctx, input.RequestID)
		}
	default:
		err = fmt.Errorf("操作查询类型不支持")
	}
	if errors.Is(err, collector.ErrOperationMissing) {
		result.ErrorCode, result.Error = "not_found", "采集器没有该操作记录"
		return result
	}
	if err != nil {
		result.ErrorCode, result.Error = "unavailable", "采集器操作查询不可用，请在本机检查策略和账本"
		return result
	}
	result.ResultJson, err = json.Marshal(value)
	if err != nil || len(result.ResultJson) > operationResponseLimit {
		result.ResultJson = nil
		result.ErrorCode, result.Error = "invalid", "采集器操作结果超出大小限制或格式无效"
	}
	return result
}

func (r *Receiver) queryOperation(parent context.Context, id, kind string, command collector.OperationCommand) (*lgmonv1.OperationReply, error) {
	budget := 10 * time.Second
	if kind == "execute" {
		budget = 45 * time.Second
	}
	ctx, cancel := context.WithTimeout(parent, budget)
	defer cancel()
	notSent := func(err error) (*lgmonv1.OperationReply, error) { return nil, &OperationDispatchError{Cause: err} }
	r.mu.Lock()
	session := r.active[id]
	if session == nil || !session.connected {
		r.mu.Unlock()
		return notSent(ErrCollectorOffline)
	}
	if !slices.Contains(session.identity.Capabilities, ServiceOperationsCapability) {
		r.mu.Unlock()
		return notSent(ErrCollectorOutdated)
	}
	if kind == "execute" {
		clock := r.clocks[id]
		if clock == nil {
			r.mu.Unlock()
			return notSent(fmt.Errorf("采集器时钟未知"))
		}
		localNow := r.now().UnixMilli() - clock.offset
		if localNow <= 0 || localNow > math.MaxInt64-40000 {
			r.mu.Unlock()
			return notSent(fmt.Errorf("采集器时钟无效"))
		}
		command.ExpiresAt = localNow + 40000
	}
	r.querySeq++
	requestID := strconv.FormatUint(r.querySeq, 10)
	response := make(chan *lgmonv1.OperationReply, 1)
	session.operationResults[requestID] = response
	r.mu.Unlock()
	defer func() { r.mu.Lock(); delete(session.operationResults, requestID); r.mu.Unlock() }()
	var data []byte
	if kind == "execute" {
		data, _ = json.Marshal(command)
	} else if kind == "result" {
		data, _ = json.Marshal(map[string]string{"requestId": command.RequestID})
	}
	request := &lgmonv1.OperationQuery{RequestId: requestID, Kind: kind, CommandJson: data}
	select {
	case <-ctx.Done():
		return notSent(ctx.Err())
	case <-session.done:
		return notSent(ErrCollectorOffline)
	case session.operationRequests <- request:
	}
	select {
	case <-ctx.Done():
		return nil, &OperationDispatchError{Sent: true, Cause: ctx.Err()}
	case <-session.done:
		return nil, &OperationDispatchError{Sent: true, Cause: ErrCollectorOffline}
	case result := <-response:
		if len(result.ResultJson) > operationResponseLimit || result.ErrorCode != "" {
			cause := fmt.Errorf("采集器操作结果不可用")
			if result.ErrorCode == "not_found" {
				cause = collector.ErrOperationMissing
			}
			return nil, &OperationDispatchError{Sent: true, Cause: cause}
		}
		return result, nil
	}
}

func (r *Receiver) operationReply(session *linkSession, result *lgmonv1.OperationReply) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if response := session.operationResults[result.RequestId]; response != nil {
		select {
		case response <- result:
		default:
		}
	}
}

func (r *Receiver) OperationPolicy(ctx context.Context, id string) (collector.OperationPolicy, error) {
	var value collector.OperationPolicy
	reply, err := r.queryOperation(ctx, id, "policy", collector.OperationCommand{})
	if err != nil {
		return value, err
	}
	if err := decodeOperationJSON(reply.ResultJson, &value); err != nil {
		return value, err
	}
	if len(value.Reason) > 4096 || value.Services == nil || len(value.Services) > 128 || (value.Version != "" && !collector.ValidOperationVersion(value.Version)) {
		return collector.OperationPolicy{}, fmt.Errorf("采集器操作策略格式无效")
	}
	if value.Enabled {
		policy := config.RemoteOperations{Enabled: true, Services: value.Services}
		if err := policy.Validate(); err != nil || value.Version != policy.Version() {
			return collector.OperationPolicy{}, fmt.Errorf("采集器操作授权格式无效")
		}
	}
	return value, nil
}

func validateOperationResult(value collector.OperationResult, id string) error {
	if value.RequestID != id || !collector.ValidOperationRequestID(id) || !collector.ValidOperationVersion(value.Version) || len(value.Message) > 4096 || !slices.Contains([]string{"running", "succeeded", "failed", "denied", "unknown"}, value.State) || value.StartedAt < 0 || value.FinishedAt < 0 {
		return fmt.Errorf("采集器操作记录格式无效")
	}
	return nil
}
func (r *Receiver) ExecuteOperation(ctx context.Context, id string, command collector.OperationCommand) (collector.OperationResult, error) {
	var value collector.OperationResult
	reply, err := r.queryOperation(ctx, id, "execute", command)
	if err != nil {
		return value, err
	}
	if err := decodeOperationJSON(reply.ResultJson, &value); err != nil {
		return value, &OperationDispatchError{Sent: true, Cause: err}
	}
	if err := validateOperationResult(value, command.RequestID); err != nil || value.GrantID != command.GrantID || value.Version != command.Version {
		return collector.OperationResult{}, &OperationDispatchError{Sent: true, Cause: fmt.Errorf("采集器操作归属或结果格式无效")}
	}
	return value, nil
}
func (r *Receiver) OperationResult(ctx context.Context, id, requestID string) (collector.OperationResult, error) {
	var value collector.OperationResult
	reply, err := r.queryOperation(ctx, id, "result", collector.OperationCommand{RequestID: requestID})
	if err != nil {
		return value, err
	}
	if err := decodeOperationJSON(reply.ResultJson, &value); err != nil {
		return value, err
	}
	if err := validateOperationResult(value, requestID); err != nil {
		return collector.OperationResult{}, err
	}
	return value, nil
}
