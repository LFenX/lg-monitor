# 系统、服务与脚本采集

collector/both 在目标采集之外独立执行系统指标（5 秒）和 Windows 服务枚举（10 秒）。系统数据使用 gopsutil；CPU 为整机 0～100%，内存和磁盘容量为字节，开机时长为秒，网络速率为字节/秒。CPU 与网络按相邻成功采集的累计计数和时间差计算，首次、计数重置或时钟回退的速率为 0。每块磁盘分别记录，单块读取失败写入本次 errors，其余磁盘仍上报；整轮失败跳过并在下一轮重试。

服务初次枚举上报全部状态，此后只上报状态、启动类型、PID 或可读信息的变化；枚举成功后缺失的服务上报 deleted。读取状态失败为 unknown，启动配置不可读时保留可读取的运行状态、启动类型为 unknown。枚举失败保留上次快照。查询只使用 SCM 连接、枚举、服务状态和配置读取权限，不启停或改写服务；服务启动命令及参数不进入消息。

`match.service` 使用服务名，大小写不敏感，每 2 秒目标周期按名称查询。服务运行时将它的进程和监听端口纳入目标；同时填写 process/cmdline/port 时，所有条件必须命中这个服务的 PID。仅服务条件不依赖进程详情可读性；附加进程条件但详情不可读时为 unknown。stopped/missing 为 down，正在启动/停止等过渡态为 unknown。

脚本配置放在监控器的 targets.yaml，随目标下发采集器：

```yaml
targets:
  - id: backup
    match: {service: demo-backup-service}
    probes:
      - type: script
        script: backup-age.ps1
        args: ["--max-hours", "24"]
        interval: 5m
        timeout: 30s
```

