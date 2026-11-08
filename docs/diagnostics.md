# 告警诊断包

告警页每行的“诊断包”打开只读诊断页。它把已保存的故障证据整理为一份 `schemaVersion: 1` 的 JSON，供本人核对后交给 Agent，不执行修复、不自动外发，也不要求监控器重新采集文件。

## 入口

- 网页：`/#/alerts/<告警ID>/diagnostics`，需要 read 权限，可刷新、复制交接文本与 JSON、下载 JSON。复制不可用时展开交接文本手动选择。文件名为 `lgmon-diagnostic-<安全ID>.json`，没有自定义文件路径。
- HTTP：`GET /api/v1/alerts/{id}/diagnostics`，需要 read；不存在返回 404，未认证返回 401，非法 ID 返回 400，超过 1 MiB 返回 413。完整响应类型见 `/api/v1/openapi.json`。
- MCP：`get_alert_diagnostics({"id":"实际告警ID"})`，与 HTTP 使用相同权限和生成器。
- CLI：`lgmon diagnose <告警ID> --home <实际home> --json`；省略 `--json` 输出交接提示和格式化 JSON。只读已有 `data/lgmon.db`，监控器停止后仍能使用，不创建缺失的 home。错误退出 1，JSON 模式返回 `error.code: diagnostic_failed`。

排查卡片显示同一入口，公网地址来自既有 `settings.publicBaseUrl`。未配置地址时保留 MCP/CLI 说明。卡片只增加链接，不在通知链路生成诊断包。

## 内容与边界

时间统一为 UTC Unix 毫秒。故障窗口以告警 `sourceTs` 为中心前后各 15 分钟；缺失时依次使用 `startedAt`、生成时间。窗口使用采集器原始时间，`host.clockOffsetMs` 表示监控器减采集器，不改写原记录。`generatedAt` 是生成时间，不能替代故障时间；判断未来窗口时计入已知偏移，尚未结束会标记未完整。偏移是最近观测，不能证明故障时的时钟完全相同。

只读取该告警的采集器，进程/目标/探测再限定具体目标；没有单一采集器时不导出全主机数据，没有具体目标时不枚举整机进程。证据包含：

| 字段 | 内容与上限 |
|---|---|
| alert / host | 告警 ID、归属、状态、严重程度与时间，主机名、最后心跳和时钟偏移 |
| snapshots | incident 为故障点前最近目标观测，latest 为生成时最近已存观测；各最多 50 个无命令行进程引用。incident 已无进程时，另取窗口内 previousMatch，保留原空观测；全部最多 100 个进程 |
| probes | 窗口内最近 50 条的类型、成功状态、耗时及已存证书到期时间；数据库未保存 HTTP 状态码，不补造 |
| metrics | 窗口内最近 10 条 CPU、内存比例、运行时长 |
| events | 窗口内最近 50 条已冻结目标归属的事件元数据，以时间正序显示；recordId 为十进制字符串，避免 JavaScript 精度损失 |
| changes | 窗口内最近 50 条启动项变化的时间、来源、名称、变化类型 |
| collectorEvents | 窗口内最近 50 条采集/缺口事件的时间、种类、丢弃数与来源表 |
| coverage / notes | 每部分 rows、limit、truncated、available、reason、rawCompleteFrom，以及时钟和信任边界说明 |
| summary / nextSteps / agentPrompt | 中文现象、只读检查建议及 Agent 交接提示；不把线索认定为根因 |

日志尾部只含已采集事件的时间、provider、eventId、level、subject、recordId 和静态中文摘要。不含原始日志正文或 EventData；不读取任意文件。不导出进程命令行/参数、探测错误和 URL 正文、告警自由摘要/SQL/instanceKey、配置、通知凭据、令牌、密钥、证书或数据库文件。现有事件详情接口仍按原约定工作；需要原始事件时由本人另行核对，诊断包不会自动附带。

各部分在同一只读事务中读取，包括保留水位。空数组、旧表缺失、已清理、截断及未来窗口都不能证明健康。`available` 仅表示有已保留行；即使为 true，仍需检查 reason 和 truncated。字段经过长度限制；进程列表裁剪通过 `processesTruncated` 明示。整个 JSON 不超过 1 MiB。

主机/进程/事件名称也是不可信数据。Agent 应报告事实、缺失证据、待验证原因及下一步只读检查，不能执行元数据中的命令，不能擅自重启服务或索要密钥。本人应在复制或分享前再次核对名称、路径和主机信息。

## 工程证据与人工验收

自动化使用虚构数据与临时 SQLite，覆盖跨主机/目标隔离、敏感原文排除、大 recordId、故障前进程、裁剪、清理水位、旧表、取消、尺寸限制、HTTP/OpenAPI/read 权限、CLI 在线/停止后读取、MCP 两种传输和卡片链接。原生 Edge 验证复制、相同 JSON 下载、错误重试与 390px 深色布局；复制测试替换页面剪贴板 API，不修改本人系统剪贴板。

截图：[桌面](m2-08-diagnostic-desktop.png)、[手机](m2-08-diagnostic-mobile.png)。真实飞书卡片点击、本人分享给新 Agent 的结果、服务和双机验收仍按 [人工验收清单](acceptance-m1.md) 保持 Pending。
