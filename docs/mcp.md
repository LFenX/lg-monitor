# MCP 接口

`export_metrics`（read）取得与 Prometheus/OTLP 同源的结构化持久快照，包含单位和真实观测时间，不发送数据或启用出口。配置、指标表和新鲜度语义见 [指标导出](exports.md)。导出设置沿用 `put_config(settings)` 的 admin 权限，凭据由本人通过本机密钥命令保存。

飞书卡片动作授权由本人在专用管理员网页或本机 CLI 完成，不提供 MCP 委托工具。put_config(notifiers) 可保留/撤销已有授权，不能新增或转移；已有 ack_alert/create_silence 不变。见 [卡片动作](card-actions.md)。

使用官方 [Go SDK v1.8.0](https://github.com/modelcontextprotocol/go-sdk/releases/tag/v1.8.0)。工具通过现有 HTTP API 处理，权限、SQL 只读限制、通知限流、配置校验和错误结构与 [API](api.md) 一致。

## 连接

监控器正常运行时，在同一 HTTP 监听地址的 `/mcp` 提供 Streamable HTTP。每个请求必须带 `Authorization: Bearer <token>`；不从 URL、Cookie 或工具参数读取令牌。本机会话只有 read 权限。Host 与 Origin 沿用 HTTP 白名单，不开放跨域。

也可以让 MCP 客户端启动 `lgmon mcp --url http://127.0.0.1:7444`，将令牌放入该子进程的环境变量 `LGMON_TOKEN`。stdio 只承载协议，日志写 stderr。`--url` 默认本机 7444，可指定内网监控器；不会读取本机监控数据库。不要在命令参数、配置示例、聊天记录或 Git 中填写真实令牌。

示例仅展示客户端常见配置结构，令牌使用假的占位值；按客户端自身方式安全注入实际环境变量：

```json
{
  "mcpServers": {
    "lg-monitor": {
      "command": "D:\\lg-monitor\\lgmon.exe",
      "args": ["mcp", "--url", "http://127.0.0.1:7444"],
      "env": {"LGMON_TOKEN": "FAKE_REPLACE_LOCALLY"}
    }
  }
}
```

HTTP 方式采用 SDK 的无持久会话模式，每次重新认证，不依赖会话 ID。返回 Streamable HTTP SSE 响应；不提供独立 GET 订阅流，实时监控数据仍用 `/api/v1/stream`。SDK 协议及兼容范围见 [官方传输文档](https://github.com/modelcontextprotocol/go-sdk/blob/v1.8.0/docs/protocol.md#streamable-transport)。普通工具参数上限 1 MiB；配置校验的草稿另受 64 文件、单文件 1 MiB、内容合计 4 MiB 的限制。为容纳 JSON 转义，MCP HTTP 编码消息上限 25 MiB，stdio 帧再留 1 KiB 协议开销。单次结果上限 16 MiB，超出时缩小查询范围或减少列。不自动重试有副作用的工具；HTTP 代理不跟随重定向。

## 工具

当前共 56 个工具。团队管理与既有网页共用 admin 权限，共享读取为 read；执行前必须有本人的明确团队/分享意图，先展示范围、核对邀请指纹并保留 revision，不自行扩大授权或发送邀请。见 [团队共享](teams.md)。全局通知暂停按 REVIEW M1-17 继续只由人操作，不提供 MCP 工具。

所有工具均提供场景说明与 JSON Schema，未知字段、错误类型和无效 ID 返回中文参数错误。结果同时提供结构化 JSON 与文本 JSON，便于不同版本客户端读取。业务失败设置 `isError=true`，保留 API 的 `error.code/message/issues`；不会把对方服务的原始错误响应直接返回。

| 工具 | 主要参数 | 最低权限 |
|---|---|---|
| list_collectors / list_targets | 无 | read |
| get_target_history | collector、target；可选 from/to/step | read |
| query_sql | sql；可选 params（range/from/to/collector） | read |
| get_schema | 无 | read |
| list_alerts | 可选 state/since，默认 firing | read |
| get_alert_events | alertId；可选 limit（1～500，默认 200）、cursor；完整故障前后 60 秒窗口 | read |
| get_alert_diagnostics | id；生成有界、按告警归属的白名单元数据包，不读取文件或导出原始消息 | read |
| list_rule_errors | 无；规则没有按预期触发时先调用，结果与 GET rule-errors 一致 | read |
| ack_alert | id | config |
| silence | rule 或 target、for、reason | config |
| get_config | kind：targets/rules/notifiers/settings | read |
| validate_config | 可选 files；路径到 YAML/JSON 内容的映射，null 假设删除 | read |
| put_config | kind、完整 config 对象 | config，settings 为 admin |
| list_processes | id：采集器 ID；可选 name 名称关键词 | read |
| test_target_match | id：采集器 ID；match：与 targets.yaml 相同的条件 | config |
| preview_service_switch_import | id；可选 publicProbes；固定来源预览，不接受路径 | read |
| import_service_switch | id、预览的 sha256/targetsVersion；可选 refs/removeMissing/publicProbes；先展示变化并取得用户同意 | config |
| test_rule | id；可选 rule/from/to | read |
| test_notifier | id；可选 template 或 draft | config |
| feishu_check / feishu_list_chats | 可选配置应用 app | config |
| list_templates | 无 | read |
| preview_template | id；可选 template 草稿及 state | read |
| put_template | id、完整 template；两处 ID 一致 | config |
| feishu_test_chat | app、chatId；可选 template/draft/state | config |
| feishu_save_chat | app、chatId；可选 template/filter/rules | config |
| list_dashboards | 无 | read |
| get_dashboard | id | read |
| get_panel_data | id、panel；可选 range/from/to/collector | read |
| list_service_operations | id（在线采集器 ID） | read |
| get_service_operation | id（原请求 ID） | read |
| restart_service | id、requestId、grantId、version、reason、confirmed=true | admin |

`put_config` 整体替换文件，先用 `get_config` 读取并保留已有条目。`query_sql` 和 `get_panel_data` 的时间与 NULL 主机语义见 [面板文档](dashboards.md)；时间由服务器计算。`test_rule` 可用 `id: "draft"` 配合临时 rule，不保存规则、不发送通知。

### M3-04 补齐的工具

这些工具沿用现有 HTTP 路径和模型，不提升调用令牌的权限。团队管理参数中的 id 是负责人团队、本机加入记录或分享的实际 ID，先查询再使用；成员身份只代表已获授权设备。

| 工具 | 参数 | 最低权限 |
|---|---|---|
| get_metrics_history | 可选 collector/name/from/to/step，时间 UTC Unix ms | read |
| delete_silence | id | config |
| get_template / delete_template | id；删除仅限未被引用的自定义模板 | read / config |
| get_team_state | 无 | read |
| list_teams / create_team | 无 / name、可选 ownerName | admin |
| list_team_members | id（团队） | admin |
| invite_team_member | id、name；可选 ttl，默认 24h，仅本次返回邀请 | admin |
| remove_team_member | id（团队）、member（成员设备） | admin |
| list_shares | 可选 team | admin |
| create_share | teamId/dashboardId；可选 panels/collectors/targets/members，语义见 teams | admin |
| update_share | id、当前 revision 及完整 ShareInput | admin |
| revoke_share | id（分享） | admin |
| preview_team_join / join_team | invitation；加入必须带由本人独立核对的 fingerprint | admin |
| list_joined_teams | 无 | read |
| change_team_address | id（本机加入记录）、address，保留原 CA 和证书 | admin |
| leave_team | id（本机加入记录），不代替负责人吊销 | admin |
| list_joined_shares | id（本机加入记录） | read |
| get_joined_share | id、share | read |
| get_joined_panel | id/share/panel；可选 range/from/to/collector，不接受 SQL | read |

分享仅针对负责人自己保存的声明式面板，成员不能重新分享；读取不返回 SQL、证书或密钥。修改范围必须使用当前 revision，冲突后重新读取并向本人展示变化。邀请只能交给本人指定的接收人，不能写日志、仓库或擅自发消息。SSE 和 HTML SDK 订阅继续使用 HTTP，不作为一次性 MCP 调用包装。

`restart_service` 会实际中断精确 Windows 服务，默认无授权。先 list_service_operations，向本人展示主机、精确服务和操作原因，取得逐次明确确认，再用返回的 version 和固定 requestId 调用。unknown/断线只 get_service_operation 查询原 ID，不自动换 ID 重试。工具带 destructive 标记；同 ID 同内容不会重复执行。MCP 不提供授权工具，put_config(settings) 必须保留 remoteOperations。见 [服务操作](service-operations.md)。

飞书检查需要已保存的应用凭证。MCP 不提供写 App Secret、Webhook 密钥或令牌管理工具；需要凭证时让用户在网页或 `lgmon feishu setup` 中填写。测试通知会真实发送，先让用户明确目的地；只有用户确认收到后才调用 `feishu_save_chat`。通知共用同一目的地十秒限流，切换 HTTP、网页或 MCP 不能绕过。

`list_processes` 读取该采集器最近一次常规快照，返回采集时间、最多 5000 个进程、监听地址/端口和服务名。超过条数或既有 4 MiB gRPC 消息预算时 truncated=true；name 在这份有界结果中筛选，不能把截断结果当作完整清单。命令行不返回，也不落库。需要按命令行区分同名进程时，先向用户确认关键词，再用 `test_target_match` 提交正则；匹配在采集器本机执行，原因与正式监控目标一致。两个工具按采集器共用两秒限频；旧版本返回 collector_outdated，断线返回 collector_offline，十秒未回应返回 collector_timeout。

`validate_config` 不带 files 时检查磁盘上的实际配置，能发现热加载失败后保留旧版本的原因。带 files 时先在内存里覆盖或隐藏指定文件，再按同一套加载和校验检查跨文件引用。只允许四个主配置、card-templates/*.yaml、dashboards/*.yaml 和同 ID 的 dashboards/*.html；拒绝路径穿越。结果始终以 valid/issues/files 表示校验结论，files 标明 disk/draft/deleted。不会写文件、热加载、下发配置、操作密钥或联系飞书。完整请求示例见 [API 文档](api.md#配置只读校验)。