管理员先把脚本放在每台目标采集器的 `<home>\scripts\`，监控器只下发文件名和参数，不上传脚本内容。旧 `run` 字段会被拒绝并提示迁移。文件名不能带路径分隔符、`..`、Windows 设备名或命令控制字符；只接受 `.ps1`、`.cmd`、`.bat`、`.exe`。参数只接受 ASCII 字母、数字、空格和 `_ . , : = / @ + -`，拒绝引号、百分号、脱字符、管道和连接命令等字符。

升级时同步更新监控器与采集器，先迁移监控器 targets.yaml 并在各采集器部署脚本。包含旧 run 的采集配置缓存会拒绝加载；保留备份后按 [集中配置](transport.md#集中配置) 的步骤在线重新获取有效配置，不恢复旧命令写法。

`.ps1` 使用系统 Windows PowerShell 的 `-NoProfile -NonInteractive -ExecutionPolicy Bypass -File`；`.cmd/.bat` 使用系统 `cmd.exe /d /c`；`.exe` 直接运行。工作目录固定为该采集器的 scripts 目录，继承采集器的账户与环境。每次执行前检查脚本和目录的所有者及 DACL：只允许 SYSTEM、Administrators、TrustedInstaller 拥有写入、删除、改权限或改所有者的能力；权限不安全时拒绝执行并给出主机和脚本诊断。文件不存在时明确报告哪台采集器缺少哪个脚本。服务 home 的权限准备和 `icacls` 核对见 [Windows 服务](windows-service.md)。

批处理的完整文件路径始终加引号；home 路径含 `%` 或 `!` 时拒绝运行 `.cmd/.bat`，防止 cmd 在引号内仍做环境变量展开。

脚本与采集器拥有同等权限，管理员只放入可信脚本。普通 CreateProcess 子进程继承本次私有 Job，执行结束、取消或超时后清理。依据：[Microsoft 进程创建标志](https://learn.microsoft.com/en-us/windows/win32/procthread/process-creation-flags)、[Job 对象和子进程继承限制](https://learn.microsoft.com/en-us/windows/win32/procthread/job-objects)。

stdout 使用 UTF-8，最后一个非空行必须为单个 JSON 对象，ok 为必填布尔值；message 和 metrics 可省略：

```json
{"ok":true,"message":"最近备份 2 小时前","metrics":{"backup_age_hours":2}}
```

ok=false、退出码非 0、超时或无效输出均失败，附 stdout/stderr 合并输出的最后 20 行。stdout 只保留末尾 64 KiB，诊断最多 32 KiB；message 最多 4096 字节。metrics 最多 256 项，名称必须符合 `[A-Za-z_][A-Za-z0-9_.-]{0,127}`，值必须是有限 JSON 数字，不能是字符串或 null。脚本结果、message、指标和失败诊断会持久保存，输出中不要放凭据。

系统、服务和脚本结果统一经过持久 outbox、mTLS 与监控器序号去重。SQLite 新增 system_metrics、disk_metrics、network_metrics、service_status 和 script_metrics；探测仍在 probe_results，新增 type/message。脚本指标按 `(collector_id,target_id,probe,ts,name)` 保存，probe 是从 0 开始的探测下标。原始指标默认保留 24 小时，分钟/小时汇总默认 14/365 天，可在 settings.yaml 配置，详见 [数据汇总与保留](storage.md)。服务只报变化，清理历史时保留每个服务的最后一条状态。新增条件及 SQL 规则见 [规则与回放](rules.md)。

默认测试使用假源；`go test -tags integration ./internal/collector` 只读系统和 SCM，脚本测试只创建并清理自己的父子进程，不注册或操作服务。

## HTTP 关键字与 TLS

HTTP 探测可增加 keyword 和 keywordAbsent，例如：

```yaml
type: http
url: https://blog.example.com/
interval: 30s
timeout: 10s
expectStatus: 2xx
keyword: '欢迎来到'
# keywordAbsent: true
```

状态码先判断，不符合时不读取关键字。keyword 为区分大小写的 UTF-8 子串，1～256 字节；keywordAbsent 必须同时有 keyword，为 true 时要求正文没有该子串。只检查 gzip 自动解压后的前 1 MiB，32 KiB 固定块保留关键字长度减一的交叠，不读取或保存完整正文，不转换 GBK 等字符集。失败原因中的关键字最多 64 个字符。这两个字段不能用于 TCP 或脚本探测。

每次 HTTPS 都重新建立连接并验证证书，耗时包含连接与 TLS 握手，不复用旧连接。记录叶子和中间证书中最早的到期时间，排除自签名根；验证失败仍从错误携带的链提取。已过期、域名不符、不受信任分别给中文原因，不在原因中输出 URL。tlsNotAfter 为 Unix 毫秒，0/缺省表示没有证书；tlsSubject 取所选证书 CN，没有时取第一个 DNS 名称，最多 200 字符。HTTP 响应失败但已完成的 TLS 握手仍保留已验证的证书。

采集器声明 http_checks；向没有该能力的采集器分配 keyword/keywordAbsent 时，该版配置不下发并显示 outdated，继续原配置。证书规则见 [规则](rules.md)，查询和保留见 [存储](storage.md)。

## 服务开关导入

services.json 可按需生成目标与探测，参见 [服务开关导入](service-switch.md)。固定位置仅在采集器本机设置，精简结果不含环境、启动命令和脚本内容；预览不新增监控、不执行操作。

## Windows 事件日志

默认只读 Application 的 Application Error/1000、Application Hang/1002，以及 System 的 Service Control Manager/7031、7034、EventLog/6008、Microsoft-Windows-Kernel-Power/41。能力名 eventlog；不读取 Security，不注册事件来源，不清理系统事件日志。

可在目标中增加 `eventlog: {channel: Application, provider: FakeApplication, ids: [1, 2], levels: [error, critical]}`，这样的目标允许省略 match。来源名称精确匹配；ids、levels 省略或空数组表示不限。级别支持 always、critical、error、warning、info、verbose。默认预设与自定义条件合成每个日志一个原生 pull 订阅，消息最多 2 KiB，渲染失败使用 EventData 文本，不上报完整 XML。

首次从当前日志末尾开始；之后按持久书签续读，事件和书签在同一 Outbox 事务保存。新增或修改订阅从采用配置时的记录号之后开始，生效边界跨重启保留；已有订阅继续处理积压。书签失效时从现存最早记录续读，写 eventlog_gap。两个日志共用每秒 100 条限额，超出的数量写 eventlog_dropped，并随已消费书签提交。

归属在采集时冻结：预设由来源和 ID 确定，进程/服务名确定 subject，自定义条件确定 subscription。同一目标同时命中表示 both；最多 32 个不同目标，先保留 subject/both，再按各组目标 ID 排序。SCM 的显示名通过配置目标的只读服务查询换成服务名；无法解析的显示名不猜测为其他服务。历史归属不随配置改写。规则与诊断见 [规则](rules.md) 和 [模板](card-templates.md)。

## 即时进程事件

Windows x64 的 collector/both 默认 `processEvents.mode: auto`，省略或空值同 auto；`disabled` 保留原间隔采样。其他值校验报错。该设置只读取采集器本机 `config/settings.yaml`，修改后重启采集器生效，不随监控器的目标快照下发。

```yaml
processEvents:
  mode: auto
