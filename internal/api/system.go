// Copyright (c) 2026 LFen (https://lfen.ink) — lg-monitor

package api

const (
	RestartSystem      Operation = "restart_system"
	GetSystemUpdate    Operation = "get_system_update"
	ActionSystemUpdate Operation = "action_system_update"
)

// SystemRestartResult confirms that lgmon has initiated a graceful restart.
type SystemRestartResult struct {
	OK      bool   `json:"ok"`
	Mode    string `json:"mode" jsonschema:"enum=service,enum=process"`
	Message string `json:"message"`
}

// SystemUpdateState reports the in-app updater status and download progress for the Web UI.
type SystemUpdateState struct {
	State   string `json:"state" jsonschema:"enum=dev,enum=idle,enum=checking,enum=none,enum=available,enum=downloading,enum=ready,enum=applying,enum=error"`
	During  string `json:"during,omitempty" jsonschema:"enum=,enum=check,enum=download,enum=apply"`
	Message string `json:"message,omitempty"`
	Current string `json:"current,omitempty"`
	Version string `json:"version,omitempty"`
	Notes   string `json:"notes,omitempty"`
	Page    string `json:"page,omitempty"`
	Size    int64  `json:"size,omitempty"`
	Done    int64  `json:"done,omitempty"`
	Total   int64  `json:"total,omitempty"`
	Checked int64  `json:"checkedAt,omitempty"`
}

// SystemUpdateActionInput triggers a step in the in-app update flow from the Web dashboard.
type SystemUpdateActionInput struct {
	Action string `json:"action" jsonschema:"enum=check,enum=refresh,enum=download,enum=apply"`
}

func systemRoutes() []Route {
	return []Route{
		{RestartSystem, "POST", "/api/v1/system/restart", "优雅重启 lgmon 服务或本机进程（常用于数据目录迁移后切换生效）", ScopeConfig, nil, SystemRestartResult{}, nil},
		{GetSystemUpdate, "GET", "/api/v1/system/update", "读取应用内更新检查与下载进度状态", ScopeRead, nil, SystemUpdateState{}, nil},
		{ActionSystemUpdate, "POST", "/api/v1/system/update", "触发应用内更新流程：检查更新、后台校验下载安装包或启动原位升级", ScopeConfig, SystemUpdateActionInput{}, SystemUpdateState{}, nil},
	}
}
