# lgmon 命令行

## 多监控器

`lgmon pair join --name <id> --monitor <主机:端口>` 为附加监控器独立配对；保留默认命令。配对后合并本机 `settings.yaml` 的 `collectorMonitors` 并重启生效。`lgmon pair monitors --json` 只读列出每槽身份、缓存和合计预算。完整步骤、容量及操作授权边界见 [多监控器](multi-monitor.md)。

## 飞书卡片动作（M2-06）

`lgmon feishu actions enable <渠道ID>`、`lgmon feishu actions disable <渠道ID>` 和 `lgmon feishu actions status <渠道ID>` 均支持 --home/--json。前两个需要本机管理员身份，授权/撤销群原消息收件人对相应告警的确认及一小时告警组静音权限。status 只读取配置，不连接飞书；实时连接状态在网页/API 中读取。接入、作用范围和真人待验见 [卡片动作](card-actions.md)。

## 团队与分享（M2-05）

命令连接指定 home 中已运行的 monitor/both，使用受保护的本机管理证书，均支持 --json。完整参数、范围和身份规则见 [团队共享](teams.md)。

| 命令 | 作用 |
|---|---|
| `lgmon team create`、`lgmon team list`、`lgmon team members` | 创建/列出团队、成员及访问记录 |
| `lgmon team invite`、`lgmon team remove` | 一次性邀请、吊销指定设备成员 |
| `lgmon team join`、`lgmon team joined` | 从 stdin 预览/核对邀请后加入、列出本机身份 |
| `lgmon team address`、`lgmon team leave` | 保留身份改地址、移除本机加入记录 |
| `lgmon team shared`、`lgmon team view` | 读取已授权的共享定义或卡片数据 |
| `lgmon share create`、`lgmon share list` | 建立分享、列出负责人已有分享 |
| `lgmon share update`、`lgmon share revoke` | 按 revision 更新、撤销分享 |

## Agent 使用入口（M1-18）

### `lgmon agent install [--target claude|codex|all] [--json]`

默认 all；将内置使用技能写入当前账户 ~/.claude/skills/lg-monitor/SKILL.md 和 ~/.codex/skills/lg-monitor/SKILL.md，填入当前 exe 和 --home 的实际绝对路径。只写该文件，不修改 MCP 客户端配置，不生成令牌、不连接监控器。仅更新本程序管理的普通文件，其他文件或链接报冲突，不覆盖用户内容。单文件原子替换；多目标失败时退出 1，installed 数组如实列出已经完成的路径，便于修复后重跑。

成功 JSON 形状为 `{"ok":true,"installed":["绝对技能路径"]}`；失败包含 ok=false、installed 和 error，实际目录不同不会写进固定示例。安装测试只使用临时用户目录，不修改本人真实技能。

### `lgmon agent prompt --wish "任务描述" [--json]`

输出可直接粘贴给新 Agent 会话的指令：含原样 JSON 编码的愿望、实际程序/home/config 路径、PowerShell 转义命令及自包含精简使用指南。不执行 wish，不读取凭据/数据库、不安装技能，不要求预先安装。--json 返回 executable、home、config、prompt。wish 必须非空；所有命令继续支持通用 --home、--log-level。

完整配方与最终新会话验收见 [agent-guide.md](agent-guide.md)。工程验证与本人验收分开记录；安装成功不代表 Agent 已完成 Nebula 的实际监控配置。

## 全局通知暂停与托盘（M1-17）

### `lgmon notify pause --for 1h [--reason 开会]`

直接写入 home/data/lgmon.db；不通过 HTTP。时长为 1 分钟到 24 小时，原因最多 200 个 Unicode 字符。返回当前暂停状态 JSON，发起人为 local。再次执行会把截止时间改为现在加上指定时长，保留本次暂停的开始时间与最初发起人。监控照常记录；运行中的监控器每 5 秒读取变化。

### `lgmon notify resume`

结束当前暂停并返回状态 JSON。结束后仍在触发的告警补发一次；暂停前已通知的告警在暂停期间恢复时补发恢复；暂停期间开始并恢复的不补发。补发仍受当前静音、确认及渠道过滤限制。多渠道分别记账，重启不重复已成功的渠道。

`lgmon status` 的 JSON 增加 pause，与 GET /api/v1/notifications/pause 相同。未暂停时 paused=false，其余字段为 null；文本模式显示是否暂停及截止时间、压下的事件数。

### `lgmon tray`

登录用户的独立托盘，退出只影响本进程。每 10 秒读取告警与暂停状态；没有配置令牌时通过真正本机的只读会话查看，暂停菜单禁用。红色代表 critical，橙色代表 warning/info，绿色代表无告警，灰色暂停标记代表暂停，灰色无标记代表断连。点击设置帮助打开面板。

前台 `lgmon run --role monitor` 或 both 默认显示进程内托盘，无 HTTP 令牌；退出同时停止该前台程序。collector 及 Windows 服务不显示托盘。`run --no-tray` 可用于终端或自动化运行。

### `lgmon tray setup [--autostart] [--remove]`

需要以平时登录的同一 Windows 账户提权运行。配置令牌名为 tray-用户名，权限 config，使用用户级 DPAPI 加密到 %LOCALAPPDATA%\lg-monitor\tray-token；tray.json 保存监听 URL 和用于吊销的令牌登记信息。明文不输出、不写日志、不放参数。再次 setup 会吊销旧令牌并生成新令牌；跨 home 操作先用原 home 删除登记。其他账户不能解密。

