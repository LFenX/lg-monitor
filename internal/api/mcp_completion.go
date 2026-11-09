package api

import "github.com/LFenX/lg-monitor/internal/config"

type mcpTeamInvite struct {
	mcpID
	TeamInviteInput
}

type mcpTeamMember struct {
	mcpID
	Member string `json:"member" jsonschema:"minLength=1"`
}

type mcpShareUpdate struct {
	mcpID
	ShareInput
}

type mcpTeamAddress struct {
	mcpID
	TeamAddressInput
}

type mcpJoinedShare struct {
	mcpID
	Share string `json:"share" jsonschema:"minLength=1"`
}

type mcpJoinedPanel struct {
	mcpJoinedShare
	Panel     string           `json:"panel" jsonschema:"minLength=1"`
	Range     *config.Duration `json:"range,omitempty"`
	From      *int64           `json:"from,omitempty" jsonschema:"minimum=0"`
	To        *int64           `json:"to,omitempty" jsonschema:"minimum=0"`
	Collector *string          `json:"collector,omitempty"`
}

type mcpShares struct {
	Team string `json:"team,omitempty"`
}

type mcpMetricHistory struct {
	Collector string `json:"collector,omitempty"`
	Name      string `json:"name,omitempty"`
	From      *int64 `json:"from,omitempty" jsonschema:"minimum=0"`
	To        *int64 `json:"to,omitempty" jsonschema:"minimum=0"`
	Step      string `json:"step,omitempty" jsonschema:"enum=raw,enum=1m,enum=1h"`
}

func mcpCompletionBindings() []mcpBinding {
	return []mcpBinding{
		{name: "get_metrics_history", description: "只读取得系统/脚本指标历史；先用 get_schema 核对指标名称，时间为 UTC Unix 毫秒。最低 read。", operation: Metrics, input: mcpMetricHistory{}, query: []string{"collector", "name", "from", "to", "step"}},
		{name: "delete_silence", description: "撤销已有静音，恢复该规则或目标的通知。须有本人撤销该静音的明确意图。最低 config。", operation: DeleteSilence, input: mcpID{}, path: []string{"id"}},
		{name: "get_template", description: "读取预设或自定义模板定义，修改前保留原字段。最低 read。", operation: GetTemplate, input: mcpID{}, path: []string{"id"}},
		{name: "delete_template", description: "删除自定义模板，不能删除预设或仍被引用的模板。先核对本人删除意图。最低 config。", operation: DeleteTemplate, input: mcpID{}, path: []string{"id"}},
		{name: "get_team_state", description: "只读取得本机自建/加入团队计数，不授予任何团队权限。最低 read。", operation: TeamState, input: struct{}{}},
		{name: "list_teams", description: "负责人列出本机自建团队，不授予成员读取权限。最低 admin。", operation: ListTeams, input: struct{}{}},
		{name: "create_team", description: "按本人明确要求建立自建团队；不会安装服务或改防火墙。先确认团队名称和公开地址配置。最低 admin。", operation: CreateTeam, input: TeamCreateInput{}, body: true},
		{name: "list_team_members", description: "负责人查看团队成员设备和最近访问记录。最低 admin。", operation: ListTeamMembers, input: mcpID{}, path: []string{"id"}},
		{name: "invite_team_member", description: "按本人明确要求生成一次性成员邀请，仅本次返回；先确认接收人和期限，通过本人选择的安全方式交付，不记录邀请到日志或仓库。最低 admin。", operation: InviteTeamMember, input: mcpTeamInvite{}, path: []string{"id"}, body: true},
		{name: "remove_team_member", description: "吊销精确成员设备，立即中断读取；先取得本人对团队/成员的明确删除意图。最低 admin。", operation: RemoveTeamMember, input: mcpTeamMember{}, path: []string{"id", "member"}},
		{name: "list_shares", description: "负责人列出已有分享和范围；team 省略表示所有自建团队。最低 admin。", operation: ListShares, input: mcpShares{}, query: []string{"team"}},
		{name: "create_share", description: "把指定声明式面板分享给团队成员；先向本人展示卡片、主机、目标和成员范围并确认，空范围的含义沿用现有 HTTP API。成员不能再分享。最低 admin。", operation: CreateShare, input: ShareInput{}, body: true},
		{name: "update_share", description: "整体更新分享范围；先 list_shares 读取当前 revision，向本人展示范围变化并确认。并发变更必须重新读取，不能擅自扩大范围。最低 admin。", operation: UpdateShare, input: mcpShareUpdate{}, path: []string{"id"}, body: true},
		{name: "revoke_share", description: "撤销精确分享并取消在途读取，须有本人撤销该分享的明确意图。最低 admin。", operation: RevokeShare, input: mcpID{}, path: []string{"id"}},
		{name: "preview_team_join", description: "只检查邀请格式、负责人地址和公开 CA 指纹，不消耗邀请；先展示结果，让本人从独立来源核对指纹。不要把邀请记录到日志。最低 admin。", operation: PreviewTeamJoin, input: TeamJoinInput{}, body: true},
		{name: "join_team", description: "消费一次性邀请加入团队；必须先 preview_team_join，并由本人从独立来源确认指纹和负责人地址，传入已核对 fingerprint。设备私钥仅存本机。最低 admin。", operation: JoinTeam, input: TeamJoinInput{}, body: true},
		{name: "list_joined_teams", description: "只读列出本机已加入团队，不返回证书、密钥或邀请。最低 read。", operation: ListJoinedTeams, input: struct{}{}},
		{name: "change_team_address", description: "按本人明确要求改变已加入团队的负责人地址；保留原证书和指纹，不跳过身份校验。最低 admin。", operation: ChangeTeamAddress, input: mcpTeamAddress{}, path: []string{"id"}, body: true},
		{name: "leave_team", description: "按本人明确要求删除本机加入记录；不代替负责人吊销成员。最低 admin。", operation: LeaveTeam, input: mcpID{}, path: []string{"id"}},
		{name: "list_joined_shares", description: "实时只读列出负责人授权给本设备的声明式面板，不复制授权或再次分享。最低 read。", operation: ListJoinedShares, input: mcpID{}, path: []string{"id"}},
		{name: "get_joined_share", description: "读取已授权的卡片定义和主机范围，不返回 SQL。最低 read。", operation: GetJoinedShare, input: mcpJoinedShare{}, path: []string{"id", "share"}},
		{name: "get_joined_panel", description: "实时只读取得已授权卡片结果，不接受 SQL。时间/主机参数沿用现有分享接口，不能越过主机、目标或成员范围。最低 read。", operation: GetJoinedPanel, input: mcpJoinedPanel{}, path: []string{"id", "share", "panel"}, query: []string{"range", "from", "to", "collector"}},
	}
}
