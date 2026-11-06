# HTTP API

M1-10 提供 REST、OpenAPI 3.1 和 SSE。监控器与 both 角色启动 HTTP 监听；collector 角色不启动。Web 面板在 M1-13 接入。

## 启动与令牌

`config/settings.yaml`：

```yaml
httpListen: 127.0.0.1:7444
publicBaseUrl: http://127.0.0.1:7444
```

`httpListen` 省略时使用 `127.0.0.1:7444`。手机通过 Tailscale 查看时，把两个地址改成监控器的 Tailscale 地址，例如 `100.64.0.4:7444`；该地址是虚构示例。也支持 `0.0.0.0:7444` 和 `[::]:7444`。按 REVIEW 的建议，远程访问优先只监听 Tailscale 地址；本阶段提供 HTTP，不自动配置 HTTPS 或防火墙。

监听地址热更新时先建立新监听，再关闭旧监听和旧 SSE。新地址绑定失败时保留旧监听并写明日志，释放占用后重新保存配置可重试。`publicBaseUrl` 热更新卡片“打开面板”链接和 Host 白名单，不重新绑定端口。

在管理员 PowerShell 中，为自己的开发 home 创建令牌：

```powershell
$created = .\bin\lgmon.exe token create --home .\dev-home --scope read --name phone --json | ConvertFrom-Json
$headers = @{ Authorization = 'Bearer ' + $created.token }
Invoke-RestMethod -Uri 'http://127.0.0.1:7444/api/v1/collectors' -Headers $headers
Invoke-RestMethod -Uri 'http://127.0.0.1:7444/api/v1/openapi.json' -Headers $headers
```

令牌原文只显示一次；`token list --json` 只列 ID、名称、权限及时间，`token revoke <ID>` 吊销。三个命令均需本机管理员身份，没有令牌管理 HTTP 路由。令牌哈希和访问记录保存在受保护的 `data/auth.db`，与监控数据、配对数据库分开。

认证只读 `Authorization: Bearer <token>`；Cookie、URL 参数和请求体中的令牌无效。不发送 CORS 头。Host 只接受回环地址、本机实际地址、监听主机和 `publicBaseUrl` 主机；带 Origin 时同样检查。配置和管理员请求记录令牌名称、方法、路径与 HTTP 结果，不记录请求体或查询参数。

本机面板会话只提供 read 权限，要求来源回环、Host 为指定回环主机、没有代理头；经反向代理或以公共域名访问时不发放。M1-10 已提供会话发放和认证函数，M1-13 面板使用它，当前没有会话发放 HTTP 路由。

## 权限与接口

admin 包含 config，config 包含 read。完整请求、响应 Schema 及 `x-lgmon-scope` 从同一份路由定义生成，以 `/api/v1/openapi.json` 为准。

| 最低权限 | 接口 |
|---|---|
| read | GET collectors、targets、目标 history、metrics、alerts、config、templates、单个 template、stream、openapi.json |
| read | POST rules/{id}/test、templates/{id}/preview、query |
| config | POST alerts/{id}/ack、silences、notifiers/{id}/test；DELETE silences/{id} |
| config | PUT config/targets、rules、notifiers；PUT/DELETE templates/{id} |
| config | GET feishu/check、feishu/chats |
| admin | PUT config/settings；POST feishu/apps |

表内路径均以 `/api/v1/` 开头。手机只查看时使用 read；需要确认和静音时使用 config。`POST /api/v1/query` 按计划预留，M1-11 之前返回 501。

## 数据与历史

- `/collectors`：ID、主机名、版本、lastSeen、connected、online。在线状态按实际连接与 30 秒心跳计算。
- `/targets`：当前仍分配给未吊销采集器的目标。已配置但尚未采到的目标为 unknown，不能当成健康；移除或改派的目标不再出现在当前列表。
- `/targets/{collector}/{target}/history`：states、probes、processes；`/metrics`：metrics。支持 `from`、`to`、`step`，metrics 另支持 `collector`、`name`。
- 时间是 UTC Unix 毫秒，`from <= ts <= to`；默认最近一小时。step 支持 raw、1m、1h。未指定时，范围不超过 24 小时用 raw，不超过 14 天用 1m，更大用 1h。
- 汇总点按桶起点筛选，只返回已结束的桶；后台有 30 秒传输宽限。average 为 sum/samples，探测成功率为 successes/samples，平均耗时为 latencySum/samples，不能对均值再做无权重平均。
- 指标包含 name、resource、probe，磁盘、网卡和脚本探测保持独立；进程先按同一时刻和目标合计 CPU/RSS。名称和单位见 [storage.md](storage.md)。状态历史仍是原始采样，原始数据清理后不会从数值汇总伪造状态。
- 每个历史序列查询最多 10000 条；超限报错，要求缩小范围或添加筛选条件，不静默截断。告警列表支持 state=firing/resolved/pending/all 和 since，默认 firing；同样限制 10000 条。

