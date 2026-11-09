# lg-monitor

告警支持按需生成 [诊断包](docs/diagnostics.md)，从网页、HTTP、MCP 或本地 `lgmon diagnose` 交给 Agent 排查；只导出有界白名单元数据。

使用 lg-monitor 的 Agent 请先读 [使用指南](docs/agent-guide.md)。新会话可直接粘贴以下命令生成的指令；安装命令仅在本人选择安装技能时执行：

```powershell
.\bin\lgmon.exe agent prompt --home .\dev-home --wish "给 Nebula 加监控、写规则、做一页面板，把严重告警改用排查模板"
.\bin\lgmon.exe agent install --target all --home .\dev-home --json
```

Windows 服务监控：采集器盯住电脑上的进程、服务、端口和网站，监控器汇总多台电脑的数据、出面板、通过飞书发告警。所有能力都开放给编程 Agent。

状态：M0 工程与验收脚本已具备，真实飞书接入和群收件仍待人工验收；M1 工程已具备，已接入 gRPC+mTLS、配对/吊销、持久 outbox、配置下发、心跳失联、Windows 服务实现、更多采集项、规则与回放、数据汇总、HTTP API/SSE、只读 SQL/表结构说明、自定义机器人、通用 Webhook、Windows 通知和渠道筛选，以及含实时总览、主机/目标详情和告警操作的 Web 面板。网页飞书向导、模板编辑、声明式面板、MCP 和托盘/通知暂停也已具备工程实现。本人已选择暂不安装服务；真实服务重启、收件、弹窗和托盘操作仍待验。完整命令见 [docs/cli.md](docs/cli.md)，HTTP 接口见 [docs/api.md](docs/api.md)，通知协议见 [docs/webhooks.md](docs/webhooks.md)，需求见 [docs/PRD.md](docs/PRD.md)，任务与验证记录见 [docs/PLAN.md](docs/PLAN.md)。

M2 已加入 Windows 事件日志、[服务开关配置导入](docs/service-switch.md)、[自定义 HTML 面板](docs/html-dashboards.md)、[启动项与计划任务](docs/inventory.md)、[团队共享面板](docs/teams.md)、[飞书卡片确认/静音](docs/card-actions.md)和[受限服务重启](docs/service-operations.md)。团队按设备证书、卡片、主机和目标授权，成员实时只读查看，撤销即停止访问；HTML 页面不能分享。服务重启默认关闭，仅允许本机逐条授权并由 admin 逐次确认的精确服务。人工验收保持 Pending，工程开发用测试夹具继续推进，暂不安装真实服务。

- 语言：Go
- 采集器到监控器：gRPC + 双向 TLS，采集器主动连出
- 存储：SQLite
- 面板：React + ECharts 嵌入 exe，与 API 同端口，默认 http://127.0.0.1:7444/；使用与验证见 [Web 面板](docs/web-panel.md)

Windows 用户安装入口见 [安装包](docs/installer.md)：选择 both、monitor 或 collector，程序固定放入 Program Files，数据和配对身份在卸载后保留。工程夹具不调用真实服务；真实安装与人工验收继续 Pending。

安装 Go 1.27、Git、Node 22.22.2+（22.x）与 pnpm 11.21.0 后，在 PowerShell 中构建；编译后的 exe 不需要 Node 运行时：

```powershell
.\scripts\build.ps1
.\bin\lgmon.exe version
.\bin\lgmon.exe --help
```

构建时可用 `-Version 0.1.0` 指定版本号；提交号和 UTC 构建时间自动注入。所有子命令支持 `--home` 和 `--log-level`；数据目录依次采用 `--home`、`LGMON_HOME`、`%ProgramData%\lg-monitor`。

配置文件位于数据根目录下的 `config/`：`targets.yaml`、`rules.yaml`、`notifiers.yaml`、`settings.yaml`、`card-templates/*.yaml` 及 `dashboards/*.yaml`，HTML 页面另有同 ID 的 `.html` 文件。示例见 [examples/](examples/)，其中飞书标识均为虚构值。配置缺失时使用默认值，校验命令不创建文件或配置目录。

```powershell
.\bin\lgmon.exe config validate --home .\dev-home
.\bin\lgmon.exe config validate --home .\dev-home --json
.\bin\lgmon.exe config schema
.\bin\lgmon.exe config schema --output .\schemas
```

校验失败先显示错误总数，再逐行列出文件、行号与中文错误，退出码为 1。`config validate --json` 在标准输出返回 `{"ok":false,"errors":[{"file":"rules.yaml","line":5,"message":"错误说明"}]}`；通过时返回 `{"ok":true,"errors":[]}`。Schema 从 Go 结构体生成；`config schema` 默认输出到当前工作目录的 `schemas/`。跨文件引用和时长数值限制以 `config validate` 为准。

