package app

import (
	"context"
	"errors"
	"net/url"
	"slices"
	"strconv"
	"strings"

	"github.com/LFenX/lg-monitor/internal/api"
	"github.com/LFenX/lg-monitor/internal/config"
	"github.com/LFenX/lg-monitor/internal/rules"
	"github.com/LFenX/lg-monitor/internal/store"
)

func queryFault(err error) error {
	if errors.Is(err, context.DeadlineExceeded) {
		return &api.Fault{Status: 408, Code: "query_timeout", Message: "只读 SQL 查询达到时间上限（最多 5 秒），请缩小查询", Cause: err}
	}
	if errors.Is(err, context.Canceled) {
		return &api.Fault{Status: 408, Code: "query_cancelled", Message: "只读 SQL 查询已取消", Cause: err}
	}
	return &api.Fault{Status: 400, Code: "invalid_query", Message: err.Error(), Cause: err}
}
func (b *APIBackend) runSQL(ctx context.Context, query string, input config.QueryParams, defaultRange config.Duration) (api.QueryResult, error) {
	var result api.QueryResult
	values, err := config.ResolveQueryParams(input, defaultRange, b.options.Now)
	if err != nil {
		return result, &api.Fault{Status: 400, Code: "invalid_params", Message: err.Error(), Cause: err}
	}
	if values.Collector != nil {
		_, known, err := b.options.Read.Collector(ctx, *values.Collector)
		if err != nil {
			return result, err
		}
		if !known {
			if b.options.Collectors != nil {
				collectors, lookupErr := b.options.Collectors.Collectors(ctx)
				if lookupErr != nil {
					return result, lookupErr
				}
				known = slices.ContainsFunc(collectors, func(value rules.CollectorPresence) bool { return value.ID == *values.Collector })
			}
			if !known {
				return result, &api.Fault{Status: 400, Code: "invalid_params", Message: "采集器不存在，请从已登记的主机中选择"}
			}
		}
	}
	if _, err := config.DashboardSQLParameters(query); err != nil {
		return result, queryFault(err)
	}
	result.Params = values
	result.QueryResult, err = b.options.SQL.QueryParams(ctx, query, values.Bindings())
	if err != nil {
		return result, queryFault(err)
	}
	return result, nil
}
func dashboardParams(values url.Values) (config.QueryParams, error) {
	var result config.QueryParams
	for name, list := range values {
		if len(list) != 1 {
			return result, &api.Fault{Status: 400, Code: "invalid_params", Message: "每个查询参数只能提交一次"}
		}
		value := list[0]
		switch name {
		case "range":
			duration := config.Duration(value)
			result.Range = &duration
		case "collector":
			result.Collector = &value
		case "from", "to":
			millis, err := strconv.ParseInt(value, 10, 64)
			if err != nil {
				return result, &api.Fault{Status: 400, Code: "invalid_params", Message: name + " 必须是 UTC Unix 毫秒整数", Cause: err}
			}
			if name == "from" {
				result.From = &millis
			} else {
				result.To = &millis
			}
		default:
			return result, &api.Fault{Status: 400, Code: "invalid_params", Message: "不支持查询参数 " + name + "，只能使用 range、from、to、collector"}
		}
	}
	return result, nil
}
func dashboardDefinition(cfg *config.Config, id string) (api.DashboardDefinition, error) {
	var result api.DashboardDefinition
	index := slices.IndexFunc(cfg.DashboardStates, func(state config.DashboardState) bool { return state.ID == id })
	if index < 0 {
		return result, &api.Fault{Status: 404, Code: "dashboard_not_found", Message: "面板页不存在，请刷新面板列表"}
	}
	state := cfg.DashboardStates[index]
	index = slices.IndexFunc(cfg.Dashboards, func(definition config.Dashboard) bool { return definition.ID == id })
	if index < 0 {
		message := "面板文件尚未成功加载"
		if state.Error != nil {
			message = *state.Error
		}
		return result, &api.Fault{Status: 422, Code: "invalid_dashboard", Message: message, Cause: state.Issues}
	}
	definition := cfg.Dashboards[index]
	result = api.DashboardDefinition{ID: id, Title: definition.Title, Refresh: definition.Refresh, DefaultRange: definition.DefaultRange, File: state.File, Version: state.Version, Error: state.Error, Panels: []api.DashboardPanel{}}
	for _, panel := range definition.Panels {
		output := api.DashboardPanel{DashboardPanel: panel, Parameters: []string{}}
		for _, meta := range state.Panels {
			if meta.ID == panel.ID {
				output.Parameters = append(output.Parameters, meta.Parameters...)
				output.Error = meta.Error
			}
		}
		result.Panels = append(result.Panels, output)
	}
	return result, nil
}
func (b *APIBackend) dashboard(ctx context.Context, cfg *config.Config, operation api.Operation, request api.Request) (any, error) {
	if operation == api.ListDashboards {
		result := make([]api.DashboardSummary, 0, len(cfg.DashboardStates))
		for _, state := range cfg.DashboardStates {
			item := api.DashboardSummary{ID: state.ID, Title: state.ID, File: state.File, Version: state.Version, Error: state.Error}
			for _, definition := range cfg.Dashboards {
				if definition.ID == state.ID {
					item.Title = definition.Title
					item.PanelCount = len(definition.Panels)
				}
			}
			result = append(result, item)
		}
		slices.SortFunc(result, func(a, b api.DashboardSummary) int { return strings.Compare(a.ID, b.ID) })
		return result, nil
	}
	definition, err := dashboardDefinition(cfg, request.Params["id"])
	if err != nil {
		return nil, err
	}
	if operation == api.GetDashboard {
		return definition, nil
	}
	index := slices.IndexFunc(definition.Panels, func(panel api.DashboardPanel) bool { return panel.ID == request.Params["panel"] })
	if index < 0 {
		return nil, &api.Fault{Status: 404, Code: "panel_not_found", Message: "卡片不存在，请刷新当前面板"}
	}
	panel := definition.Panels[index]
	if panel.Error != nil {
		return nil, &api.Fault{Status: 400, Code: "invalid_query", Message: *panel.Error}
	}
	input, err := dashboardParams(request.Query)
	if err != nil {
		return nil, err
	}
	result, err := b.runSQL(ctx, panel.Query, input, definition.DefaultRange)
	if err != nil {
		return nil, err
	}
	if err := store.ValidatePanelResult(panel.Type, result.QueryResult); err != nil {
		return nil, &api.Fault{Status: 422, Code: "panel_columns", Message: err.Error(), Cause: err}
	}
	return api.PanelData{QueryResult: result, Version: definition.Version}, nil
}
