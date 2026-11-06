# 数据汇总与保留

监控器在 `config/settings.yaml` 设置保留时长，缺省使用下列值，示例见 [settings.yaml](../examples/settings.yaml)：

```yaml
retention:
  raw: 24h
  minute: 14d
  hour: 365d
```

所有时长字段支持 d，1d 等于 24h，可组合为 1d2h。raw 不得短于 24h，必须满足 raw ≤ minute ≤ hour，允许相等。`lgmon config validate` 按文件、行号报告错误；非法修改不替换上一份有效配置。设置从下一次清理生效，调大不能找回已删除的数据。规则回放的最大范围读取 raw，仍受原评估间隔、10000 次评估和 60 秒总时限限制。

## 汇总表与单位

分钟、小时按 UTC 对齐。后台每分钟运行，只计算桶结束至少 30 秒后的数据。分钟从原始表计算，小时从分钟表计算；同一桶重算时替换原值，重复执行不会累加。

| 表 | 分组与保存量 |
| --- | --- |
| metric_minute、metric_hour | source_table、collector_id、target_id、name、resource、probe、ts；samples、sum_value、max_value |
| probe_minute、probe_hour | collector_id、target_id、probe、ts；samples、successes、latency_sum |

ts 是桶起点的 UTC 毫秒。系统指标 target_id、resource、probe 为空；磁盘 resource 是盘符路径，网卡 resource 是名称；脚本 probe 是探测下标文本。进程指标先按同一采集时刻、目标合计 CPU/RSS，再按时间桶统计，避免把多进程当成多次采样。

指标名称沿用 system.cpu_percent、system.memory_percent、system.uptime_seconds、disk.used_percent/free_bytes、network.send_bytes_per_sec/receive_bytes_per_sec、process.cpu_percent/rss_bytes、script.指标名；还提供系统总/已用/可用内存、磁盘总/已用字节和网卡累计收发字节。百分比为 0～100，内存/磁盘为字节，网络速率为字节/秒，uptime 为秒。探测耗时是毫秒，成功率是 0～1。

平均值和成功率在查询时计算，不保存均值再对均值求平均。例如从小时表按天计算加权平均：

```sql
SELECT collector_id, ts / 86400000 * 86400000 AS day,
       total(sum_value) / sum(samples) AS cpu_average,
       max(max_value) AS cpu_peak
FROM metric_hour
WHERE name = 'system.cpu_percent'
GROUP BY collector_id, day
```

探测平均耗时为 `total(latency_sum)/sum(samples)`，成功率为 `1.0*sum(successes)/sum(samples)`。聚合多个 resource 或 probe 前按需要筛选，避免把独立资源混为一条曲线。

## 清理与迟到采样

截止时间统一为 `UTC整点(now - 对应保留时长)`。后台扫描走独立只读 WAL 快照，写入阶段只提交预先算好的桶；原始数据和过期汇总每批最多删除 500 行，批间释放写锁，采集和控制请求优先。关闭时取消后台任务，再排空采集写入。

每张原始表在 raw_watermarks 保存 raw_complete_from，与清理同事务更新，且只增不减。先完成相关桶汇总，再清理来源，防止半桶数据影响统计。服务状态不做数值汇总，每个服务最后一条状态仍保留。

- 时间戳不早于完整起点：照常保存，在同一事务标记对应来源的分钟桶；后台重算分钟及所属小时桶。版本检查防止旧快照覆盖刚到达的采样。
- 时间戳早于完整起点：不保存原始采样，已有汇总保持不变。按接收批次、采集器、表合并记录 collector_events，kind=late_dropped；dropped 是行数，source_table 是来源，first_ts/last_ts 是被丢弃数据的 UTC 毫秒范围，事件 ts 是监控器处理时间。事件使用独立的负数 seq，避免与正数传输序号相撞。

接收端会持久确认已记录丢弃审计的序号；重传同一序号不重复写审计，也不把被丢弃的消息交给规则引擎。调整更长保留时长后，完整起点不回退，已经缺失的旧数据仍不会冒充完整来源。

汇总表可供 SQL 规则读取，自动过滤 `桶起点 + 桶长度 <= :now`，不会看到尚未结束的桶。维护元数据表禁止 SQL 规则读取。告警、通知和确认/静音记录不随原始采样清理。

## HTTP 认证数据

M1-10 的令牌位于独立 `data/auth.db`，不参与 lgmon.db 的迁移与保留清理。只保存 SHA-256 哈希、随机令牌的 ID、名称、权限、创建/最后使用及吊销时间，原文只在 CLI 创建时输出一次。认证表不进入监控数据库；SQL 规则和后续只读 SQL 接口无法查询令牌数据。数据目录和数据库采用服务的受保护权限，令牌管理 CLI 需要管理员身份。
