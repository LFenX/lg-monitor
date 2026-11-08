# 导入服务开关配置

采集器只在预览或保存时读取本机 `services.json`，不轮询、不执行启动命令、不导入规则。监控器通过已配对的加密连接请求精简结果，能力为 `service_switch`；旧采集器需升级。

本人在文件所在电脑的 `config/settings.yaml` 设置固定位置，随后重启采集器：

```yaml
serviceSwitch:
  path: 'D:\fake-tools\service-switch\config\services.json'
```

必须是普通 Windows 绝对盘符或 UNC 路径，文件名为 services.json（大小写不限）。不展开环境变量，拒绝设备路径。HTTP、导入 CLI 和 MCP 不能指定或更换位置；HTTP 更新其他 settings 时须原样保留它。文件最多 1 MiB，仅支持 schemaVersion 1。

只上传应用 id/name/root/ports/healthPath/domain/controller/guardian.statusUrl/tunnel.metricsPort/autostart 和数据库 kind/name/ctl。环境变量、启动命令、脚本、requires、settings、Tunnel 配置及显示字段不上传，结果不进入离线缓存或数据库。非法应用单独跳过，重复应用 ID 全部跳过；脚本只显示泛化跳过说明。精简结果超出现有传输消息大小限制时明确报错，不截断。

| 来源 | 生成行为 |
|---|---|
| 有端口的网站 | 首端口识别进程；本机 HTTP、其余端口 TCP、可选 Guardian 状态探测 |
| 公网 domain | 默认增加每 5 分钟一次的 HTTPS 探测；可关闭 |
| tunnel.metricsPort | 独立目标，HTTP `/ready` 期望 200 |
| postgres 数据库 | postgres.exe + ctl 所在目录；不读数据库配置、不猜端口 |
| 无端口应用、其他数据库、脚本 | 说明原因后跳过；应用有独立 Tunnel 端口时仍可导入 Tunnel |

缺省 healthPath 为 `/`；none 关闭本机 HTTP，公网地址此时用 `/`。网站以低于 500 为正常，Guardian 期望 2xx。autostart off 仍导入，预览明确标记。所有目标使用采集器稳定 ID、service-switch 标签及监控器内部 source 归属；source 下发前移除。

网页主机卡片点击“从服务开关导入”，查看完整目标后勾选新建/更新项，再点“确认并保存所选目标”。read 只能预览，保存需 config/admin。缺失目标默认保留；“一并删除”删除预览中全部缺失导入目标，引用校验失败则整个保存不写文件。

同源目标整条更新；无 source 且分配到同一 ID/主机名、关键端口或数据库进程目录一致的手工目标显示 covered，不重复创建。`*` 或省略 collector 不算同机手工目标。ID 冲突依次加 -2、-3。手工维护导入目标前先删掉它的 source。

CLI 令牌只从 LGMON_TOKEN 读取：

```powershell
lgmon import service-switch --collector fake-host --json
lgmon import service-switch --collector fake-host --apply
lgmon import service-switch --collector fake-host --apply --json --yes --public-probes=false --refs app:blog,tunnel:blog
```

无 --apply 只预览；交互保存需本人确认；JSON 保存必须带 --yes。省略 refs 保存全部 create/update；显式 --refs= 不保存任何 create/update。remove-missing 另行控制缺失目标。网页和 CLI 会等待两秒限频，MCP 调用方也须遵守。

HTTP 为 GET `/api/v1/collectors/{id}/service-switch?publicProbes=true`（read）和 POST `/api/v1/collectors/{id}/service-switch/import`（config），结构见 [PLAN 协议](PLAN.md#导入服务开关配置已定设计)。保存带预览的 sha256、targetsVersion，可选 refs/removeMissing/publicProbes。前者来自精简内容，后者来自原 targets.yaml 字节；保存实际重读，分别以 409 source_changed/config_changed 拒绝陈旧预览。冲突后须重新预览并确认，不能自动替换指纹。所有写入共用 config.Editor 锁与完整校验，YAML 节点保留其他条目和注释。

MCP preview_service_switch_import / import_service_switch 使用同一 HTTP；先展示变化，经用户同意再保存。未设置路径为 409 source_not_configured，文件/版本/消息大小无效为 422 source_invalid；离线、旧版、超时分别为 collector_offline、collector_outdated、collector_timeout。

导入后核对匹配和探测结果，再按需要添加 port_closed、probe_failed、cert_expiry 规则，引用已有通知渠道。autostart off 的目标需自行决定告警策略；导入不改通知去向、不发测试、不启动服务。真实 A 机路径设置、真实导入及目标/探测核对仍待本人验收，假数据工程测试不代替它。
