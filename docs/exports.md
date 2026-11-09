# Prometheus 与 OTLP 指标导出

监控器与 both 角色支持两种标准出口，均默认关闭。Prometheus 使用现有 HTTP 监听和 read 令牌；OTLP 使用独立的 HTTP protobuf 指标推送。collector 角色不执行导出。配置在监控器本机 settings.yaml，热更新生效，不向采集器下发，不安装外部平台。

## 开启与认证

保留 settings.yaml 的其他配置，添加：

```yaml
exports:
  prometheus:
    enabled: true
  otlp:
    enabled: false
    endpoint: http://127.0.0.1:4318/v1/metrics
    interval: 30s
    timeout: 10s
    # authorizationRef: fake-otlp-header
    # tls:
    #   caRef: fake-otlp-root
    #   certRef: fake-otlp-client-cert
    #   keyRef: fake-otlp-client-key
```

设置修改沿用 admin 权限。interval 范围 10s～1h，timeout 范围 1s～30s；省略分别为 30s、10s。endpoint 是完整信号 URL，不自动补路径；标准接收器路径为 `/v1/metrics`。HTTPS 验证系统可信根及主机名，可用 caRef 增补自有根、certRef/keyRef 成对启用 mTLS。HTTP 只允许回环 IP 或 localhost，连接时再次检查解析结果；远端使用 HTTPS。拒绝 URL 用户信息、查询、片段、通配/组播主机及跳转，没有关闭证书校验的设置。

authorizationRef 指向完整 Authorization 请求头，例如由本人保存的 Bearer 或 Basic 凭据；YAML 只写引用。使用既有加密密钥命令：

```powershell
.\bin\lgmon.exe secret set fake-otlp-header --home .\dev-home --json
```

终端输入不回显；PEM 证书/私钥通过安全的标准输入管道保存到各自引用，支持多行且最多 64 KiB。引用在同一监控器 home 中以机器级 DPAPI 保存。每个导出周期重新读取值，轮换同名密钥下个周期生效。不要把凭据、PEM 或导出平台的令牌放入命令参数、YAML、仓库或 Agent 对话。证书引用补充到本客户端，不改系统证书库。

Prometheus 使用 read 令牌，与 OTLP 接收端凭据分别管理：

```powershell
$created = .\bin\lgmon.exe token create --scope read --name prometheus --home .\dev-home --json | ConvertFrom-Json
$headers = @{ Authorization = 'Bearer ' + $created.token }
Invoke-WebRequest -UseBasicParsing -Uri 'http://127.0.0.1:7444/metrics' -Headers $headers
```

令牌只显示一次，保存在本人管理的私有凭据文件后，Prometheus 可使用以下配置。示例为同机采集，跨机改成监控器实际可达地址，监听与 Host 白名单规则见 [HTTP API](api.md)。

```yaml
scrape_configs:
  - job_name: lgmon
    metrics_path: /metrics
    scrape_interval: 30s
    authorization:
      type: Bearer
      credentials_file: 'C:\fake-private\lgmon-read-token.txt'
    static_configs:
      - targets: ['127.0.0.1:7444']
```

## 数据、时间与指标表

两个出口和 CLI 使用同一个只读 WAL 快照，读取已持久化事实。导出器不扫描系统、不运行探测、不控制进程。目标按当前配置及采集器 ID/主机名分配；删除的配置目标不再输出。原始数据按既有保留策略清理后，缺失指标不输出，不用汇总平均值替代“当前值”。

所有指标都是 gauge。CPU/使用率转换为 ratio，容量为 bytes，速率为 bytes/second，时长为 seconds。未观察到的值省略；target_state 的 up/down/unknown 为 1/0/-1。down/unknown 不复用旧 CPU/RSS；up 还要求进程样本时间不早于当前状态，且全部 PID/创建时间与当前引用集合一致。只有部分进程样本时省略整组 CPU/RSS，不算成零。

Prometheus 不附加样本时间戳，使用下表的 observed/last_seen 指标判断新鲜度；旧值不会被声称为新采样。OTLP 数据点时间直接保留对应持久观测时间，单位为 Unix 纳秒；逻辑计数/登记元数据时间为本次快照时间。两种出口的数值和身份一致。离线主机仍能保留最后已知值，判断在线或告警时同时检查 heartbeat 年龄；例如 `time() - lgmon_collector_last_seen_timestamp_seconds > 30`。last_seen=0 表示没有已保存心跳。

下表名称均带 `lgmon_` 前缀；除全局 alerts_active 外默认带 collector_id。磁盘 volume 是挂载点标识，网卡 interface、服务 service、脚本 metric 是已有资源名称。没有 PID、命令行、进程路径、错误正文、告警摘要、通知地址、密钥或任意业务标签。

