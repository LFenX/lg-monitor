package api

const (
	GetCardActions Operation = "get_card_actions"
	PutCardActions Operation = "put_card_actions"
)

type CardActionsInput struct {
	Enabled bool `json:"enabled"`
}

type CardActionsStatus struct {
	Notifier  string `json:"notifier"`
	App       string `json:"app"`
	Enabled   bool   `json:"enabled"`
	State     string `json:"state" jsonschema:"enum=disabled,enum=waiting,enum=connecting,enum=connected,enum=reconnecting,enum=error,enum=disconnected,enum=stopped"`
	UpdatedAt int64  `json:"updatedAt"`
}

func CardActionRoutes() []Route {
	return []Route{
		{GetCardActions, "GET", "/api/v1/notifiers/{id}/card-actions", "读取通知群的卡片动作授权与应用长连接状态", ScopeRead, nil, CardActionsStatus{}, nil},
		{PutCardActions, "PUT", "/api/v1/notifiers/{id}/card-actions", "管理员授权或撤销原消息收件人的确认与一小时告警组静音权限", ScopeAdmin, CardActionsInput{}, CardActionsStatus{}, nil},
	}
}
