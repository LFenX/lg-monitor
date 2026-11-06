# lg-monitor

Windows 服务监控：采集器盯住电脑上的进程、服务、端口和网站，监控器汇总多台电脑的数据、出面板、通过飞书发告警。所有能力都开放给编程 Agent。

状态：M0 工程与验收脚本已具备，真实飞书接入和群收件仍待人工验收；正在推进 M1，已接入 gRPC+mTLS、指纹配对、证书吊销及持久 outbox/断线补发。配置下发等按后续任务实现。完整命令见 [docs/cli.md](docs/cli.md)，需求见 [docs/PRD.md](docs/PRD.md)，任务与验证记录见 [docs/PLAN.md](docs/PLAN.md)。

- 语言：Go
- 采集器到监控器：gRPC + 双向 TLS，采集器主动连出
- 存储：SQLite
- 面板：React + ECharts，嵌入在程序里

安装 Go 1.27 和 Git 后，在 PowerShell 中构建：

```powershell
.\scripts\build.ps1
.\bin\lgmon.exe version
.\bin\lgmon.exe --help
```

构建时可用 `-Version 0.1.0` 指定版本号；提交号和 UTC 构建时间自动注入。所有子命令支持 `--home` 和 `--log-level`；数据目录依次采用 `--home`、`LGMON_HOME`、`%ProgramData%\lg-monitor`。

配置文件位于数据根目录下的 `config/`：`targets.yaml`、`rules.yaml`、`notifiers.yaml` 及 `card-templates/*.yaml`。示例见 [examples/](examples/)，其中飞书标识均为虚构值。配置缺失时视为空，校验命令不创建文件或配置目录。

```powershell
.\bin\lgmon.exe config validate --home .\dev-home
.\bin\lgmon.exe config validate --home .\dev-home --json
.\bin\lgmon.exe config schema
.\bin\lgmon.exe config schema --output .\schemas
```

校验失败先显示错误总数，再逐行列出文件、行号与中文错误，退出码为 1。`config validate --json` 在标准输出返回 `{"ok":false,"errors":[{"file":"rules.yaml","line":5,"message":"错误说明"}]}`；通过时返回 `{"ok":true,"errors":[]}`。Schema 从 Go 结构体生成；`config schema` 默认输出到当前工作目录的 `schemas/`。跨文件引用和时长数值限制以 `config validate` 为准。

TCP 探测使用 `address: 主机:端口`；IPv6 地址写成 `address: "[::1]:3000"`。规则省略 `for` 时表示 `0s`，探测的 `interval` 和 `timeout` 必须显式配置正时长；通知渠道省略 `template` 时使用标准模板。自定义模板支持渲染、校验和预览，详见 [卡片模板](docs/card-templates.md)。

配置热加载接口位于 `internal/config`：`NewFileWatcher` 监听配置及模板目录，`Manager` 只发布完整校验通过的配置快照，错误修改会保留上次有效配置并记录错误。`lgmon run --role both` 组装采集、存储、规则和通知，Ctrl+C 依次停止采集、排空写入与通知、关闭数据库。

Windows 目标匹配不区分进程名大小写，`process: node` 同时匹配 `node` 和 `node.exe`。命令行正则默认不区分大小写，例如路径盘符大小写不同仍能匹配；需要区分时在表达式前加 `(?-i)`。

进程与端口采集接口位于 `internal/collector`：`ProcessSource.Snapshot` 返回带 `(PID, 创建时间)` 身份的进程快照；Windows 核心字段通过一次 `NtQuerySystemInformation` 读取，无需逐个打开进程。路径和命令行按身份缓存（含失败结果），进程退出后清理；无权读取命令行时留空并设置 `cmdlineUnavailable`。`PortSource.ListeningTCP` 返回 TCP 监听地址、端口及 PID。创建时间使用 UTC Unix 毫秒，RSS 使用字节；CPU 以相邻成功采样的用户态和内核态时间差除以整机逻辑处理器总数计算，范围为 0～100%，全部核心满载为 100%，首次采样为 0；采集超过一秒记录警告。

真实 Windows 集成测试只操作自身创建的 `ping` 进程和回环监听端口：`go test -tags integration ./internal/collector`。默认 `go test ./...` 使用假实现。

`internal/app.Collector` 默认每 2 秒采集，状态变化立即发送，每 10 秒发送全量目标快照；被匹配进程的 CPU/RSS 和独立探测结果走同一个 `pipeline.Sink/Source`。每条消息包含采集器 ID、递增序号和 UTC 毫秒时间戳。`FileIDStore` 将 UUID 保存在数据根目录的 `data/collector-id`，重复启动保留身份；配置订阅更新后会停止旧探测并重建调度。

单轮采集失败会记录连续失败次数并跳过本轮，下轮继续；不会用空快照代替失败结果。规则保存失败保留原告警 ID 和事件，下次评估重试；通知入队失败保留待通知状态，并保证触发先于恢复入队。

命令行只保留在本机采集快照中用于匹配。pipeline 的 `ProcessRef` 只包含 PID、父 PID、创建时间、名称、路径、CPU、RSS 和线程数，类型中没有命令行字段，避免传输或数据库保存命令行里的凭证。

`internal/store` 使用纯 Go SQLite，自动执行内嵌版本迁移，启用 WAL。单一写入协程每 500ms 或 500 条提交；`Write` 表示入队，`Flush` 等待落盘，`Close` 排空尾批。原始采样每小时清理，保留最近 24 小时；告警审计独立保留。`status` 与 `alerts` 通过只读连接查询，程序停止后仍可使用。

原始批次写入失败后保留数据，以 0.5/1/2 秒递增至最多 30 秒重试；默认待提交缓冲上限 10000 条，另有 2048 个有界请求槽位。缓冲溢出只丢最旧原始采样，日志记录丢弃数量；告警、事件和通知操作不按采样丢弃。单次操作失败返回调用方，写入协程继续服务；`Flush` 失败须重试，只有成功才确认当时仍保留的采样已提交。关闭时做最后一次提交，仍失败则返回错误。

`internal/rules` 按最新状态和探测结果评估进程不在、端口关闭、探测失败规则，支持持续时长及触发/恢复生命周期。unknown 不新建或恢复告警，会取消 pending；告警和事件同事务保存。重启只补发最近 24 小时尚无对应状态、对应渠道成功记录的转换，已成功的渠道跳过。

目标状态另含独立的 `process`（found/missing/unknown）和 `port`（listening/closed/unknown）事实，不配置对应条件时为空。process_absent 要求配置 process 或 cmdline，只在进程 missing 时触发；port_closed 只在端口 closed 且进程没有 missing 时触发。进程仍在但端口未监听时只报端口故障，进程退出时只报进程故障；命令行不可读时保持未知，避免重复或错误告警。

删除规则或修改规则类型、目标时，旧触发告警以 `closed` 事件关闭，状态存为 resolved，不发送恢复卡片；重启发现规则已不存在时同样处理。关闭写入失败会保留原告警及关闭事件，在后续评估中重试。

密钥通过 `lgmon secret set <名称> --home .\dev-home` 从标准输入保存，终端输入不回显；`lgmon secret list --json` 只返回名称。文件使用机器级 DPAPI 加密，并限制 SYSTEM、Administrators 和创建者访问；没有打印密钥明文的命令。飞书客户端使用官方 Go SDK，通知发送器提供按告警/状态/群的幂等键、重试和每次尝试的 SQLite 审计；运行 `lgmon feishu setup --home .\dev-home` 进行接入。
