package transport

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"time"
	"unicode/utf8"

	"github.com/LFenX/lg-monitor/internal/collector"
	"github.com/LFenX/lg-monitor/internal/config"
	lgmonv1 "github.com/LFenX/lg-monitor/proto/lgmon/v1"
)

const ServiceSwitchCapability = "service_switch"

func answerServiceSwitch(ctx context.Context, source collector.ServiceSwitchSource, query *lgmonv1.ServiceSwitchQuery) *lgmonv1.ServiceSwitchSummary {
	result := &lgmonv1.ServiceSwitchSummary{RequestId: query.RequestId}
	if source == nil || query.RequestId == "" || len(query.RequestId) > 64 {
		result.ErrorCode, result.Error = "source_not_configured", collector.ErrServiceSwitchNotConfigured.Error()
		return result
	}
	snapshot, err := source.ReadServiceSwitch(ctx)
	if err != nil {
		result.ErrorCode, result.Error = "source_invalid", "读取服务开关配置失败，请在采集器本机检查文件"
		var invalid *collector.ServiceSwitchError
		if errors.Is(err, collector.ErrServiceSwitchNotConfigured) {
			result.ErrorCode, result.Error = "source_not_configured", collector.ErrServiceSwitchNotConfigured.Error()
		} else if errors.As(err, &invalid) {
			result.Error = invalid.Message
		}
		return result
	}
	sha, data, err := config.ServiceSwitchFingerprint(snapshot.Summary)
	if err != nil || sha != snapshot.SHA256 || snapshot.ReadAt <= 0 {
		result.ErrorCode, result.Error = "source_invalid", "服务开关精简结果无效或超过传输消息大小限制"
		return result
	}
	result.SummaryJson, result.Sha256, result.ReadAt = data, sha, snapshot.ReadAt
	return result
}

func (r *Receiver) QueryServiceSwitch(parent context.Context, id string) (config.ServiceSwitchSnapshot, error) {
	var empty config.ServiceSwitchSnapshot
	ctx, cancel := context.WithTimeout(parent, 10*time.Second)
	defer cancel()
	r.mu.Lock()
	session := r.active[id]
	if session == nil || !session.connected {
		r.mu.Unlock()
		return empty, ErrCollectorOffline
	}
	if !slices.Contains(session.identity.Capabilities, ServiceSwitchCapability) {
		r.mu.Unlock()
		return empty, ErrCollectorOutdated
	}
	r.querySeq++
	requestID := strconv.FormatUint(r.querySeq, 10)
	response := make(chan *lgmonv1.ServiceSwitchSummary, 1)
	session.serviceSwitchResults[requestID] = response
	r.mu.Unlock()
	defer func() {
		r.mu.Lock()
		delete(session.serviceSwitchResults, requestID)
		r.mu.Unlock()
	}()
	select {
	case <-ctx.Done():
		return empty, ctx.Err()
	case <-session.done:
		return empty, ErrCollectorOffline
	case session.serviceSwitchRequests <- &lgmonv1.ServiceSwitchQuery{RequestId: requestID}:
	}
	select {
	case <-ctx.Done():
		return empty, ctx.Err()
	case <-session.done:
		return empty, ErrCollectorOffline
	case result := <-response:
		return decodeServiceSwitch(result)
	}
}

func (r *Receiver) serviceSwitchReply(session *linkSession, result *lgmonv1.ServiceSwitchSummary) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if pending := session.serviceSwitchResults[result.RequestId]; pending != nil {
		select {
		case pending <- result:
		default:
		}
	}
}

func decodeServiceSwitch(source *lgmonv1.ServiceSwitchSummary) (config.ServiceSwitchSnapshot, error) {
	var result config.ServiceSwitchSnapshot
	if source.ErrorCode == "source_not_configured" {
		return result, collector.ErrServiceSwitchNotConfigured
	}
	if source.ErrorCode == "source_invalid" && source.Error != "" && len(source.Error) <= 4096 && utf8.ValidString(source.Error) {
		return result, &collector.ServiceSwitchError{Message: source.Error}
	}
	if source.ErrorCode != "" || source.Error != "" || source.ReadAt <= 0 {
		return result, fmt.Errorf("采集器返回了无效的服务开关结果")
	}
	var err error
	result.Summary, err = config.DecodeServiceSwitchSummary(source.SummaryJson)
	if err != nil {
		return result, err
	}
	sha, _, err := config.ServiceSwitchFingerprint(result.Summary)
	if err != nil || sha != source.Sha256 {
		return result, fmt.Errorf("采集器服务开关结果的指纹不一致")
	}
	result.SHA256, result.ReadAt = sha, source.ReadAt
	return result, nil
}
