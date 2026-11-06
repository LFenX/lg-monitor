# lg-monitor 执行计划

需求见 [PRD.md](PRD.md)。本文是给执行者（Codex 等编程 Agent）的逐步计划。

## 怎么用这份计划

- 按编号顺序做。每个任务是一个独立提交，提交信息以任务编号开头，例如 `M0-03: process collector`。
- 每个任务都写了“完成标准”。全部满足、`go vet ./...` 和 `go test ./...` 都通过后才算完成，然后在本文对应任务前把 `[ ]` 改成 `[x]`，和代码放在同一个提交里。
- 遇到计划没写清、或者照做明显不对的地方：在本文末尾的“执行记录”里写下问题和你的处理方式，然后继续。不要悄悄偏离。
- 不要提前做后面阶段的功能。接口可以预留，功能不要写。

## 全局约定

| 项目 | 约定 |
|---|---|
| Go 版本 | 本机是 Go 1.27.3，`go.mod` 写 `go 1.27` |
| 模块名 | `github.com/LFenX/lg-monitor` |
| 程序 | 只有一个可执行文件 `lgmon.exe`，入口在 `cmd/lgmon` |
| 命令行框架 | `github.com/spf13/cobra` |
| 日志 | 标准库 `log/slog`，JSON 格式写文件，开发时文本格式输出到控制台 |
| SQLite | `modernc.org/sqlite`（纯 Go）。禁止引入任何需要 cgo 的依赖，`CGO_ENABLED=0` 必须能编译 |
| 飞书 | 官方 Go SDK `github.com/larksuite/oapi-sdk-go/v3`，用应用自己的 App ID 和 App Secret 调用开放平台接口。**程序运行时不依赖 lark-cli** |
| 配置文件 | YAML，用 `gopkg.in/yaml.v3` 解析（报错要能带行号） |
| 时间 | 一律存 UTC 的 Unix 毫秒（`int64`），只在显示时转本地时间 |
| 错误信息 | 面向用户的报错用中文，写明哪里错了、怎么改；日志和代码注释用英文 |
| 测试 | 依赖系统的部分（进程、端口、飞书接口、时间）都要定义成接口，测试里用假实现。真实系统测试加构建标签 `//go:build integration`，默认不跑 |
| 安全 | 测试和验收只能操作自己启动的演示进程。**不准关闭、重启或修改这台电脑上已经在跑的真实服务**（LFEN、lgplayblog、Nebula、PostgreSQL、cloudflared 等） |

### 数据目录

| 用途 | 正式运行（Windows 服务） | 开发时 |
|---|---|---|
| 配置 | `%ProgramData%\lg-monitor\config\` | `--home .\dev-home` 下的 `config\` |
| 数据库、缓存、证书、密钥 | `%ProgramData%\lg-monitor\data\` | `dev-home\data\` |
| 日志 | `%ProgramData%\lg-monitor\logs\` | `dev-home\logs\` |

所有子命令都支持 `--home <目录>`，也可以用环境变量 `LGMON_HOME`。`dev-home/` 已写进 `.gitignore`。

### 目录结构

```
cmd/lgmon/                main.go，子命令注册
internal/app/             把各模块组装起来（run 命令）
internal/config/          配置结构体、加载、校验、热加载、生成 JSON Schema
internal/secrets/         密钥存储（DPAPI 加密）
internal/collector/       采集器：进程、端口、探测、系统指标、服务、脚本
internal/collector/match/ 监控目标匹配
internal/pipeline/        采集器与监控器之间的接口（M0 用内存实现，M1 换成 gRPC）
internal/transport/       M1：gRPC、配对、证书、离线缓存
internal/store/           SQLite：建表、迁移、写入、查询、清理
internal/rules/           规则判断和告警状态机
internal/feishu/          飞书开放平台客户端、错误识别、开通向导的逻辑
internal/notify/          通知调度与各渠道
internal/notify/cards/    卡片模板引擎与预设模板
internal/api/             M1：HTTP API、SSE、只读 SQL、MCP
internal/winsvc/          M1：Windows 服务安装与运行
proto/                    M1：protobuf 定义
web/                      M1：React 面板
schemas/                  生成的 JSON Schema（提交进仓库）
examples/                 示例配置
scripts/                  build.ps1、验收脚本
```

### 已核实的飞书事实

2026-11-06 用 lark-cli 1.0.99，以机器人身份在真实租户上核实过下面这些事实，可以直接依赖。和实际返回不一致时，以飞书的实际返回为准，并在执行记录里更新本节。

| 事实 | 内容 |
|---|---|
| 获取令牌 | `POST /open-apis/auth/v3/tenant_access_token/internal`，用 App ID 和 App Secret 换取 |
| 机器人信息 | `GET /open-apis/bot/v3/info`，返回 `app_name`、`avatar_url`、`open_id`、`activate_status`（实测已启用机器人的应用为 `2`） |
| 查询应用权限 | `GET /open-apis/application/v6/scopes`（机器人身份），返回 `data.scopes[]`，每项含 `scope_name`、`scope_type`（`tenant` 或 `user`）、`grant_status`（实测 `1` 表示已授权）。只列出应用已经申请过的权限，没申请过的不出现 |
| 缺权限的报错 | 错误码 `99991672`，报错里带缺少的权限名（可能不止一个） |
| 一键开通链接 | `https://open.feishu.cn/page/scope-apply?clientID=<AppID>&scopes=<权限名用英文逗号连接后整体做 URL 编码>`。实例：`.../page/scope-apply?clientID=cli_xxx&scopes=hire%3Ajob%3Areadonly%2Chire%3Apeople_cli` |
| 机器人所在的群 | `GET /open-apis/im/v1/chats`（机器人身份），返回 `data.items[]`，含 `chat_id`（`oc_` 开头），分页参数 `page_size`、`page_token`。在只开通了 `im:chat:read`、没有 `im:chat:readonly` 的应用上调用成功 |
| 发消息 | `POST /open-apis/im/v1/messages?receive_id_type=chat_id`，卡片用 `msg_type: interactive`，`content` 是卡片 JSON 序列化后的字符串 |
| 卡片格式 | 卡片 JSON 2.0（`"schema": "2.0"`） |
| 代理 | 这台电脑的用户环境变量里有 `HTTPS_PROXY=http://127.0.0.1:7890`；以 Windows 服务运行时没有这个变量 |

本机的 lark-cli（`lark-cli api GET <路径> --as bot`）已经配好一个测试应用，可以用它查看接口的真实返回，写成测试用的样例数据。**只能调用只读接口**；发消息只能发到用户确认过的测试群。

---

## M0 单机原型

**目标**：一台电脑上运行 `lgmon run --role both`。用户用 `lgmon feishu setup` 填入飞书应用的 App ID 和 App Secret，按引导一键开通权限、把机器人拉进群；之后关掉一个被监控的进程，15 秒内群里收到告警卡片，进程恢复后收到恢复卡片。

整个 M0 里，采集器和监控器在同一个进程内，通过 `internal/pipeline` 定义的接口用 Go channel 通信。这个接口要设计成 M1 换成 gRPC 时，采集器和监控器两边的代码都不用改。

### [x] M0-01 项目骨架

- `go mod init github.com/LFenX/lg-monitor`，建立上面的目录结构（空目录放 `doc.go`，写一行包说明）。
- `cmd/lgmon`：cobra 根命令，带 `--home`、`--log-level`；子命令 `version`，输出版本号、提交号和构建时间（通过 `-ldflags` 注入）。
- `scripts/build.ps1`：`CGO_ENABLED=0` 编译出 `bin\lgmon.exe`，注入版本信息。
- `.github/workflows/ci.yml`：在 `windows-latest` 上运行 `go vet`、`go test ./...`，并执行 build 脚本。

