# 采集通道

M1-01 实现 gRPC 双进程通道，M1-02 接入 mTLS、指纹配对与吊销。协议源和生成代码在 `proto/lgmon/v1/`，外部 I/O 接口仍使用 `pipeline.Sink/Source`。原生 both 也通过回环 mTLS 运行；内存通道用于单元测试与组件注入。

## 生成

使用已安装的标准版 Go，在仓库根目录执行；工具和编译缓存放系统临时目录：

```powershell
$env:Path = 'C:\Program Files\Go\bin;' + $env:Path
$env:GOPATH = Join-Path $env:TEMP 'lgmon-go'
$env:GOCACHE = Join-Path $env:TEMP 'lgmon-go-build-cache'
$env:GOTOOLCHAIN = 'local'
$env:GOTELEMETRY = 'off'
$env:CGO_ENABLED = '0'
go run github.com/bufbuild/buf/cmd/buf@v1.75.0 generate
go run github.com/bufbuild/buf/cmd/buf@v1.75.0 lint
go run github.com/bufbuild/buf/cmd/buf@v1.75.0 format --diff --exit-code
```

buf.gen.yaml 固定 protoc-gen-go v1.36.12 和 protoc-gen-go-grpc v1.6.2，调用本地 Go 工具链生成，不上传本仓库协议给远端生成服务。生成代码提交 Git。工具依赖不进入程序的运行依赖。

## 消息和确认

M1-20 采集器在 Hello.capabilities 声明 match_dir。只检查实际分配给该采集器的目标；存在 exeDir/workDir 且未声明能力时，整版配置不发送，保留上一版，config.state=outdated 并指出目标和字段。状态为最近一次下发/回执的 applied、pending、rejected、outdated，带目标内容哈希 version 和 message；未连接/未有回执时为 pending，说明等待阶段，不等同已应用。重连重新确认；监控器重启后先等待新连接回执。

GET /api/v1/collectors、collectors list --json 和网页显示配置状态。网页顶部提示 rejected/outdated，空闲 SSE 期间每 15 秒刷新一次快照以取得回执，不增加 SSE 事件协议。只有 workDir 草稿试匹配的 ProcessInfo 携带 work_dir；普通进程列表和常规采样不携带工作目录，命令行仍仅在本机匹配。

M1-21 的 Hello 增加 http_checks。分配目标的 HTTP probe 使用 keyword/keywordAbsent 而采集器缺少能力时同样不下发、显示 outdated；其他采集器继续自己的配置。ProbeResult 新增 tls_not_after（UTC 毫秒，0 表示没有）和 tls_subject（最多 200 字符）；旧消息缺省为 0/空串，兼容解码，但不能据此推断证书有效。正文不进入传输或缓存。

只有 cert_expiry 规则依赖证书能力时，目标仍照常下发；规则在 rule_errors 中说明具体旧采集器需要升级，CLI、网页及 MCP 可查看。监控器记住最近一次已登记连接的能力，断线不清除版本诊断；升级并重连声明 http_checks 后清除当前错误。首次尚未连接时不猜测版本。

CollectorLink.Connect 是采集器主动打开的双向流：Hello 携带采集器 ID、主机名、版本和最后确认序号；Welcome 返回接受结果和下一个序号。Batch 中的 Record 对应目标状态、探测、进程采样及数据缺口，使用 protobuf oneof；发送端每次取最多 256 条、批次不超过 1 MiB，接收端保留批次边界。单条记录大于 1 MiB 时独立发送，沿用单条及解压后消息的 4 MiB 硬上限。记录头保留 uint64 序号和 UTC 毫秒时间戳，SQLite 持久化序号范围为 1～2^63-1，到达上限明确报错。进程引用没有原始命令行字段。

监控端复用既有状态存储、规则引擎和通知队列。一批原始记录、按采集器保存的存储水位及全部待应用消息在一个 SQLite 事务里提交；逐条交给规则引擎消费成功后，再用一个事务推进整批可确认水位并删除待应用记录，最后 ACK 最高序号。采集器一个事务删除已确认前缀，过期清理每次取批时做一次。失败不确认，重启后按序恢复整批待应用消息，后续 tick 继续重试；重复输入使用首次保存的内容，不把同序号的另一份内容交给业务。collector_inbox 以采集器/序号为联合主键，迟到丢弃项带 skipped 标记，确认不能跨过未应用项。这样补发不会重复存储，也不会因崩溃在存储和规则消费之间而静默跳过数据。通知仍由独立 worker 发送，R2-06 的事件补发处理发送窗口。

