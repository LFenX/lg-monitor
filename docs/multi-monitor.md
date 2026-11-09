# 同一采集器连接多个监控器

一个 collector 进程可以同时向默认监控器和最多 7 个附加监控器报告。它们使用同一个本机 collector ID，各自核对 CA、签发证书、下发目标并保存数据。both 角色的默认连接是本机监控器；monitor 角色不启动采集。

## 配置步骤

以下主机名、配对码和指纹均为占位符，实际配对须使用本人确认的监控器与指纹。保留原 home 和 `data/collector-id`，不要为附加监控器重新创建本机身份。

1. 在附加监控器运行 `lgmon --home <监控器 home> pair new`，核对它显示的完整 CA 指纹。
2. 在采集器运行 `lgmon --home <采集器 home> pair join --name company --monitor monitor.example:7443 --code <一次性码> --fingerprint <已核对的16位或完整指纹>`。JSON 调用须显式提供指纹，配对码不要写进脚本或聊天记录。
3. 将下列条目合并到采集器本机 `config/settings.yaml`。保留已有设置，随后由有权操作该采集器的人重启。服务安装/重启另需本人授权。

```yaml
collectorMonitors:
  - id: company
    enabled: true
    allowOperations: false
cache:
  maxSize: 500MB
```

`enabled` 省略为 true；`allowOperations` 省略为 false。这些设置只能在采集器本机编辑，HTTP/MCP 管理员也只能保留现值。ID 是小写字母开头的 1～32 位字母、数字、横线，不能为 `default` 或 Windows 设备名。关闭或移除条目后重启即可停止该连接，既有缓存保留。重新启用仍连接原信任域；切换 CA 应使用新的 ID。重配同一 CA 前，按既有流程在对应监控器明确吊销旧证书。

原来的 `pair join --monitor ...` 仍配置默认连接。`run --monitor ...` 只覆盖默认地址，继续核验原 CA。每次启动读取启用列表、证书和授权，因此修改这些内容需要重启采集器。监控器只能修改自己下发的目标配置。

## 数据和故障边界

默认数据路径不变。附加监控器的状态位于 `data/monitors/<id>/` 下的 `pki/`、`collector-config/`、`outbox.db`、`operations.db`。每份私钥在根 home 的 DPAPI vault 内有独立引用；备份和恢复须保留整个 home，以及原 Windows 机器的解密条件。

同名目标、探测、事件日志书签、序列号、确认水位和操作请求分别隔离。一个监控器确认数据不会清理另一个的队列；断网或吊销后其他连接继续运行，恢复后各自补传。证书或缓存错误在日志中带监控器 ID；修复本机错误后管线按 10 秒间隔重试，证书和启用列表变更重启后生效。没有任何有效启用身份时启动明确失败。

原生进程、端口、系统指标、服务全表和启动项事实合并读取并复制给消费者；500ms 内复用一份 CPU/速率基线。进程事件先让缓存失效再触发核验。配置、目标匹配、探测、各连接快照和持久数据始终独立。脚本从根 home 的受保护 `scripts/` 运行，沿用脚本名和安全参数限制。

`cache.maxSize` 是**每槽位**物理预算，默认每槽 500MB。默认加 company 两个启用槽位共预算 1GB；8 个槽位共预算 4GB。实际时长取决于各自负载：每槽独立估算写入率、保留 24h，并在容量不足时按原策略淘汰、记录缺口。移除或关闭槽位不会自动删除磁盘数据。

```powershell
lgmon --home <采集器 home> pair monitors --json
```

该命令只读当前配置和保存的身份/缓存，输出每槽地址、完整指纹、启用、槽位操作授权、身份错误、缓存状态，以及每槽上限、启用槽总预算和所有已配置槽的实际缓存合计。配置值不代表正在运行进程已重启；离线状态仍在对应监控器查看。只列出默认槽和本机配置列出的槽。

附加监控器 `allowOperations: true` 只是本机额外授权开关，仍须满足全局 `remoteOperations.enabled`、逐条服务授权、受保护 LocalSystem 和每次管理员确认。它不能打开全局开关。不同监控器的相同 requestId 分别去重；同一个服务被两个管理员分别授权重启仍会发生两次操作，应由本人安排操作。

## 采集进程资源

Windows collector 角色在生命周期内对当前进程应用 36 MiB 工作集预算，未显式设置 GOMAXPROCS 时最多使用 2 个 Go P；结束时恢复原限制。both/monitor 使用监控器的资源边界。设置工作集失败时明确报错，已有更小的硬上限保留。

此预算控制实际常驻页，虚拟内存和私有提交量可以高于 40 MiB；需要换页的系统会产生额外开销。机制依据[微软的工作集接口](https://learn.microsoft.com/en-us/windows/win32/api/memoryapi/nf-memoryapi-setprocessworkingsetsizeex)。短期夹具验证资源、断网补传和取消；完整 24 小时自监控仍按 PLAN M1-19 原始 cpu/rss 口径由本人验收，保持 Pending。

真实多电脑配对、真实断网/重启与服务启动继续按本人授权操作；工程测试仅使用临时目录、回环连接、自建进程和假重启执行器。