**完成标准**：`scripts\build.ps1` 生成可运行的 `bin\lgmon.exe`，`lgmon version` 能输出版本信息；CI 通过。

### [ ] M0-02 配置

配置目录下的文件，每个都可以不存在（视为空）：

`targets.yaml`：监控目标

```yaml
targets:
  - id: nebula                 # 小写字母、数字、横线
    name: Nebula 网站
    match:                     # 以下条件全部满足才算匹配；至少写一个
      process: node.exe        # 进程名，不区分大小写
      cmdline: 'D:\\appmy\\GRM' # 正则，匹配命令行
      port: 3001               # 该进程在监听这个端口
    probes:
      - type: http
        url: http://127.0.0.1:3001/
        interval: 15s
        timeout: 5s
        expectStatus: "<500"   # 也可以写 200、"2xx"
    tags: [web]
```

`match` 还支持 `service`（Windows 服务名），但 M0 只需要解析和校验，不用实现。

`rules.yaml`：规则

```yaml
rules:
  - id: nebula-down
    name: Nebula 进程不在
    type: process_absent       # M0 支持：process_absent、port_closed、probe_failed
    target: nebula
    for: 10s                   # 持续多久才触发
    severity: critical         # critical | warning | info
    notify: [feishu-alerts]
    template: detailed         # 可选；不写就用通知渠道的默认模板
```

`notifiers.yaml`：飞书应用和通知渠道

```yaml
feishuApps:
  - id: default
    appId: cli_xxxxxxxx
    secretRef: feishu-default   # App Secret 不写在这里，存在加密的密钥库里（见 M0-09）
    domain: feishu              # feishu | lark
    proxy: auto                 # auto（读取环境变量）| none | http://...

notifiers:
  - id: feishu-alerts
    type: feishu_app            # M0 只做这一种
    app: default
    chatId: oc_xxxxxxxx
    chatName: 服务告警           # 只用于显示，由向导自动填写
    template: standard          # 默认卡片模板
```

`card-templates/*.yaml`：自定义卡片模板，格式见 M0-10。

要求：

- 配置结构体定义在 `internal/config`，校验所有引用关系（规则引用的目标、通知渠道、模板必须存在；通知渠道引用的飞书应用必须存在；ID 不能重复；`for` 必须是合法时长）。
- 报错格式：`rules.yaml 第 7 行：规则 nebula-down 引用的目标 nebla 不存在`。
- `lgmon config validate`：校验配置并以中文列出所有错误，有错时退出码为 1。
- `lgmon config schema`：用 `github.com/invopop/jsonschema` 从结构体生成 `schemas/*.schema.json`，提交进仓库。`examples/` 里的示例文件开头写上 `# yaml-language-server: $schema=...`。
- 用 `github.com/fsnotify/fsnotify` 监听配置目录。文件变化后重新加载，校验失败就保留旧配置并记录错误，不能让程序崩溃。

**完成标准**：`examples/` 里的示例文件都能通过校验；每类错误各写一个会失败的测试用例；热加载测试通过（写入错误配置后旧配置仍然有效）。

### [ ] M0-03 进程与端口采集

- 定义接口 `ProcessSource`：返回进程快照（PID、父 PID、名称、命令行、可执行文件路径、创建时间、CPU%、内存 RSS、线程数）。真实实现用 `github.com/shirou/gopsutil/v4/process`。
- 定义接口 `PortSource`：返回 TCP 监听列表（本地地址、端口、PID）。真实实现用 gopsutil 的 `net.Connections("tcp")`，只保留 `LISTEN` 状态。
- 进程的身份由（PID，创建时间）共同确定，防止 PID 被复用后认错。
- 拿不到命令行（权限不足）时字段留空，不能报错退出。
- 每次采集要计时，超过 1 秒记一条警告日志。

**完成标准**：用假实现写单元测试；加一个 `integration` 标签的测试，自己启动 `ping -t 127.0.0.1` 子进程，确认快照里能找到它，测试结束时关掉它。

### [ ] M0-04 监控目标匹配

- `internal/collector/match`：输入进程快照和端口列表，输出每个监控目标的状态：`up`（找到匹配进程）或 `down`，以及匹配到的进程和端口。
- 匹配规则见 M0-02。一个目标匹配到多个进程时都记录下来，只要有一个就算 `up`。
- 匹配结果要附带原因，方便排查，例如“进程名匹配，但没有在监听 3001”。

**完成标准**：表驱动测试覆盖：只按名称匹配、名称加命令行、名称加端口、端口被别的进程占用、多个进程匹配、没有任何匹配。

### [ ] M0-05 HTTP 与 TCP 探测

- 每个探测按各自的间隔独立运行，同一时间只允许一个实例在跑。
- HTTP 探测：不走系统代理，不自动跳转，记录状态码、耗时和错误；按 `expectStatus` 判断成功或失败。
- TCP 探测：`host:port` 能否在超时内连上。
- 结果写入探测结果流，包含目标 ID、探测编号、成功与否、耗时、错误信息。

**完成标准**：用 `net/http/httptest` 测试各种状态码、超时和连接被拒；TCP 用一个本地监听测试。

### [ ] M0-06 采集器组装与 pipeline 接口

- `internal/pipeline` 定义采集器发出的消息：`TargetStatus`（目标状态变化，以及每 10 秒一次的全量快照）、`ProbeResult`、`ProcessSample`（被匹配进程的 CPU 和内存），每条消息带采集器 ID、递增序号和时间戳。
- 定义接口 `Sink`（采集器往里写）和 `Source`（监控器从里读），M0 用带缓冲的 channel 实现。
- 采集器主循环：每 2 秒采集一次进程和端口，计算目标状态，只在状态变化时立即发送；探测按自己的间隔运行。
- 采集器 ID：第一次运行时生成 UUID，存在 `data\collector-id`。

**完成标准**：测试里用假的进程源模拟“进程消失 → 恢复”，确认 `Sink` 按顺序收到对应的状态变化。

### [ ] M0-07 存储

表结构（用 `embed` 内嵌迁移 SQL，按版本号顺序执行，启动时自动迁移）：

```sql
CREATE TABLE collectors (id TEXT PRIMARY KEY, hostname TEXT, first_seen INTEGER, last_seen INTEGER, version TEXT);
CREATE TABLE target_status (collector_id TEXT, target_id TEXT, ts INTEGER, state TEXT, detail TEXT, PRIMARY KEY (collector_id, target_id, ts));
CREATE TABLE probe_results (collector_id TEXT, target_id TEXT, probe TEXT, ts INTEGER, ok INTEGER, latency_ms INTEGER, error TEXT);
CREATE TABLE process_samples (collector_id TEXT, target_id TEXT, ts INTEGER, pid INTEGER, cpu REAL, rss INTEGER);
CREATE TABLE alerts (id TEXT PRIMARY KEY, rule_id TEXT, collector_id TEXT, target_id TEXT, state TEXT, severity TEXT,
                     started_at INTEGER, resolved_at INTEGER, last_notified_at INTEGER, summary TEXT);
CREATE TABLE alert_events (alert_id TEXT, ts INTEGER, kind TEXT, message TEXT);
CREATE TABLE notifications (id TEXT PRIMARY KEY, alert_id TEXT, notifier_id TEXT, template TEXT, ts INTEGER,
                            ok INTEGER, error TEXT, message_id TEXT);
```

给需要的列加索引（按时间范围查询的表都要有 `(collector_id, target_id, ts)` 索引）。