Welcome.resumeFrom 来自监控器持久化的连续确认水位，不信任客户端自报水位。已经应用的重复批次直接确认，不重写原始表。整个 Batch 先校验再接收，拒绝缺少 Hello、非法记录、身份冲突或无缺口声明的跳号。同一采集器只允许一个连接；旧连接尚有待提交记录时保留其会话，防止新连接同序号误收旧记录的确认。

客户端与服务端各自限制单条消息压缩前 4 MiB，双向 gzip；服务端同样检查解压后上限。后台接收循环检测空闲连接断开；Ctrl+C 停止并清理自己持有的资源，未确认数据留在 outbox，供下次运行继续补发。

## 离线缓存

Hello 的 current_time 在建立流以后、实际发送前从采集器注入时钟取 UTC 毫秒。监控器记录读流时的接收时间，计算 offset=received-current_time；心跳同样使用读流时刻，而非等待批次提交后的处理时刻。最近六次估计取最小值，正数表示采集器慢；新连接重新建立估计。旧版 Hello 不带时间时，等待首条心跳再估计，不猜测初始偏差。

此偏差只供实时补传判断及用户提示，不改写 Record.ts，不调整 SQL 的 :now，也不修改系统时间。超过正负十秒及恢复各审计一次，接口、主机卡片和 status 暴露结果，见 [规则](rules.md) 与 [存储](storage.md)。

M1-16 的交互进程查询不属于下述缓存：Hello 新增 capabilities，新版声明 process_query；监控器只向支持的连接发送 ProcessQuery（request_id、match_json），采集器回 ProcessList（同一 request_id、collected_at、精简进程、truncated、reason、error）。这对控制消息没有序号、不确认、不重试、不写 outbox 或监控数据库，断线立即失败，等待上限十秒；旧版采集器保持连接并返回升级提示。