## 写入与试运行

配置 PUT 是整体替换，接受 JSON 或 `application/yaml`，采用配置文件相同的严格字段、类型、引用及行号校验。先校验整套配置，再原子替换；加载失败恢复原文件和旧快照。目标、规则和通知去向随后热加载。config 令牌只允许使用已经登记的应用密钥引用，不能借通知配置引用其他内部密钥；新增凭证使用 admin 的飞书应用接口。

确认：`POST /alerts/{id}/ack`。静音请求：

```json
{"rule":"demo-down","for":"1h","reason":"维护"}
```

rule 和 target 只填一个；for 支持 14d。响应返回静音 ID，使用 `DELETE /silences/{id}` 取消。确认和静音只改变通知策略，不发送测试消息。

`POST /rules/{id}/test`：空请求体回放已保存规则的最近 24 小时，也可传 `{"from":1999999900000,"to":2000000000000}`。提供带 rule 字段的封装对象时使用草稿规则，也支持直接传完整规则对象；草稿仍检查当前目标和通知引用。结果和 CLI 共用 ReplayResult，包含完整性、触发/恢复次数、事件和缺口说明；不改实际告警，不发通知。同一时间只允许一个回放，另一个返回 429。

模板 PUT 使用与文件相同的自定义模板定义，ID 必须与路径相同；预设不能覆盖或删除。预览 POST 接受空请求体、`{"state":"resolved"}`，或带 template 和 state 字段的封装对象，也支持直接传完整自定义模板对象。草稿不保存。语法、渲染错误直接报错；range、递归和输出限制见 [card-templates.md](card-templates.md)。

`POST /notifiers/{id}/test` 接受 `{"template":"standard"}`，省略 template 使用渠道默认值。同一渠道十秒内第二次调用返回 429。返回字段只有 ok 和中文 message，不返回对方响应体或消息 ID；测试卡片标明“这是测试消息”。

`GET /feishu/check?app=` 返回与 `feishu check --json` 相同的检查报告；`GET /feishu/chats?app=` 返回群列表步骤。`POST /feishu/apps` 接受 id、appId、domain、secret；Secret 必须由本人提供。先验证凭证，再加密保存并登记应用，不创建群或发送卡片。每次更新用新密钥引用，让通知客户端重新读取凭证；旧加密条目保留供已排队任务使用，配置发布失败也不改变原生效引用。任何读取接口都不返回 Secret。

## SSE

用带 Authorization 头的 fetch 读取 `/api/v1/stream`。事件：sample（pipeline 消息）、target（目标事实变化）、alert（已落库的 alert 和 event）。每条含进程内递增 id，data 是单行 JSON；每 15 秒发送注释心跳，同时重新检查令牌，吊销后结束连接。

每个连接缓冲 64 个事件，慢连接溢出后发送 reset 并断开，不阻塞采集。客户端收到 reset 或重新连接后重新查询当前快照；SSE 不承诺历史回放，也不以 Last-Event-ID 补传。监听热切换、客户端退出或程序关闭会释放订阅。

## 错误

```json
{"error":{"code":"insufficient_scope","message":"这个操作需要 config 权限，当前令牌只有 read。请以管理员身份运行 lgmon token create --scope config"}}
```

401 为 unauthorized；权限不足为 403 insufficient_scope；Host/Origin 不匹配为 403 forbidden_host。配置错误为 422 invalid_config，error.issues 列出 file、line、message。未知接口 404，方法不支持 405，请求体超过 1 MiB 为 413，回放或测试限频为 429。SSE 成功响应使用 text/event-stream，其余接口成功和失败均返回 JSON。

接口描述采用 [OpenAPI 3.1.0](https://spec.openapis.org/oas/v3.1.0.html)。Bearer 请求头写法见 [RFC 6750 §2.1](https://www.rfc-editor.org/rfc/rfc6750.html#section-2.1)；本项目的权限和本机会话规则以 [PLAN.md](PLAN.md) M1-10 为准。
