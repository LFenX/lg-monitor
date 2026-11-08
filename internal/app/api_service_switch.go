package app

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/LFenX/lg-monitor/internal/api"
	"github.com/LFenX/lg-monitor/internal/collector"
	"github.com/LFenX/lg-monitor/internal/config"
	"github.com/LFenX/lg-monitor/internal/transport"
)

var errServiceSwitchUnchanged = errors.New("服务开关导入没有配置变化")

func (b *APIBackend) serviceSwitch(ctx context.Context, op api.Operation, request api.Request) (any, error) {
	public := true
	var input config.ServiceSwitchImportInput
	for key, values := range request.Query {
		if op != api.PreviewServiceSwitch || key != "publicProbes" || len(values) != 1 || (values[0] != "true" && values[0] != "false") {
			return nil, invalidAPI("预览只接受一次 publicProbes=true 或 false，不接受文件路径")
		}
		public = values[0] == "true"
	}
	if op == api.PreviewServiceSwitch && len(request.Body) > 0 {
		return nil, invalidAPI("预览不接受请求体或路径参数")
	}
	if op == api.ImportServiceSwitch {
		if err := api.DecodeBody(request.Body, &input); err != nil {
			return nil, err
		}
		var fields map[string]json.RawMessage
		if json.Unmarshal(request.Body, &fields) == nil && bytes.Equal(bytes.TrimSpace(fields["refs"]), []byte("null")) {
			return nil, invalidAPI("refs 必须省略或填写数组；空数组表示不保存任何 create/update 项，不能用 null 代替")
		}
		for _, value := range []string{input.SHA256, input.TargetsVersion} {
			decoded, err := hex.DecodeString(value)
			if err != nil || len(decoded) != 32 {
				return nil, invalidAPI("sha256 和 targetsVersion 必须来自先前的预览")
			}
		}
		if input.PublicProbes != nil {
			public = *input.PublicProbes
		}
	}
	id := request.Params["id"]
	host := config.ServiceSwitchCollector{ID: id}
	record, known, err := b.options.Read.Collector(ctx, id)
	if err != nil {
		return nil, err
	}
	if known {
		host.Hostname = record.Hostname
	} else {
		collectors, err := b.options.Collectors.Collectors(ctx)
		if err != nil {
			return nil, err
		}
		for _, item := range collectors {
			if item.ID == id {
				known, host.Hostname = true, item.Hostname
				break
			}
		}
	}
	if !known {
		return nil, missingAPI("采集器不存在，请先用采集器列表核对 ID")
	}
	if b.options.ServiceSwitch == nil {
		return nil, serviceSwitchFault(transport.ErrCollectorOffline)
	}
	now := b.options.TestNow()
	b.processMu.Lock()
	last, exists := b.serviceSwitchTests[id]
	if exists && now.Sub(last) < 2*time.Second {
		b.processMu.Unlock()
		return nil, &api.Fault{Status: 429, Code: "service_switch_rate_limited", Message: "同一采集器的服务开关预览和保存合计每两秒只能请求一次"}
	}
	b.serviceSwitchTests[id] = now
	b.processMu.Unlock()
	snapshot, err := b.options.ServiceSwitch.QueryServiceSwitch(ctx, id)
	if err != nil {
		return nil, serviceSwitchFault(err)
	}
	if op == api.PreviewServiceSwitch {
		reader, ok := b.options.Editor.(interface {
			Document(context.Context, string) ([]byte, error)
		})
		if !ok {
			return nil, fmt.Errorf("配置编辑器缺少原文读取能力")
		}
		data, err := reader.Document(ctx, "targets.yaml")
		if err != nil {
			return nil, err
		}
		targets, err := config.ServiceSwitchTargets(data)
		if err != nil {
			return nil, err
		}
		return config.ServiceSwitchPreviewTargets(host, snapshot, targets, config.TargetsFingerprint(data), public), nil
	}
	if snapshot.SHA256 != input.SHA256 {
		return nil, &api.Fault{Status: 409, Code: "source_changed", Message: "服务开关配置已变化，请重新预览并确认"}
	}
	var result config.ServiceSwitchImportResult
	_, err = b.options.Editor.Update(ctx, "targets.yaml", func(data []byte) ([]byte, error) {
		if config.TargetsFingerprint(data) != input.TargetsVersion {
			return nil, &api.Fault{Status: 409, Code: "config_changed", Message: "目标配置已变化，请重新预览并确认"}
		}
		targets, err := config.ServiceSwitchTargets(data)
		if err != nil {
			return nil, err
		}
		preview := config.ServiceSwitchPreviewTargets(host, snapshot, targets, input.TargetsVersion, public)
		updated, changes, err := config.ApplyServiceSwitchImport(data, preview, input)
		if err != nil {
			return nil, invalidAPI(err.Error())
		}
		result = changes
		if bytes.Equal(updated, data) {
			return nil, errServiceSwitchUnchanged
		}
		return updated, nil
	})
	if errors.Is(err, errServiceSwitchUnchanged) {
		return result, nil
	}
	return result, err
}

func serviceSwitchFault(err error) error {
	fault := &api.Fault{Status: 502, Code: "collector_query_failed", Message: "采集器返回的服务开关结果无效，请检查日志后重试", Cause: err}
	var source *collector.ServiceSwitchError
	switch {
	case errors.Is(err, transport.ErrCollectorOffline):
		fault.Status, fault.Code, fault.Message = 503, "collector_offline", err.Error()
	case errors.Is(err, transport.ErrCollectorOutdated):
		fault.Status, fault.Code, fault.Message = 409, "collector_outdated", err.Error()
	case errors.Is(err, context.DeadlineExceeded):
		fault.Status, fault.Code, fault.Message = 504, "collector_timeout", "采集器十秒内未回应，请检查连接后重试"
	case errors.Is(err, context.Canceled):
		fault.Status, fault.Code, fault.Message = 408, "collector_cancelled", "服务开关查询已取消"
	case errors.Is(err, collector.ErrServiceSwitchNotConfigured):
		fault.Status, fault.Code, fault.Message = 409, "source_not_configured", collector.ErrServiceSwitchNotConfigured.Error()
	case errors.As(err, &source):
		fault.Status, fault.Code, fault.Message = 422, "source_invalid", source.Message
	}
	return fault
}
