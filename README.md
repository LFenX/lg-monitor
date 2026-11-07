# lg-monitor

Windows 服务监控：采集器盯住电脑上的进程、服务、端口和网站，监控器汇总多台电脑的数据、出面板、通过飞书发告警。所有能力都开放给编程 Agent。

状态：M0 工程与验收脚本已具备，真实飞书接入和群收件仍待人工验收；正在推进 M1，已接入 gRPC+mTLS、配对/吊销、持久 outbox、配置下发、心跳失联、Windows 服务实现、更多采集项、规则与回放、数据汇总、HTTP API/SSE、只读 SQL/表结构说明、自定义机器人、通用 Webhook、Windows 通知和渠道筛选，以及含实时总览、主机/目标详情和告警操作的 Web 面板。网页飞书向导、模板编辑与声明式面板按后续任务继续实现。本人已选择暂不安装服务；真实服务重启、收件和弹窗仍待验。完整命令见 [docs/cli.md](docs/cli.md)，HTTP 接口见 [docs/api.md](docs/api.md)，通知协议见 [docs/webhooks.md](docs/webhooks.md)，需求见 [docs/PRD.md](docs/PRD.md)，任务与验证记录见 [docs/PLAN.md](docs/PLAN.md)。

- 语言：Go
- 采集器到监控器：gRPC + 双向 TLS，采集器主动连出
- 存储：SQLite
- 面板：React + ECharts 嵌入 exe，与 API 同端口，默认 http://127.0.0.1:7444/；使用与验证见 [Web 面板](docs/web-panel.md)

安装 Go 1.27、Git、Node 22.22.2+（22.x）与 pnpm 11.21.0 后，在 PowerShell 中构建；编译后的 exe 不需要 Node 运行时：

```powershell
.\scripts\build.ps1
.\bin\lgmon.exe version
.\bin\lgmon.exe --help
```

构建时可用 `-Version 0.1.0` 指定版本号；提交号和 UTC 构建时间自动注入。所有子命令支持 `--home` 和 `--log-level`；数据目录依次采用 `--home`、`LGMON_HOME`、`%ProgramData%\lg-monitor`。

配置文件位于数据根目录下的 `config/`：`targets.yaml`、`rules.yaml`、`notifiers.yaml`、`settings.yaml` 及 `card-templates/*.yaml`。示例见 [examples/](examples/)，其中飞书标识均为虚构值。配置缺失时使用默认值，校验命令不创建文件或配置目录。

```powershell
.\bin\lgmon.exe config validate --home .\dev-home
.\bin\lgmon.exe config validate --home .\dev-home --json
.\bin\lgmon.exe config schema
.\bin\lgmon.exe config schema --output .\schemas
```

校验失败先显示错误总数，再逐行列出文件、行号与中文错误，退出码为 1。`config validate --json` 在标准输出返回 `{"ok":false,"errors":[{"file":"rules.yaml","line":5,"message":"错误说明"}]}`；通过时返回 `{"ok":true,"errors":[]}`。Schema 从 Go 结构体生成；`config schema` 默认输出到当前工作目录的 `schemas/`。跨文件引用和时长数值限制以 `config validate` 为准。

TCP 探测使用 `address: 主机:端口`；IPv6 地址写成 `address: "[::1]:3000"`。规则省略 `for` 时表示 `0s`，探测的 `interval` 和 `timeout` 必须显式配置正时长；通知渠道省略 `template` 时使用标准模板。自定义模板支持渲染、校验和预览，详见 [卡片模板](docs/card-templates.md)。

配置热加载接口位于 `internal/config`：`NewFileWatcher` 监听配置及模板目录，`Manager` 只发布完整校验通过的配置快照，错误修改会保留上次有效配置并记录错误。`lgmon run --role both` 组装采集、存储、规则和通知，Ctrl+C 依次停止采集、排空写入与通知、关闭数据库。

