package api

import "strings"

// MCPModule is one area of lgmon as an agent meets it: what it is for, when to reach for it, and its tools.
type MCPModule struct {
	Name    string   `json:"name"`
	Purpose string   `json:"purpose" jsonschema_description:"这个模块做什么、什么时候用、要注意什么。"`
	Tools   []string `json:"tools"`
}

// MCPModules covers every MCP tool exactly once; a test keeps it in step with the bindings and docs/mcp.md.
var MCPModules = []MCPModule{
	{Name: "开始", Purpose: "第一步永远是 start_here：现状、你的权限、下一步和推荐顺序。任何工具报错不知所措时也回到它。", Tools: []string{"start_here"}},
	{Name: "面板与服务", Purpose: "日常最常用。面板是一组服务，主服务下面可以挂子服务（隧道、网关、数据库等，传 parent）。add_monitor 一次建好监控、告警和通知并放进面板，同一个 id 再调用就是更新；get_monitor 看 24 小时、30 天和最近 7 天的异常时段；edit_monitor 改名字、挂靠、说明、角色和依赖（依赖关系图）；get_monitor_doc / put_monitor_doc 读写服务的 Markdown 文档；update_board 还能换卡片样式。删除前先向用户确认。", Tools: []string{"list_boards", "get_board", "create_board", "update_board", "delete_board", "add_monitor", "list_monitors", "get_monitor", "edit_monitor", "remove_monitor", "get_monitor_doc", "put_monitor_doc"}},
	{Name: "这台电脑", Purpose: "找要监控的东西、看采集器和原始数据。加服务前先 explore（端口、Windows 服务、进程、计划任务，结果里的 suggest 可以直接交给 add_monitor）；同名进程要按命令行区分时先问用户关键词，再 test_target_match。", Tools: []string{"explore", "list_collectors", "list_targets", "get_target_history", "list_processes", "test_target_match", "get_metrics_history", "export_metrics"}},
	{Name: "告警", Purpose: "排查和处理告警。先 list_alerts，再 get_alert_diagnostics 和 get_alert_events 看前后发生了什么；规则没按预期触发时 list_rule_errors，想先试一条规则用 test_rule 回放。ack_alert、silence 会改变告警或通知，先确认。", Tools: []string{"list_alerts", "get_alert_diagnostics", "get_alert_events", "ack_alert", "silence", "delete_silence", "list_rule_errors", "test_rule"}},
	{Name: "通知与飞书", Purpose: "把告警发到飞书群。先 feishu_check，用 feishu_list_chats 找到用户指定的群，feishu_test_chat 发测试卡片，用户明确说收到后才 feishu_save_chat。应用密钥只由用户在本机填写，不经过 Agent。test_notifier 会真的发一条测试通知。", Tools: []string{"feishu_check", "feishu_list_chats", "feishu_test_chat", "feishu_save_chat", "test_notifier"}},
	{Name: "告警卡片模板", Purpose: "飞书告警卡片的样式和字段。改之前先 preview_template，四套预设不能覆盖或删除。", Tools: []string{"list_templates", "get_template", "preview_template", "put_template", "delete_template"}},
	{Name: "自定义视图", Purpose: "面板之外的图表页，只在用户明确想要图表时用。可以按模板 create_dashboard，或者先 get_schema 再 put_dashboard 写只读 SQL；保存前服务器会真的执行每条查询并指出问题。", Tools: []string{"list_dashboard_templates", "create_dashboard", "put_dashboard", "delete_dashboard", "list_dashboards", "get_dashboard", "get_panel_data"}},
	{Name: "数据与 SQL", Purpose: "直接查监控数据库：先 get_schema，再 query_sql（只读，五秒、一万行以内）。", Tools: []string{"get_schema", "query_sql"}},
	{Name: "配置文件", Purpose: "高级：直接读写 targets、rules、notifiers、settings。加服务不需要它（用 add_monitor）；要改时先 get_config 保留已有内容，validate_config 检查草稿，再 put_config。不能写入密钥。", Tools: []string{"get_config", "validate_config", "put_config"}},
	{Name: "服务开关导入", Purpose: "把采集器上服务开关工具的 services.json 导入成监控目标。先预览、展示变化，用户同意后再导入。", Tools: []string{"preview_service_switch_import", "import_service_switch"}},
	{Name: "远程重启服务", Purpose: "只能重启采集器本机逐条授权过的服务，会真的中断服务。每次都要向用户展示采集器、服务和原因并取得明确确认；结果不明时只查原 requestId。", Tools: []string{"list_service_operations", "get_service_operation", "restart_service"}},
	{Name: "内网共享", Purpose: "同一局域网里互相申请查看面板。打开内网可见、同意申请都要用户明确要求；同意前让用户和对方核对 6 位配对码。", Tools: []string{"lan_status", "lan_configure", "lan_discover", "lan_request", "lan_requests", "lan_approve", "lan_deny", "lan_grants", "lan_revoke", "lan_remote", "lan_remote_board"}},
	{Name: "团队与分享", Purpose: "跨网络把自定义视图分享给团队成员（负责人和成员两侧）。建团队、邀请、分享和加入都要用户明确要求，邀请和指纹通过用户选择的安全方式核对。", Tools: []string{"get_team_state", "list_teams", "create_team", "list_team_members", "invite_team_member", "remove_team_member", "list_shares", "create_share", "update_share", "revoke_share", "preview_team_join", "join_team", "list_joined_teams", "change_team_address", "leave_team", "list_joined_shares", "get_joined_share", "get_joined_panel"}},
	{Name: "波西米亚", Purpose: "面板里的助手。get_assistant 看还差什么；configure_assistant 保存 DeepSeek 和百炼的密钥（只能由用户本人提供，先验证再保存，调用后不要复述）。", Tools: []string{"get_assistant", "configure_assistant"}},
}

// mcpInstructions is what an agent reads when it connects: the ground rules, then a map of every module.
func mcpInstructions(essential bool) string {
	var text strings.Builder
	text.WriteString("lgmon 监控 Windows 电脑上的服务：端口、进程、Windows 服务、网址和计划任务，按面板组织，主服务下面可以挂子服务。" +
		"你可以替用户完成 lgmon 里的所有事情：加服务、看状态、排查告警、接飞书、做图表、内网共享和配置助手。" +
		"第一步永远是调用 start_here，它会告诉你现状、你的权限和下一步。" +
		"要监控什么、发到哪个群、想怎么改，不清楚就直接问用户，不要让用户自己去查端口、进程名或群 ID。" +
		"这些工具一次调用就完成整件事，不要手写配置文件。" +
		"工具返回 isError 时读 error.message 并按它说的修改，不要原样重试。" +
		"不要停止或重启用户已有的进程和服务（远程重启除外，且每次都要用户明确确认）；不要主动索取密钥，用户主动提供的密钥只交给对应的保存工具，之后不复述；时间均为 UTC Unix 毫秒。")
	if essential {
		text.WriteString("当前连接只有常用工具；需要下面列出但这里没有的工具时，按 start_here 里的接入命令改用完整工具集（--tools all）。")
	}
	text.WriteString("\n\n模块：")
	for _, module := range MCPModules {
		text.WriteString("\n- " + module.Name + "：" + module.Purpose + "工具：" + strings.Join(module.Tools, "、") + "。")
	}
	return text.String()
}
