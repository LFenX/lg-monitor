# 飞书卡片确认与静音

M2-06 对应 PRD N5。管理员为指定飞书应用通知群启用后，新触发卡片提供“确认”和“静音 1 小时”。使用已固定的官方 Go SDK 长连接；监控器主动连出，无需公网回调入口，运行时不依赖 lark-cli。

## 授权与配置

1. 本人在自己的飞书应用后台配置卡片回传交互：使用长连接接收，订阅 `card.action.trigger`。应用已有机器人和发送权限，并在已确认的通知群中。程序不会代改开发者后台或创建群。
2. 在网页“通知 → 飞书卡片操作”选择已有群，使用 admin 令牌启用。启用意味着把两种有限操作委托给该群原消息的收件人；请确认这个群的成员有权处理相应告警。
3. 检查“已连接”。等待真实问题触发的新卡片；测试发送、模板预览和恢复卡片没有有效动作按钮。旧消息不会自动补按钮。

同一飞书应用只让一个监控器接收回调。平台长连接采用集群分发，多台监控器消费同一个应用会收到不属于自己的消息，不能保证处理；多监控器请使用不同应用。

本机多个配置别名引用同一 App ID 和平台域名时，共享一条连接，使用配置中第一个已授权别名的凭据。其他别名渠道仍单独授权、签名和核对发送记录。

通知渠道的可选字段 `cardActions: true` 只支持 `feishu_app`，省略或 false 不开连接、不创建动作签名密钥、不加按钮。凭据沿用受保护密钥库。管理员也可在本机终端执行：

```powershell
.\bin\lgmon.exe feishu actions status feishu-alerts --home .\dev-home --json
.\bin\lgmon.exe feishu actions enable feishu-alerts --home .\dev-home --json
.\bin\lgmon.exe feishu actions disable feishu-alerts --home .\dev-home --json
```

enable/disable 检查本机管理员身份，更新配置并保留注释，由已有热加载生效。CLI status 读取磁盘授权，`waiting` 不代表已经建立连接；实时连接状态从网页或 GET 接口读取。离线状态下也能撤销授权。

通用配置 HTTP PUT 和 MCP put_config 只能保留或撤销已有委托，不能新增或转移委托，包括更换已授权渠道的应用、App ID、平台域名或群。请先撤销旧授权，更换后通过专用管理员开关重新授权。直接编辑受保护本机配置是本机所有者的操作。

## 动作效果

- **确认**：确认这一次告警，停止后续提醒；恢复仍通知。不会确认同名目标的其他主机或其他告警生命周期。
- **静音 1 小时**：从点击时间起，静音同一规则、采集器、目标和规则实例的全部通知渠道。规则状态继续计算，其他告警组不受影响。不同的再次点击从新时间延长一小时；同一平台事件重投不会延长。抖动汇总卡片的操作对象是卡片说明的代表告警及其组。
- 已恢复、已关闭、已删除、移除路由、群不再符合筛选条件或撤销授权的旧卡片不会产生新效果。相同已处理事件的重投只返回原处理结果。
- 回调只返回飞书原生 toast，不额外发送确认消息或重写卡片。网页通过同一 SSE 连接上的 `cardAction` 事件刷新状态，事件只含已提交的效果，不含操作人/回调正文。历史卡片保留原显示，处理结果以 toast 和告警状态为准。

关闭开关立即拒绝后续回调，应用没有其他已授权渠道时停止本机连接。配置和凭据变化会结束并等待旧连接，之后建立新连接；程序退出也等待清理。连接故障、密钥库失败或按钮准备失败均不阻止普通告警发送。

## HTTP 与审计

| 接口 | 最低权限 | 内容 |
|---|---|---|
| GET `/api/v1/notifiers/{id}/card-actions` | read | 当前配置授权及该应用连接状态 |
| PUT 同一路径 | admin | `{"enabled":true}` 或 `{"enabled":false}`，仅修改选中渠道 |

返回 `notifier`、`app`（本机配置别名）、`enabled`、`state`、`updatedAt`（状态更新时间的 UTC Unix 毫秒，尚无连接状态时为 0）。state 为 disabled/waiting/connecting/connected/reconnecting/error/disconnected/stopped。OpenAPI 包含同一结构；没有公网回调 HTTP 路由，也没有 MCP 授权工具。已有 MCP ack_alert/create_silence 不变。

回调同时核对认证连接所属应用、原群/消息、成功发送审计、当前授权/路由和独立 HMAC 签名。签名密钥为密钥库中的 `lgmon-card-actions-hmac`，卡片只含签名值，不含 API 令牌、App Secret 或签名密钥。转发卡片、其他群/应用/消息、篡改字段和任意动作都被拒绝。

`card_action_events` 保存效果和去重记录，与确认/静音处于同一事务。操作者只存按应用隔离的 HMAC，不保存 open_id、原始回调或平台更新令牌。可用只读 SQL 查验：

```sql
SELECT alert_id, collector_id, target_id, rule_id, operation, ts, silence_until, acknowledged_at
FROM card_action_events ORDER BY ts DESC LIMIT 100;
```

活动告警的去重记录不会因 raw 保留清理而删除；告警结束后按 raw 截止时间清理。历史规则回放只看到评估时间以前的动作事件，不能通过 silences 读取当前静音污染历史。团队分享按已授权采集器和目标限制动作审计。

## 工程证据与本人待验

工程夹具覆盖官方 SDK JSON 2.0 dispatcher、实际本地 WebSocket 的 protobuf 帧和 toast、通知发送及模板回退/迟到更新、事务回滚/并发/重投/重启、跨群/转发/篡改、权限与配置授权、CLI 和真实 Edge 网页。它们不证明真实飞书客户端收件或点击效果。

人工验收保持 **Pending**：本人选择自己的测试群并完成第 1～3 步，用自己的测试进程触发通知；分别点击确认、恢复后重触发并点击一小时静音，核对提醒/恢复及其他主机组不受影响；等待或重投同一事件确认不延长静音；撤销授权后点旧卡片应拒绝。核对手机和桌面卡片显示及网页状态。服务安装、系统重启、真实通知发送仍由本人操作，当前未安装真实服务。

参考实现：[固定版本官方 SDK](https://github.com/larksuite/oapi-sdk-go/tree/v3.12.2/ws)、[官方卡片回调模型](https://github.com/larksuite/oapi-sdk-go/blob/v3.12.2/event/dispatcher/callback/model.go)、[官方 CLI 卡片交互说明](https://github.com/larksuite/cli/blob/main/skills/lark-im/references/lark-im-card-action-reply.md)。