目标和探测集中放在监控器的 targets.yaml：collector 为采集器 ID、主机名或 `"*"`，省略表示全部，主机名不区分大小写。监控器通过 mTLS 按主机下发目标快照，采集器校验并持久保存后返回配置回执，离线重启使用上次有效缓存。collector 不读取本地 config，规则、飞书应用、通知渠道和模板均留在监控器。协议和缓存边界见 [采集通道](docs/transport.md#集中配置)。

Windows 目标匹配不区分进程名大小写，`process: node` 同时匹配 `node` 和 `node.exe`。命令行正则默认不区分大小写，例如路径盘符大小写不同仍能匹配；需要区分时在表达式前加 `(?-i)`。

进程与端口采集接口位于 `internal/collector`：`ProcessSource.Snapshot` 返回带 `(PID, 创建时间)` 身份的进程快照；Windows 核心字段通过一次 `NtQuerySystemInformation` 读取，无需逐个打开进程。路径和命令行按身份缓存（含失败结果），进程退出后清理；无权读取命令行时留空并设置 `cmdlineUnavailable`。`PortSource.ListeningTCP` 返回 TCP 监听地址、端口及 PID。创建时间使用 UTC Unix 毫秒，RSS 使用字节；CPU 以相邻成功采样的用户态和内核态时间差除以整机逻辑处理器总数计算，范围为 0～100%，全部核心满载为 100%，首次采样为 0；采集超过一秒记录警告。

系统指标每 5 秒采集，Windows 服务每 10 秒枚举并只上报变化；match.service 纳入对应服务进程，并与其他匹配条件在同一 PID 上求交。script 探测支持 JSON 结果、自定义指标与超时进程清理。配置、单位和输出边界见 [更多采集项](docs/collection.md)。

真实 Windows 集成测试只读系统与 SCM，只操作自身创建的进程和回环监听端口：`go test -tags integration ./internal/collector`。默认 `go test ./...` 使用假实现。

`internal/app.Collector` 默认每 2 秒采集，状态变化立即发送，每 10 秒发送全量目标快照；被匹配进程的 CPU/RSS 和独立探测结果走同一个 `pipeline.Sink/Source`。每条消息包含采集器 ID、递增序号和 UTC 毫秒时间戳。`FileIDStore` 将 UUID 保存在数据根目录的 `data/collector-id`，重复启动保留身份；配置订阅更新后会停止旧探测并重建调度。

单轮采集失败会记录连续失败次数并跳过本轮，下轮继续；不会用空快照代替失败结果。规则保存失败保留原告警 ID 和事件，下次评估重试；通知入队失败保留待通知状态，并保证触发先于恢复入队。

命令行只保留在本机采集快照中用于匹配。pipeline 的 `ProcessRef` 只包含 PID、父 PID、创建时间、名称、路径、CPU、RSS 和线程数，类型中没有命令行字段，避免传输或数据库保存命令行里的凭证。

`internal/store` 使用纯 Go SQLite，自动执行内嵌版本迁移，启用 WAL。单一写入协程每 500ms 或 500 条提交；`Write` 表示入队，`Flush` 等待落盘，`Close` 排空尾批。后台每分钟汇总和清理，原始/分钟/小时数据默认保留 24h/14d/365d，在 settings.yaml 中设置；告警审计独立保留。后台扫描使用独立只读快照，采集写入优先于汇总提交。完整水位之前的迟到采样会丢弃并记录 late_dropped，详见 [数据汇总与保留](docs/storage.md)。`status` 与 `alerts` 通过只读连接查询，程序停止后仍可使用。

原始批次写入失败后保留数据，以 0.5/1/2 秒递增至最多 30 秒重试；默认待提交缓冲上限 10000 条，另有 2048 个有界请求槽位。缓冲溢出只丢最旧原始采样，日志记录丢弃数量；告警、事件和通知操作不按采样丢弃。单次操作失败返回调用方，写入协程继续服务；`Flush` 失败须重试，只有成功才确认当时仍保留的采样已提交。关闭时做最后一次提交，仍失败则返回错误。

`internal/rules` 按最新状态和探测结果评估进程不在、端口关闭、探测失败规则，支持持续时长及触发/恢复生命周期。unknown 不新建或恢复告警，会取消 pending；告警和事件同事务保存。重启只补发最近 24 小时尚无对应状态、对应渠道成功记录的转换，已成功的渠道跳过。

还支持指标阈值、服务停止、脚本失败和 SQL 多实例规则。renotify 配置重复提醒；alerts ack 停止该次告警的提醒，silence add 按规则或目标静音。10 分钟内第四次触发进入抖动合并并改发一张 digest。`rules test` 用历史时钟回放已保存规则或 YAML 草稿，不发送通知；SQL 只读连接过滤未来数据并限制可读表。配置、边界和示例见 [规则与回放](docs/rules.md)。

采集器每 10 秒发送心跳；监控器按自己的接收时间维护 last_seen，为全部未吊销的配对采集器自动启用 30 秒失联告警，重新连接后恢复同一告警。可用 collector_offline 全局规则修改 for、严重程度、渠道和模板，不指定 target；无显式策略时默认 warning、通知全部当前渠道。无目标采集器也发送心跳，补传采样不会延长在线状态。详见 [失联规则](docs/transport.md#心跳与失联)。

Windows 服务命令为 install、uninstall 和 service start/stop/status。安装需要管理员，注册固定 lgmon 服务，以 LocalSystem 自动启动、失败 10 秒重启；运行日志为 JSON，启动/异常写应用事件日志。`feishu test --service` 请求监控服务以自身身份读取凭据并发送。交互式 run 不安装服务；真实注册、重启和收件验收尚待本人授权，步骤见 [Windows 服务](docs/windows-service.md)。

目标状态另含独立的 `process`（found/missing/unknown）和 `port`（listening/closed/unknown）事实，不配置对应条件时为空。process_absent 要求配置 process 或 cmdline，只在进程 missing 时触发；port_closed 只在端口 closed 且进程没有 missing 时触发。进程仍在但端口未监听时只报端口故障，进程退出时只报进程故障；命令行不可读时保持未知，避免重复或错误告警。

删除规则或修改规则类型、目标时，旧触发告警以 `closed` 事件关闭，状态存为 resolved，不发送恢复卡片；重启发现规则已不存在时同样处理。关闭写入失败会保留原告警及关闭事件，在后续评估中重试。

密钥通过 `lgmon secret set <名称> --home .\dev-home` 从标准输入保存，终端输入不回显；`lgmon secret list --json` 只返回名称。文件使用机器级 DPAPI 加密，并限制 SYSTEM、Administrators 和创建者访问；没有打印密钥明文的命令。飞书客户端使用官方 Go SDK，通知发送器提供按告警/状态/群的幂等键、重试和每次尝试的 SQLite 审计；运行 `lgmon feishu setup --home .\dev-home` 进行接入。
