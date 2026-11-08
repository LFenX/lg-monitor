# 规则与回放

规则保存在监控器 `config/rules.yaml` 的 `rules` 数组，修改后严格校验并热加载。每条规则设置 id、type、severity 和 notify，name、for、renotify、template 可选。for 默认 0s，cert_expiry 禁止填写 for；renotify 必须是正数时长。通知渠道和目标必须存在。

## 内置条件

| type | 条件和前提 |
| --- | --- |
| process_absent | target 配置 process/cmdline/exeDir/workDir，进程明确 missing |
| port_closed | target 配置 port，端口 closed 且进程没有 missing |
| probe_failed | target 的探测明确失败 |
| collector_offline | 不填 target，按监控器接收的心跳时间判断；省略 for 为 30s |
| service_stopped | target 配置 match.service；stopped/missing/deleted/paused 触发，启动和停止的中间状态为 unknown |
| script_failed | target 配置 script 探测，只检查这些脚本的失败结果 |
| metric_threshold | metric、op、threshold 定义阈值；op 为 gt/gte/lt/lte/eq/ne |
| sql | query 返回的 key 定义独立告警实例 |
| cert_expiry | target 的 HTTPS 探测证书剩余时间不超过 within；每采集器/目标/探测独立 |

证书规则示例：

```yaml
id: blog-cert
type: cert_expiry
target: blog
within: 14d
# probe: 0
severity: warning
notify: [feishu-alerts]
```

within 默认 14d，范围 1h～365d；不接受 for（包括 0s）。probe 下标从 0 开始，省略时对目标每个 HTTPS 探测建立独立实例，实例键为“采集器/目标/probe/下标”。有证书信息且到期时间不晚于当前时间加 within 时触发；新证书超过阈值时恢复。没有证书信息的失败结果算未知，不能恢复或新触发，probe_failed 负责网站不可访问。最后一次有效证书仍保留用于展示；未知期间不根据旧证书新建告警。

日期按监控器本地时区写入摘要，剩余天数向上取整；到期时间仍是精确毫秒，规则阈值不按整天取整。卡片和通用 Webhook 带证书快照，延迟通知和重启不被后来的续期覆盖。静音、确认、提醒、补传门控与其他内置规则共用；回放读取原始证书结果并按历史评估时间计算。

metric 支持下列名称。进程和脚本指标必须指定 target，其他指标可省略 target；省略时对各采集器独立评估。

| metric | 单位和选择 |
| --- | --- |
| system.cpu_percent、system.memory_percent | 0～100 百分比 |
| system.uptime_seconds | 秒 |
| disk.used_percent、disk.free_bytes | 百分比、字节；resource 可选磁盘名 |
| network.send_bytes_per_sec、network.receive_bytes_per_sec | 字节/秒；resource 可选网卡名 |
| process.cpu_percent、process.rss_bytes | 匹配进程之和；CPU 按整机容量归一化，内存字节 |
| script.指标名 | 脚本输出的有限数字；probe 可指定从 0 开始的 script 探测下标 |

未选择 resource 时，任意一块磁盘或网卡满足条件即触发，不对网卡求和。没有匹配资源或指标时为 unknown。unknown 不新建、不恢复告警，取消尚未触发的 pending。

采集消息比监控器当前时间早 30 秒以上时，该采集器进入补传状态。历史数据照常入库、更新条件，但内置目标和指标规则暂停触发、恢复及通知；其他采集器照常评估。收到时间回到 30 秒以内的消息后，按最新条件恢复评估。持续至今的故障使用历史条件开始满足的时间作为 startedAt 和 for 起点，已经达到 for 时立即触发一次；历史中的 unknown 或恢复会打断持续时间。离线前已触发的告警保留原身份，补传中间的短暂恢复不发卡片。SQL 和 collector_offline 仍按监控器当前时间评估。

离线期间开始又结束的故障不单独发卡片，可用 `lgmon rules test` 回放查看；回放不受传输补传门控影响。

以下是规则对象示例，放入 rules 数组前替换为已有目标和通知渠道：

```yaml
id: high-cpu
name: CPU 持续过高
type: metric_threshold
metric: system.cpu_percent
op: gt
threshold: 90
for: 5m
renotify: 1h
severity: warning
notify: [feishu-alerts]
```

## SQL 条件

```yaml
id: slow
type: sql
interval: 1m
for: 5m
severity: warning
notify: [feishu-alerts]
query: |
  SELECT collector_id || '/' || target_id AS key,
         '最近 5 分钟平均耗时 ' || CAST(avg(latency_ms) AS INTEGER) || ' ms' AS summary
  FROM probe_results
  WHERE ts > :now - 300000
  GROUP BY collector_id, target_id
  HAVING avg(latency_ms) > 2000
```

interval 默认 1m，最小 10s；每次绑定 `:now`，单位为 UTC 毫秒。必须返回非空、非 NULL 的 key，值转换为文本；可选 summary。相同 key 多行合并，保留第一行摘要；最多返回 100 行。key 出现表示条件满足，消失表示恢复，for 按同一生命周期处理。SQL 没有 target 时，告警 targetId 为 `@sql`，collectorId 为 `@monitor`，真实主机/目标由实例 key 和摘要表达。