- 打开数据库时设置 `journal_mode=WAL`、`busy_timeout=5000`、`foreign_keys=ON`。
- 只用一个协程负责写入，批量提交（每 500 毫秒或攒满 500 条提交一次）。
- 清理：原始数据保留 24 小时，每小时清理一次。M0 不做汇总表。

**完成标准**：迁移可以重复执行；写入 1 万条数据的测试在 2 秒内完成；清理测试通过。

### [ ] M0-08 规则引擎与告警状态机

- 状态：`ok` → `pending`（条件满足但还没到 `for` 的时长）→ `firing` → `resolved`。在 `pending` 期间条件不再满足就回到 `ok`，不发通知。
- 规则只看最新的目标状态和探测结果；每 1 秒评估一次，收到状态变化时也立即评估。
- 同一条规则、同一个采集器、同一个目标只能有一个处于触发状态的告警。
- 触发时生成告警 ID，写入 `alerts` 和 `alert_events`，通知一次；恢复时通知一次。M0 不做重复提醒、静音和确认。
- 程序重启后，从数据库恢复仍处于 `firing` 状态的告警，不能重复发送“触发”通知。

**完成标准**：用可控时钟（注入 `now func() time.Time`）的测试覆盖：抖动（`pending` 时恢复）、正常触发再恢复、重启后恢复状态、`for: 0s` 立即触发。

### [ ] M0-09 密钥存储与飞书客户端

**密钥库**（`internal/secrets`）

- 存放在 `data\secrets.json`，每个值用 Windows DPAPI 加密（`golang.org/x/sys/windows` 的 `CryptProtectData`），使用机器级别（`CRYPTPROTECT_LOCAL_MACHINE`）。原因：M1 起监控器以 SYSTEM 身份作为服务运行，用户级加密的值服务读不出来。为弥补机器级加密的范围，`secrets.json` 的访问权限设为只允许 SYSTEM、Administrators 和创建它的用户。
- `lgmon secret set <名称>`：从标准输入读取，在终端里输入时不回显（`golang.org/x/term`）。`lgmon secret list` 只列出名称。不提供把明文打印出来的命令。
- 日志、报错、`--json` 输出里都不能出现密钥明文。写一个测试：把一个密钥值走一遍会写日志的流程，检查日志里不含它。

**飞书客户端**（`internal/feishu`）

- 用官方 SDK 处理令牌获取和缓存。SDK 没有直接封装的接口（权限查询、机器人信息），用 SDK 的通用请求方法调用。
- 域名：`feishu` 用 `https://open.feishu.cn`，`lark` 用 `https://open.larksuite.com`。
- 代理：`proxy: auto` 时读取 `HTTPS_PROXY`，`none` 时直连，也可以写具体地址。
- 提供这些方法：`TenantToken`、`BotInfo`、`AppScopes`、`ListChats`（自动翻页）、`SendCard`、`CreateChat`（M0-11 的可选功能用）。
- **错误识别**：把飞书的错误转换成有类型的错误，向导和日志都靠它给出人能看懂的提示：

  | 类型 | 识别依据 | 给用户的提示 |
  |---|---|---|
  | 凭证错误 | 获取令牌失败（App ID 不存在、Secret 错误） | App ID 或 App Secret 不对，去开发者后台“凭证与基础信息”页复制 |
  | 缺少权限 | 错误码 `99991672` | 列出缺少的权限，附上一键开通链接 |
  | 机器人未启用 | `bot/v3/info` 失败，或 `activate_status` 不是已启用 | 去开发者后台“应用能力”里添加“机器人” |
  | 机器人不在群里 | 发消息时报“不在群里” | 把机器人重新拉进群 |
  | 频率限制 | 限流错误码 | 自动退避重试，不提示用户 |
  | 网络问题 | 连接失败、超时、证书错误 | 检查网络和代理设置 |

  具体错误码以真实返回为准：用 lark-cli 的只读接口、用故意写错的 Secret 获取令牌等方式拿到真实响应，去掉应用 ID 和 log_id 后存成 `internal/feishu/testdata/*.json`，测试用这些样例数据。“机器人不在群里”的样例需要真的发一次消息才能拿到；拿不到就按飞书文档写，并在执行记录里注明“未经实测”。
- **一键开通链接**：`ApplyURL(appID string, scopes []string) string`，格式见“已核实的飞书事实”。用文中的实例写测试，生成结果必须逐字一致。
- 发消息的幂等：请求体里带 `uuid`，取 `告警ID + 状态` 的哈希（不超过 50 个字符），重试时不会重复发出。
- 失败重试 3 次（间隔 2、5、15 秒），超时 20 秒。每次结果写入 `notifications` 表，成功时记录飞书返回的消息 ID。

**完成标准**：用 `httptest` 模拟飞书服务器，测试令牌缓存与过期刷新、每种错误类型的识别、翻页、幂等字段、重试；DPAPI 加密后解密一致；密钥不出现在日志里。

### [ ] M0-10 卡片模板

**预设模板**：四套，每套都包含“触发”和“恢复”两种状态的样子。用卡片 JSON 2.0 实现。

| 编号 | 名称 | 适合 | 内容 |
|---|---|---|---|
| `standard` | 标准 | 大多数情况，默认 | 标题栏按严重程度着色（严重红色、警告橙色、提示蓝色、恢复绿色）；字段两列排列：主机、目标、开始时间、持续时长、当前值；一段详情；按钮“打开面板”（配置了面板地址时才显示） |
| `compact` | 简洁 | 告警多、不想刷屏的群 | 没有标题栏，一行带图标的文字加一行时间 |
| `detailed` | 排查 | 需要马上动手处理的严重告警 | 在“标准”的基础上增加：相关进程（PID、CPU、内存）、最近日志末尾（折叠面板，默认收起）、可能的原因和建议、一段“复制给 Agent 排查”的诊断文本 |
| `digest` | 汇总 | 抖动合并、短时间内大量告警 | 一张卡片列出多条告警：每条一行，包含状态、目标、主机和时间；最多 20 条，超出时显示“还有 N 条” |

`compact` 和 `standard` 的恢复卡片要写明“持续了多久”。

**自定义模板**：放在配置目录的 `card-templates/<id>.yaml`，热加载。有两种写法：

1. **在预设基础上改**，适合大多数人：

   ```yaml
   id: oncall
   name: 值班群
   extends: standard          # 基于哪个预设
   title: "{{.SeverityIcon}} {{.Rule.Name}}"
   colors: { critical: carmine, warning: orange, info: blue, resolved: green }
   fields: [host, target, startedAt, duration]   # 从内置字段里选，可以调整顺序
   extraFields:
     - label: 负责人
       value: 张三
   mention:
     critical: all            # 严重告警 @所有人；也可以写 open_id 列表
   buttons:
     - text: 值班手册
       url: https://example.com/runbook
   note: "处理完请在群里回复"
   ```

2. **完全自定义**：`raw` 字段直接写整张卡片的 JSON 2.0，值的位置写模板变量。用户可以先在飞书的“卡片搭建工具”里拖拽设计好卡片，导出 JSON，再把具体值换成变量。可以分别为 `firing`、`resolved` 写两份，不写 `resolved` 时恢复卡片沿用同一份。

四套预设本身也用第 1 种写法实现（放在 `internal/notify/cards/presets/`，用 `embed` 内嵌），这样“在预设基础上改”和预设走的是同一条代码路径。

**模板变量**（全部写进 `docs/card-templates.md`，附例子）：

