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

监听非回环且不属于 Tailscale 的 IPv4 `100.64.0.0/10`、IPv6 `fd7a:115c:a1e0::/48` 时，每次成功启动或切换监听都会写明 HTTP 未加密、网络内其他人可截获令牌的警告。`localhost` 与回环地址不警告；其他 DNS 主机名不推断路由，显示警告。`lgmon status` 按磁盘 settings.yaml 检查并向 stderr 提示，不能用来确认热更新绑定是否成功；运行监听以启动日志为准。网页依据浏览器当前访问地址独立判断，在登录前与页面顶部提示；HTTPS、回环和上述 Tailscale 地址不显示网页提示。

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

本机面板会话只提供 read 权限，要求来源回环、Host 为指定回环主机、没有代理头；经反向代理或以公共域名访问时不发放。GET /api/v1/session（M1-17）不需要 Bearer，返回与 HTML 相同的 `{"token":"...","scope":"read"}`；不满足真正本机条件时一律 403。它不会提供 config 权限。

## 权限与接口

M2-05 团队管理与本机加入/改地址/离开为 admin；teams/state、team-joins 列表和已加入团队的共享读取为 read。团队成员使用单独 HTTPS/mTLS 端口，不获得负责人 `/api/v1` 权限。分享只执行负责人保存的卡片 SQL，成员不能传 SQL，更新以 revision 防止覆盖。完整管理/代理路径由 OpenAPI 的团队路由生成；成员端路径、范围、撤销和错误行为见 [团队共享](teams.md)。

采集器列表每项的 config 为 {state,version,message}，表示最近一次配置下发/回执；state 为 applied、pending、rejected 或 outdated。缺少 match_dir 时含 exeDir/workDir 的整版配置不发送，message 指出目标和字段，其他采集器不受影响。无连接/回执时返回 pending 并说明等待，不代表已应用。

每项还包括 cache: {bytes,maxBytes,bytesPerHour,projectedHours}，来自最近心跳；旧采集器未报告时 cache=null，预热不足五分钟或最近无写入时后两项为 null。bytes 为已用页字节，不含单独预留的 8 MiB WAL；projectedHours=(maxBytes−8 MiB)/bytesPerHour，低于 24 小时需减少目标或调整采集器本机 cache.maxSize。settings 的 cache.maxSize 为可选整数大小 64MB～10GB，默认 500MB，仅对应本机采集角色，重启生效，不随 targets 推送到其他采集器。

processes/match 的 match 支持 exeDir、workDir；路径按 Windows 绝对盘符/UNC 规则处理。workDir 必须配合 process、cmdline 或 exeDir。只有这份草稿包含 workDir 且匹配成功时，进程项才包含 workDir；普通 GET processes 的数据与 Schema 都不增加该字段。旧版不支持 match_dir 的目录草稿返回 collector_outdated。权限与原来一致：进程列表 read，试匹配 config，命令行仍不返回。

M1-21 沿用现有配置与历史路径：Probe 增加 HTTP 专用 keyword/keywordAbsent，Rule 增加 cert_expiry/within（不接受 for，可选 probe）。GET /api/v1/targets/{collector}/{target}/history 的原始 probes 项增加 tlsNotAfter（毫秒，可为 null）和 tlsSubject；分钟/小时统计不合成证书日期，这两项为 null/空串。另有 certificates 数组，按探测返回最近一次带证书的 {probe,ts,tlsNotAfter,tlsSubject}，独立于所选历史窗口，空时为 []。这些数据仍只需 read；配置写入仍需 config，权限不提升。采集器不支持 http_checks 时含新关键字字段的配置显示 outdated，消息指出目标和字段。

全局暂停使用 `GET/PUT/DELETE /api/v1/notifications/pause`，与规则/目标静音分开。PUT 请求为 `{"for":"1h","reason":"开会"}`；时长闭区间 1 分钟到 24 小时，原因可省略，最多 200 个 Unicode 字符。重复 PUT 从当前时间重算截止时间，保留本次开始时间及最初发起令牌名。DELETE 立即结束暂停并登记补发，运行时 5 秒检查开始发送。

