# HTTP API

M1-10 提供 REST、OpenAPI 3.1 和 SSE，M1-11 接入只读 SQL 与表结构说明，M1-12 增加飞书自定义机器人与 Windows 通知。监控器与 both 角色启动 HTTP 监听；collector 角色不启动。Web 面板在 M1-13 接入。

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

认证成功后，响应的 X-Lgmon-Scope 为当前 read/config/admin，仅用于网页显示权限；请求中同名头不能提升权限。正式构建在同一端口 `/` 提供嵌入面板，首页只向直接回环请求注入仅限本机的 read 会话，HTML 不缓存；远程和带代理来源头的首页不提供会话。未知 API 仍返回 JSON 404，静态页面不会覆盖接口错误。使用与控件说明见 [web-panel.md](web-panel.md)。

本机面板会话只提供 read 权限，要求来源回环、Host 为指定回环主机、没有代理头；经反向代理或以公共域名访问时不发放。M1-10 已提供会话发放和认证函数，M1-13 面板使用它，当前没有会话发放 HTTP 路由。

## 权限与接口

admin 包含 config，config 包含 read。完整请求、响应 Schema 及 `x-lgmon-scope` 从同一份路由定义生成，以 `/api/v1/openapi.json` 为准。

| 最低权限 | 接口 |
|---|---|
| read | GET collectors、targets、目标 history、metrics、alerts、config、templates、单个 template、schema、stream、openapi.json |
| read | POST rules/{id}/test、templates/{id}/preview、query |
| config | POST alerts/{id}/ack、silences、notifiers/{id}/test、feishu/test、feishu/notifiers；DELETE silences/{id} |
| config | PUT config/targets、rules、notifiers；PUT/DELETE templates/{id} |
| config | GET feishu/check、feishu/chats |
| admin | PUT config/settings；POST feishu/apps、notifiers/{id}/credentials |

表内路径均以 `/api/v1/` 开头。手机只查看时使用 read；需要确认和静音时使用 config。

## 只读 SQL 与表结构

`POST /api/v1/query` 接受一个 JSON 对象，例如：

```json
{"sql":"SELECT id, hostname, last_seen FROM collectors ORDER BY id"}
```

返回 columns、rows、truncated。最多返回 10000 行；发现第 10001 行时截断并置 truncated=true，不丢掉已读取的前 10000 行。NULL 返回 null，BLOB 返回 Base64 字符串；非有限数值报错，避免产生无效 JSON。`:now` 绑定注入时钟的 UTC Unix 毫秒。

只允许单条 SELECT/WITH，允许末尾一个分号、注释及字符串中的分号。写入语句、多语句、扩展加载和文件访问函数拒绝。查询连接独立于状态/历史读取及写入连接，使用 mode=ro 和 query_only=ON，执行预算五秒；超时返回 408 query_timeout。普通语法或查询错误为 400 invalid_query；不记录 SQL 请求体。

`GET /api/v1/schema` 返回 tables，每张表包含 name、kind、description 和 columns。列字段为 name、type、notNull、default、primaryKeyPosition、description；default 为原始 SQL 默认表达式或 null，主键位置为 0 表示不是主键。实际结构从监控数据库读取，说明从迁移 SQL 的 @table/@column 注释解析。认证、配对和密钥位于其他存储，不会作为监控表返回。

通用查询可以读取告警审计、传输水位及汇总维护表；SQL 告警规则仍使用 M1-08 的专用时间过滤、表权限和 100 行限制。两者的用途和限制分别执行。

## 数据与历史

- `/collectors`：ID、主机名、版本、lastSeen、connected、online。在线状态按实际连接与 30 秒心跳计算。
- `/targets`：当前仍分配给未吊销采集器的目标。已配置但尚未采到的目标为 unknown，不能当成健康；移除或改派的目标不再出现在当前列表。
- `/targets/{collector}/{target}/history`：states、probes、processes；`/metrics`：metrics。支持 `from`、`to`、`step`，metrics 另支持 `collector`、`name`。
- 时间是 UTC Unix 毫秒，`from <= ts <= to`；默认最近一小时。step 支持 raw、1m、1h。未指定时，范围不超过 24 小时用 raw，不超过 14 天用 1m，更大用 1h。
- 汇总点按桶起点筛选，只返回已结束的桶；后台有 30 秒传输宽限。average 为 sum/samples，探测成功率为 successes/samples，平均耗时为 latencySum/samples，不能对均值再做无权重平均。
- 指标包含 name、resource、probe，磁盘、网卡和脚本探测保持独立；进程先按同一时刻和目标合计 CPU/RSS。名称和单位见 [storage.md](storage.md)。状态历史仍是原始采样，原始数据清理后不会从数值汇总伪造状态。
- 每个历史序列查询最多 10000 条；超限报错，要求缩小范围或添加筛选条件，不静默截断。告警列表支持 state=firing/resolved/pending/all 和 since，默认 firing；同样限制 10000 条。

