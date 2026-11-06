# lgmon 命令行

## 通用约定

程序为 `bin\lgmon.exe`。以下使用 `lgmon` 作为命令名；可在 PowerShell 中换成 `.\bin\lgmon.exe`。

所有命令支持 `--home <目录>`、`--log-level debug|info|warn|error`。home 优先级：命令参数、`LGMON_HOME`、`%ProgramData%\lg-monitor`；开发示例使用 `--home .\dev-home`。配置在 `config\`，数据库、采集器 ID、密钥和向导进度在 `data\`。不要把包含真实凭证或数据库的 home 提交到 Git。

命令成功退出 0，错误或需要处理的步骤退出 1。错误写到 stderr；除配置校验和飞书步骤的结构化结果外，失败时 stdout 可以为空。`--help` 查看命令参数；参数错误会提示查看帮助，运行错误直接给出原因。

JSON 字段使用 camelCase；时间戳为 UTC Unix 毫秒。文本时间使用本地时区。JSON 对象字段顺序不作为接口约定，列表按各命令约定排序。没有数据的状态/告警列表为 `[]`。日志和回退提示写到 stderr，不混入 JSON stdout。

## 版本与配置

### `lgmon version`

显示版本、提交号和构建时间，没有 JSON 模式。

### `lgmon config validate [--json]`

校验 home/config 中的 targets.yaml、rules.yaml、notifiers.yaml、card-templates/*.yaml。缺少文件视为空；未知字段、非法值和无效引用会一次列出。无效配置退出 1。

```json
{"ok":true,"errors":[]}
```

错误项字段为 `file`（相对配置目录的文件）、`line`（1 起始行号）、`message`（中文说明）。文本模式显示错误总数及每处文件/行号。

### `lgmon config schema [--output schemas]`

从 Go 配置结构生成四份 JSON Schema，逐行输出生成路径。该命令不使用 home/config 保存 Schema，不支持 `--json`。仓库示例见 examples/，编辑器可用文件头的 `$schema` 提示。

## 密钥

### `lgmon secret set <名称> [--json]`

交互终端输入不回显；通过管道输入时读取至 EOF，限制 64KiB，移除末尾一组换行。Secret 以机器级 DPAPI 加密，文件 ACL 限制为 SYSTEM、Administrators 和创建者。成功 JSON 为 `{"ok":true}`。不提供通过参数传递明文 Secret 或读取明文的命令。

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

### `lgmon feishu test --notifier <id> [--template <id>] [--json]`

向指定已确认渠道发送一张标明“这是测试消息”的卡片，模板默认使用渠道配置。实际网络尝试经 Sender 重试及 SQLite 审计；永久配置错误直接提示处理，网络/限流按 2、5、15 秒最多再尝试三次。成功返回飞书 messageId；接口成功仍需核对群内收件。

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

M0 支持 both，一个进程运行采集、存储、规则评估和通知；独立 collector/monitor 角色由 M1 提供。程序不注册 Windows 服务、开机启动或防火墙规则。未配置飞书渠道时打印运行 feishu setup 的提示。

配置启动时严格校验，并监听有效修改；加载失败保留上一份配置。默认每 2 秒采集、10 秒全量状态、1 秒评估规则。数据库为 home/data/lgmon.db，WAL 模式；原始采样保留 24 小时，告警和发送审计不作为采样清理。采集器身份保存在 data/collector-id。

Ctrl+C 先停止采集和探测，再排空已接受的消息及待发送通知，刷新并关闭数据库；网络重试期间退出会等待发送完成。单轮采集失败会跳过本轮并在下轮重试；原始写批失败保留数据，按 0.5 秒起、最长 30 秒退避重试；规则保存或通知入队失败保留原告警 ID 和待办，下次评估重试。通知队列有 256 个槽，满时立即报错供规则重试，不阻塞其他规则评估。

启动先恢复触发状态，再将最近 24 小时未成功送达的触发/恢复转换按事件顺序补入发送队列，之后处理新采集消息。查询每页 256 条，发送串行；同一告警的触发先于恢复。成功记录按状态及通知渠道区分，已成功的群跳过；closed 事件不补发。通知审计增加 state 字段，不把同毫秒的触发发送成功误判成恢复成功。旧版本没有 state 的审计不能确认具体状态，补发采取保守策略；飞书 UUID 的服务器去重窗口只有 1 小时，补发不承诺永久 exactly-once。

通知网络失败完成既定重试并保留审计，继续监控。卡片被服务端明确拒绝（230099）时，可由非 standard/compact 模板降级为标准模板，保留告警 ID/状态/群的幂等键与发送审计。原始待提交缓冲默认 10000 条、另有 2048 个有界请求槽；溢出只丢最旧原始采样并记录数量。关闭时最后提交仍失败会返回错误。

### `lgmon status [--json]`

使用 mode=ro、query_only 的独立 SQLite 连接，程序停止时仍可读取已保存状态；不进行迁移或原始数据清理。输出每个主机/目标的最近状态和触发中告警。保存的 up/down 是最近一次采集值，不代表当前采集进程正在运行；采集器在线检测按 M1 心跳实现。

```json
{"available":false,"targets":[],"alerts":[]}
```

无数据库时 available=false，退出 0，不创建目录；有数据库时为 true。targets 按 collectorId、targetId 排序，字段为 collectorId、targetId、ts、state（up/down/unknown）、detail。detail 为 TargetStatus 对象：targetId、state、process（found/missing/unknown 或空）、port（listening/closed/unknown 或空）、reason、full、processes、ports。processes 为精简进程引用，含 pid、parentPid、name、exe、createdAt、cpuPercent、rss、threads，不含命令行。

alerts 按 startedAt 倒序、id 排序，字段见下一节。status 同时查询两个列表，显示数据库里已经提交的记录，不等待另一个进程的待提交批次。

### `lgmon alerts [--json] [--all]`

默认只读 firing；--all 包含 resolved。输出形状为 `{"available":true,"alerts":[]}`，缺少数据库时 available=false。每个告警字段：

| 字段 | 含义 |
| --- | --- |
| id / ruleId / collectorId / targetId | 告警、规则、采集器、目标标识 |
| state / severity / summary | firing 或 resolved、严重度、摘要 |
| startedAt | 首次持续满足条件的 UTC 毫秒 |
| resolvedAt | 恢复毫秒；尚未恢复为 0 |
| lastNotifiedAt | 最近一次成功发送时间；尚未成功为 0 |

告警持久化和发送成功是两个阶段。lastNotifiedAt=0 表示未记录成功发送；非零也只表示曾有一次成功，不能据此认定当前状态已发到所有群。规则删除或修改类型、目标时，旧触发告警以 closed 事件关闭，状态存为 resolved，且不发送恢复卡片。

### `lgmon targets test <id> [--json]`

从 home/config 加载配置，只读采集一次进程和 TCP 监听，不启动探测 worker、不写数据库、不操作已有进程。显示匹配状态、原因、进程和端口；不匹配本身仍退出 0，读取配置或采集失败退出 1。

JSON 字段为 targetId、state、reason、processes、ports。进程字段：pid、parentPid、name、cmdline、exe、createdAt、cpuPercent、rss、threads，可选 cmdlineUnavailable；端口字段：localAddress、port、pid。cpuPercent 是整机容量归一化百分比，rss 是字节。