| 变量 | 含义 |
|---|---|
| `.Alert.ID` `.Alert.State` `.Alert.Severity` `.Alert.Summary` `.Alert.Detail` | 告警本身 |
| `.Alert.StartedAt` `.Alert.ResolvedAt` `.Alert.Duration` | 时间，已格式化成本地时间和中文时长 |
| `.Rule.ID` `.Rule.Name` | 规则 |
| `.Target.ID` `.Target.Name` `.Target.Tags` | 监控目标 |
| `.Host.Name` `.Host.CollectorID` | 主机 |
| `.Value` | 触发时的数值（有的话） |
| `.SeverityIcon` `.SeverityText` | 🔴 严重 / 🟠 警告 / 🔵 提示 / ✅ 已恢复 |
| `.DashboardURL` | 面板里这条告警的地址，没配置时为空 |
| `.Processes` `.LogTail` `.Diagnosis` | 排查信息（`detailed` 用） |
| `.Alerts` | 告警列表（`digest` 用） |

**渲染要求**：

- 使用 `text/template`，开启 `missingkey=error`；提供 `duration`、`time`、`truncate`、`default` 等辅助函数。
- 所有变量的值在放进卡片前都要转义：JSON 转义，以及飞书 Markdown 里有特殊含义的字符。特别是要把 `<` 转义，防止进程名或日志里的 `<at id=all>` 变成真的 @所有人。只有 `mention` 配置能产生 @。
- 渲染结果必须是合法 JSON，并且不超过 28 KB（飞书单张卡片的上限约 30 KB，留出余量）。超出时先截断日志末尾和告警列表，还超出就退回 `compact`。
- 自定义模板渲染失败时，改用 `standard` 发出，并在卡片底部加一行“自定义模板 xxx 出错，已使用标准模板”，同时记录错误日志。**告警绝不能因为模板错误而发不出去。**

**命令**：

- `lgmon template list [--json]`：列出预设模板和自定义模板。
- `lgmon template validate [<id>]`：用内置的样例告警（触发、恢复、汇总三种）渲染，检查合法性和大小。
- `lgmon template preview <id> --state firing|resolved|digest`：输出渲染后的卡片 JSON，可以直接贴进飞书卡片搭建工具查看效果。
- `lgmon template new <id> --from standard`：生成一份带注释的自定义模板文件，用户在此基础上修改。

**完成标准**：四套预设 × 三种状态都有黄金文件测试（`testdata/*.golden.json`）；转义测试覆盖引号、换行、Markdown 符号和 `<at id=all>`；超长测试验证截断和退回；模板出错时能退回标准模板。

### [ ] M0-11 飞书开通向导（命令行版）

把“填凭证 → 启用机器人 → 开通权限 → 拉进群 → 选模板 → 发测试卡片”做成一条龙。向导的逻辑写在 `internal/feishu/setup`，只依赖 M0-09 的客户端接口，不包含任何界面代码；M1 的网页向导、HTTP API 和 MCP 都复用这里。

**所需权限**（写在代码里，每项都带中文说明，向导原样显示给用户）：

| 用途 | 权限 | 是否必需 | 给用户看的说明 |
|---|---|---|---|
| 发送告警 | `im:message:send_as_bot` | 必需 | 以机器人身份往群里发告警卡片 |
| 自动获取群 ID | `im:chat:read` | 必需 | 读取机器人所在的群，这样不用你手动查群 ID |
| 一键创建告警群 | `im:chat:create` | 可选 | 让机器人帮你新建一个告警群 |

权限名以“已核实的飞书事实”为准。如果真实调用时 `99991672` 报出的缺少权限和上表不同，以报错为准，更新上表并写进执行记录。

**向导步骤**：

1. **填写凭证**：输入 App ID（校验以 `cli_` 开头）和 App Secret（不回显），选择飞书或 Lark。获取令牌验证凭证，成功后把 Secret 存进密钥库。
   如果用户还没有应用，打印创建步骤：打开 `https://open.feishu.cn/app` → 创建企业自建应用 → 填写名称和描述 → 进入应用后在“凭证与基础信息”里复制 App ID 和 App Secret。
2. **检查机器人能力**：调用机器人信息接口。没有启用就说明怎么开（开发者后台 → 应用能力 → 添加应用能力 → 机器人），然后每 5 秒检查一次，启用后自动进入下一步。成功时显示机器人的名称，后面会用到。
3. **检查权限**：调用应用权限接口，对比所需权限，逐项显示“已开通 / 未开通”。有未开通的：
   - 生成一键开通链接，把未开通的权限全部放进去，自动用浏览器打开（`rundll32 url.dll,FileProtocolHandler <链接>`，不经过 `cmd`）；
   - 每 5 秒重新检查，全部开通后自动进入下一步；
   - 60 秒后还没生效，提示：自建应用新开通的权限通常要发布新版本才生效（开发者后台 → 版本管理与发布 → 创建版本 → 申请发布）；如果企业开启了审核，还要等管理员审批。
   - 权限查询接口本身也可能报缺少权限。这时用报错里的权限名生成开通链接，同样处理。
   - 发布流程的具体界面需要用户用真实应用走一遍来确认，把实际步骤更新进提示文字和执行记录。
4. **选择告警群**：列出机器人所在的所有群（群名和人数）。
   - 已经有群：让用户选一个，把群 ID 和群名写进 `notifiers.yaml`。**用户不需要自己查群 ID。**
   - 一个群都没有：显示拉机器人进群的步骤——在飞书里打开要接收告警的群 → 右上角“设置” → “群机器人” → “添加机器人” → 搜索「<第 2 步拿到的机器人名称>」→ 添加。然后每 3 秒检查一次，发现新群就显示“检测到机器人已加入「群名」，用这个群接收告警吗？”。
   - 搜不到机器人时，给出常见原因：应用还没有发布；你不在应用的可用范围内（开发者后台 → 版本管理与发布 → 可用范围）；第 2 步的机器人能力没有生效。
   - 也可以选择“帮我新建一个告警群”：需要可选权限 `im:chat:create`（没开通就回到第 3 步的流程），机器人建群后把用户拉进来。用户的 open_id 怎么获取，由执行者调查后选一种最省事的方式（例如让用户先给机器人发一条私聊消息）；调查结果写进执行记录。做不出来就只保留“拉机器人进群”这一条路。
5. **选择模板并测试**：列出四套预设和自定义模板，选一个作为这个群的默认模板；发送一张测试卡片（内容标明“这是测试消息”）。询问“群里收到了吗？”：收到就保存配置并结束；没收到就显示发送结果和对应的错误提示。

**命令**：

- `lgmon feishu setup`：交互式走完以上 5 步。也可以用参数一步完成，供 Agent 和脚本使用：`--app-id`、`--secret-stdin`、`--chat-id`、`--template`、`--yes`。中途退出后再次运行，已经完成的步骤会显示为已完成并跳过。
- `lgmon feishu check [--json]`：只检查，不修改配置。输出凭证、机器人能力、每项权限的状态、一键开通链接、机器人所在的群列表。
- `lgmon feishu chats [--watch] [--json]`：列出机器人所在的群；`--watch` 持续检查，有新群加入时输出。
- `lgmon feishu test --notifier <id> [--template <id>]`：发一张测试卡片。
- `--json` 输出里，每一步都带 `status`（`ok` | `action_required` | `error`）、给人看的 `message`、可以直接打开的 `url`（如果有），这样 Agent 也能带着用户走完流程。

**完成标准**：用假的飞书客户端测试五个步骤的每个分支（凭证错误、机器人未启用、缺少权限后补齐、没有群后加入、测试发送失败）；`ApplyURL` 与实例逐字一致；交互流程测试中途退出后可以续上。真实应用上的完整流程放在 M0-13 由用户确认。

### [ ] M0-12 运行命令与 CLI

