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
        run: powershell -NoProfile -File C:\checks\backup-age.ps1
        interval: 5m
        timeout: 30s
```

Windows 使用系统 cmd.exe 的 `/d /s /c` 语义执行 run。使用绝对文件路径；脚本继承采集器的账户、环境和工作目录，服务运行时不要依赖用户的当前目录。脚本与采集器拥有同等权限，只配置自己信任的脚本。普通 CreateProcess 子进程继承本次私有 Job，执行结束、取消或超时后清理；这不提供恶意脚本隔离。依据：[Microsoft 进程创建标志](https://learn.microsoft.com/en-us/windows/win32/procthread/process-creation-flags)、[Job 对象和子进程继承限制](https://learn.microsoft.com/en-us/windows/win32/procthread/job-objects)。

stdout 使用 UTF-8，最后一个非空行必须为单个 JSON 对象，ok 为必填布尔值；message 和 metrics 可省略：

```json
{"ok":true,"message":"最近备份 2 小时前","metrics":{"backup_age_hours":2}}
```

ok=false、退出码非 0、超时或无效输出均失败，附 stdout/stderr 合并输出的最后 20 行。stdout 只保留末尾 64 KiB，诊断最多 32 KiB；message 最多 4096 字节。metrics 最多 256 项，名称必须符合 `[A-Za-z_][A-Za-z0-9_.-]{0,127}`，值必须是有限 JSON 数字，不能是字符串或 null。脚本结果、message、指标和失败诊断会持久保存，输出中不要放凭据。

系统、服务和脚本结果统一经过持久 outbox、mTLS 与监控器序号去重。SQLite 新增 system_metrics、disk_metrics、network_metrics、service_status 和 script_metrics；探测仍在 probe_results，新增 type/message。脚本指标按 `(collector_id,target_id,probe,ts,name)` 保存，probe 是从 0 开始的探测下标。原始指标保留 24 小时；服务只报变化，清理历史时保留每个服务的最后一条状态。新增规则类型按 M1-08 实现。

默认测试使用假源；`go test -tags integration ./internal/collector` 只读系统和 SCM，脚本测试只创建并清理自己的父子进程，不注册或操作服务。