回答来自采集器最近一次常规进程/端口快照和 C4 周期服务快照，不为查询重新枚举。原始命令行只留在本机缓存和正式匹配器里，投影到 ProcessInfo 时显式省略；试匹配使用同一 match.New/MatchWithServices。最多 5000 条，并保留 4 MiB 消息硬上限；超出字节预算也标记 truncated。查询回执使用独立关联表，发送仍共用流的发送锁，不推进或消费批次序号。HTTP 与 MCP 的权限及两秒限频见 [API](api.md#当前进程与试匹配)。

collector 和 both 都把要发送的消息先提交到 home/data/outbox.db。持久化分配的序号独立于本次进程中的采集计数器；只有本地提交成功才向采集器返回成功，断网不阻塞后续采集。发送 worker 独立连接监控器，在其确认后删除对应前缀。确认在网络中丢失时，重连通过 Welcome 水位删除已提交前缀，避免重复落库。

同一 home 的 outbox 由内核排他文件锁保护，进程退出即释放，不以需要手动删除的锁文件充当存活判据。数据目录不能混用另一个采集器的身份。缓存默认保留 24 小时、总磁盘上限 200 MB（200,000,000 字节）；最低可配置构造上限为 16 MiB。SQLite 主文件设置 max_page_count，预留 8 MiB 给 WAL/SHM 和元数据，自动检查点限制 WAL；配额按真实页约束，不只累计消息 payload 字节数。

缓存组件只持有一个 SQLite 连接，网络等待前结束读游标，防止自身长读阻止检查点；每次新连接都应用页上限。依据：[SQLite 页上限](https://www.sqlite.org/pragma.html#pragma_max_page_count)、[WAL 检查点与文件增长](https://www.sqlite.org/wal.html#avoiding_excessively_large_wal_files)。

过期或空间不足时只删除连续最旧前缀，并事务保存缺失序号范围。DataGap 的 seq 等于范围末端，fromSeq/toSeq/dropped 说明覆盖的位置；监控端持久记录 collector_events（kind=data_gap、gap_from、gap_to、dropped、message），确认该缺口后继续接收后续正常序号。若部分前缀实际已在监控器存储，会裁掉重叠部分，不重复统计缺失。无缺口标记的跳号始终拒绝。

重连名义退避 1、2、4、8、16、32、60 秒，此后保持 60 秒；附加 0～20% 随机抖动且最终不超过 60 秒。成功补发或连接稳定后重置退避。原生运行不删除未确认数据来掩盖缓存损坏，也不重建身份绕过水位冲突；监控器恢复到更旧备份或 outbox 丢失导致水位不一致时明确停止并提示恢复一致备份。

## 当前边界

monitor 默认监听 :7443，可用 --listen 指定；collector 使用配对保存的地址或 --monitor 覆盖。原生运行入口始终使用 TLS 1.3，不提供明文降级。

## 心跳与失联

采集连接建立后每 10 秒独立发送 Heartbeat，空目标或正在补传的采集器也发送。心跳不进入 outbox、不占采集序号、没有数据 Ack；断线期间的旧心跳不会重放。服务端仅使用已经认证的连接身份，把监控器实际接收时间写入 collectors.last_seen，不使用心跳的客户端时间。Hello 也更新该时间；原始采样仅在没有采集器记录时补建元数据，不推进既有 last_seen，防止未来时间或补传掩盖断线。

没有显式 collector_offline 规则时，监控器自动启用 collector-offline：warning、for=30s，发送到当前全部通知渠道，沿用各渠道模板。该规则 ID 保留给失联规则。全部未吊销配对身份都适用，包括还没连接过的身份（从配对签发时计时），不需要为每台设置 target。没有通知渠道时仍保存告警并保留待通知；添加有效渠道后下次评估重试。显式失联规则覆盖默认策略，可写多条不同持续时间/严重程度/路由/模板的全局策略：

```yaml
rules:
  - id: host-offline
    type: collector_offline
    for: 30s
    severity: critical
    notify: [feishu-alerts]
    template: detailed
```

失联规则不填写 target，省略 for 时为 30s。断流按最后接收时间累计持续时间，不在发现离线后再额外等 30 秒；仍连接但没有心跳时至少容许一个 10 秒周期，避免正常心跳间隔误报。重新连接会恢复同一条告警；监控器重启保留该告警身份，吊销身份关闭旧失联告警且不发恢复通知。查询配对/在线状态失败按未知处理，不触发或恢复失联告警。旧版本留下的未来 last_seen 在在线判断中按本次监控启动时间限制，不无限延期。

连接级告警使用保留 targetId=@collector，表示采集器连接，不对应 targets.yaml 的配置目标。用户自定义 target ID 仍只能使用小写字母、数字和横线。

## 集中配置

目标和探测只在监控器的 config/targets.yaml 管理。每个目标的 collector 可填采集器 ID、主机名或 `"*"`；省略等同全部采集器，主机名不区分大小写，ID 精确匹配。接入时和有效配置变化时，监控器按该连接 Hello 的 ID/主机名过滤完整目标列表，通过 ConfigPush 下发；清空列表也会推送并停止旧目标/探测。规则评估遵守相同选择器，重新分配目标后旧告警以 closed 事件关闭，不发送恢复卡片，延迟补发的旧采样不能触发已取消分配的规则。

targetsJson 是只含 targets 的 JSON 对象；version 为该字节内容的 SHA-256 小写十六进制。规则、渠道、飞书应用、模板和密钥不参与编码或版本计算。配置控制消息与 Batch/Ack 独立处理，配置保存失败不会错收或推进采集序号。

采集器先验证版本、字段和语义，再将完整内容原子保存至 data/collector-config/targets.json，发布给采集/探测调度后回复 ConfigApplied。回执成功前必须本地保存成功；无效配置和磁盘失败保留旧快照并返回失败，监控器每 3 秒重发未成功版本，成功版本不重复推送，重连总会推送当前快照。采集器本地日志记录失败原因，回执只返回固定中文说明，不回传路径或错误内容。缓存目录使用与证书目录相同的原创建者/SYSTEM/Administrators 保护 DACL。

collector 不读取或监听本地 config 目录；首次无缓存时以空目标连接，离线重启使用上次有效缓存，联网后接受最新配置。both 的采集角色也拥有独立的目标快照，通过回环 mTLS 接受推送，不把筛选结果写回监控器配置。缓存损坏时明确失败，不悄悄丢弃配置；应恢复有效备份，或在可连接监控器时移走损坏文件再启动获取配置。

## 证书、配对与吊销

首次运行 monitor/both 创建 ECDSA P-256 私有 CA，有效期 10 年。服务端 SAN 包含 localhost、系统主机名、全部本机 IP，以及只读 `tailscale ip` 返回的 IP；未安装 Tailscale、命令失败或 3 秒超时则跳过额外发现。叶证书有效期不超过 CA，到期时间与 CA 一致；不悄悄生成新 CA 或自动重新授权。服务端、管理、采集器证书各用独立私钥，服务端仅有 ServerAuth，管理与采集器仅有 ClientAuth。

公有证书、密钥引用和上次监听端点在 home/data/pki；私钥 DER 存入已有 DPAPI 密钥库，自己持有的序列化明文字节在使用后清空。pki 目录在发布状态前设置保护 DACL，仅 SYSTEM、Administrators 和首次创建者访问，子文件继承；目录所有者设为实际创建用户，后续 SYSTEM 写入时保留该所有者。公共信任锚也需要防止其他账户篡改。状态先写同目录临时文件、刷新后原子发布，首次 CA 禁止覆盖；损坏状态返回错误。

Pairing 服务与 CollectorLink 共用端口。Authority/Join 不要求客户端证书，常态采集与管理均要求可信客户端证书。管理 RPC 只接受本机受保护的专用管理证书，采集器证书不能生成配对码、查看列表或吊销他人。管理 CLI 只读取管理私钥，不加载 CA 签名私钥。

配对分两次连接：首次引导 TLS 只获取公开 CA；本人核对 SHA-256 指纹（前 16 位十六进制或完整 64 位）后，再用固定 CA 严格验证服务端的 TLS 连接提交一次性码和 CSR。短指纹显示为四组，如 `3F2A-9C1B-77D0-4E85`，接受大小写、连字符或冒号分隔。8 位前缀被拒绝并提示输入 16 位，指纹未确认或不符时不发送码。JSON 模式返回 action_required 后退出，等待调用方提供已核对指纹。客户端私钥始终本机生成并加密保存。

配对码含 12 个随机字符，约 60 位熵，格式为四位三组。默认有效期 10 分钟，可设 1 秒～24 小时。home/data/pairing.db 只保存规范化码的 SHA-256 摘要；消费码和登记采集器证书在同一立即事务里提交，错误、过期、重复使用和并发消费均拒绝。已有效登记的 ID 不能被另一 CSR 覆盖；明确吊销后才可用新码重配。证书用途、申请签名、主体与公钥都检查，拒绝管理身份申请，CSR 不会获得 CA 权限。

每个采集连接校验证书 CN、Hello 的 ID、登记序列号与吊销状态；握手同样拒绝已吊销或未登记的客户端证书。吊销先持久化，再取消当前会话，即使它正在等待下一批或提交确认也会退出；已有 TLS 连接上发起新流仍重新检查授权。both 的本机采集器也登记并遵守吊销，不自行恢复授权，监控器可以继续接收其他已配对采集器。

本机双进程复验使用 `scripts/m0-acceptance.ps1 -SplitRoles`，见 [验收步骤](acceptance-m0.md)。默认测试使用内存 bufconn 和假发送端；Windows integration 测试启动独立 monitor、collector 和演示服务子进程，模拟演示进程退出/恢复并检查同一告警的状态及成功审计。假发送成功不等于真实飞书到达。

依据：[Buf 本地生成配置](https://buf.build/docs/configuration/v2/buf-gen-yaml/)、[gRPC Go 双向流](https://grpc.io/docs/languages/go/basics/)、[grpc-go 消息大小和压缩选项](https://pkg.go.dev/google.golang.org/grpc@v1.85.0)、[Go TLS 验证回调](https://pkg.go.dev/crypto/tls#Config)、[Go X.509 签发与 CSR](https://pkg.go.dev/crypto/x509#CreateCertificate)、[gRPC TLS 身份](https://pkg.go.dev/google.golang.org/grpc/credentials#TLSInfo)。