- `lgmon run --role both`：加载配置，启动采集器、存储、规则引擎和通知，Ctrl+C 时按顺序优雅退出（先停采集，等写入和通知完成，再关闭数据库）。
- 启动时如果还没有配置任何飞书通知渠道，打印一行提示：运行 `lgmon feishu setup` 完成飞书接入。
- `lgmon status [--json]`：读数据库，列出每个目标的当前状态和正在触发的告警。程序没有运行时也能用。
- `lgmon alerts [--json] [--all]`：列出告警，默认只列出正在触发的。
- `lgmon targets test <id>`：只采集一次，打印这个目标的匹配结果和原因，用来调试配置。
- 所有 `--json` 输出要稳定，字段名用 camelCase，并写进 `docs/cli.md`。

**完成标准**：`docs/cli.md` 写完，覆盖到目前为止的所有命令；每个命令都有至少一个测试（用临时目录作为 `--home`）。

### [ ] M0-13 原型验收

分两部分。

**A. 飞书接入（由用户操作，执行者写好步骤）**：在 `docs/acceptance-m0.md` 里写清楚：

1. 用一个飞书自建应用运行 `lgmon feishu setup --home .\dev-home`（可以是本机 lark-cli 正在用的那个测试应用，也可以新建一个）；
2. 按提示开通权限，记录实际看到的页面和步骤（包括是否需要发布版本），把真实流程更新进 M0-11 的提示文字；
3. 新建一个测试群，把机器人拉进去，确认向导自动发现了这个群；
4. 用四套模板各发一张测试卡片，截图放进 `docs/images/`。

**B. 告警链路（脚本）**：`scripts/m0-acceptance.ps1`，复用 A 中保存好的飞书配置：

1. 在临时的 `--home` 下生成配置：监控目标是脚本自己启动的演示 HTTP 服务（`python -m http.server <空闲端口>`，或一个小的 Go 测试程序），规则 `for: 5s`，模板 `detailed`。
2. 启动 `lgmon run --role both`。
3. 等目标变为 `up` 后关掉演示服务，开始计时。
4. 轮询 `lgmon alerts --json`，确认 15 秒内出现 `firing` 状态的告警，且 `notifications` 记录成功。
5. 重新启动演示服务，确认告警变为 `resolved` 并且恢复通知发送成功。
6. 清理所有自己启动的进程和临时目录。

**完成标准**：脚本打印每一步的耗时和结果；用户确认测试群里收到了触发和恢复两张卡片，并确认 A 部分走通。

---

## M1 第一版

**目标**：两台电脑通过 Tailscale 互联，采集器作为 Windows 服务运行；断网 10 分钟后恢复，数据完整；Agent 只看文档就能加监控目标、规则和面板；有 Web 面板（含飞书开通向导和模板编辑）、HTTP API 和 MCP。

### [ ] M1-01 protobuf 与 gRPC 通道

`proto/lgmon/v1/collector.proto`，用 `buf` 生成代码（`buf.gen.yaml` 提交进仓库，生成的代码也提交）：

```proto
service CollectorLink {
  // The collector opens one long-lived stream; everything flows over it.
  rpc Connect(stream CollectorMessage) returns (stream MonitorMessage);
}
message CollectorMessage {
  oneof body {
    Hello hello = 1;            // first message: collector id, hostname, version, last acked seq
    Batch batch = 2;            // seq-numbered records
    Heartbeat heartbeat = 3;
    ConfigApplied config_applied = 4;
  }
}
message MonitorMessage {
  oneof body {
    Welcome welcome = 1;        // accepted, resume from seq
    Ack ack = 2;                // highest contiguous seq stored
    ConfigPush config = 3;      // targets + probes for this collector, with a version
  }
}
```

`Batch` 里的记录与 M0 的 `pipeline` 消息一一对应。把 pipeline 的 channel 实现换成 gRPC 客户端和服务端实现，采集器和监控器的业务代码不改。

- 监控器默认监听 `:7443`，可配置。
- 开启 gzip 压缩；单条消息上限 4 MB。

**完成标准**：在同一台机器上用两个进程（`--role collector` 和 `--role monitor`，不同的 `--home`）跑通 M0 验收的 B 部分。

### [ ] M1-02 证书与配对

- 监控器首次启动时生成私有 CA（ECDSA P-256，有效期 10 年）和服务端证书。服务端证书的 SAN 包括主机名、所有本机 IP，以及 Tailscale IP（用 `tailscale ip` 获取，失败就跳过）。
- `lgmon pair new [--ttl 10m]`：生成一次性配对码（例如 `K7QF-3M9X`），过期或用过一次即作废。
- `lgmon pair join --monitor 100.126.79.86:7443 --code K7QF-3M9X`（在采集器上运行）：
  1. 用 TLS 连接监控器，但这一步不校验服务端证书；
  2. 发送配对码和自己生成的 CSR；
  3. 监控器验证配对码后签发客户端证书，连同 CA 证书一起返回；
  4. 采集器保存证书。之后的所有连接都用 mTLS 并校验 CA。
  为防止第一步被中间人截获，监控器的 `pair new` 同时显示 CA 证书指纹的前 8 位，`pair join` 打印收到的指纹，要求两边一致（`--fingerprint` 参数可以直接传入进行校验）。
- 配对接口与 gRPC 共用一个端口，用一个不要求客户端证书的独立 gRPC 方法实现。
- `lgmon collectors list|revoke <id>`：列出和吊销采集器。吊销后该采集器立即断开，并且再也连不上。

**完成标准**：测试覆盖：正确配对、配对码错误、过期、重复使用、指纹不一致、被吊销的证书被拒绝。

### [ ] M1-03 离线缓存与断点续传

- 采集器把要发送的批次先写进本地 `data\outbox.db`（SQLite），收到监控器的 `Ack` 后再删除。
- 重连后用 `Hello` 中的最后确认序号和监控器返回的 `Welcome.resumeFrom` 对齐，从断点补发。
- 监控器按（采集器 ID，序号）去重，重复的批次直接确认，不重复写入。
- 缓存上限默认 24 小时或 200 MB，超出后丢弃最旧的数据，并在恢复连接后发送一条“数据缺口”事件。
- 重连使用指数退避（1 秒起步，最长 60 秒，带随机抖动）。

**完成标准**：集成测试：运行中断开连接 2 分钟，期间目标状态变化 3 次，恢复后监控器数据库里 3 次变化都在、顺序正确、没有重复。

### [ ] M1-04 配置下发

- 监控目标和探测配置改成在监控器上统一管理：每个目标增加 `collector` 字段（采集器 ID 或主机名，写 `"*"` 表示所有采集器）。
- 监控器在采集器连接时，以及配置变化时，推送 `ConfigPush`（带版本号）；采集器应用后回复 `ConfigApplied`，并把配置缓存到本地，离线重启时使用缓存。
- 规则、通知渠道、飞书应用和卡片模板只存在于监控器，不下发。

**完成标准**：在监控器上修改 `targets.yaml`，5 秒内采集器开始监控新目标。

### [ ] M1-05 心跳与采集器失联

- 采集器每 10 秒发送一次心跳；监控器更新 `collectors.last_seen`。
- 新规则类型 `collector_offline`：`for` 默认 30 秒。所有配对过的采集器自动适用，不用为每台单独写规则；在 `rules.yaml` 里可以修改它的 `for`、通知渠道和模板。

**完成标准**：关掉采集器进程，35 秒内产生告警；重新启动后告警恢复。

### [ ] M1-06 Windows 服务