三种方法均返回 `{"paused":true,"startedAt":1762500000000,"until":1762503600000,"reason":"开会","by":"tray-alice","suppressed":3}`。未暂停时除 paused=false 外，其余字段均为 null。suppressed 按逻辑通知事件计数，多渠道与重试不重复累加；按事件时间判断暂停区间，告警历史不受影响。发起、修改、恢复和到期通过 SSE pause 推送当前状态；CLI 修改最迟在下一次 5 秒检查推送。

恢复后仍 firing 的补发一次；已恢复且暂停前发送成功的只向原成功渠道补发 resolved；暂停中产生并恢复的不补发。当前静音、确认和渠道过滤继续生效，补发事件为 resume:暂停ID，provider UUID/签名幂等身份稳定。所有渠道均暂停，主动测试发送绕过暂停。恢复进度和每渠道结果持久化，停机期间到期及部分失败在启动后继续处理。MCP 不开放暂停写操作；当前 30 个工具包含只读的 list_rule_errors、get_alert_diagnostics 和两个服务开关导入工具。

GET /api/v1/rule-errors 需要 read，无参数，返回 [{ruleId,ruleName,ruleType,error,at,consecutive,active}]。只包含当前配置仍存在的规则，当前错误排在前面，同组按 at 从新到旧；已恢复的最近错误仍返回，active=false。版本错误的 consecutive 固定为 0，SQL 错误为连续失败次数。删除规则时清理其数据库错误记录。

SSE ruleErrors 事件发送同一完整数组，包含新增、变化和删除；网页在初次加载、重新连接后读取 GET，并实时更新顶部提示，只列前三条 active 错误。cert_expiry 分配到没有声明 http_checks 的采集器时记录升级提示，不阻止目标下发；升级重连后自动清除当前错误。POST /query 仍禁止读取 rule_errors。

admin 包含 config，config 包含 read。完整请求、响应 Schema 及 `x-lgmon-scope` 从同一份路由定义生成，以 `/api/v1/openapi.json` 为准。

| 最低权限 | 接口 |
|---|---|
| read | GET collectors、targets、目标 history、metrics、alerts、alerts/{id}/events、alerts/{id}/diagnostics、rule-errors、config、templates、单个 template、schema、stream、openapi.json |
| read | POST rules/{id}/test、templates/{id}/preview、query |
| read | GET dashboards、dashboards/{id}、dashboards/{id}/panels/{panel}/data |
| read | GET collectors/{id}/processes；POST config/validate |
| read | GET notifications/pause |
| config | PUT/DELETE notifications/pause（全局通知暂停，不提供 MCP 工具） |
| config | POST collectors/{id}/processes/match |
| config | POST alerts/{id}/ack、silences、notifiers/{id}/test、feishu/test、feishu/notifiers；DELETE silences/{id} |
| config | PUT config/targets、rules、notifiers；PUT/DELETE templates/{id} |
| config | GET feishu/check、feishu/chats |
| admin | PUT config/settings；POST feishu/apps、notifiers/{id}/credentials |

表内路径均以 `/api/v1/` 开头。手机只查看时使用 read；需要确认和静音时使用 config。

同一监听地址的 `/mcp` 使用 Streamable HTTP，stdio 客户端可启动 `lgmon mcp`；完整工具与连接方式见 [mcp.md](mcp.md)。两种方式都复用上述接口和权限，不提供密钥写入或令牌管理工具。

## 当前进程与试匹配

`GET /api/v1/collectors/{id}/processes?name=python` 从在线采集器最近一次常规快照读取进程，name 按进程名包含关键词筛选，不区分大小写。返回 collector（id/hostname）、collectedAt（UTC 毫秒）、processes、truncated、reason（无原因时 null）。进程含 pid、parentPid、name、exe、createdAt、cpuPercent、rss、threads、ports（address/port）和 services（服务名数组），不含命令行。

`POST /api/v1/collectors/{id}/processes/match` 需要 config，接收 `{"match":{"process":"python","cmdline":"blog.py"}}`。条件和正式目标完全相同，正则只在采集器本机运行；返回相同结构，reason 与正式匹配原因一致。read 不能试探命令行。

