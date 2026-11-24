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
	State    string `json:"state" jsonschema:"enum=window,enum=dev,enum=idle,enum=checking,enum=none,enum=available,enum=downloading,enum=ready,enum=applying,enum=error"`
	During   string `json:"during,omitempty" jsonschema:"enum=,enum=check,enum=download,enum=apply"`
	Message  string `json:"message,omitempty"`
	Current  string `json:"current,omitempty"`
	Version  string `json:"version,omitempty"`
	Platform string `json:"platform,omitempty"`
	Notes    string `json:"notes,omitempty"`
	Page     string `json:"page,omitempty"`
	Size     int64  `json:"size,omitempty"`
	Done     int64  `json:"done,omitempty"`
	Total    int64  `json:"total,omitempty"`
	Checked  int64  `json:"checkedAt,omitempty"`
}

// SystemUpdateActionInput triggers a step in the in-app update flow from the Web dashboard.
type SystemUpdateActionInput struct {
	Action string `json:"action" jsonschema:"enum=check,enum=refresh,enum=download,enum=apply"`
	Force  bool   `json:"force,omitempty"`
}

func systemRoutes() []Route {
	return []Route{
		{RestartSystem, "POST", "/api/v1/system/restart", "优雅重启 lgmon 服务或本机进程（常用于数据目录迁移后切换生效）", ScopeConfig, nil, SystemRestartResult{}, nil},
		{GetSystemUpdate, "GET", "/api/v1/system/update", "读取应用内更新检查与下载进度状态", ScopeRead, nil, SystemUpdateState{}, nil},
		{ActionSystemUpdate, "POST", "/api/v1/system/update", "检查可用版本，或在监控器的桌面小窗中继续更新", ScopeConfig, SystemUpdateActionInput{}, SystemUpdateState{}, nil},
	}
}
