# 采集通道

M1-01 实现本机 gRPC 双进程通道。协议源和生成代码在 `proto/lgmon/v1/`，外部 I/O 接口仍使用 `pipeline.Sink/Source`。M0 内存模式保留作兼容和单元测试路径。

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

CollectorLink.Connect 是采集器主动打开的双向流：Hello 携带采集器 ID、主机名、版本和最后确认序号；Welcome 返回接受结果和下一个序号。Batch 中的 Record 与 pipeline 的三种消息逐条对应；发送端当前每批一条，接收端最多接受 256 条。记录头保留 uint64 序号和 UTC 毫秒时间戳，三种数据使用 protobuf oneof。进程引用没有原始命令行字段。

监控端复用既有状态存储、规则引擎和通知队列；只有对应消息经 SQLite Flush 提交成功才确认序号，提交失败返回 Unavailable。整个 Batch 先校验再接收，拒绝缺少 Hello、非法记录、身份冲突或不连续序号。同一采集器只允许一个连接；旧连接尚有待提交记录时保留其会话，防止新连接同序号误收旧记录的确认。

客户端与服务端各自限制单条消息压缩前 4 MiB，双向 gzip；服务端同样检查解压后上限。网络故障关闭本次写通道；Ctrl+C 停止并清理自己持有的资源。

## 当前边界

monitor 默认监听 :7443，可用 --listen 指定；collector 用 --monitor 指定目的地。M1-01 明文阶段仅允许本机回环 TCP 对端，客户端仅接受回环 IP 或 localhost。它不是跨机器认证方案。mTLS、配对和吊销是 M1-02；离线 outbox、持久确认与按序去重是 M1-03；配置推送是 M1-04；心跳是 M1-05。这些消息类型已预留，当前服务端不会静默接受尚未实现的消息。

本机双进程复验使用 `scripts/m0-acceptance.ps1 -SplitRoles`，见 [验收步骤](acceptance-m0.md)。默认测试使用内存 bufconn 和假发送端；Windows integration 测试启动独立 monitor、collector 和演示服务子进程，模拟演示进程退出/恢复并检查同一告警的状态及成功审计。假发送成功不等于真实飞书到达。

依据：[Buf 本地生成配置](https://buf.build/docs/configuration/v2/buf-gen-yaml/)、[gRPC Go 双向流](https://grpc.io/docs/languages/go/basics/)、[grpc-go 消息大小和压缩选项](https://pkg.go.dev/google.golang.org/grpc@v1.85.0)。