TCP 探测使用 `address: 主机:端口`；IPv6 地址写成 `address: "[::1]:3000"`。规则省略 `for` 时表示 `0s`，探测的 `interval` 和 `timeout` 必须显式配置正时长；通知渠道省略 `template` 时使用标准模板。自定义模板支持渲染、校验和预览，详见 [卡片模板](docs/card-templates.md)。

配置热加载接口位于 `internal/config`：`NewFileWatcher` 监听配置及模板目录，`Manager` 只发布完整校验通过的配置快照，错误修改会保留上次有效配置并记录错误。`lgmon run --role both` 组装采集、存储、规则和通知，Ctrl+C 依次停止采集、排空写入与通知、关闭数据库。

目标和探测集中放在监控器的 targets.yaml：collector 为采集器 ID、主机名或 `"*"`，省略表示全部，主机名不区分大小写。监控器通过 mTLS 按主机下发目标快照，采集器校验并持久保存后返回配置回执，离线重启使用上次有效缓存。collector 只读取本机 settings.yaml 设置缓存，目标、规则、飞书应用、通知渠道和模板均由监控器管理。协议和缓存边界见 [采集通道](docs/transport.md#集中配置)。

Windows 目标匹配不区分进程名大小写，`process: node` 同时匹配 `node` 和 `node.exe`。命令行正则默认不区分大小写，例如路径盘符大小写不同仍能匹配；需要区分时在表达式前加 `(?-i)`。

进程与端口采集接口位于 `internal/collector`：`ProcessSource.Snapshot` 返回带 `(PID, 创建时间)` 身份的进程快照；Windows 核心字段通过一次 `NtQuerySystemInformation` 读取，无需逐个打开进程。路径和命令行按身份缓存（含失败结果），进程退出后清理；无权读取命令行时留空并设置 `cmdlineUnavailable`。`PortSource.ListeningTCP` 返回 TCP 监听地址、端口及 PID。创建时间使用 UTC Unix 毫秒，RSS 使用字节；CPU 以相邻成功采样的用户态和内核态时间差除以整机逻辑处理器总数计算，范围为 0～100%，全部核心满载为 100%，首次采样为 0；采集超过一秒记录警告。

系统指标每 5 秒采集，Windows 服务每 10 秒枚举并只上报变化；match.service 纳入对应服务进程，并与其他匹配条件在同一 PID 上求交。script 探测支持 JSON 结果、自定义指标与超时进程清理。配置、单位和输出边界见 [更多采集项](docs/collection.md)。

真实 Windows 集成测试只读系统与 SCM，只操作自身创建的进程和回环监听端口：`go test -tags integration ./internal/collector`。默认 `go test ./...` 使用假实现。

`internal/app.Collector` 默认每 2 秒采集，状态变化立即发送，每 10 秒发送全量目标快照；Windows x64 默认额外使用自有 ETW 进程事件，在 200ms 合并窗口触发真实快照核验，退出状态沿用同一管线。权限不足等源错误明确记录并回退原采样；本机 `processEvents.mode: disabled` 可关闭事件源，重启采集器生效，详见 [即时进程事件](docs/collection.md#即时进程事件)。被匹配进程的 CPU/RSS 和独立探测结果走同一个 `pipeline.Sink/Source`。每条消息包含采集器 ID、递增序号和 UTC 毫秒时间戳。`FileIDStore` 将 UUID 保存在数据根目录的 `data/collector-id`，重复启动保留身份；配置订阅更新后会停止旧探测并重建调度。

单轮采集失败会记录连续失败次数并跳过本轮，下轮继续；不会用空快照代替失败结果。规则保存失败保留原告警 ID 和事件，下次评估重试；通知入队失败保留待通知状态，并保证触发先于恢复入队。

命令行只保留在本机采集快照中用于匹配。pipeline 的 `ProcessRef` 只包含 PID、父 PID、创建时间、名称、路径、CPU、RSS 和线程数，类型中没有命令行字段，避免传输或数据库保存命令行里的凭证。

`internal/store` 使用纯 Go SQLite，自动执行内嵌版本迁移，启用 WAL。单一写入协程每 500ms 或 500 条提交；`Write` 表示入队，`Flush` 等待落盘，`Close` 排空尾批。后台每分钟汇总和清理，原始/分钟/小时数据默认保留 24h/14d/365d，在 settings.yaml 中设置；告警审计独立保留。后台扫描使用独立只读快照，采集写入优先于汇总提交。完整水位之前的迟到采样会丢弃并记录 late_dropped，详见 [数据汇总与保留](docs/storage.md)。`status` 与 `alerts` 通过只读连接查询，程序停止后仍可使用。

原始批次写入失败后保留数据，以 0.5/1/2 秒递增至最多 30 秒重试；默认待提交缓冲上限 10000 条，另有 2048 个有界请求槽位。缓冲溢出只丢最旧原始采样，日志记录丢弃数量；告警、事件和通知操作不按采样丢弃。单次操作失败返回调用方，写入协程继续服务；`Flush` 失败须重试，只有成功才确认当时仍保留的采样已提交。关闭时做最后一次提交，仍失败则返回错误。

`internal/rules` 按最新状态和探测结果评估进程不在、端口关闭、探测失败规则，支持持续时长及触发/恢复生命周期。unknown 不新建或恢复告警，会取消 pending；告警和事件同事务保存。重启只补发最近 24 小时尚无对应状态、对应渠道成功记录的转换，已成功的渠道跳过。

还支持指标阈值、服务停止、脚本失败和 SQL 多实例规则。renotify 配置重复提醒；alerts ack 停止该次告警的提醒，silence add 按规则或目标静音。10 分钟内第四次触发进入抖动合并并改发一张 digest。`rules test` 用历史时钟回放已保存规则或 YAML 草稿，不发送通知；SQL 只读连接过滤未来数据并限制可读表。配置、边界和示例见 [规则与回放](docs/rules.md)。

采集器每 10 秒发送心跳；监控器按自己的接收时间维护 last_seen，为全部未吊销的配对采集器自动启用 30 秒失联告警，重新连接后恢复同一告警。可用 collector_offline 全局规则修改 for、严重程度、渠道和模板，不指定 target；无显式策略时默认 warning、通知全部当前渠道。无目标采集器也发送心跳，补传采样不会延长在线状态。详见 [失联规则](docs/transport.md#心跳与失联)。

Windows 服务命令为 install、uninstall 和 service start/stop/status。安装需要管理员，注册固定 lgmon 服务，以 LocalSystem 自动启动、失败 10 秒重启；运行日志为 JSON，启动/异常写应用事件日志。`feishu test --service` 请求监控服务以自身身份读取凭据并发送。交互式 run 不安装服务；真实注册、重启和收件验收尚待本人授权，步骤见 [Windows 服务](docs/windows-service.md)。

第一版双机、真实收件、离线补传、未登录服务恢复、Agent 任务和 24 小时资源验收见 [第一版验收](docs/acceptance-m1.md)。`scripts/m1-acceptance.ps1` 只做只读检查；当前人工结论及 PRD 差异仍为 Pending，不能用 CI 代替实机验收。

目标状态另含独立的 `process`（found/missing/unknown）和 `port`（listening/closed/unknown）事实，不配置对应条件时为空。process_absent 要求配置 process 或 cmdline，只在进程 missing 时触发；port_closed 只在端口 closed 且进程没有 missing 时触发。进程仍在但端口未监听时只报端口故障，进程退出时只报进程故障；命令行不可读时保持未知，避免重复或错误告警。

删除规则或修改规则类型、目标时，旧触发告警以 `closed` 事件关闭，状态存为 resolved，不发送恢复卡片；重启发现规则已不存在时同样处理。关闭写入失败会保留原告警及关闭事件，在后续评估中重试。

密钥通过 `lgmon secret set <名称> --home .\dev-home` 从标准输入保存，终端输入不回显；`lgmon secret list --json` 只返回名称。文件使用机器级 DPAPI 加密，并限制 SYSTEM、Administrators 和创建者访问；没有打印密钥明文的命令。飞书客户端使用官方 Go SDK，通知发送器提供按告警/状态/群的幂等键、重试和每次尝试的 SQLite 审计；运行 `lgmon feishu setup --home .\dev-home` 进行接入。

## 资源需求

20 个模拟采集器、每台 50 个目标的整链路实测：进程 2 秒、完整状态 10 秒、系统 5 秒、每目标 HTTP 结果 30 秒，回环 mTLS，真实运行 30 分钟。监控器使用真实 SQLite、规则和通知队列，通知为假渠道。测试环境为 Windows 11、Go 1.27.3、24 个逻辑处理器；CPU 按整机容量归一化，以下只量独立监控器进程。

| 项目 | 实测或折算 |
|---|---|
| 监控器平均 CPU / 最大 RSS | 1.74% / 102.8 MiB |
| 30 分钟数据库文件 | 239.9 MB，折算当时增长约 479.6 MB/小时 |
| 原始数据 | 约 430.6 MB/小时 |
| 分钟 / 小时汇总 | 约 1.092 GB/天 / 18.87 MB/天 |
| 默认 raw 24h、minute 14d、hour 365d | 约 32.52 GB（表、索引和现有其他页的折算，不是全年实测，不含备份及额外 WAL 空间） |
| 每台采集器离线缓存 | 默认 500 MB；固定 50 目标容量测试折算 24h 含 WAL 预留为 248.4 MB，占默认 49.7% |

MB/GB 使用十进制，MiB 使用二进制。多进程、更多指标和探测会增加占用；网页和 `status` 根据实际写入速度提示缓存不足 24h。上述监控器数字不代表采集器的 24 小时资源验收；该人工项仍待本人。完整场景、门槛和原始报告见 [M1-22 工程证据](docs/acceptance-m1.md#m1-22-工程证据)。