## 写入与试运行

配置 PUT 是整体替换，接受 JSON 或 `application/yaml`，采用配置文件相同的严格字段、类型、引用及行号校验。先校验整套配置，再原子替换；加载失败恢复原文件和旧快照。目标、规则和通知去向随后热加载。config 令牌只允许使用已经登记的应用密钥引用，不能借通知配置引用其他内部密钥；新增凭证使用 admin 的飞书应用或渠道凭据接口。Webhook 的 urlRef、secretRef 按渠道固定，config 令牌只能调整其他字段；新增渠道、更换地址引用或取消签名引用均需要 admin。

确认：`POST /alerts/{id}/ack`。静音请求：

```json
{"rule":"demo-down","for":"1h","reason":"维护"}
```

rule 和 target 只填一个；for 支持 14d。响应返回静音 ID，使用 `DELETE /silences/{id}` 取消。确认和静音只改变通知策略，不发送测试消息。

`POST /rules/{id}/test`：空请求体回放已保存规则的最近 24 小时，也可传 `{"from":1999999900000,"to":2000000000000}`。提供带 rule 字段的封装对象时使用草稿规则，也支持直接传完整规则对象；草稿仍检查当前目标和通知引用。结果和 CLI 共用 ReplayResult，包含完整性、触发/恢复次数、事件和缺口说明；不改实际告警，不发通知。同一时间只允许一个回放，另一个返回 429。

模板 PUT 使用与文件相同的自定义模板定义，ID 必须与路径相同；预设不能覆盖或删除。预览 POST 接受空请求体、`{"state":"resolved"}`，或带 template 和 state 字段的封装对象，也支持直接传完整自定义模板对象。草稿不保存。语法、渲染错误直接报错；range、递归和输出限制见 [card-templates.md](card-templates.md)。

`POST /notifiers/{id}/test` 对飞书应用和自定义机器人接受 `{"template":"standard"}`，也接受 `{"draft":{...模板定义}}`，两字段按是否出现互斥，draft 不能为 null。都省略时使用渠道默认模板；通用 Webhook 和 Windows 渠道使用空对象，不接受卡片模板。测试绕过渠道 filter。飞书应用的计数按实际应用和群共享，包括别名渠道和下面的新入口；其余渠道按渠道计数，十秒内第二次调用返回 429 和 Retry-After: 10。返回字段只有 ok 和中文 message，不返回对方响应体或消息 ID；测试内容标明“这是测试消息”。ok 只表示通知已提交，实际收件仍需本人确认；Windows 无登录会话或通知被禁用时返回 ok=false，并写明跳过原因。测试预算 90 秒，包含发送重试。

`POST /feishu/test`（config）接受 app（仅一个应用时可省略）、chatId、template 或 draft、state（firing/resolved，默认 firing）；不要求已有通知渠道。两种模板都不写时使用 standard。草稿通过与保存相同的 ParseTemplate、语法树、样例和输出限制校验；错误直接返回，不发送标准模板。超过卡片大小时允许简洁模板降级，message 说明原因；所有卡片都有测试标记。草稿只存在于本次调用，不保存模板或渠道。

`POST /feishu/notifiers`（config）接受 app、chatId、可选 template/filter/rules。保存前重新查机器人所在群及群名，已不在群中返回 409；已有同应用同群渠道更新原 ID，未给字段保留，新渠道依次尝试 NotifierID(app)、-2、-3。rules 是要加入渠道的规则代号列表，去重追加 notify。先合并、完整校验、原子写入并热加载 notifiers.yaml，再同样更新 rules.yaml，保留无关节点和注释。规则写入失败返回 `ok=false` 和“群已保存，但没能加进规则：…”；已保存的渠道仍有效，可去网页规则通知设置补充。filter 省略保留已有值，显式 {} 表示全部，null 无效。服务端不维护收件状态，网页在本人点击“收到了，保存”后才调用此接口。

`GET /feishu/check?app=` 返回与 `feishu check --json` 相同的检查报告；`GET /feishu/chats?app=` 返回群列表步骤。`POST /feishu/apps` 接受 id、appId、domain、secret；Secret 必须由本人提供。先验证凭证，再加密保存并登记应用，不创建群或发送卡片。每次更新用新密钥引用，让通知客户端重新读取凭证；旧加密条目保留供已排队任务使用，配置发布失败也不改变原生效引用。任何读取接口都不返回 Secret。

