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
