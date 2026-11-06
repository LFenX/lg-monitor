# lg-monitor

Windows 服务监控：采集器盯住电脑上的进程、服务、端口和网站，监控器汇总多台电脑的数据、出面板、通过飞书发告警。所有能力都开放给编程 Agent。

状态：正在执行 M0 单机原型，当前实现项目骨架与配置加载、校验及热加载。需求见 [docs/PRD.md](docs/PRD.md)，任务与验证记录见 [docs/PLAN.md](docs/PLAN.md)。

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
.\bin\lgmon.exe config schema
.\bin\lgmon.exe config schema --output .\schemas
```

校验失败会列出文件、行号与中文错误，退出码为 1。Schema 从 Go 结构体生成；`config schema` 默认输出到当前工作目录的 `schemas/`。跨文件引用和时长数值限制以 `config validate` 为准。

TCP 探测使用 `address: 主机:端口`；IPv6 地址写成 `address: "[::1]:3000"`。规则省略 `for` 时表示 `0s`，探测的 `interval` 和 `timeout` 必须显式配置正时长；通知渠道省略 `template` 时由后续通知模块使用标准模板。自定义模板本轮只解析结构及检查引用，渲染和预览在 M0-10 实现。

配置热加载接口位于 `internal/config`：`NewFileWatcher` 监听配置及模板目录，`Manager` 只发布完整校验通过的配置快照，错误修改会保留上次有效配置并记录错误。运行命令将在 M0-12 组装该接口。