- `lgmon install --role collector|monitor|both`：用 `golang.org/x/sys/windows/svc/mgr` 注册服务 `lgmon`，以 LocalSystem 运行，开机自动启动，失败后 10 秒自动重启。需要管理员权限，没有就提示用户用管理员身份运行。
- `lgmon uninstall`、`lgmon service start|stop|status`。
- 以服务运行时，程序日志写到 `logs\`，同时把启动和异常退出写入 Windows 事件日志（来源名 `lgmon`）。
- 以服务运行时没有用户的环境变量，`proxy: auto` 读不到 `HTTPS_PROXY`。安装时如果检测到当前用户设置了代理，提示用户：飞书接口一般不需要代理，保持直连即可；确实需要代理时，在 `notifiers.yaml` 里写明代理地址。
- 安装后用 `lgmon feishu test` 以服务身份发一张测试卡片，确认服务能读出密钥并发出消息（密钥库是机器级加密，见 M0-09）。

**完成标准**：在一台电脑上安装为服务，重启电脑后不登录也能在另一台电脑的监控器上看到它在线，并且告警能发到飞书（由用户手动验证）。

### [ ] M1-07 更多采集项

| 采集项 | 实现 | 频率 |
|---|---|---|
| 系统指标：CPU、内存、各磁盘使用率、网络收发速率、开机时长 | gopsutil | 5 秒 |
| Windows 服务：状态、启动类型 | `svc/mgr` 枚举，只上报变化 | 10 秒 |
| 监控目标 `match.service` | 按服务名匹配，服务的进程也纳入 | 跟随目标 |
| 脚本探测 | 见下 | 按配置 |

脚本探测（`probes` 里的 `type: script`）：

```yaml
- type: script
  run: powershell -NoProfile -File C:\checks\backup-age.ps1
  interval: 5m
  timeout: 30s