两个接口按采集器共用两秒限频，超出返回 429 process_rate_limited 及 Retry-After: 2。不存在为 404，离线为 503 collector_offline，旧版本为 409 collector_outdated，十秒未回应为 504 collector_timeout。只在内存转交结果，不进入离线缓存、不推进序号、不落库。最多 5000 条，并受既有 4 MiB gRPC 预算约束；truncated=true 时不是完整列表，name 仅筛选这份有界结果。

## 配置只读校验

`POST /api/v1/config/validate` 需要 read；空请求体、`{}` 或空 files 都校验磁盘上的实际配置。带 files 时，字符串覆盖对应文件，null 假设删除，其他文件仍来自磁盘：

```json
{"files":{"rules.yaml":"rules: []\n","card-templates/unused.yaml":null}}
```

只允许 targets.yaml、rules.yaml、notifiers.yaml、settings.yaml、card-templates/<名字>.yaml 和 dashboards/<名字>.yaml；其他路径及 .. 返回 400。最多 64 文件、单文件内容 1 MiB、合计 4 MiB，超过返回 400。该接口的编码请求上限为 25 MiB，用于容纳 JSON 最坏六倍转义；其他接口仍为 1 MiB，超过编码上限返回 413。

返回 `{"valid":true,"issues":[],"files":[{"path":"rules.yaml","source":"draft"}]}`，不合格也返回 200，通过 valid 表示结论。issues 使用相对文件名、草稿自身行号和中文 message；files 按路径排序，source 为 disk/draft/deleted。校验包含同一加载器的跨文件引用、SQL 规则、模板语法树、通知 filter 冲突、面板定义和查询参数；不会执行面板数据查询。整个过程不写文件、不热加载、不下发、不操作密钥或调用飞书；`lgmon config validate` 共用磁盘校验函数。

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

声明式面板的读取接口、version、逐文件诊断、逐卡执行与参数表见 [dashboards.md](dashboards.md)。POST /query 增加可选 params 对象：range/from/to/collector；返回值新增实际 params，未给时间时用服务端现在和 1h，未给 collector 时绑定 NULL。面板请求不提交 SQL，而按页和卡片 ID 执行服务器已经加载的查询。

- `/collectors`：ID、主机名、版本、lastSeen、connected、online 和 clockOffsetMs。在线状态按实际连接与 30 秒心跳计算。clockOffsetMs 是“监控器时间减采集器时间”的估计毫秒数，正数表示采集器慢，负数表示快；偏差超过十秒时主机卡片提示同步时间。
- `/targets`：当前仍分配给未吊销采集器的目标。已配置但尚未采到的目标为 unknown，不能当成健康；移除或改派的目标不再出现在当前列表。
- `/targets/{collector}/{target}/history`：states、probes、processes；`/metrics`：metrics。支持 `from`、`to`、`step`，metrics 另支持 `collector`、`name`。
- 时间是 UTC Unix 毫秒，`from <= ts <= to`；默认最近一小时。step 支持 raw、1m、1h。未指定时，范围不超过 24 小时用 raw，不超过 14 天用 1m，更大用 1h。
- 汇总点按桶起点筛选，只返回已结束的桶；后台有 30 秒传输宽限。average 为 sum/samples，探测成功率为 successes/samples，平均耗时为 latencySum/samples，不能对均值再做无权重平均。
- 指标包含 name、resource、probe，磁盘、网卡和脚本探测保持独立；进程先按同一时刻和目标合计 CPU/RSS。名称和单位见 [storage.md](storage.md)。状态历史仍是原始采样，原始数据清理后不会从数值汇总伪造状态。
- 每个历史序列查询最多 10000 条；超限报错，要求缩小范围或添加筛选条件，不静默截断。告警列表支持 state=firing/resolved/pending/all 和 since，默认 firing；同样限制 10000 条。

## 告警关联事件

`GET /api/v1/alerts/{id}/events` 需 read，查询进程或服务告警原始 down 采样前后各 60000ms、同一采集器和目标的 subject 事件。返回 `{alertId,from,to,events,nextCursor}`；from/to 为原始 UTC 毫秒，缺少原始采样时间的历史告警为 null、events=[]；不存在的告警为 404。

事件项是 `{ts,channel,provider,eventId,level,recordId,subject,message}`；recordId 为十进制字符串，message 保留采集时最多 2 KiB 文本。limit 默认为 200，范围 1～500；按 `(ts,channel,record_id)` 升序，nextCursor 不为 null 时可传 cursor 续页。无效或属于其他告警/窗口的游标返回 400。刷新时省略 cursor，读取后来到达的事件。MCP get_alert_events 与此相同。

