# lg-monitor 使用指南（给 Agent）

飞书卡片动作须由本人或管理员按通知群授权，见 [卡片动作](card-actions.md)。Agent 的通用 notifiers 配置/MCP 写入只可保留或撤销已有授权，不能新增或转移。需要本人使用专用网页开关或管理员 `lgmon feishu actions enable <渠道ID> --home <home> --json`；不要自行授权群收件人、改飞书订阅或发送真实消息。

本指南供操作已安装 lgmon 的 Agent 使用。开发仓库的规则在 [AGENTS.md](../AGENTS.md)，不应把开发任务的目录、测试群或本机服务当成用户此次监控任务的授权。实际需求、程序路径、监控 home 和操作权限以用户本次任务为准。

## 入口与定位

用户可运行以下命令生成新会话指令；生成指令不会启动 Agent、修改监控配置或发送通知：

```powershell
.\bin\lgmon.exe agent prompt --home .\dev-home --wish "给 Nebula 加监控、写规则、做一页面板，把严重告警改用排查模板"
```

prompt 自包含精简使用说明与本机实际绝对路径，不要求预先安装技能。生成时不读取密钥、令牌、数据库或服务配置。程序仅解析当前可执行文件与 --home；用户要确认传入的是预期监控器的 home，不是某台采集器或另一套开发实例。

需要自动发现技能时，用户明确执行安装：

```powershell
.\bin\lgmon.exe agent install --target all --home .\dev-home --json
```

默认 all，也可选 claude 或 codex。分别写入当前账户 ~/.claude/skills/lg-monitor/SKILL.md 和 ~/.codex/skills/lg-monitor/SKILL.md；不修改 AGENTS.md、客户端 MCP 配置或环境凭据。只更新含本程序管理标记的普通文件；其他既有内容报冲突，由本人先备份或移走。链接/重解析点不自动写入。多目标安装预检全部路径，单文件原子替换；若写入部分失败，JSON 中 installed 只列已经成功的路径，命令失败退出 1，修复后可重新运行。不要把部分成功报告为全部完成。

## 概念和权限

