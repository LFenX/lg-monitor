package api

import (
	"github.com/LFenX/lg-monitor/internal/config"
	"github.com/LFenX/lg-monitor/internal/store"
)

const (
	TeamState         Operation = "team_state"
	ListTeams         Operation = "list_teams"
	CreateTeam        Operation = "create_team"
	ListTeamMembers   Operation = "list_team_members"
	InviteTeamMember  Operation = "invite_team_member"
	RemoveTeamMember  Operation = "remove_team_member"
	ListShares        Operation = "list_shares"
	CreateShare       Operation = "create_share"
	UpdateShare       Operation = "update_share"
	RevokeShare       Operation = "revoke_share"
	PreviewTeamJoin   Operation = "preview_team_join"
	JoinTeam          Operation = "join_team"
	ListJoinedTeams   Operation = "list_joined_teams"
	ChangeTeamAddress Operation = "change_team_address"
	LeaveTeam         Operation = "leave_team"
	ListJoinedShares  Operation = "list_joined_shares"
	GetJoinedShare    Operation = "get_joined_share"
	GetJoinedPanel    Operation = "get_joined_panel"
)

type TeamStateResult struct {
	Owned   int  `json:"owned"`
	Joined  int  `json:"joined"`
	Enabled bool `json:"enabled"`
}
type TeamCreateInput struct {
	Name      string `json:"name" jsonschema:"minLength=1,maxLength=256"`
	OwnerName string `json:"ownerName,omitempty" jsonschema:"minLength=1,maxLength=256"`
}
type TeamInviteInput struct {
	Name string          `json:"name" jsonschema:"minLength=1,maxLength=256"`
	TTL  config.Duration `json:"ttl,omitempty"`
}
type TeamInviteResult struct {
	Invitation  string `json:"invitation"`
	Address     string `json:"address"`
	Fingerprint string `json:"fingerprint"`
	ExpiresAt   int64  `json:"expiresAt"`
}
type ShareInput struct {
	TeamID      string   `json:"teamId"`
	DashboardID string   `json:"dashboardId"`
	Panels      []string `json:"panels,omitempty"`
	Collectors  []string `json:"collectors,omitempty"`
	Targets     []string `json:"targets,omitempty"`
	Members     []string `json:"members,omitempty"`
	Revision    int64    `json:"revision,omitempty"`
}
type TeamJoinInput struct {
	Invitation  string `json:"invitation"`
	Fingerprint string `json:"fingerprint,omitempty"`
}
type TeamJoinPreview struct {
	Address     string `json:"address"`
	Fingerprint string `json:"fingerprint"`
	TeamID      string `json:"teamId"`
}
type TeamAddressInput struct {
	Address string `json:"address"`
}

type SharedPanel struct {
	ID    string `json:"id"`
	Type  string `json:"type"`
	Title string `json:"title"`
	Width int    `json:"width"`
}
type SharedCollector struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}
type SharedDashboard struct {
	ID           string            `json:"id"`
	TeamID       string            `json:"teamId"`
	TeamName     string            `json:"teamName"`
	OwnerName    string            `json:"ownerName"`
	DashboardID  string            `json:"dashboardId"`
	Title        string            `json:"title"`
	Revision     int64             `json:"revision"`
	Version      string            `json:"version"`
	Refresh      config.Duration   `json:"refresh"`
	DefaultRange config.Duration   `json:"defaultRange"`
	Panels       []SharedPanel     `json:"panels"`
	Collectors   []SharedCollector `json:"collectors"`
}
type SharedPanelData struct {
	QueryResult
	Version  string `json:"version"`
	Revision int64  `json:"revision"`
}

func TeamRoutes() []Route {
	period := []Parameter{{"range", "时间范围；默认面板的 defaultRange"}, {"from", "UTC Unix 毫秒；与 range 互斥"}, {"to", "UTC Unix 毫秒；默认现在"}, {"collector", "分享范围内的采集器 ID"}}
	return []Route{
		{TeamState, "GET", "/api/v1/teams/state", "本机自建和加入团队的计数，不授予访问权", ScopeRead, nil, TeamStateResult{}, nil},
		{ListTeams, "GET", "/api/v1/teams", "负责人列出本机自建团队", ScopeAdmin, nil, []store.Team{}, nil},
		{CreateTeam, "POST", "/api/v1/teams", "负责人建立团队，不安装服务或修改防火墙", ScopeAdmin, TeamCreateInput{}, store.Team{}, nil},
		{ListTeamMembers, "GET", "/api/v1/teams/{id}/members", "列出成员设备与最后访问记录", ScopeAdmin, nil, []store.TeamMember{}, nil},
		{InviteTeamMember, "POST", "/api/v1/teams/{id}/invites", "生成一次性团队邀请，默认24小时，仅本次返回", ScopeAdmin, TeamInviteInput{}, TeamInviteResult{}, nil},
		{RemoveTeamMember, "DELETE", "/api/v1/teams/{id}/members/{member}", "吊销成员设备身份并断开连接", ScopeAdmin, nil, Confirmation{}, nil},
		{ListShares, "GET", "/api/v1/shares", "负责人列出分享与范围", ScopeAdmin, nil, []store.TeamShare{}, []Parameter{{"team", "可选的团队 ID"}}},
		{CreateShare, "POST", "/api/v1/shares", "分享声明式面板的全部或选定卡片与数据范围", ScopeAdmin, ShareInput{}, store.TeamShare{}, nil},
		{UpdateShare, "PUT", "/api/v1/shares/{id}", "按当前 revision 整体更新分享范围", ScopeAdmin, ShareInput{}, store.TeamShare{}, nil},
		{RevokeShare, "DELETE", "/api/v1/shares/{id}", "撤销分享并取消在途读取", ScopeAdmin, nil, Confirmation{}, nil},
		{PreviewTeamJoin, "POST", "/api/v1/team-joins/preview", "只检查邀请格式与公开 CA 指纹，不消耗邀请码", ScopeAdmin, TeamJoinInput{}, TeamJoinPreview{}, nil},
		{JoinTeam, "POST", "/api/v1/team-joins", "核对指纹后加入团队，设备私钥只存本机密钥库", ScopeAdmin, TeamJoinInput{}, store.JoinedTeam{}, nil},
		{ListJoinedTeams, "GET", "/api/v1/team-joins", "列出本机已加入团队，不返回证书或密钥引用", ScopeRead, nil, []store.JoinedTeam{}, nil},
		{ChangeTeamAddress, "PUT", "/api/v1/team-joins/{id}", "修改负责人地址，保留证书和 CA 指纹", ScopeAdmin, TeamAddressInput{}, store.JoinedTeam{}, nil},
		{LeaveTeam, "DELETE", "/api/v1/team-joins/{id}", "移除本机加入记录，不修改负责人的成员授权", ScopeAdmin, nil, Confirmation{}, nil},
		{ListJoinedShares, "GET", "/api/v1/team-joins/{id}/shares", "实时列出负责人分享给本设备的声明式面板", ScopeRead, nil, []SharedDashboard{}, nil},
		{GetJoinedShare, "GET", "/api/v1/team-joins/{id}/shares/{share}", "读取获授权的卡片定义和主机选项，不返回 SQL", ScopeRead, nil, SharedDashboard{}, nil},
		{GetJoinedPanel, "GET", "/api/v1/team-joins/{id}/shares/{share}/panels/{panel}", "只读取得获授权卡片结果，不接受 SQL", ScopeRead, nil, SharedPanelData{}, period},
	}
}
