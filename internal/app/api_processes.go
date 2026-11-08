package app

import (
	"context"
	"errors"
	"slices"
	"strings"
	"time"

	"github.com/LFenX/lg-monitor/internal/api"
	"github.com/LFenX/lg-monitor/internal/collector"
	"github.com/LFenX/lg-monitor/internal/config"
	"github.com/LFenX/lg-monitor/internal/rules"
	"github.com/LFenX/lg-monitor/internal/transport"
)

func (b *APIBackend) processes(ctx context.Context, operation api.Operation, request api.Request) (any, error) {
	id := request.Params["id"]
	var spec *config.Match
	if operation == api.TestTargetMatch {
		var input api.TargetMatchInput
		if err := api.DecodeBody(request.Body, &input); err != nil {
			return nil, err
		}
		if err := config.ValidateTargetMatch(input.Match); err != nil {
			return nil, invalidAPI("match 条件无效：" + err.Error())
		}
		spec = &input.Match
	}
	for key, values := range request.Query {
		if operation != api.ListProcesses || key != "name" || len(values) != 1 {
			return nil, invalidAPI("进程列表只接受一次 name 查询参数")
		}
	}
	_, known, err := b.options.Read.Collector(ctx, id)
	if err != nil {
		return nil, err
	}
	if !known {
		items, err := b.options.Collectors.Collectors(ctx)
		if err != nil {
			return nil, err
		}
		if !slices.ContainsFunc(items, func(value rules.CollectorPresence) bool { return value.ID == id }) {
			return nil, missingAPI("采集器不存在，请先用采集器列表核对 ID")
		}
	}
	if b.options.Processes == nil {
		return nil, &api.Fault{Status: 503, Code: "collector_offline", Message: transport.ErrCollectorOffline.Error()}
	}
	now := b.options.Now()
	b.processMu.Lock()
	last, exists := b.processTests[id]
	if exists && now.Sub(last) < 2*time.Second {
		b.processMu.Unlock()
		return nil, &api.Fault{Status: 429, Code: "process_rate_limited", Message: "同一采集器的进程列表和试匹配合计每两秒只能请求一次"}
	}
	b.processTests[id] = now
	b.processMu.Unlock()
	result, err := b.options.Processes.QueryProcesses(ctx, id, spec)
	if err != nil {
		switch {
		case errors.Is(err, transport.ErrCollectorOffline):
			return nil, &api.Fault{Status: 503, Code: "collector_offline", Message: err.Error()}
		case errors.Is(err, transport.ErrCollectorOutdated):
			return nil, &api.Fault{Status: 409, Code: "collector_outdated", Message: err.Error()}
		case errors.Is(err, context.DeadlineExceeded):
			return nil, &api.Fault{Status: 504, Code: "collector_timeout", Message: "采集器十秒内未回应，请检查连接后重试"}
		case errors.Is(err, context.Canceled):
			return nil, &api.Fault{Status: 408, Code: "collector_cancelled", Message: "进程查询已取消"}
		default:
			return nil, &api.Fault{Status: 502, Code: "collector_query_failed", Message: transport.ErrProcessSnapshot.Error()}
		}
	}
	if name := strings.ToLower(request.Query.Get("name")); name != "" {
		result.Processes = slices.DeleteFunc(result.Processes, func(process collector.ProcessInfo) bool {
			return !strings.Contains(strings.ToLower(process.Name), name)
		})
	}
	if operation == api.TestTargetMatch {
		matched := api.TargetMatchResult{Collector: result.Collector, CollectedAt: result.CollectedAt, Processes: []api.MatchedProcess{}, Truncated: result.Truncated, Reason: result.Reason}
		for _, process := range result.Processes {
			row := api.MatchedProcess{ProcessInfo: process}
			if spec != nil && spec.WorkDir != "" {
				row.WorkDir = process.WorkDir
			}
			matched.Processes = append(matched.Processes, row)
		}
		return matched, nil
	}
	return result, nil
}