```

脚本最后一行输出 JSON：`{"ok": true, "message": "最近备份 2 小时前", "metrics": {"backup_age_hours": 2}}`。退出码非 0 或输出不是合法 JSON 都算失败，失败时附带输出的最后 20 行。`metrics` 写入新表 `script_metrics(collector_id, target_id, probe, ts, name, value)`。

**完成标准**：每种采集项都有单元测试；脚本探测测试成功、失败、超时、输出不合法四种情况。

### [ ] M1-08 规则补全

- 新规则类型：`metric_threshold`（例：CPU > 90% 持续 5 分钟）、`service_stopped`、`script_failed`、`sql`。
- `sql` 规则：一条只读查询，每 `interval` 执行一次，返回的每一行是一个告警实例；必须返回 `key` 列（用来区分实例），可选返回 `summary` 列。查询走 M1-11 的只读连接。
- 重复提醒：`renotify: 1h`，告警持续期间每隔这么久再提醒一次。
- 静音：`lgmon silence add --rule <id> | --target <id> --for 2h --reason "..."`。静音期间状态照常记录，只是不发通知。
- 确认：`lgmon alerts ack <告警ID>`。确认后不再重复提醒，恢复时仍然通知。
- 抖动合并：10 分钟内同一告警触发超过 3 次，标记为“抖动”，改用 `digest` 模板只发一张汇总卡片，直到稳定 10 分钟。
- `lgmon rules test <id> --since 24h`：用历史数据回放这条规则，输出会在什么时间触发、什么时间恢复。

**完成标准**：每个功能都有使用可控时钟的测试。

### [ ] M1-09 数据汇总与保留

- 新增汇总表：分钟级与小时级的指标平均值、最大值，以及探测成功率和平均耗时。
- 默认保留：原始数据 24 小时，分钟级 14 天，小时级 365 天，在 `settings.yaml` 里可以修改。
- 清理和汇总放在后台任务中运行，不能阻塞写入。

**完成标准**：测试用伪造的 3 天数据验证汇总值正确、过期数据被删除。

### [ ] M1-10 HTTP API

- 监听 `127.0.0.1:7444`；在 `settings.yaml` 里可以改成监听所有地址，以便手机访问。`settings.yaml` 的 `publicBaseUrl` 用于卡片里的“打开面板”按钮。
- 认证：`Authorization: Bearer <token>`。令牌分三种权限：`read`、`config`（可以修改配置）、`admin`。`lgmon token create --scope read --name phone` 生成令牌；数据库只保存令牌的哈希。本机回环地址访问 Web 面板时，自动使用一个仅限本机的会话令牌。
- 接口（全部返回 JSON，错误格式统一为 `{"error": {"code": "...", "message": "中文说明"}}`）：

| 方法与路径 | 说明 |
|---|---|
| `GET /api/v1/collectors` | 采集器列表与在线状态 |
| `GET /api/v1/targets` | 所有目标的当前状态 |
| `GET /api/v1/targets/{collector}/{target}/history?from&to&step` | 状态、探测与进程指标的历史数据 |
| `GET /api/v1/metrics?collector&name&from&to&step` | 系统指标与脚本指标 |
| `GET /api/v1/alerts?state&since` | 告警列表 |
| `POST /api/v1/alerts/{id}/ack` | 确认告警 |
| `POST /api/v1/silences`、`DELETE /api/v1/silences/{id}` | 静音 |
| `GET/PUT /api/v1/config/{targets|rules|notifiers|settings}` | 读取或整体替换配置；写入前先校验，失败返回带行号的错误 |
| `POST /api/v1/rules/{id}/test` | 回放规则 |
| `POST /api/v1/notifiers/{id}/test` | 发送测试通知 |
| `GET /api/v1/feishu/check?app=` | 与 `lgmon feishu check --json` 输出一致 |
| `GET /api/v1/feishu/chats?app=` | 机器人所在的群 |
| `POST /api/v1/feishu/apps` | 新增或修改飞书应用；App Secret 只能通过这个接口或命令行写入，写入后任何接口都不再返回它 |
| `GET /api/v1/templates`、`GET/PUT/DELETE /api/v1/templates/{id}` | 卡片模板 |
| `POST /api/v1/templates/{id}/preview` | 用样例告警渲染，返回卡片 JSON |
| `POST /api/v1/query` | 只读 SQL（见 M1-11） |
| `GET /api/v1/stream` | SSE 实时推送：目标状态变化、新告警、新采集数据 |
| `GET /api/v1/openapi.json` | OpenAPI 3.1 描述 |

**完成标准**：每个接口都有 `httptest` 测试；OpenAPI 描述与实际实现一致（写一个测试，检查所有路由都出现在 OpenAPI 里）。

### [ ] M1-11 只读 SQL

- 单独打开一个只读连接（`mode=ro`，并执行 `PRAGMA query_only=ON`）。
- 只允许单条 `SELECT` 或 `WITH` 语句；执行超时 5 秒；结果最多 10000 行，超出时截断并标记 `truncated: true`。
- `GET /api/v1/schema` 和 `lgmon query --schema`：返回所有表和列的说明（说明写在迁移文件的注释里，由程序解析）。
- `lgmon query "SELECT ..." [--json]`。

**完成标准**：测试覆盖：写入语句被拒绝、多条语句被拒绝、超时、截断。

### [ ] M1-12 通知补全

- `feishu_webhook`：飞书群的自定义机器人，给不想创建应用的用户用。只需要 Webhook 地址（和可选的签名密钥，存进密钥库），不能自动列出群，也不能检查权限；卡片模板照常适用。
- `webhook`：通用 HTTP POST，发送告警的 JSON。
- `toast`：在监控器所在电脑上弹出 Windows 通知。以服务运行时，要通过当前登录用户的会话弹出；做不到就跳过并记录日志。
- 路由：通知渠道可以设置 `filter`（按严重程度、标签、采集器筛选）。

**完成标准**：每个渠道都用 `httptest` 测试请求内容和签名。

### [ ] M1-13 Web 面板

- 前端放在 `web/`：React、TypeScript、Vite，图表用 ECharts，包管理用 pnpm。构建产物通过 `go:embed` 嵌入程序，由 HTTP API 的同一个端口提供，路径 `/`。
- `scripts/build.ps1` 先构建前端再编译 Go。仓库里不提交构建产物；CI 里要先构建前端。
- 页面：
  - **总览**：每台采集器一张卡片（在线状态、CPU、内存、目标正常数）；正在触发的告警放在最上面。还没有接入飞书时，顶部显示提示条“还没有设置告警通知”，点击进入飞书向导。
  - **主机详情**：系统指标曲线、目标列表、Windows 服务列表。
  - **目标详情**：状态时间线、探测耗时曲线、进程 CPU 与内存曲线、最近告警。
  - **告警**：列表，可以确认、静音。
  - **面板**：渲染 M1-15 的声明式面板。
  - **通知**：飞书应用、通知渠道和卡片模板，内容见 M1-14。
- 数据实时更新走 SSE；手机宽度可用；支持深浅色。
- 视觉风格与“服务开关”保持一致：浅色背景、白色圆角卡片、蓝色主色。

**完成标准**：`pnpm build` 不报错；前端组件有基本的测试（`vitest`）；在 1440 和 390 两种宽度下截图，放进本文的执行记录。

### [ ] M1-14 飞书开通向导与模板编辑（网页版）

**向导**：把 M0-11 的五个步骤做成网页上的分步向导，逻辑完全复用 `internal/feishu/setup`，通过 M1-10 的接口调用。

- 每一步显示状态（已完成 / 需要你操作 / 出错），需要用户操作的步骤给出带编号的说明和一个大按钮：
  - 第 3 步的按钮是“一键开通权限”，在新标签页打开开通链接；页面上同时列出要开通的权限及用途，并每 5 秒自动刷新状态。
  - 第 4 步显示“去飞书把「机器人名称」拉进群”的步骤，自动刷新群列表，有新群时高亮显示；也提供“帮我新建告警群”（如果 M0-11 做出来了）。
- 整个过程不出现“群 ID”“scope”这类术语，只说“群”“权限”；技术细节放进可展开的“详情”里。
- 可以配置多个群：例如严重告警发“值班群”，其他发“通知群”，每个群各自选默认模板。

**模板库与编辑器**：

- 模板库：列出四套预设和自定义模板，每张都显示“触发”和“恢复”两种状态的预览图。
- 预览：前端实现一个简化的卡片渲染器，只覆盖预设和常见自定义会用到的组件（标题栏及颜色、Markdown 文本、两列字段、分割线、按钮、折叠面板），效果接近飞书即可，并注明“以飞书实际显示为准”。
- 编辑器：
  - 基础模式：表单编辑 `extends` 写法的各个字段（标题、颜色、字段的选择和顺序、附加字段、@ 谁、按钮、底部备注），右侧实时预览。
  - 高级模式：编辑 `raw` JSON，带语法校验、变量提示和实时预览；提供“去飞书卡片搭建工具设计”的链接。
  - “发送到测试群”：用当前编辑中的模板发一张卡片到选定的群。
- 保存前先在后端校验（与 `lgmon template validate` 相同），失败时把错误标注到对应字段上。
- 规则编辑处可以给单条规则选择模板。

**完成标准**：用 Playwright 写端到端测试，飞书接口使用假实现，覆盖：向导从零走完、缺权限时出现开通按钮、拉群后自动发现、基础模式改字段后预览变化、高级模式写错 JSON 时报错。真实飞书上的完整流程由用户确认。

### [ ] M1-15 声明式面板

- 配置目录下的 `dashboards/*.yaml`，每个文件是一页面板，热加载。
- 格式（同时生成 JSON Schema）：

```yaml
id: web-sites
title: 网站
refresh: 10s
panels:
  - type: stat            # stat | line | bar | status_grid | table | alerts | log
    title: 在线网站
    query: "SELECT count(*) AS value FROM v_target_current WHERE state = 'up'"
    width: 3              # 12 列栅格
  - type: line
    title: 探测耗时
    query: "SELECT ts, target_id AS series, latency_ms AS value FROM probe_results WHERE ts > :from"
    width: 9
```

- 查询都走只读 SQL。提供常用视图，降低写查询的难度：`v_target_current`（每个目标的最新状态）、`v_alerts_active`、`v_collectors`。
- 查询可以使用参数 `:from`、`:to`、`:collector`，由面板顶部的时间范围和主机选择器提供。
- 每种面板类型规定了查询必须返回哪些列，在 `docs/dashboards.md` 里写清楚，并给出示例。

**完成标准**：`examples/dashboards/` 里提供三个示例面板，都能正确渲染；查询列不符合要求时，面板上要显示具体错误，不能整页崩溃。

### [ ] M1-16 MCP 服务

- 使用官方 Go SDK `github.com/modelcontextprotocol/go-sdk`。
- `lgmon mcp`：通过 stdio 提供服务，通过 HTTP API 访问监控器（需要令牌，从 `LGMON_TOKEN` 读取）。HTTP API 同时在 `/mcp` 提供 Streamable HTTP 方式。
- 工具：`list_collectors`、`list_targets`、`get_target_history`、`query_sql`、`get_schema`、`list_alerts`、`ack_alert`、`silence`、`get_config`、`put_config`、`validate_config`、`test_rule`、`test_notifier`、`list_processes`（某台采集器的当前进程表，用来帮助 Agent 写匹配规则）、`feishu_check`、`feishu_list_chats`、`list_templates`、`preview_template`、`put_template`。
- MCP 不提供写入 App Secret 的工具。需要用户提供密钥时，工具返回提示，让用户在命令行或网页里填写。
- 每个工具的描述写清楚适用场景，参数要有 JSON Schema。

**完成标准**：用 SDK 自带的客户端写集成测试，逐个调用所有工具。

### [ ] M1-17 托盘

- 监控器以普通程序运行时显示托盘图标（`fyne.io/systray`）：图标颜色反映是否有正在触发的告警；菜单包括“打开面板”“暂停通知 1 小时”“退出”。
- 以服务运行时没有托盘。`lgmon tray` 作为单独的命令，在用户登录时启动，通过 HTTP API 读取状态。

**完成标准**：用户手动确认托盘能正常显示和操作。

### [ ] M1-18 给 Agent 的文档

- `docs/agent-guide.md`：写给使用 lg-monitor 的 Agent（不是开发它的 Agent）。内容包括：概念、各个配置文件、CLI、API、MCP，以及常见任务的完整步骤：添加一个网站的监控、接入飞书（Agent 怎样带着用户完成需要人工操作的步骤）、告警发到另一个群、改卡片模板、做一页面板、排查一条一直触发的告警。
- `lgmon agent install [--target claude|codex|all]`：把技能文件安装到 `~/.claude/skills/lg-monitor/` 和 `~/.codex/skills/lg-monitor/`，文件内容来自 `docs/agent-guide.md` 的精简版，并填入本机实际的路径。
- `lgmon agent prompt --wish "..."`：生成一段可以直接粘贴给 Agent 的指令。

**完成标准**：新开一个 Agent 会话，只给它 `agent prompt` 生成的指令，它能独立完成“给 Nebula 加监控、写规则、做一页面板、把严重告警改用排查模板”（由用户执行并确认）。

### [ ] M1-19 第一版验收

写 `docs/acceptance-m1.md`（需要人参与的步骤）和 `scripts/m1-acceptance.ps1`（能自动化的步骤）：

1. 电脑 B 安装为监控器，在网页向导里从零完成飞书接入（新应用或已有应用都可以）。
2. 电脑 A 安装为采集器服务，通过 Tailscale 与 B 配对。
3. 电脑 A 关掉演示进程，15 秒内飞书收到告警卡片。
4. 断开电脑 A 的网络 10 分钟，期间关掉再打开演示进程。恢复网络后，监控器上能看到这两次变化，且时间正确。
5. 电脑 A 关机，35 秒内收到“采集器失联”告警；开机后（不登录）告警恢复。
6. 在网页上基于“标准”新建一个自定义模板，发到测试群确认效果。
7. 只读 SQL、MCP、面板各做一次 Agent 任务，见 M1-18。
8. 采集器 24 小时平均 CPU < 1%，内存 < 40 MB（用 `lgmon` 自己的进程监控记录）。

---

## M2 之后

M2 和 M3 的内容见 PRD 第 8 节。M1 验收通过后，再在这里展开成任务。已经确定要放进 M2 的：卡片上的“确认”“静音 1 小时”按钮（需要接收飞书的卡片回调）。

## 执行记录

（执行者在这里记录：与计划不一致的地方、做出的选择及原因、需要用户确认的事项、截图。）

### 2026-11-06 M0-01 项目骨架

- 用户请求原文：
  > 按照 AGENTS.md 的要求，先阅读 docs/PRD.md 和 docs/PLAN.md，从第一个未完成的任务开始开发，验证通过后提交。此后，claude制定计划和推进方案，你来执行
- 环境补充授权原文：`go需要你安装的`、`改呀，我批准`、`不要装便携版`。
- 勘察：基线提交 `eaf6ea7`，工作区初始干净，仅有需求与计划文档；第一个未完成任务为 M0-01。实际工作目录为 `D:\LFEN_project\dev_project\lgmon`，沿用该目录，不按 PRD 的旧路径迁移。PATH 和 `C:\Program Files\Go` 起初均没有 Go。
- 澄清告知原文：任务范围和验收标准已明确，本次跳过提问。范围与完成标准直接采用本任务原文；本次只执行 M0-01，后续由 Claude 制定推进方案。初始授权覆盖开发、必要验证、本地提交与 Go 标准版安装；本地提交后，用户答复原文 `推送并验证 CI`，追加推送与验证 Actions 的授权。不包含产品发布。
- 执行依据：先建立可编译模块及计划指定的目录，再完成根命令、版本注入与 Windows CI，最后逐项验证。保持原状无法生成可执行程序；改用其他语言、命令行框架或目录会偏离已定计划，因此沿用既定方案。不提前实现 M0-02 或 M1 功能，不接触任何已运行服务。回退可通过撤回本任务的 Git 提交完成。
- 留痕采用现有 PLAN 执行记录，遵守用户不改变项目结构的要求，不新增治理目录。
- 工具链安装：官方 Go 1.27.3 Windows amd64 MSI，SHA256 为 `89ec8325f667c674fe1b94a5a88e26932aa12323589881927ff54da99d4b7faa`，校验与官方清单一致，Google 数字签名有效。下载与执行合并的安装命令被自动审批拒绝；拆开下载、独立核验哈希和签名后，针对已核验安装包的标准安装命令获准执行。安装退出码为 0，`C:\Program Files\Go\bin\go.exe version` 返回 `go1.27.3 windows/amd64`，系统 PATH 已包含 `C:\Program Files\Go\bin`。未使用便携工具链进行开发或验证；未使用的下载与解压文件已移到系统临时目录。
- 原文逐项对照：

  | M0-01 原文要求 | 实现与证据 | 结果 |
  |---|---|---|
  | 初始化指定模块、建立计划目录、空目录放包说明 | `go.mod`、`go.sum`；`cmd/`、`internal/`、`proto/`、`web/`、`schemas/`、`examples/`、`scripts/`；空目录均有 `doc.go` | 已实现 |
  | Cobra 根命令、`--home`、`--log-level` | `cmd/lgmon/main.go`；四个 CLI 测试覆盖继承参数、目录优先级、无效日志级别和多余参数 | 已实现，测试通过 |
  | `version` 输出版本、提交、构建时间，由链接参数注入 | 构建后输出 `dev` / `eaf6ea7` / `2026-11-06T04:02:07Z`；指定版本再次构建输出 `0.1.0-m0.01` / `eaf6ea7` / `2026-11-06T04:03:26Z` | 已实现，实机通过 |
  | `build.ps1` 禁用 cgo，生成 `bin\lgmon.exe` | Windows PowerShell 5.1 实际执行成功；`go version -m bin\lgmon.exe` 确认 `CGO_ENABLED=0`、`GOOS=windows`、`GOARCH=amd64`；从 `docs/` 调用也能构建，且恢复调用者目录和环境变量 | 已实现，实机通过 |
  | `windows-latest` CI 运行 vet、test、build | `.github/workflows/ci.yml` 已配置，并加上格式及版本命令检查；全部对应检查已在本机和托管 runner 执行 | 已实现，远端通过 |
  | CI 通过 | [Actions 42161061255](https://github.com/LFenX/lg-monitor/actions/runs/42161061255)，代码版本 `e6393a4`，全部步骤成功 | 已验证 |

- 提交前检查：`gofmt -l .` 输出为空；`go vet ./...`、`go test ./...`、`scripts\build.ps1` 全部退出 0。四个测试全部通过，其他包当前仅保留文档占位。构建脚本使用 UTF-8 BOM，已验证 Windows PowerShell 5.1 可正确处理中文。
- 格式兼容：本机 Git 的 CRLF 转换会改变 Go 文件换行，新增 `.gitattributes` 固定 Go 源码与模块文件使用 LF，避免 Windows 检出后 `gofmt -l .` 报出格式差异。
- 首轮远端验证：[Actions 42160459757](https://github.com/LFenX/lg-monitor/actions/runs/42160459757) 在格式检查步骤执行前发生 PowerShell 语法错误。日志中的中文 `throw` 字符串被解码成乱码，报 `The string is missing the terminator`。将 CI 内联诊断文本改为 ASCII，避免 Windows PowerShell 5.1 对脚本编码的依赖；构建脚本本身已用 UTF-8 BOM 并在本机通过 5.1 验证。保留失败结果，修订本任务提交后重跑，不据此改动后续任务方案。
- 第二轮远端验证：[Actions 42160714774](https://github.com/LFenX/lg-monitor/actions/runs/42160714774) 的格式、vet、test 均通过，build 失败。日志显示 runner 同时有 Go 1.27.3 和 Go 1.24.13；`Get-Command -CommandType Application` 返回多个结果，脚本将两个路径拼成命令。修复为只取 PATH 中的第一个 Go，按调用者环境的优先级选择工具链。本地用临时目录联接指向既有 Go 安装，确认 PowerShell 5.1 返回两个 Go 命令后，构建、版本输出、格式、vet、test 全部通过；没有另装便携工具链。
- 工程结果：[Actions 42161061255](https://github.com/LFenX/lg-monitor/actions/runs/42161061255) 的格式、vet、test、build、version 和作业收尾全部成功，M0-01 已勾选。末次修订只更新本文件的勾选和结果记录，运行代码及 CI 工作流与该通过版本一致；本任务在 main 历史中保持一个提交。
- 未完成项：本任务无工程遗留项；用户验收决定为 `Pending`，没有代填。未启动 M0-02，后续由 Claude 制定计划和推进方案，Codex 执行。
- 外部依据：[Go 官方下载与校验清单](https://go.dev/dl/)、[Cobra 官方用法](https://github.com/spf13/cobra/blob/main/site/content/user_guide.md)、[checkout](https://github.com/actions/checkout)、[setup-go](https://github.com/actions/setup-go)。
