package api

import (
	"github.com/LFenX/lg-monitor/internal/collector"
	"github.com/LFenX/lg-monitor/internal/store"
)

const (
	ListServiceOperations Operation = "list_service_operations"
	RestartService        Operation = "restart_service"
	GetServiceOperation   Operation = "get_service_operation"
)

type ServiceOperationInput struct {
	RequestID string `json:"requestId" jsonschema:"minLength=32,maxLength=32,pattern=^[a-f0-9]+$"`
	GrantID   string `json:"grantId" jsonschema:"minLength=1,maxLength=64,pattern=^[a-z0-9-]+$"`
	Version   string `json:"version" jsonschema:"minLength=64,maxLength=64,pattern=^[a-f0-9]+$"`
	Reason    string `json:"reason" jsonschema:"minLength=1,maxLength=200"`
	Confirmed bool   `json:"confirmed" jsonschema_description:"必须为 true，表示本人已核对采集器、精确服务及重启会中断服务；未知结果不得自动用新 ID 重试。"`
}

func ServiceOperationRoutes() []Route {
	return []Route{
		{ListServiceOperations, "GET", "/api/v1/collectors/{id}/operations", "读取在线采集器的本机精确服务授权及策略版本", ScopeRead, nil, collector.OperationPolicy{}, nil},
		{RestartService, "POST", "/api/v1/collectors/{id}/operations", "管理员确认后重启采集器本机逐条授权的精确服务；相同 requestId 不重复执行", ScopeAdmin, ServiceOperationInput{}, store.OperationRequest{}, nil},
		{GetServiceOperation, "GET", "/api/v1/operations/{id}", "只读查询已知操作意图与结果；向采集器对账不触发服务操作", ScopeRead, nil, store.OperationRequest{}, nil},
	}
}