模板的 fields 省略时继承预设，显式 [] 表示不显示内置字段；HTTP 编码保留这一区别，草稿预览和读取后再次编辑不能把 [] 变回继承。字段校验错误沿用 error.issues 的文件、行号和 message；网页在对应字段标注错误，渲染错误保留可定位的模板表达式。

检查报告包含可选 botName，来自本次 BotInfo，供网页生成拉群步骤；没有查到名称时省略，不从提示文字中解析。网页每五秒只读复查，离开或切换应用取消请求；报告不表示已经测试或本人确认收到消息。

## 自定义机器人与 Windows 通知

管理员可用 `POST /api/v1/notifiers/{id}/credentials` 创建或更新 feishu_webhook 渠道。新 ID 会创建渠道，已有 ID 必须是同类型；保存后再将该 ID 加入规则的 notify。下面是虚构值，实际地址和签名密钥由本人填写：

```json
{"type":"feishu_webhook","url":"https://open.feishu.cn/open-apis/bot/v2/hook/FAKE_REPLACE_LOCALLY","signingSecret":"FAKE_SIGNING_KEY"}
```

已有渠道的 type 可省略；新渠道省略时默认 feishu_webhook。signingSecret 省略或为 null 时保留原签名密钥，空字符串取消签名。URL 与签名分别加密保存；配置及所有读取接口只包含 urlRef、secretRef，不包含地址或密钥。新凭据使用新的引用，旧条目保留给已排队任务；配置写入失败保留旧生效引用。保存仅校验地址格式，随后通过测试通知确认机器人接入，不会自动发送卡片。

同一凭据接口用 `type: webhook` 创建通用 Webhook，URL 可用 http/https，推荐 https。signingSecret 使用 `whsec_` 加标准 Base64（解码后 24～64 字节）；新建时省略或 null 会生成 32 字节密钥，成功响应额外包含 signingSecret，只显示一次。更新省略或 null 保留已有设置（包括已取消的签名），空字符串明确取消。已有或本人提供的密钥不返回；保存不联系接收方。完整请求体、签名协议、重试和筛选配置见 [webhooks.md](webhooks.md)。

自定义机器人沿用预设、自定义模板和规则模板覆盖，拒绝卡片时使用既有标准模板回退。单次完整请求限制 20 KB（包含签名及外层字段），比应用卡片限制更小；需要相应减少模板内容。网络和限流错误复用 2/5/15 秒重试，并记录每次结果。自定义机器人不返回应用消息 ID，也不提供应用消息的 UUID 去重能力；网络结果不确定时重试存在重复投递，通知审计不能作为本人收件确认。[官方机器人协议](https://open.feishu.cn/document/client-docs/bot-v3/add-custom-bot?lang=zh-CN)

Windows 渠道配置为 `notifiers: [{id: desktop, type: toast}]`，规则 notify 引用 desktop 即生效，无需应用、群、模板或密钥。通知包含主机、目标、严重程度和触发/恢复摘要；标题限 128 字符，正文限 1024 字符。普通进程使用自己的交互会话，服务通过活动用户会话提交；无可用会话或系统禁用通知时记录跳过。实际弹窗与 LocalSystem 会话切换仍需在本人环境验收。

## SSE

用带 Authorization 头的 fetch 读取 `/api/v1/stream`。事件：sample（pipeline 消息）、target（目标事实变化）、alert（已落库的 alert 和 event）。每条含进程内递增 id，data 是单行 JSON；每 15 秒发送注释心跳，同时重新检查令牌，吊销后结束连接。

每个连接缓冲 64 个事件，慢连接溢出后发送 reset 并断开，不阻塞采集。客户端收到 reset 或重新连接后重新查询当前快照；SSE 不承诺历史回放，也不以 Last-Event-ID 补传。监听热切换、客户端退出或程序关闭会释放订阅。

## 错误

```json
{"error":{"code":"insufficient_scope","message":"这个操作需要 config 权限，当前令牌只有 read。请以管理员身份运行 lgmon token create --scope config"}}
```

401 为 unauthorized；权限不足为 403 insufficient_scope；Host/Origin 不匹配为 403 forbidden_host。配置错误为 422 invalid_config，error.issues 列出 file、line、message。未知接口 404，方法不支持 405，请求体超过 1 MiB 为 413，回放或测试限频为 429。SSE 成功响应使用 text/event-stream，其余接口成功和失败均返回 JSON。

接口描述采用 [OpenAPI 3.1.0](https://spec.openapis.org/oas/v3.1.0.html)。Bearer 请求头写法见 [RFC 6750 §2.1](https://www.rfc-editor.org/rfc/rfc6750.html#section-2.1)；本项目的权限和本机会话规则以 [PLAN.md](PLAN.md) M1-10 为准。