| 指标后缀 | 附加标签 | 数值/单位 |
|---|---|---|
| collector_info | hostname, version | 登记身份，值 1 |
| collector_last_seen_timestamp_seconds | 无 | 心跳接收时间，Unix seconds |
| system_cpu_ratio | 无 | 整机 CPU / 100 |
| system_memory_total_bytes / system_memory_used_bytes / system_memory_available_bytes | 无 | 物理内存总量/已用/可用 |
| system_memory_used_ratio | 无 | 内存使用率 / 100 |
| system_uptime_seconds | 无 | 开机时长 |
| system_observed_timestamp_seconds | 无 | 系统观测时间 |
| disk_total_bytes / disk_used_bytes / disk_free_bytes / disk_used_ratio | volume | 容量或使用率 |
| disk_observed_timestamp_seconds | volume | 磁盘观测时间 |
| network_send_bytes_per_second / network_receive_bytes_per_second | interface | 最近速率 |
| network_observed_timestamp_seconds | interface | 网卡观测时间 |
| target_state | target_id | up=1, down=0, unknown=-1 |
| target_process_count | target_id | 最新 up/down 中已上报的匹配进程数 |
| target_observed_timestamp_seconds | target_id | 目标状态观测时间 |
| target_cpu_ratio / target_rss_bytes | target_id | 当前进程集合 CPU 之和 / 100、RSS 之和 |
| target_process_observed_timestamp_seconds | target_id | 进程指标观测时间 |
| probe_success / probe_latency_seconds | target_id, probe | 成功 1/失败 0、延迟秒 |
| probe_observed_timestamp_seconds | target_id, probe | 探测时间，probe 从 0 开始 |
| probe_tls_not_after_timestamp_seconds | target_id, probe | 已观察到的证书到期时间，无证书时省略 |
| script_value | target_id, probe, metric | 原始脚本数值，单位未声明，不推断或换算 |
| script_observed_timestamp_seconds | target_id, probe, metric | 脚本指标观测时间 |
| service_state | service | running=1，stopped/paused/deleted=0，pending/unknown=-1 |
| service_observed_timestamp_seconds | service | 服务状态观测时间；服务只报变化 |
| collector_alerts_active | severity | 对应登记采集器当前 firing 数量，三个严重度分别输出 |
| alerts_active | severity，**无 collector_id** | 全部 firing 数量，包含全局/SQL 规则；resolved/closed 不计入 |

OTLP 为每个采集器建立 resource：service.name=lg-monitor，service.instance.id=采集器 ID，host.name 和 service.version 来自登记信息，lgmon.monitor.id 标识本监控器。全局计数使用监控器身份。scope 为 lg-monitor/exports。数据点保留同名指标和同组标签；未声明的脚本单位为空，其他单位使用 `1`、`By`、`By/s` 或 `s`。

## CLI、HTTP 与 MCP

```powershell
.\bin\lgmon.exe metrics --home .\dev-home
.\bin\lgmon.exe metrics --home .\dev-home --json
```

CLI 只读本机已有数据库和配置，停止运行时也可使用，不创建数据库、不迁移、不发送请求。默认输出标准 Prometheus 文本，--json 返回结构化快照，失败返回结构化 error 和非零退出码；没有配置目标时仍可读取系统/服务/告警等已有数据。磁盘配置无效时先按定位修复，不假定它就是运行时上次有效配置。

| 接口 | 最低权限 | 输出 |
|---|---|---|
| GET /metrics | read | `text/plain; version=0.0.4; charset=utf-8`，未启用 404 |
| GET /api/v1/exports/metrics | read | ts、families；每族 name/help/unit/points，每点 labels/value/ts |
| MCP export_metrics | read | 同一结构化快照，不开启出口或发送数据 |
| PUT /api/v1/config/settings | admin | 原设置接口，校验并热更新导出设置 |

结构化只读查询独立于自动出口开关，供 Agent 和操作者核对数据。接口继承 Bearer、Host/Origin 检查，不接受 Cookie/URL 令牌，不开放 CORS。OpenAPI 区分文本和 JSON。现有的 GET /api/v1/metrics 仍用于历史范围查询。

## 失败、界限与验收

每个快照最多 100000 个点，只读事务最长 5 秒；超限/读取失败返回错误，HTTP 不输出部分成功快照。Prometheus 文本最大 16 MiB。OTLP 每批最多 1000 点/1 MiB，单并发，周期总预算包括读取、编码、连接、重试及等待，响应解压后最大 64 KiB。配置变更先取消并等待旧任务，关闭会释放自己的连接和 ticker。

OTLP 200 按 protobuf 响应解析，partial_success 记录 rejected 数量并跳过该批重试；不打印接收端 error_message。网络错误及 429/502/503/504 在总预算内最多尝试三次，指数退避带抖动，遵守 Retry-After 秒数或 HTTP 日期。其他 HTTP 错误、非法/过大响应和信任失败直接结束本周期。失败日志仅记录安全的 class/status；部分拒绝仅记录数量。指标推送没有无限积压：下一周期读取最新 gauge，保留内部采集缓存、数据库、规则和通知的原运行方式。

协议依据：[Prometheus 文本格式](https://prometheus.io/docs/instrumenting/exposition_formats/)、[命名约定](https://prometheus.io/docs/practices/naming/)、[OTLP](https://opentelemetry.io/docs/specs/otlp/)。工程夹具使用官方解析器/生成消息在真实本地 HTTP、TLS/mTLS 连接上核对，不连接生产平台。真实 Grafana/Collector 接收、仪表盘展示和本人验收保持 Pending；本人可在独立测试平台启用接收器、填写自己保存的凭据、检查原生单位与观测时间，再分别停用出口确认请求停止。真实服务安装、重启、开机启动和防火墙继续按既有授权规则执行。
