# lg-monitor

Windows 服务监控：采集器盯住电脑上的进程、服务、端口和网站，监控器汇总多台电脑的数据、出面板、通过飞书发告警。所有能力都开放给编程 Agent。

状态：正在执行 M0 单机原型，当前实现项目骨架、配置管理、进程与 TCP 监听采集接口。需求见 [docs/PRD.md](docs/PRD.md)，任务与验证记录见 [docs/PLAN.md](docs/PLAN.md)。

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

TCP 探测使用 `address: 主机:端口`；IPv6 地址写成 `address: "[::1]:3000"`。规则省略 `for` 时表示 `0s`，探测的 `interval` 和 `timeout` 必须显式配置正时长；通知渠道省略 `template` 时由后续通知模块使用标准模板。自定义模板本轮只解析结构及检查引用，渲染和预览在 M0-10 实现。

配置热加载接口位于 `internal/config`：`NewFileWatcher` 监听配置及模板目录，`Manager` 只发布完整校验通过的配置快照，错误修改会保留上次有效配置并记录错误。运行命令将在 M0-12 组装该接口。

Windows 目标匹配不区分进程名大小写，`process: node` 同时匹配 `node` 和 `node.exe`。命令行正则默认不区分大小写，例如路径盘符大小写不同仍能匹配；需要区分时在表达式前加 `(?-i)`。

进程与端口采集接口位于 `internal/collector`：`ProcessSource.Snapshot` 返回带 `(PID, 创建时间)` 身份的进程快照；Windows 核心字段通过一次 `NtQuerySystemInformation` 读取，无需逐个打开进程。路径和命令行按身份缓存（含失败结果），进程退出后清理；无权读取命令行时留空并设置 `cmdlineUnavailable`。`PortSource.ListeningTCP` 返回 TCP 监听地址、端口及 PID。创建时间使用 UTC Unix 毫秒，RSS 使用字节；CPU 以相邻成功采样的用户态和内核态时间差除以整机逻辑处理器总数计算，范围为 0～100%，全部核心满载为 100%，首次采样为 0；采集超过一秒记录警告。

真实 Windows 集成测试只操作自身创建的 `ping` 进程和回环监听端口：`go test -tags integration ./internal/collector`。默认 `go test ./...` 使用假实现。