--remove 吊销并删除文件。只有显式 --autostart 才改 HKCU 登录启动；与 --remove 同用删除本程序的启动项。不指定时保留注册表现状。运行中的托盘下一次刷新会重读文件；令牌被吊销后回到只读状态。仅监听非回环地址时必须先 setup。

真实托盘、账户切换及登录启动操作由本人验收；开发自动化只操作临时目录和假的注册表接口，不安装服务。

## 通用约定

程序为 `bin\lgmon.exe`。以下使用 `lgmon` 作为命令名；可在 PowerShell 中换成 `.\bin\lgmon.exe`。

所有命令支持 `--home <目录>`、`--log-level debug|info|warn|error`。home 优先级：命令参数、`LGMON_HOME`、`%ProgramData%\lg-monitor`；开发示例使用 `--home .\dev-home`。配置在 `config\`，数据库、采集器 ID、密钥和向导进度在 `data\`。不要把包含真实凭证或数据库的 home 提交到 Git。

命令成功退出 0，错误或需要处理的步骤退出 1。错误写到 stderr；除配置校验和飞书步骤的结构化结果外，失败时 stdout 可以为空。`--help` 查看命令参数；参数错误会提示查看帮助，运行错误直接给出原因。

JSON 字段使用 camelCase；时间戳为 UTC Unix 毫秒。文本时间使用本地时区。JSON 对象字段顺序不作为接口约定，列表按各命令约定排序。没有数据的状态/告警列表为 `[]`。日志和回退提示写到 stderr，不混入 JSON stdout。

## 版本与配置

### `lgmon version`

显示版本、提交号和构建时间，没有 JSON 模式。

### `lgmon config validate [--json]`

校验 home/config 中的 targets.yaml、rules.yaml、notifiers.yaml、settings.yaml、card-templates/*.yaml 和 dashboards/*.yaml（HTML 页面还检查其同 ID 的 .html）。缺少文件视为空；未知字段、非法值和无效引用会一次列出。无效配置退出 1。与 HTTP/MCP 的磁盘校验共用同一个函数；本命令不接受草稿。

```json
{"ok":true,"errors":[]}
```

错误项字段为 `file`（相对配置目录的文件）、`line`（1 起始行号）、`message`（中文说明）。文本模式显示错误总数及每处文件/行号。

### `lgmon config schema [--output schemas]`

从 Go 结构生成七份 JSON Schema（含通用 Webhook 请求体和声明式面板），逐行输出生成路径。该命令不使用 home/config 保存 Schema，不支持 `--json`。仓库示例见 examples/，编辑器可用文件头的 `$schema` 提示。

## 指标导出

当前持久指标可用 `lgmon metrics --home <home>` 导出 Prometheus 文本，`--json` 返回同源结构化快照。停止运行时可用，不发送网络请求；配置与单位见 [指标导出](exports.md)。

## 密钥

### `lgmon secret set <名称> [--json]`

交互终端输入不回显；通过管道输入时读取至 EOF，限制 64KiB，移除末尾一组换行。Secret 以机器级 DPAPI 加密，文件 ACL 限制为 SYSTEM、Administrators 和创建者。成功 JSON 为 `{"ok":true}`。不提供通过参数传递明文 Secret 或读取明文的命令。

飞书自定义机器人的地址也按密钥处理。在有权写入该 home 的管理员终端分别执行以下命令，按提示输入地址与签名密钥；不使用签名时只保存地址：

```powershell
lgmon secret set hook-url --home .\dev-home --json
lgmon secret set hook-sign --home .\dev-home --json
```

在同一 home 的 notifiers.yaml 通知列表中添加渠道，保留原有应用和渠道。下面是独立配置示例，字段内容只有密钥名称：

```yaml
notifiers:
  - id: custom-bot
    type: feishu_webhook
    urlRef: hook-url
    secretRef: hook-sign
    template: standard