SQL 每次执行最长 5 秒，查询在引擎锁外运行。出错时全部实例按 unknown 处理，连续失败记录日志，`lgmon status` 显示最近错误。已触发告警不会因为查询失败而恢复。

可读取 target_status、probe_results、process_samples、system_metrics、disk_metrics、network_metrics、service_status、script_metrics、collector_events；全部自动过滤 `ts <= :now`。collectors 只提供 first_seen 不晚于评估时刻的 id、hostname、first_seen、version；判断失联请使用 collector_offline。

专用连接为 mode=ro、query_only，并在每条物理连接上安装授权回调。规则不能读取告警/通知、传输内部表、sqlite_schema、pragma_* 或未登记视图。只接受单条 SELECT/WITH，可用自己的 WITH、WITH RECURSIVE、相关子查询、json_each、ORDER BY 和 LIMIT。

不能指定 main.、temp.（包括加引号的库名），直接使用表名。不能使用 CURRENT_TIMESTAMP/DATE/TIME、字面量 'now' 或不带时间参数的日期函数，使用 `:now - 300000` 代替 `datetime('now','-5 minutes')`；显式转换 `datetime(ts/1000,'unixepoch')` 可用。

## 通知控制

```powershell
.\bin\lgmon.exe alerts ack <告警ID> --home .\dev-home\monitor
.\bin\lgmon.exe silence add --rule slow --for 2h --reason "维护" --home .\dev-home\monitor
```

ack 只停止该告警的重复提醒，恢复仍通知；新一轮故障使用新告警 ID。silence 按 rule 或 target 选择一项，静音期间状态正常保存，触发、提醒和恢复消息均抑制，重启不补发被静音的转换。

10 分钟内同一规则、采集器、目标和实例的第四次触发进入抖动，发一张 digest。之后抑制消息，直到连续 10 分钟没有状态变化；提醒不延长抖动。策略和确认持久化，重启有效。

## 规则试运行

```powershell
.\bin\lgmon.exe rules test slow --since 24h --json --home .\dev-home\monitor
.\bin\lgmon.exe rules test --file .\draft.yaml --since 1h --json --home .\dev-home\monitor
```

草稿只写一条上方的规则对象，不加 rules 数组；不会保存或改变实际配置。输出结构、错误和部分结果含义见 [CLI](cli.md)。

SQL 用独立只读连接，按原 interval 从起点逐次执行，不开覆盖整个回放的长事务。最多 10000 次评估，整个回放上限 60 秒；超过 settings.yaml 的 retention.raw 会截断，默认 24 小时。内置规则按历史事实及 1 秒时钟驱动相同引擎；collector_offline 没有历史心跳，拒绝回放。

回放从没有告警开始，起点附近第一次触发会晚于实际；统计窗口在起点附近的数据不完整。按数据自带时间戳截断，事后离线补传的数据在实际评估时尚未到达，回放能报出当时未发生的告警。实际记录以 alerts 为准。实时 SQL 也过滤未来时间戳：采集器时钟领先时，要等监控器时钟追上才可见。

回放不写实际告警和审计、不发通知，不模拟静音、确认、重复提醒或抖动合并。

实时补传判断使用按主机估计的时钟偏差：将记录时间加上 offset 后，与监控器时间比较，仍以严格超过 30 秒判为补传。Hello 在连接建立时提供初始估计，随后取最近六次实时心跳的最小偏差，排队延迟不会把一次迟到心跳直接当作时钟改变。正在补传的故障仍不触发或恢复，追平后按最新状态判断。

补传故障的 for 按校正后的经过时间计算，通知保留数据里的原始故障起点；已落库的采样时间不会平移。主机卡片或 lgmon status 提示“时钟快/慢”时，请同步该主机时间。SQL 规则继续使用原始数据时间和监控器的 :now，快时钟仍会延迟数据可见性；历史回放也按原始时间进行。时钟提示不能替代同步时间。

## 事件日志规则

```yaml
rules:
  - id: application-crashes
    type: eventlog
    preset: app_crash
    count: 2
    window: 10m
    severity: critical
    notify: [ops]
```

预设为 app_crash、service_crash、unexpected_shutdown。省略 target 时对各采集器独立评估；指定进程或服务目标并填写对应 preset 时，必须同时命中该预设与该目标的 subject 归属。带自定义 eventlog 的目标可以只指定 target，不写 preset，按 subscription 归属判断，both 同时满足两类归属。

count 默认为 1，范围 1～8640000；window 默认为 10m，范围 1s～24h。达到次数立即触发，不接受 for；触发之后只更新次数和最近事件，直到整个 window 没有事件才恢复。窗口左侧不包含边界、右侧包含边界。重复提醒、确认、静音遵循现有通知策略。

窗口按原始事件时间加采集器时钟偏差评估；已过窗口的补传仅落库，不产生新通知。重启从持久事件查询窗口，已触发实例不会重复触发。回放加载窗口起点之前所需的保留事件，按原始时间和冻结归属运行，不发送消息。旧采集器没有 eventlog 能力时，规则错误提示升级；包含自定义订阅的配置不会发给旧版采集器。

process_absent、service_stopped 告警保存第一次 down 的原始采样时间，供通知和网页查询前后 60 秒事件。网页“事件详情”可分页、刷新迟到事件，不受通知最多五条的限制。