## 告警诊断包

`GET /api/v1/alerts/{id}/diagnostics` 需要 read，返回 schemaVersion=1 的 typed JSON：alert、host、window、snapshots、probes、metrics、events、changes、collectorEvents、coverage、notes、summary、nextSteps、agentPrompt。按告警归属限定范围，在同一只读事务中查询；不读取文件，不导出原始消息、命令行、配置或凭据。最多 1 MiB，超出返回 413 diagnostic_too_large；不存在为 404，非法 ID 为 400。MCP get_alert_diagnostics({id}) 和本地 diagnose CLI 复用相同生成器。字段、查询上限和完整性含义见 [诊断包](diagnostics.md)。

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

每个连接缓冲 64 个事件，慢连接溢出后发送 reset 并断开，不阻塞采集。客户端收到 reset 或重新连接后重新查询当前快照；SSE 不承诺历史回放，也不以 Last-Event-ID 补传。监听热切换、客户端退出或程序关闭会释放订阅。M1-15 增加 dashboard 事件，只带 id/version，删除时带 deleted: true；客户端重读对应面板定义。

## 错误

```json
{"error":{"code":"insufficient_scope","message":"这个操作需要 config 权限，当前令牌只有 read。请以管理员身份运行 lgmon token create --scope config"}}
```

401 为 unauthorized；权限不足为 403 insufficient_scope；Host/Origin 不匹配为 403 forbidden_host。配置错误为 422 invalid_config，error.issues 列出 file、line、message。未知接口 404，方法不支持 405，请求体超过 1 MiB 为 413，回放或测试限频为 429。SSE 成功响应使用 text/event-stream，其余接口成功和失败均返回 JSON。

接口描述采用 [OpenAPI 3.1.0](https://spec.openapis.org/oas/v3.1.0.html)。Bearer 请求头写法见 [RFC 6750 §2.1](https://www.rfc-editor.org/rfc/rfc6750.html#section-2.1)；本项目的权限和本机会话规则以 [PLAN.md](PLAN.md) M1-10 为准。

## 自定义 HTML 面板

HTML 面板沿用 GET /dashboards 和 GET /dashboards/{id}（read）：列表可选 html:true，详情可选 html:{file,source}，HTML 页 panels:[]。未启用 HTML 的响应保持原字段。源码不是公开 HTML URL；通用 /lgmon-frame.html、/lgmon.js 只含框架且不发本机会话，GET/HEAD 不读配置或数据。SDK 仅由宿主页代理只读查询和既有 SSE，参见 [HTML 面板](html-dashboards.md)。validate 的草稿路径新增同目录 dashboards/<id>.html，原大小/数量/权限限制不变。

## 启动项与计划任务

复用 POST /api/v1/query（read）、GET /schema 和既有 SSE sample，没有新的写入口。inventory_current 为当前白名单条目，inventory_scopes 为覆盖/错误/最近成功，inventory_snapshots 与 inventory_changes 为原始保留期内历史。CLI query 和 MCP query_sql 返回同一数据；来源、字段和时间含义见 [清单协议](inventory.md)。新采集器声明 inventory 能力，旧采集器继续原协议且显示尚无清单数据。

## 服务开关导入

GET `/api/v1/collectors/{id}/service-switch?publicProbes=true` 需要 read；POST 同一路径加 `/import` 需要 config。仅接受采集器 ID，不接受路径参数；预览/保存共用每台两秒限频（429 service_switch_rate_limited，Retry-After: 2）。返回结构、字段选择和完整错误约定见 [服务开关导入](service-switch.md)。

保存必须带预览的 sha256 和 targetsVersion，重新读取源文件并在编辑锁内核对原 targets.yaml 字节指纹；变化分别返回 409 source_changed/config_changed。省略 refs 表示全部 create/update，空数组表示不保存任何 create/update；removeMissing 默认 false，true 删除全部 missing 项，仍须通过规则引用等完整配置校验。零变化不写文件。serviceSwitch.path 只能本机设置，HTTP settings PUT 不得修改或删除现值。