```

不签名时省略 secretRef，规则 notify 引用 custom-bot。也可以通过 admin 的渠道凭据接口完成加密保存与配置登记，见 [API](api.md#自定义机器人与-windows-通知)。

### `lgmon secret list [--json]`

按名称排序列出密钥，只读名称、不解密值。JSON 为 `{"names":["feishu-default"]}`；不存在密钥库时为 `{"names":[]}`，不创建目录。

## 卡片模板

完整配置、变量、辅助函数、转义与大小规则见 [card-templates.md](card-templates.md)。预设为 standard、compact、detailed、digest。

### `lgmon template list [--json]`

预设在前，自定义在后，各组按 ID 排序。JSON 是数组，项目字段为 `id`、`name`、`preset`（布尔值）。

### `lgmon template validate [id]`

使用触发、恢复、汇总三种虚构样例严格校验指定或全部模板；检查模板执行、JSON 基础结构和 28,000 字节上限。任一错误退出 1，不支持 `--json`，不发送消息。

### `lgmon template preview <id> [--state firing|resolved|digest]`

默认 firing，stdout 输出一张 JSON 2.0 卡片。自定义模板错误时输出标准卡片，提示写到 stderr。只预览，不发送消息。内置样例为虚构数据，不能代替真实客户端显示验收。

### `lgmon template new <id> [--from standard|compact|detailed|digest]`

默认从 standard 生成有注释的 home/config/card-templates/id.yaml。ID 以小写字母或数字开头，可带连字符，长度 1～100；不能占用预设名。已有文件不会覆盖。

## 飞书接入

`feishu` 命令支持 `--app <配置中的应用 ID>`，区别于开发者后台 `cli_` 开头的 App ID。check/chats 有多个配置应用时必须指定 --app；setup 未指定时使用 default，对应渠道 ID 为 feishu-alerts。其他 setup 应用生成渠道 feishu-<应用 ID>。

### `lgmon feishu setup`

依次填写 App ID/隐藏的 Secret、启用机器人、开通应用身份权限、拉机器人入群、选择群和模板、发送测试、确认收到、保存通知配置。机器人与权限每 5 秒检查，入群每 3 秒检查。权限链接自动在浏览器打开；等待超过 60 秒提示创建和发布应用版本。实际后台界面及发布步骤需按 M0 验收核实。

必需权限为 im:message:send_as_bot（发送卡片）、im:chat:read（读取机器人所在群）；user 身份权限不能代替 tenant 权限。查询接口返回 99991672 时使用其提示的权限和开通链接。向导提供将机器人加入现有群的步骤，不自动建群或拉用户。

```powershell
lgmon feishu setup --home .\dev-home
```

Agent/脚本参数：`--app-id <cli_开头的ID>`、`--secret-stdin`、`--domain feishu|lark`、`--chat-id <已确认的群ID>`、`--template <模板ID>`、`--yes`、`--json`。Secret 由安全的输入来源通过 stdin 提供，禁止在命令参数里写明文。未明确选群时不会自动发送；--yes 确认向所选群发送并跳过收件询问，成功提示仍要求到群核实真实显示。

进度保存在 data/feishu-setup-<应用ID>.json，不含 Secret/令牌。Ctrl+C 或输入结束后重新运行可续上；已经发送的测试会显示消息 ID，不因续跑重复发送。更换群或模板后重新生成测试 ID。凭证验证后 Secret 即加密保存，通知配置在最后确认步骤保存。

setup --json 为 NDJSON，每行一个步骤对象。需要人工处理时立即退出 1，不在 JSON 流中询问或持续轮询；处理后再次运行续上。JSON 模式发送测试需要 --yes。

### `lgmon feishu check [--json]`

只读检查配置中的应用，不开浏览器、不改配置、不发送消息、不打开通知写库。输出形状：

```json
{"steps":[{"step":"credentials","status":"action_required","message":"尚未配置飞书应用，请先运行 lgmon feishu setup"}]}
```

正常检查包含 credentials、bot、permissions、chat 四个步骤；凭证失败时只含第一步。群为空或必需权限未开通时退出 1。

### `lgmon feishu chats [--watch] [--json]`

列出机器人所在群；人数来自额外的只读群详情查询，只计算用户人数，不包含机器人。详情没有返回人数或无权限时为未知，不写成零。--watch 每 3 秒检查，首次显示现有群，之后只输出新群；Ctrl+C 结束。

JSON 为 chat 步骤对象，--watch 为 NDJSON。每个群字段为 `id`、`name`、`memberCount`（整数或 null）。

### `lgmon feishu test --notifier <id> [--template <id>] [--service] [--json]`

向指定已确认渠道发送一张标明“这是测试消息”的卡片，模板默认使用渠道配置。实际网络尝试经 Sender 重试及 SQLite 审计；永久配置错误直接提示处理，网络/限流按 2、5、15 秒最多再尝试三次。成功返回飞书 messageId；接口成功仍需核对群内收件。

此命令只支持 feishu_app；自定义机器人、通用 Webhook 和 Windows 渠道使用 `POST /api/v1/notifiers/{id}/test`，需要 config 权限。自定义机器人没有应用消息 ID，Windows 通知提交成功也不能代替本人确认弹窗。通用 Webhook 的密钥格式、接收端验签与 filter 见 [webhooks.md](webhooks.md)。

--service 使用 home 中的本机管理证书，请正在 LocalSystem 上运行的 monitor/both 服务读取飞书密钥、发送并审计；CLI 不读取飞书应用密钥。发送端校验管理证书和服务身份，普通控制台监控器、采集器证书和匿名连接均被拒绝。该模式用于验证服务身份的凭据与网络，仍需本人确认渠道为测试群；collector 角色不保存通知凭据。JSON 沿用相同步骤对象，message 明确 LocalSystem，返回接口成功仍须检查群内收件。

所有飞书步骤对象共享字段：

| 字段 | 含义 |
| --- | --- |
| step | credentials / bot / permissions / chat / template / test / complete |
| status | ok / action_required / error |
| message | 给人看的中文说明 |
| url | 可直接打开的处理链接，可省略 |
| permissions | 权限数组，可省略；含 name、required、description、granted |
| chats | 群数组，可省略；含 id、name、memberCount |
| messageId | 测试消息 ID，可省略 |

## 运行与查询

### `lgmon run [--role both]`

```powershell
lgmon run --role both --home .\dev-home
```

支持 both、collector、monitor。both 在同一进程中通过回环 gRPC+mTLS 运行两角色；monitor 只运行接收、存储、规则和通知；collector 只采集并主动连接监控器，不打开监控数据库或读取飞书密钥。采集器自己的 TLS 私钥也通过 DPAPI 密钥库存储。交互式 run 不注册服务或修改防火墙；SCM 启动的 run 自动进入服务宿主。监控器没有配置任何通知渠道时提示 feishu setup。

### `lgmon install [--role both] [--listen :7443] [--monitor address] [--json]`

需要管理员终端。只注册固定名称 lgmon 的 Windows 服务，不覆盖同名服务、不立即启动；账户 LocalSystem、开机自动启动、失败 10 秒后重启。安装把程序复制到 Program Files/lg-monitor/lgmon.exe，保护程序目录为 SYSTEM/Administrators 完全控制、Users 只读执行；home 在安装及每次服务启动时限制为 SYSTEM/Administrators。服务启动检查程序及目录权限，不安全时记事件日志并拒绝运行。home 转为绝对路径并按 Windows 参数规则转义，保存 role/listen/monitor/log-level。monitor/both 安装前校验配置，collector 应先完成配对。检测到当前用户代理时给出显式 proxy 配置提示，不把代理值写入结果或复制到服务环境。注册或恢复策略失败回滚本次新建服务，既有服务/日志来源不覆盖。权限核对与升级见 [Windows 服务](windows-service.md)。

### `lgmon service start [--json]`

启动固定服务 lgmon，等待 running，已运行时直接返回；需要管理员权限。默认最多等待 35 秒，超时后通过 service status 与日志确认，不强杀进程。

### `lgmon service stop [--json]`

发送停止控制，等待 stopped，已停止时直接返回；需要管理员权限。服务先取消采集并排空，停止期限 30 秒；到期主动退出，持久 outbox/通知审计用于下次恢复。本人请求停止返回成功，不触发 SCM 失败重启。

### `lgmon service status [--json]`

只读 SCM 查询，不要求管理员、不打开程序数据库或创建 home。没有安装时 installed=false、state=not_installed；已安装时返回状态及 PID。服务名称在本机固定，--home 不改变所查询的 Windows 服务。

### `lgmon uninstall [--json]`

需要管理员权限，先停止 lgmon 并确认 stopped，再删除服务与其事件日志来源；配置、数据库、证书和密钥保留。仅操作名称 lgmon，不操作其他服务。

以上服务命令 JSON 共用 status、message、name、warnings（数组）。查询成功时包含 installed、state、pid；安装成功为 installed=true/state=stopped，卸载为 installed=false/state=not_installed。失败 status=error、退出 1，不输出未经成功查询的 installed/state/pid。服务运行日志写 home/logs/lgmon.log（JSON），启动/异常同时写 Windows 应用日志来源 lgmon。

```powershell
lgmon run --role monitor --home .\dev-home\monitor --listen 127.0.0.1:7443
lgmon run --role collector --home .\dev-home\collector --monitor 127.0.0.1:7443
```

--listen 默认 :7443；--monitor 默认使用 pair join 保存的地址，也可以显式覆盖为同一可信 CA 对应的另一个地址。collector 必须先配对；both 在本机首次启动时自行签发本机采集器证书，之后保留原身份并遵守吊销状态，不静默重新授权。targets.yaml 只在监控器管理：目标 collector 字段为采集器 ID、主机名或 "*"（省略表示全部，主机名不区分大小写），修改校验通过后自动推送。collector 只读取本地 settings.yaml 设置缓存，离线重启使用 data/collector-config/targets.json。规则、飞书应用、渠道、模板和飞书密钥只放监控器。详见 [配置协议](transport.md#集中配置)。

gRPC 单条消息压缩前上限 4 MiB，双向 gzip。collector/both 先将消息和持久序号提交到 data/outbox.db，独立 worker 按最多 256 条/1 MiB 发批次，单条较大记录独立发送；监控器以采集器 ID/序号去重，一批一次存储事务、逐条规则消费、一次应用确认事务，Ack 最高序号后，采集器一次删除已确认前缀。断线持续采集并按 1～60 秒抖动退避重连，Ctrl+C 保留未确认缓存，重启继续补发。

默认缓存 24 小时或 500 MB（500000000 字节），本机 settings.yaml 可设 cache: {maxSize: 500MB}，范围 64MB～10GB、重启生效、不经配置下发。超限删除最旧前缀并在恢复后上报数据缺口，详细边界见 [采集通道](transport.md)。同一 home 只允许一个 outbox 持有者。损坏或不一致的数据库水位会报错，程序不静默清空数据或重新配对。

collectors list --json 中每个采集器增加 cache: {bytes,maxBytes,bytesPerHour,projectedHours}；旧版未报告时为 null，启动不足五分钟或最近无写入时后两项为 null。当前占用为 SQLite 已用页 ×4096，速率按每条负载+24 字节、最近一小时分钟桶折算，在线确认、过期和超限丢弃都不扣掉已写入计数。projectedHours=(maxBytes−8 MiB)/bytesPerHour，表示从空缓存起算；status 在不足 24 小时时警告。

status --json 的 collectors 保留各主机最近心跳统计，cache 另列本机 outbox 的最近统计。纯 collector 没有监控数据库时仍能读取本机缓存；窗口计数不跨重启恢复。修改磁盘 maxSize 后 status 提示“cache.maxSize 已修改，重启采集器后生效”。调小且已有数据装不下时先沿用现有文件预算，不删数据；清空后自动整理生效，失败原因显示在 status 和采集器日志中。

估算：单进程目标每两秒采样约 4.8 MB/天，每条每 30 秒一次的 HTTP 探测另约 0.3 MB/天。目标匹配多个进程时按进程数增加，实际时长以缓存统计为准。

### `lgmon pair new [--ttl 10m] [--json]`

在已运行 monitor 或 both 的电脑上执行，--home 指向该监控器。命令读取受保护的本机管理证书，通过同一 TLS 端口申请一次性配对码；无需另开管理端口。--ttl 范围为 1s～24h，默认 10m。配对码仅在本次输出出现，数据库只存摘要；不要提交或转发给无关人员。

JSON 字段为 status、message、code、expiresAt（UTC 毫秒）、fingerprint（SHA-256 完整 64 位十六进制）、fingerprintShort（前 16 位，按四位一组显示，如 `3F2A-9C1B-77D0-4E85`）。文本同时显示短指纹和完整指纹，供采集器一侧人工核对。

### `lgmon pair join --monitor <host:port> --code <code> [--fingerprint <hex>] [--json]`

在采集器电脑执行，--home 指向采集器目录。先用仅获取公共 CA 的引导 TLS 连接读取 CA，显示指纹；与监控器核对后，再固定该 CA 建立验证服务端身份的 TLS 连接，发送配对码和本机 CSR。私钥在采集器本机生成并经 DPAPI 加密保存，不发给监控器。

--fingerprint 接受 16 位短指纹或完整 64 位，忽略大小写、连字符和冒号；8 位前缀被拒绝并提示输入 16 位。Agent 使用完整值。交互模式缺少参数时要求本人输入指纹。--json 不读取交互输入；缺少指纹时返回 status=action_required、message、fingerprint 和 fingerprintShort，不发送配对码；补齐 --fingerprint 后续跑。成功为 status=ok，失败为 status=error，退出 1。所有结果包含 message，错误不会重复污染 JSON 标准输出。

```powershell
.\bin\lgmon.exe pair new --home .\dev-home\monitor --ttl 10m --json
# 在采集器电脑运行以下部分，输入本人从监控器核对的值。
$monitorAddress = Read-Host '监控器主机名或 IP:端口'
$pairCode = Read-Host '一次性配对码'
$caFingerprint = Read-Host '核对后的完整 CA 指纹'
.\bin\lgmon.exe pair join --home .\dev-home\collector --monitor $monitorAddress --code $pairCode --fingerprint $caFingerprint --json
.\bin\lgmon.exe run --role collector --home .\dev-home\collector
```

错误、过期或重复使用的码均拒绝；指纹不一致立即停止。已有有效身份不能被另一次 CSR 覆盖；恢复原身份，或在监控器明确吊销后使用新码重配。服务端证书校验主机名/IP，常态连接需要有效且未吊销的客户端证书，并与 Hello 的采集器 ID 一致。不会降级为明文。

### `lgmon collectors list [--json]`

每项增加 config 对象：state 为 applied/pending/rejected/outdated，version 是目标内容哈希，message 说明回执或升级原因。无回执时保守为 pending；旧版缺少 match_dir 且配置使用 exeDir/workDir 时不下发该版，采集器保留上一版。状态为最近一次下发/回执，监控器重启后等待新连接确认。

在监控器目录使用本机管理证书查询，monitor/both 需运行。JSON 为 status、message、collectors 数组（空时为 []）；各项 id、hostname、serial、issuedAt（UTC 毫秒）、revoked。只返回登记信息，不返回证书私钥、密钥引用或飞书凭据。

### `lgmon collectors revoke <id> [--json]`

明确吊销该采集器证书并取消现有采集流，后续连接会被拒绝。JSON 为 status、message、collectorId；失败 status=error，退出 1。若在 both 中吊销本机采集器，监控器继续接收其他采集器；再次运行 both 不会自行撤销吊销，需要明确重新配对。

配置启动时严格校验，并监听有效修改；加载失败保留上一份配置。默认每 2 秒采集、10 秒全量状态、1 秒评估规则。数据库为 home/data/lgmon.db，WAL 模式；原始采样默认保留 24 小时，分钟汇总 14 天、小时汇总 365 天，告警和发送审计不作为采样清理。settings.yaml 的 retention.raw/minute/hour 支持 24h、14d 等写法，raw 不短于 24h，三级保留时长依次递增或相等；修改从下一轮后台清理生效，增大不能找回已清理的数据。详见 [数据汇总与保留](storage.md)。采集器身份保存在 data/collector-id。

collector/both 每 10 秒发送独立心跳；monitor/both 自动为全部有效配对采集器启用 30 秒失联规则。失联后告警、重新连接后恢复，连接级告警 targetId 为 @collector。可在 rules.yaml 写 collector_offline 类型修改持续时间、严重程度、通知渠道或模板，不填写 target；省略 for 为 30s。无显式规则时默认 ID 为 collector-offline，warning、发往全部当前渠道。详见 [心跳与失联](transport.md#心跳与失联)。

Ctrl+C 先停止采集和探测，再排空已接受的消息及待发送通知，刷新并关闭数据库；网络重试期间退出会等待发送完成。单轮采集失败会跳过本轮并在下轮重试；原始写批失败保留数据，按 0.5 秒起、最长 30 秒退避重试；规则保存或通知入队失败保留原告警 ID 和待办，下次评估重试。通知队列有 256 个槽，满时立即报错供规则重试，不阻塞其他规则评估。

启动先恢复触发状态，再将最近 24 小时未成功送达的触发/恢复转换按事件顺序补入发送队列，之后处理新采集消息。查询每页 256 条；每个渠道有独立 FIFO 队列，同一渠道内触发先于恢复，一个接收方阻塞不拖住其他渠道。成功记录按状态及通知渠道区分，成功和明确跳过的渠道不补发，filter 筛掉的渠道按当前配置重新判断；closed 事件不补发。通知审计使用 state 和 event_key 区分转换和重复提醒，不把同毫秒的触发发送成功误判成恢复成功。旧版本没有 state 的审计不能确认具体状态，补发采取保守策略；飞书 UUID 的服务器去重窗口只有 1 小时，补发不承诺永久 exactly-once。

通知网络失败完成既定重试并保留审计，继续监控。卡片被服务端明确拒绝（230099）时，可由非 standard/compact 模板降级为标准模板，保留告警 ID/状态/群的幂等键与发送审计。原始待提交缓冲默认 10000 条、另有 2048 个有界请求槽；溢出只丢最旧原始采样并记录数量。关闭时最后提交仍失败会返回错误。

### `lgmon status [--json]`

JSON 增加 collectors，包含保存的采集器元数据和 clockOffsetMs；文本模式在偏差超过十秒时提示该主机的时钟快/慢及同步时间。status 从本机数据库读取，停止监控器后仍能查看最后保存的估计；不会修改系统时间。

按本机 config/settings.yaml 的 httpListen 检查明文访问风险，非回环且不在 Tailscale 网段时向 stderr 输出中文提示，stdout 的 JSON 结构不变。只读取 settings 文件，其他配置出错不阻止提示或读取已有状态；settings 本身损坏时明确报告无法检查，不隐藏数据库状态。该检查反映磁盘配置，运行监听是否成功切换以监控器日志为准。

使用 mode=ro、query_only 的独立 SQLite 连接，程序停止时仍可读取已保存状态；不进行迁移或原始数据清理。输出每个主机/目标的最近状态和触发中告警。保存的 up/down 是最近一次采集值，不代表当前采集进程正在运行；采集器在线检测按 M1 心跳实现。

```json
{"available":false,"targets":[],"alerts":[],"ruleErrors":[]}
```

无数据库时 available=false，退出 0，不创建目录；有数据库时为 true。targets 按 collectorId、targetId 排序，字段为 collectorId、targetId、ts、state（up/down/unknown）、detail。detail 为 TargetStatus 对象：targetId、state、process（found/missing/unknown 或空）、port（listening/closed/unknown 或空）、reason、full、processes、ports。processes 为精简进程引用，含 pid、parentPid、name、exe、createdAt、cpuPercent、rss、threads，不含命令行。

alerts 按 startedAt 倒序、id 排序，字段见下一节。status 同时查询两个列表，显示数据库里已经提交的记录，不等待另一个进程的待提交批次。

ruleErrors 列出 SQL 规则最近一次错误：ruleId、error、at（UTC 毫秒）、consecutive（当前连续失败次数）、active（当前是否失败）。查询恢复成功后保留最后错误，active=false、consecutive=0。语法错误、5 秒超时、超出 100 行和读表受限都按 unknown 处理，不触发或恢复告警，取消 pending。

### `lgmon alerts [--json] [--all]`

默认只读 firing；--all 包含 resolved。输出形状为 `{"available":true,"alerts":[]}`，缺少数据库时 available=false。每个告警字段：

| 字段 | 含义 |
| --- | --- |
| id / ruleId / collectorId / targetId | 告警、规则、采集器、目标标识 |
| state / severity / summary | firing 或 resolved、严重度、摘要 |
| startedAt | 首次持续满足条件的 UTC 毫秒 |
| resolvedAt | 恢复毫秒；尚未恢复为 0 |
| lastNotifiedAt | 最近一次成功发送时间；尚未成功为 0 |
| instanceKey | SQL 查询返回的实例 key；内置规则省略 |
| acknowledgedAt | 首次手动确认时间；未确认省略 |
| notificationAt / notificationKey | 最近一次通知事件时间及身份，用于区分重复提醒 |
| flapping | 当前处于抖动合并时为 true；稳定 10 分钟后清除 |

告警持久化和发送成功是两个阶段。lastNotifiedAt=0 表示未记录成功发送；非零也只表示曾有一次成功，不能据此认定当前状态已发到所有群。规则删除或修改类型、目标时，旧触发告警以 closed 事件关闭，状态存为 resolved，且不发送恢复卡片。

### `lgmon targets test <id> [--json]`

从 home/config 加载配置，只读采集一次进程和 TCP 监听，不启动探测 worker、不写数据库、不操作已有进程。显示匹配状态、原因、进程和端口；不匹配本身仍退出 0，读取配置或采集失败退出 1。

JSON 字段为 targetId、state、reason、processes、ports。进程字段：pid、parentPid、name、cmdline、exe、createdAt、cpuPercent、rss、threads，可选 cmdlineUnavailable；端口字段：localAddress、port、pid。cpuPercent 是整机容量归一化百分比，rss 是字节。

### `lgmon diagnose <告警ID> [--json]`

从 home/data/lgmon.db 只读生成 schemaVersion=1 的告警诊断包，监控器停止后仍可使用；不创建缺失 home、不执行修复。--json 输出 JSON，省略时输出交接提示和格式化 JSON。错误退出 1；JSON 模式为 `{"error":{"code":"diagnostic_failed","message":"中文错误"}}`。内容、上限与隐私边界见 [诊断包](diagnostics.md)。

### `lgmon alerts ack <告警ID> [--json]`

确认指定 firing 告警，停止它的重复提醒；恢复仍通知。操作写入监控器本地数据库，可以在监控运行或停止时使用。不存在或已恢复的告警返回错误。确认幂等，保留第一次确认时间；新一轮故障使用新 ID，不继承确认。JSON 为 `{"ok":true,"alertId":"..."}`，alerts/status 新增 acknowledgedAt、instanceKey、notificationAt、notificationKey 字段。

### `lgmon silence add --rule <id> | --target <id> --for 2h --reason "维护" [--json]`

rule 与 target 必须且只能选一个。静音保存在监控器数据库，跨重启有效；状态继续记录，期间的通知转换标记为抑制，重启不会补发。确认与静音分开：静音也覆盖恢复通知。到期后新发生的转换/提醒照常处理。JSON 返回 id、ruleId 或 targetId、startsAt、endsAt（UTC 毫秒）、reason；缺少数据库时返回错误且不创建目录。

### `lgmon rules test <规则ID> [--since 24h] [--json]`

也可运行 `lgmon rules test --file draft.yaml`，文件只写一条规则对象，与规则 ID 互斥；草稿严格校验字段类型和当前目标、渠道引用，不保存到 rules.yaml。完整规则写法见 [规则与回放](rules.md)。

回放结束时刻取命令开始时的时钟；请求超过 settings.yaml 中 retention.raw 的保留时长时截断并在 notes 中说明，默认 24 小时。--since 支持 1d 等写法。SQL 按原 interval（默认 1m、最小 10s）逐次执行，超过 10000 次评估直接报错并给出最大范围。每次查询最长 5 秒，整个回放最长 60 秒；取消或超时返回 complete=false 的部分结果。首次评估错误直接返回错误，中途错误计入 errors，不制造触发或恢复。

SQL 实时与回放都绑定 `:now` UTC 毫秒，并通过同名 NOT MATERIALIZED CTE 过滤未来记录；只读专用连接的每条物理连接都安装授权回调。内置规则把历史事实按时间顺序喂入同一个引擎，窗口起点使用此前最后样本，for 从窗口起计；同毫秒事实合并后评估，最多加载 200000 条样本。collector_offline 缺少心跳历史，明确拒绝回放；无数据不推断健康，旧进程身份不可确认时报告跳过。

JSON、未来 API 和 MCP 统一使用以下结构，时间和 step 均为 UTC 毫秒：

```json
{"rule":"slow","type":"sql","requestedFrom":1000000,"from":1000000,"to":1060000,"step":10000,"evaluations":7,"complete":true,"events":[{"key":"fake/web","state":"firing","ts":1020000,"startedAt":1010000,"summary":"探测耗时过高"},{"key":"fake/web","state":"resolved","ts":1030000}],"totals":{"fired":1,"resolved":1,"firingAtEnd":0},"errors":{"count":0,"first":null},"notes":[]}
```

回放不恢复已有告警，不写实际 alerts、alert_events、notifications，不发送消息，也不模拟静音、确认、重复提醒和抖动合并。notes 说明保留截断、起点首次触发延迟、起点窗口数据不足及补传数据差异。实际发生过什么以 alerts 为准。

### `lgmon token create --scope read|config|admin --name <名称> [--json]`

生成 HTTP API 令牌，scope 默认 read。手机只查看时使用 read，需要确认和静音时使用 config。所有令牌管理命令都必须以管理员身份运行；认证数据放在独立的 home/data/auth.db，服务 home 的文件权限同样生效。令牌名称不能为空、最长 128 字节。程序生成 256 位随机值，数据库只保存 SHA-256 哈希及元数据。

令牌原文只在创建结果中显示一次。JSON 字段为 status、message、token、id、name、scope、createdAt（UTC 毫秒）；失败结果不包含 token。不要把实际令牌放进配置示例、日志或 Git。默认 HTTP 端口为 7444，配置与接口示例见 [api.md](api.md)。

```powershell
.\bin\lgmon.exe token create --scope read --name phone --home .\dev-home --json
```

### `lgmon token list [--json]`

以管理员身份列出 ID、名称、权限、创建/最后使用时间和吊销状态；不返回原文或哈希。没有令牌时 JSON 的 tokens 为数组。

### `lgmon token revoke <令牌ID> [--json]`

以管理员身份吊销令牌，立即停止它的认证能力。令牌管理没有 HTTP 接口。

### `lgmon query [SQL] [--schema] [--json] [--range/--from/--to/--collector]`

SQL 查询支持 --range 6h、--from/--to UTC 毫秒和 --collector 采集器 ID。range 与 from 互斥，from 早于 to；省略时间时为当前服务端时间往前 1h，省略 collector 绑定 NULL。参数表及 NULL 查询写法见 [dashboards.md](dashboards.md)。JSON 输出包含实际 params。

使用独立只读连接查询 `<home>/data/lgmon.db`。SQL 只接受一条 SELECT 或 WITH，执行预算 5 秒，最多返回 10000 行，超出时保留前 10000 行并置 truncated=true。`--schema` 不接收 SQL，返回实际表列和迁移注释里的说明。服务正在运行或已停止时均可查询；数据库不存在时提示先启动监控器。

```powershell
.\bin\lgmon.exe query 'SELECT id, hostname, last_seen FROM collectors' --home .\dev-home --json
.\bin\lgmon.exe query --schema --home .\dev-home --json
```

查询 JSON 为 columns、rows、truncated；NULL 为 null，BLOB 为 Base64 字符串。`:now` 可读取注入时钟的 UTC Unix 毫秒。查询错误返回结构化 error，退出码为 1；文本模式显示转义后的单元格和截断提示。认证令牌、配对与密钥数据库不属于这个连接。

### `lgmon mcp [--url http://127.0.0.1:7444]`

以 stdio 提供 MCP 服务，通过监控器的 HTTP API 访问数据，令牌只从 `LGMON_TOKEN` 读取。`--url` 指定监控器 HTTP/HTTPS 基础地址，不接受路径、查询参数或凭据。不会直接打开本机数据库；`--home` 不改变连接地址。

stdout 专用于 MCP JSON-RPC，日志和启动错误写到 stderr，不支持额外的 `--json` 模式。令牌权限与 HTTP 一致，修改配置需要 config，写 settings 需要 admin。缺少令牌时启动失败；断开 stdio 或取消进程时结束服务。工具和 Streamable HTTP 方式见 [mcp.md](mcp.md)。

### `lgmon operations`

| 命令 | 用途 |
|---|---|
| `lgmon operations status` | 读取本机授权配置 |
| `lgmon operations allow <授权ID> --service <精确SCM名>` | 本机管理员登记一项授权 |
| `lgmon operations revoke <授权ID>` | 本机管理员撤销一项授权 |
| `lgmon operations enable` | 本机管理员启用已登记授权 |
| `lgmon operations disable` | 本机管理员关闭全部服务操作 |
| `lgmon operations list --collector <ID>` | 在线读取授权及实际执行环境 |
| `lgmon operations run <授权ID>` | admin 逐次确认重启，必需参数见下文 |
| `lgmon operations result <请求ID>` | 只读查询原请求，不重新执行 |

本机命令支持 --home/--json，不安装或控制服务。默认关闭，逐条授权；在线环境还要求 LocalSystem 和安全路径检查。

远程 list --collector <ID>、result <原请求ID> 需要 read，run <授权ID> --collector <ID> --request-id <32位小写hex> --version <list返回版本> --reason <原因> --confirm 需要 admin。--url 默认 http://127.0.0.1:7444，令牌仅从 LGMON_TOKEN 读取。run 会中断服务，未知结果保留原 ID 只读查询，不自动换 ID 重试。完整示例与人工验收边界见 [服务操作](service-operations.md)。

### `lgmon setup`

| 命令 | 行为 |
|---|---|
| `lgmon setup check` | 管理员只读检查安装；可用 --service-absent、--panel-url、--require-paired |
| `lgmon setup install` | --confirm 后初始化、配对、注册并启动本产品服务 |
| `lgmon setup remove` | --confirm 后验证程序/home 绑定并移除服务，保留 home |
| `lgmon setup inbox` | --directory 创建新的受保护输入目录及空 pair.json |

均支持 --json；check/install 接受 --role both（默认）或 collector/monitor，--listen 默认 127.0.0.1:7443，collector 使用 --monitor 且不监听。首次 collector 通过 --pair-input <受保护文件> 或 --pair-stdin 提供 code/fingerprint，不能在参数里传配对码。已有身份只在同一地址复用。--home 仍指定数据目录；程序固定在 Program Files/lg-monitor。已有 install/uninstall/service 命令不变；完整安装包、错误恢复和 Pending 人工步骤见 [安装器](installer.md)。

### `lgmon import service-switch --collector <ID 或主机名> [--apply] [--json --yes]`

通过监控器 HTTP API 向采集器读取本机固定 services.json，令牌仅从 LGMON_TOKEN 读取，不接受文件路径。--url 默认 http://127.0.0.1:7444；主机名必须唯一。无 --apply 只预览，带 --apply 时先展示完整变化并交互确认；JSON 保存必须同时有 --yes，否则请求 API 前失败。

可用 --public-probes=false 关闭公网域名探测，--refs app:blog,tunnel:blog 选择 create/update 项，省略 refs 为全部，显式 --refs= 为一个也不选。--remove-missing 明确删除全部缺失导入目标，默认保留。预览和保存间自动等待两秒，保存实际重读并核对两个指纹；source_changed/config_changed 时重新预览和确认，不自动重试写入。成功 JSON 为 created/updated/removed ID 数组及 targetsVersion。只生成目标，后续添加规则见 [服务开关导入](service-switch.md)。