```

有进程、目录、端口或服务匹配目标时，创建随机名称 `lgmon-process-<PID>-<随机值>` 和独立 GUID 的实时 ETW 会话，只跟踪进程启动/退出。进程事件是核验提示：每 200ms 合并相关唤醒，在原串行循环读取最新进程/监听端口/必要服务事实，沿用 `(PID, createdAt)` 匹配、unknown、状态消息、持久缓存、mTLS、SQLite 和规则。延迟退出事件不能直接把已复用的 PID 判为 down。普通 2 秒采样和 10 秒全量快照继续运行，保留指标和故障回退。

源只提取事件类型、PID、进程名和接收时间，不提取命令行、SID 或进程内容。最近匹配 PID 与目标进程名筛选退出/启动提示；复杂匹配及中文等非 ASCII 进程名接收全部启动提示，再由原快照核验，避免依赖 ANSI 图像名误过滤。目标热更新替换兴趣集合，清空目标或取消运行时释放自己的会话。唤醒队列容量为 1，突发被合并；源缓冲为每块 64 KiB、4～16 块，每 100ms 刷新本次会话，发生丢失即核验当前事实，不能据此恢复两个快照之间已经结束的短暂进程历史。

只有自有会话创建成功且实时消费者打开后，日志才出现 `Process event source ready`。权限、会话容量、解码或流错误出现 `Process event source unavailable; using interval sampling`，继续原周期采集，每 10 秒重试；恢复 ready 后立即核验。`Process event source lost events; reconciling current facts` 表示源报告丢失。若自有会话或消费者清理失败，记录 `Process event cleanup unconfirmed; automatic retry disabled` 和自己的会话名，本次运行停止自动创建更多会话，等待管理员核实该会话；不能停止其他现有跟踪会话来恢复。

ETW 系统提供者受本机权限和会话资源限制；程序只临时启用当前进程令牌中已有的 `SeSystemProfilePrivilege` 并恢复，不为账户分配新权限，不注册 AutoLogger、不写 ETL、不修改或停止 `NT Kernel Logger` 或其他已有会话，也不安装驱动或系统服务。原生布局仅启用 Windows x64，其他平台明确回退。API 依据：[Microsoft SystemTraceProvider](https://learn.microsoft.com/en-us/windows/win32/etw/configuring-and-starting-a-systemtraceprovider-session)、[会话属性](https://learn.microsoft.com/en-us/windows/win32/api/evntrace/ns-evntrace-event_trace_properties)、[实时消费](https://learn.microsoft.com/en-us/windows/win32/api/evntrace/ns-evntrace-event_trace_logfilew)。

工程原生测试只启动/退出自建子进程，先等待真实 ready，把普通周期设为 30 秒，独立断言退出到有效目标 down 小于 1 秒，并核对同一状态写入临时 SQLite、触发 process_absent 及假通知；另查询本次会话已关闭。运行：

```powershell
go test -count=1 -tags integration ./internal/collector -run '^TestNativeProcessEvent' -v
go test -count=1 -tags integration ./internal/app -run '^TestNativeProcessEvent' -v
```

LocalSystem 真实服务、双机和真实飞书收件继续 Pending。本人验收须先批准安装/重启本产品服务，确认 ready 后只退出自建演示目标，记录退出、监控器 down、规则和收件时间；再在目标采集器本机改为 disabled，重启后确认回退日志与周期采样。不可把工程夹具或无权限回退标为这些人工步骤已通过。
