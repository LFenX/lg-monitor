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

## HTTP 证书数据

probe_results 增加 tls_not_after INTEGER（未取得时为 NULL）和 tls_subject TEXT（缺省空串）。它们和原始探测一起保留、清理，不把证书日期做平均或写进数值汇总。v_probe_certificates 按 collector_id、target_id、probe 提供最近一次带证书的 ts、tls_not_after、tls_subject，同时间戳取最后写入的记录；暂时的无证书失败不擦掉这条观测。原始行清理后相应视图数据也消失，不能冒充长期历史。

alerts.cert_json 和 alert_events.cert_json 保存 cert_expiry 对应时刻的证书快照（notAfter 毫秒、subject），其他告警为 JSON null；触发和恢复各保留自己的快照，供重启恢复和通知补发使用，不随原始探测清理。Schema 接口从迁移说明导出这些列。视图保留主机/目标列供面板使用；和其他当前视图一样不能在 SQL 规则中读取，规则直接查按 :now 截断的 probe_results。

## HTTP 认证数据

M1-10 的令牌位于独立 `data/auth.db`，不参与 lgmon.db 的迁移与保留清理。只保存 SHA-256 哈希、随机令牌的 ID、名称、权限、创建/最后使用及吊销时间，原文只在 CLI 创建时输出一次。认证表不进入监控数据库；SQL 规则和后续只读 SQL 接口无法查询令牌数据。数据目录和数据库采用服务的受保护权限，令牌管理 CLI 需要管理员身份。

## 通用只读查询与表结构

M1-11 的 CLI query 和 HTTP query 共用 View 查询实现，单独用 `mode=ro` 打开监控数据库，并设置 `PRAGMA query_only=ON`。监控器给通用 SQL 额外打开一个只读 View，避免长 SQL 占用状态/历史读取连接。SQL 只允许单条 SELECT/WITH，查询预算五秒，超过 10000 行时保留前 10000 行并标记 truncated；连接中断后可以继续查询。

`GET /api/v1/schema` 与 `lgmon query --schema --json` 返回同一结构，按实际数据库的表/视图和列逐项读取。类型、空值约束、默认 SQL 表达式和主键位置来自 SQLite 元数据；说明写在现有迁移文件的英文数据注释里，由程序按迁移顺序解析，不新增迁移版本或改变 DDL：

```sql
-- @table example Example table used to describe the annotation format.
-- @column example.id Stable example identity.
```

多个表列可用逗号共享一条说明，后续迁移中的同名注释更新旧说明。当前 27 张监控表的每个字段均有说明；表结构接口不读取其他数据库。HTTP 与 CLI 示例见 [api.md](api.md) 和 [cli.md](cli.md)。

SQLite 的只读 URI 参数见[官方 URI 文档](https://www.sqlite.org/uri.html)，query_only 与用于读取列信息的 PRAGMA 表函数见[官方 PRAGMA 文档](https://www.sqlite.org/pragma.html)。

## 通知提交与跳过

M1-12 的迁移 009 为 notifications 增加 skipped 与 status_code，旧记录默认 0，保留已有成功标志与消息回执。status_code 保存通用 Webhook 的 HTTP 状态，尚无 HTTP 响应及其他渠道为 0；不保存响应正文。ok=1 表示渠道接受了提交，不能证明本人收到。只有提供回执的渠道填 message_id；Webhook 与 Windows 渠道保持空值，不生成虚假提供方消息 ID。

没有登录用户、用户会话无法访问或 Windows 通知被禁用时，记录 skipped=1、ok=0 及中文原因，日志说明跳过，不推进 alerts.last_notified_at。重启补发将成功渠道与跳过渠道分别识别，均不重复发送同一事件；网络失败仍保留补发资格。告警状态、事件键和通知渠道共同确定补发范围，恢复和再次通知相互独立。

Webhook 的地址、签名密钥位于 DPAPI 密钥库，配置只保存引用；不进入 lgmon.db、SQL 结果或通知错误文本。管理员更新凭据先保存新加密条目，再原子发布新配置引用；发布失败保留原生效配置。

filter 筛掉的渠道不写失败记录，不重试，不改变告警状态。最近 24 小时的未成功事件在重启时按当前 filter 再判断。每个渠道独立顺序发送；内存队列随退出释放，未完成通知依据持久事件和审计恢复。

## 采集器时钟

迁移 011 给 collectors 增加 clock_offset_ms，保存监控器时间减采集器时间的估计值。首次超过正负十秒写 clock_skew，恢复至十秒以内写 clock_synced；持续偏差期间更新估计值但不重复写事件。偏差和事件在同一事务里提交，失败一起回滚。

这两类 collector_events 使用独立负序号，与 late_dropped 共用“当前最小非正序号减一”的分配方式，不消费传输序号。gap_from、gap_to、dropped 为零。事件时间采用监控器时钟，采样表仍保存采集器上报的原始时间；记录补传判断的校正值不会改写历史数据。