| 对象 | 含义与位置 |
|---|---|
| 采集器 collector | 在被监控电脑采集、探测、缓存，通过 gRPC+mTLS 主动上报；ID 跨重启稳定 |
| 监控器 monitor | 长期保存采样、判断规则、发送通知、提供网页/API/MCP；单机 both 仍走同一协议 |
| 目标 target | 逻辑身份，按进程名/命令行正则/程序目录/工作目录/端口/服务名匹配，多个条件是 AND；collector 可指定 ID 或主机名，空值/* 会匹配所有采集器 |
| 规则 rule | 指定条件、持续时间、严重度、通知渠道和模板；unknown 不代表正常，不触发新告警，也不恢复旧告警 |
| 通知 notifier | 飞书应用、飞书机器人、通用 Webhook 或 Windows toast；规则 notify 引用渠道 ID，不能猜群 ID |
| home | 全部命令通用 --home；省略时依次取 LGMON_HOME、%ProgramData%\lg-monitor。config 为配置、data 为数据库/身份/加密凭据、logs 为日志 |

先确认用户要改哪台机器。MCP 的 list_collectors/list_targets 给出实际 ID；list_processes 取远端最近快照，仅返回精简进程信息，不含命令行。命令行只在采集器本机做匹配，使用 test_target_match 验证草稿。CLI targets test 只检查运行 CLI 的本机，不能据此断言另一台采集器的匹配。

API 只从 Authorization: Bearer 头认证。read 可查询、预览、只读 SQL、回放和校验草稿；config 可改 targets/rules/notifiers/模板、确认、静音与授权测试发送；settings、应用密钥和 Webhook 凭据需要 admin。真正本机 HTML 或 GET /api/v1/session 只提供 read，不代表机器上的普通用户有配置权。没有权限时交付完整草稿和本人操作步骤，保留“未保存/未生效”状态。

只做当前用户授权的配置任务。不停止或重启已有进程/服务，不改防火墙、注册启动或安装服务；故障验收只能用自建演示进程，并先确认操作范围。真实飞书测试要有明确目的群和发送授权。App Secret、Webhook 地址/密钥和访问令牌由本人在本地界面或终端输入，不向聊天索取，不打印或读取密钥库/auth.db，不提交真实应用/群/用户 ID、凭据、证书、数据库。

## 文件与校验

| 相对 config 路径 | 内容 | 保存与验证 |
|---|---|---|
| targets.yaml | targets：匹配、collector、tags、probes | 合并目标，先试匹配，再校验 |
| rules.yaml | rules：条件、for、severity、notify、template | 保留现有规则，必要时 test_rule 历史回放 |
| notifiers.yaml | feishuApps/notifiers；敏感值只放 secretRef/urlRef 引用 | 飞书本人向导；只改指定渠道，保留其他条目 |
| settings.yaml | httpListen/publicBaseUrl/retention | admin；raw≤minute≤hour，raw≥24h，支持 14d |
| card-templates/<id>.yaml | 自定义模板，文件名与 id 一致 | 先预览 firing/resolved，校验后保存 |
| dashboards/<id>.yaml | 声明式页面，文件名与 id 一致 | 本地文件写入；查询与列契约验证后热加载 |

文件可缺省，未知字段、无效引用或不合法值会报文件/行号。CLI config validate --json 与 API/MCP validate_config 共用校验；草稿 files 为文件名到 YAML/JSON 字符串的映射，null 表示假设删除，校验不会保存。Schema 可用 config schema --output <目录> 导出，具体命令先读 --help。

配置热加载保留最后一次有效快照。写入后重新读取 API/MCP 配置，检查目标的新采样和面板实际数据，不能把磁盘保存成功当成已生效。API/MCP put_config 是完整替换；写前重新读取，合并此次差异，保留其他条目及字段。目标应先保存，再保存引用它的规则；草稿可一次校验两个文件。不要向数据库直接写配置或告警状态。

## CLI、HTTP 和 MCP 的选择

本地且有权限访问正确 home 时，用 prompt 中已转义的完整 CLI 命令，或编辑配置文件；读取 status/alerts/query 可在监控器停止后使用，但只是已提交快照。远端操作优先用已连接 MCP；HTTP 与 MCP 的权限、校验、限流、SQL 约束一致。

| 任务 | CLI | HTTP（/api/v1 下）或 MCP |
|---|---|---|
| 当前状态/告警 | status --json；alerts --json | GET collectors/targets/alerts；list_collectors/list_targets/list_alerts |
| 故障关联事件 | 使用 HTTP/MCP，不读取任意系统日志 | GET alerts/{id}/events；get_alert_events（alertId、可选 limit/cursor，read） |
| 诊断交接包 | diagnose <实际告警ID> --json（停止后可用） | GET alerts/{id}/diagnostics；get_alert_diagnostics（id，read）；完整性和隐私边界见 [诊断包](diagnostics.md) |
| 团队共享 | team、share 子命令（使用本机管理证书） | 团队管理/本机加入 admin，共享读取 read；不提供 MCP 授权工具，规则见 [团队共享](teams.md) |
| 受限服务重启 | operations list/run/result（HTTP）；本机管理员 allow/revoke/enable/disable | list_service_operations/get_service_operation 为 read，restart_service 为 admin；默认关闭、精确授权、本人逐次确认，见 [服务操作](service-operations.md) |
| 进程和试匹配 | targets test <id> --json（仅本机） | GET collectors/{id}/processes、POST collectors/{id}/processes/match；list_processes/test_target_match |
| 配置读取/保存 | 编辑实际 config，config validate --json | GET/PUT config/{kind}；get_config/put_config |
| 草稿校验 | config validate --json 校验磁盘 | POST config/validate；validate_config(files) 校验未保存覆盖层 |
| 结构/只读查询 | query --schema --json；query <SQL> --json | GET schema、POST query；get_schema/query_sql |
| 规则回放 | rules test <id> --since 24h --json；或 --file 草稿 | POST rules/{id}/test；test_rule |
| 模板 | template list/new/validate/preview | GET/PUT templates/{id}、POST templates/{id}/preview；list_templates/put_template/preview_template |
| 飞书接入 | 本人 feishu setup；feishu check --json | 网页通知向导；feishu_check/feishu_list_chats/feishu_test_chat/feishu_save_chat |
| 读取面板 | query 可验证 SQL | GET dashboards、dashboards/{id}、dashboards/{id}/panels/{panel}/data；list_dashboards/get_dashboard/get_panel_data |
| 确认/静音 | alerts ack、silence add | POST alerts/{id}/ack、silences；ack_alert/silence |
| 全局暂停 | notify pause/resume；status 显示 | GET/PUT/DELETE notifications/pause；MCP 不提供暂停工具 |

上表是短写，命令是否接受 --json 以当前 --help 为准；模板 preview 输出 JSON 卡片，version 输出文本。不臆造 config set、targets add、dashboards put 或新 MCP 工具。

HTTP API 描述在 /api/v1/openapi.json，实时状态 SSE 在 /api/v1/stream。MCP Streamable HTTP 在 /mcp；stdio 入口 lgmon mcp --url <监控器URL> 只从 LGMON_TOKEN 环境变量读令牌，stdout 仅协议、日志到 stderr。MCP 客户端由本人按 [mcp.md](mcp.md) 配置实际程序、URL 和安全环境注入；agent install 不替本人生成或索取令牌，也不会自动连接客户端。公共 URL 与 HTTP 监听地址含义不同，不能把卡片按钮 publicBaseUrl 当成实际监听配置。

## 配方一：添加一个网站的监控

1. 确认用户指定的网站、所在采集器和需要检测的故障。list_collectors 找真实 ID，查看进程/端口/服务，test_target_match 验证唯一匹配；同名进程用命令行正则或端口区分。没有实测资料就保留缺失点，不填假端口/可执行文件。
2. 读取现有 targets/rules/notifiers。将新目标与规则合并进完整文档；collector 限定实际机器，HTTP URL 的 127.0.0.1 指该采集器。HTTP probe 使用 url/interval/timeout/expectStatus，可选 keyword/keywordAbsent；不提供 method/bodyContains 字段。keyword 按 UTF-8 字节区分大小写，长度 1～256，只读解压正文前 1 MiB。需要 http_checks 能力，保存后核对采集器 config.state。
3. process_absent 需要 match.process、match.cmdline 或目录条件；只有服务名时选 service_stopped。port_closed 在进程没有 missing 且端口关闭时触发，避免和进程告警重复。probe_failed 需要 probes。HTTPS 自动记录最早非根证书到期时间；cert_expiry 指向已有目标，可选 probe 下标，within 默认 14d、范围 1h～365d，禁止 for。通知规则必须引用已配置渠道；没有渠道时先由本人接入，不自行建群或默认为另一个收件人。
4. 用 validate_config 校验完整草稿，先保存目标再保存规则，重新读取确认。观察下一轮实际采样的采集器、目标、匹配状态、probe 结果。只读校验和回放不发送通知。

规则没有按预期触发时先调用 list_rule_errors（read，无参数），查看 active、ruleType 和 error。cert_expiry 的采集器版本错误需要升级后重连；SQL 错误按具体诊断修正。已恢复的错误也会返回，active=false；不要把历史错误当作当前故障。

以下六个示例共同组成可校验的独立样例，只用于理解字段。生产任务必须换成实际已核实的目标和渠道并合并当前文件；不能用这套示例覆盖现有配置，也不自动运行它来触发 toast。

<!-- lgmon-example: targets.yaml -->
```yaml
targets:
  - id: demo-web
    name: 示例网站
    collector: fake-collector
    match:
      process: fake-web.exe
      port: 18080
    probes:
      - type: http
        url: http://127.0.0.1:18080/health
        interval: 10s
        timeout: 2s
        expectStatus: "2xx"
```

<!-- lgmon-example: rules.yaml -->
```yaml
rules:
  - id: demo-web-down
    type: process_absent
    target: demo-web
    for: 4s
    severity: critical
    notify: [fake-toast]
    template: standard
  - id: demo-web-probe
    type: probe_failed
    target: demo-web
    for: 20s
    severity: warning
    notify: [fake-toast]
    template: standard
```

<!-- lgmon-example: notifiers.yaml -->
```yaml
notifiers:
  - id: fake-toast
    type: toast
```

<!-- lgmon-example: settings.yaml -->
```yaml
httpListen: 127.0.0.1:7444
publicBaseUrl: http://127.0.0.1:7444
retention:
  raw: 24h
  minute: 14d
  hour: 365d
```

### 两个工作目录不同的 node 博客

同一台机器的 node.exe 路径相同、命令行都使用相对路径时，用工作目录区分。以下两个条目合并进既有 targets.yaml 的 targets 列表，collector 换成实际 ID，保留其他条目：

```yaml
- id: node-blog-a
  collector: FAKE_COLLECTOR_ID
  match:
    process: node.exe
    workDir: 'D:\sites\blog-a'
- id: node-blog-b
  collector: FAKE_COLLECTOR_ID
  match:
    process: node.exe
    workDir: 'D:\sites\blog-b'
```

exeDir 用于程序文件所在目录，例如 Nebula 安装目录；workDir 是进程当前工作目录。二者匹配自身及子目录，不区分大小写，按完整分段比较，D:\site 不匹配 D:\sites。只接受绝对盘符或 UNC 路径，/ 与 \ 等价，规整 .、.. 和末尾分隔符，不展开 %VAR%。workDir 必须同时有 process、cmdline 或 exeDir，采集器只对已满足其他条件的候选读取工作目录，十秒内复用缓存。

先在实际采集器调用 test_target_match，不能用监控器本机的 targets test 代替远端结果。只有草稿包含 workDir 时，试匹配结果才提供 workDir；list_processes 不读取或返回它。权限不足、程序路径/工作目录为空时是 unknown，不应改成进程不在。本人可选择以服务身份运行采集器或去掉该条件；Agent 不自行提权或安装服务。

读取 list_collectors 的 config.state/version/message 核对下发进度：applied 已确认、pending 等待连接或回执、rejected 采集器拒收、outdated 缺少 match_dir 能力。outdated 时整版配置不下发，旧采集器继续上一版；先由本人升级，再确认 applied 和真实目标新采样。文件保存和监控器校验通过都不能代替采集器回执。

## 配方二：带用户接入飞书

1. 本人在正确 home 上运行 feishu setup，或打开网页“通知 → 接入向导”，自己填写 App ID/Secret。Agent 不读取 Secret、不代为粘贴聊天里的凭据。
2. Agent 可在 config 权限下 feishu_check(app) 检查凭据、机器人与权限，把返回的官方操作链接交给本人。本人开启机器人、开通权限、需要时发布应用；再次检查实际状态，不能仅凭“已操作”推断成功。
3. feishu_list_chats(app) 列出机器人实际所在的群。没有目标群时由本人拉机器人进群，再重新列出；不手写或猜 chat ID，不自行建群/拉人。
4. 本人确认具体测试群并授权后 feishu_test_chat(app,chatId,template)，或本人点网页“测试发送”。等本人确认真实收到；成功返回/messageId 不是收件确认。
5. 确认后 feishu_save_chat(app,chatId,template,rules) 保存；读回 notifiers/rules 核对渠道及引用，未确认收到时不把该渠道当成已验收。

程序运行时通过自己的官方 SDK 凭据调用飞书，不依赖 lark-cli。已有开发工具或测试应用不代表 Agent 可向任意群发送。代理、应用凭据和群信息按 [CLI 飞书接入说明](cli.md) 与实际向导处理。

## 配方三：让告警发到另一个群

1. 读取当前规则的 notify、对应渠道及 filter，确定用户要替换、追加还是按严重度分流。先显示可辨识的群名让本人确认，不要求本人查 chat ID。
2. 对新群走上一配方的实际发现、发送授权、本人收件确认及保存。保存操作会合并渠道，不应移除其他渠道。
3. 重新读取完整 rules，仅修改被指定规则的 notify；需改渠道默认模板或 filter 时也只合并该渠道。过滤支持 severity/tags/collectors，同组条件与跨组关系见 [通用 Webhook 与过滤](webhooks.md)，不猜参数形状。
4. validate_config 校验，再 put_config 保存完整文件并读回差异。需要真实触发/恢复卡片验证时取得明确授权，只操作自建演示进程；真实收件仍由本人确认。

Webhook URL/签名密钥由本人在管理员凭据界面或本地 secret set 输入，Agent 只使用引用名。更换群不应停止采集器、重装服务或调整全局暂停。

## 配方四：改卡片模板

标准 standard、简洁 compact、排查 detailed、汇总 digest 均为内置预设。“严重告警改用排查模板”应重新读完整 rules，将用户范围内 severity=critical 的规则 template 改成 detailed；保留其 notify、for、其他字段，以及 warning/info 规则。单条规则的 template 优先于渠道默认模板。

自定义先 template new <id> --from detailed，或 put_template(id,template)。修改标题、颜色、fields/extraFields、备注或完整 raw 卡片；结构见 [card-templates.md](card-templates.md)。fields=[] 表示隐藏内置字段，不能当成省略字段。展示已有进程/LogTail/Diagnosis 不等于产品会自动读取任意日志文件。

<!-- lgmon-example: card-templates/oncall.yaml -->
```yaml
id: oncall
name: 示例值班模板
extends: detailed
title: 网站需要排查
colors:
  firing: red
  resolved: green
fields: [host, target, startedAt, duration, value]
note: 请记录处理结果
```

先 preview_template(id,template,state=firing/resolved) 或本地 template preview，检查两态与配置校验。预览不发消息。规则试运行 test_rule 不发消息。只有明确授权给指定群试发，才能 test_notifier/feishu_test_chat；草稿模板可直接试发而不保存，真实显示与收件由本人确认。

## 配方五：做一页面板

1. 确认看哪些主机、目标、时间范围与指标。get_schema 取当前数据库的列说明；用 query_sql 验证查询、空值和单位，再决定类型与列契约。
2. 声明式 YAML 写到监控器 config/dashboards/<id>.yaml。当前没有写面板 HTTP/MCP 接口；不能访问该文件系统时交付完整 YAML 和准确保存位置，由本人落盘后继续只读验证，不宣称已经保存。
3. 使用稳定卡片 id，顶层 id/title/refresh/panels，defaultRange 可选。保留 collector_id/target_id 的数据范围，以便按用户选择过滤。例中 v_target_current 与 v_alerts_active 适合当前面板；v_collectors 的持久 last_seen 不等于实时在线。
4. 参数只有 :from/:to/:collector；省略 collector 绑定 NULL，全部主机条件必须写 (:collector IS NULL OR collector_id=:collector)。params 的 range 由服务端计算时间，固定 from/to 为 UTC Unix 毫秒，不拼接 SQL。

<!-- lgmon-example: dashboards/demo-web.yaml -->
```yaml
id: demo-web
title: 示例网站
refresh: 10s
defaultRange: 1h
panels:
  - id: web-health
    type: status_grid
    title: 网站当前状态
    width: 6
    query: >-
      SELECT collector_id || ' / ' || target_id AS label, state
      FROM v_target_current
      WHERE target_id = 'demo-web'
      AND (:collector IS NULL OR collector_id = :collector)
      ORDER BY collector_id
  - id: web-latency
    type: line
    title: 探测耗时（毫秒）
    width: 6
    query: >-
      SELECT ts, collector_id AS series, latency_ms AS value
      FROM probe_results
      WHERE target_id = 'demo-web' AND ts BETWEEN :from AND :to
      AND (:collector IS NULL OR collector_id = :collector)
      ORDER BY ts
  - id: web-alerts
    type: alerts
    title: 网站触发中告警
    width: 12
    query: >-
      SELECT summary, severity, state, started_at
      FROM v_alerts_active
      WHERE target_id = 'demo-web'
      AND (:collector IS NULL OR collector_id = :collector)
      ORDER BY started_at DESC
```

stat 列 value；line 列 ts/value，可选 series；bar 列 label/value；status_grid 列 label/state；alerts 列 summary/severity/state；table 至少一列；log 列 ts/message，可选 level。列与类型不符合会报 panel_columns；null 不当成 0。完整约定见 [dashboards.md](dashboards.md)。

5. 保存后 config validate；list_dashboards/get_dashboard 确认实际版本和错误，get_panel_data 检查每卡 params/columns/rows/truncated。网页 /#/boards/<id> 查看桌面/手机与深浅色；热加载失败保留旧版本，文件存在不能证明新面板已生效。

需要完全自定义 HTML 布局时，按 [HTML 面板](html-dashboards.md) 写同 ID 的 YAML/HTML。lgmon.js 已自动注入，只能 query/subscribe；不要把令牌放进 HTML、不要直接 fetch API。先 get_schema/query_sql 验证真实表列，使用上下文时间/主机参数并处理重连，文件保存后 config validate。可用 get_dashboard 读取完整源码和版本核对，未获本机文件写权限时给本人完整文件与位置。

## 配方六：排查一直触发的告警

1. list_alerts 取真实告警 ID、ruleId/collectorId/targetId、state/severity、startedAt/acknowledgedAt。不要依据群里残留的旧卡片判定当前状态。
2. 读当前规则、目标及渠道，get_target_history 查看最近采样/探测和 unknown/缺口，list_collectors 查看实时连接与时钟偏差。采集器离线、进程权限不足和真正 down 分开说明；日志只读取用户明确授权的文件，不能擅自收集环境变量或命令行凭据。
3. get_schema 后按目标、采集器、时间范围 query_sql，检查匹配是否过宽、端口属于谁、服务状态、探测耗时/状态码、阈值持续时间。只读 SQL 最多 5 秒、1 万行；结果空或 truncated 要如实解释，不据此自动恢复告警。
4. test_rule 可回放最近历史，不发送。SQL 规则 query 返回 key 定义实例，使用采样表与 :now；运行时 alerts/notifications/当前视图不可用，避免历史回放读取未来状态。与普通面板 SQL 的可读范围不同，不能把面板查询直接当 SQL 规则。
5. 根据实际原因提供修复建议。ack 只停止重复提醒，恢复仍通知；silence 需 rule 或 target、for/reason，静音期间不补发；全局 notify pause/resume 影响全部通知，恢复会补发仍需关注的事件，不提供 MCP 写工具。没有用户意图时不以静音、确认、删规则消除红色，也不修改已有服务。
6. 变更后看新的采样、告警状态和规则试运行结果。只有用户授权的自建演示进程可用于故障/恢复验收；真实收件另列本人待确认。

进程或服务故障可调用 get_alert_events 读取该告警原始采样前后各 60 秒的 subject 事件，按 nextCursor 续页；刷新时不带 cursor。recordId 是十进制字符串，不转成 JavaScript Number。没有原始 down 时间的旧告警返回空窗口，不据此断言没有崩溃。事件归属按采集时配置冻结，预设和订阅规则用法见 [事件日志规则](rules.md#事件日志规则)；不要把 subscription-only 的事件当作某进程已崩溃的证据。

## 从服务开关导入以后添加规则

先调用 preview_service_switch_import（采集器 ID，不传文件路径），向用户展示 create/update/covered/missing/skipped 及完整目标。用户同意后至少等待两秒，用该次 sha256 和 targetsVersion 调用 import_service_switch；冲突必须重新预览，不自动接受新版本。缺失目标默认保留，详情见 [服务开关导入](service-switch.md)。

导入只生成目标。随后读取目标和当前规则/通知渠道，确认进程匹配与探测结果，再按用户需求添加 port_closed、probe_failed、cert_expiry 等规则；保留已有规则，使用已有通知渠道。autostart off 的目标需和用户确认停止期间是否告警。不要把导入成功当作通知配置或真实收件验收。

## 查询启动项与计划任务

先 get_schema，再 query_sql 读取 inventory_current、inventory_scopes 和 inventory_changes；CLI 使用同一 lgmon query。检查来源 error_code 和 last_success，读取失败或无数据不能解释为项目已删除。任务 lastResult 是原始系统代码，不把所有非零值判为失败。SQL 回放只能读 inventory_snapshots/changes，不能读当前态 current/scopes。仅做只读诊断，不运行、注册、启停或删除系统任务/启动项；完整字段与来源边界见 [启动项与计划任务](inventory.md)。

## 交付与人工验收

报告实际改动的目标/规则/渠道/模板/面板 ID，配置差异、校验结果、新采样与面板数据，以及缺权限、离线、待本人收件或视觉确认的项目。草稿、保存、热加载成功、真实通知收到是不同证据。

M1-18 的最终本人验收：新开 Agent 会话，仅粘贴 agent prompt 生成内容，让它完成“给 Nebula 加监控、写规则、做一页面板、把严重告警改用排查模板”。本人确认目标匹配、配置与面板数据正确；不得关闭或重启真实 Nebula 来演示故障。需要故障验证时另建自己的演示进程。当前自动化只能验证命令、样例和接口路径，不代替这个新会话决定。
