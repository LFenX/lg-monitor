// Copyright (c) 2026 LFen (https://lfen.ink) — lg-monitor

package api

// Boards are the page people live on: a named group of monitors with their child services.
const (
	ListBoards  Operation = "list_boards"
	GetBoard    Operation = "get_board"
	CreateBoard Operation = "create_board"
	UpdateBoard Operation = "update_board"
	DeleteBoard Operation = "delete_board"
)

type BoardSummary struct {
	Services int      `json:"services" jsonschema_description:"面板上的主服务数（顶层）。"`
	Children int      `json:"children" jsonschema_description:"子服务数，包含多层。"`
	Up       int      `json:"up" jsonschema_description:"自身正常的服务数，含子服务。"`
	Down     int      `json:"down" jsonschema_description:"自身异常的服务数，含子服务。"`
	Unknown  int      `json:"unknown" jsonschema_description:"还没有数据或数据过期的服务数，含子服务。"`
	Uptime   *float64 `json:"uptime" jsonschema:"nullable" jsonschema_description:"面板上全部服务最近 24 小时可用率的平均值，百分比。"`
	State    string   `json:"state" jsonschema:"enum=up,enum=down,enum=unknown,enum=empty" jsonschema_description:"面板整体：有任一异常为 down；empty 表示还没有服务。"`
}

type BoardInfo struct {
	ID          string            `json:"id"`
	Name        string            `json:"name"`
	Description string            `json:"description,omitempty"`
	Monitors    []string          `json:"monitors" jsonschema_description:"面板上列出的监控 ID（不含随父服务显示的子服务）。"`
	Summary     BoardSummary      `json:"summary"`
	URL         string            `json:"url" jsonschema_description:"把这个地址交给用户，在浏览器里打开面板。"`
	CardStyle   string            `json:"cardStyle" jsonschema_description:"卡片视图的默认样式。"`
	Cards       map[string]string `json:"cards,omitempty" jsonschema_description:"单独指定了样式的服务。"`
}

type BoardView struct {
	BoardInfo
	Items      []Monitor `json:"items" jsonschema_description:"按显示顺序展开的服务：每个主服务后面紧跟它的子服务（depth 表示层级）。"`
	Unassigned []string  `json:"unassigned" jsonschema_description:"不在任何面板上的监控 ID；可用 update_board 的 add 放进来。"`
}

type BoardCreateInput struct {
	Name        string `json:"name" jsonschema:"minLength=1,maxLength=40" jsonschema_description:"面板名称，例如“我的服务”。"`
	ID          string `json:"id,omitempty" jsonschema:"pattern=^[a-z0-9-]+$,maxLength=48" jsonschema_description:"稳定 ID；省略时由名称生成。"`
	Description string `json:"description,omitempty" jsonschema:"maxLength=200" jsonschema_description:"一句话介绍；打开内网可见时别人能看到名称和这句介绍。"`
}

type BoardUpdateInput struct {
	Name        *string           `json:"name,omitempty" jsonschema:"minLength=1,maxLength=40"`
	Description *string           `json:"description,omitempty" jsonschema:"maxLength=200"`
	Monitors    []string          `json:"monitors,omitempty" jsonschema_description:"整体替换面板上的监控及顺序；省略时不变。"`
	Add         []string          `json:"add,omitempty" jsonschema_description:"追加到末尾的监控 ID。"`
	Remove      []string          `json:"remove,omitempty" jsonschema_description:"从这个面板移走的监控 ID；监控本身不删除。"`
	CardStyle   *string           `json:"cardStyle,omitempty" jsonschema:"enum=pulse,enum=minimal,enum=ring,enum=latency,enum=days,enum=number" jsonschema_description:"卡片视图的默认样式：pulse 心跳、minimal 极简、ring 可用率环、latency 响应曲线、days 30 天、number 大数字。"`
	Cards       map[string]string `json:"cards,omitempty" jsonschema_description:"给单个服务指定样式：服务 ID → 样式；值为空字符串表示改回跟随面板默认。只改列出的服务。"`
}

func boardRoutes() []Route {
	return []Route{
		{ListBoards, "GET", "/api/v1/boards", "全部面板及其摘要：服务数、子服务数、正常/异常数和 24 小时可用率", ScopeRead, nil, []BoardInfo{}, nil},
		{GetBoard, "GET", "/api/v1/boards/{id}", "一个面板的摘要和服务树（主服务后面紧跟子服务）", ScopeRead, nil, BoardView{}, nil},
		{CreateBoard, "POST", "/api/v1/boards", "新建面板；第一个面板会自动收进还没有归属的监控", ScopeConfig, BoardCreateInput{}, BoardView{}, nil},
		{UpdateBoard, "PUT", "/api/v1/boards/{id}", "改面板名称、简介、卡片样式，或调整面板上的监控", ScopeConfig, BoardUpdateInput{}, BoardView{}, nil},
		{DeleteBoard, "DELETE", "/api/v1/boards/{id}", "删除面板；上面的监控保留", ScopeConfig, nil, Confirmation{}, nil},
	}
}
