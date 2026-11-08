# 声明式面板

每个配置目录 dashboards/*.yaml 文件描述一页面板。网页“面板”入口或 /#/boards/<id> 打开它；页面只按卡片 ID 请求服务端结果，不提交卡片 SQL。接口与参数遵循 PLAN M1-15 的“读取面板与查询参数”。

## 文件

三个示例位于 [examples/dashboards](../examples/dashboards/)：overview.yaml、hosts.yaml、diagnostics.yaml。JSON 格式也可作为 YAML 文件内容。字段与 PLAN 示例一致，id、title、refresh、panels 必填；每个块的 id、type、title、query、width 必填，不增加面板编辑接口或拖拽配置。卡片 id 在同一页面内唯一，改标题、调顺序时保持不变；这是 M2 分享选卡片的准备，M1 不实现团队或分享。

- id 使用小写字母、数字和横线，同一目录不可重复。
- 文件名必须等于 id，例如 overview.yaml 对应 id: overview。defaultRange 可省略，默认 1h；hosts.yaml 演示 6h。
- refresh 使用正时长，例如 10s。width 是 1–12 的整数，使用 12 列栅格；手机排列为单列。
- query 使用现有单条只读 SQL 校验、最多 5 秒及 10000 行限制。错误显示在对应面板块，其他块继续显示；结果截断也明确提示。
- 配置支持新增、修改、原子替换及删除后的热加载；错误文件报告文件名和行号，保留上一份有效配置。子目录只接受 .yaml，.yml 会提示改扩展名。
- Schema 由配置类型生成：[dashboard.schema.json](../schemas/dashboard.schema.json)。生成命令：`lgmon config schema --output schemas`；配置检查使用 `lgmon --home .\dev-home config validate`。

## 查询结果

列名区分大小写。服务端统一检查列与类型，网页直接展示结果或错误，MCP 与后续共享使用同一份校验。多余列允许保留；重复列名、行数与列数不匹配、字段类型错误均在本块显示具体原因。所有文字按纯文本显示，不执行 HTML、脚本或日志内容。

| type | 必需列 | 值要求与呈现 |
|---|---|---|
| stat | value | 最多一行；有限数值、文字或 null。0 显示 0，null 显示 —，零行显示暂无数据 |
| line | ts、value | ts 为有效 UTC Unix 毫秒整数；value 为有限数值或 null，null 留缺口。可选 series 为文字或 null，按系列分组、按 ts 排序 |
| bar | label、value | label 为文字；value 为有限数值或 null。每行一根柱，保留 0 和负值 |
| status_grid | label、state | label 为文字；state 为 up/down/unknown，分别显示正常、异常、未知 |
| table | 至少一个非空列名 | 按返回顺序显示全部列和行，null 显示 —；长表在块内横向滚动 |
| alerts | summary、severity、state | summary 为文字；severity 为 critical/warning/info；state 为 pending/firing/resolved。可选 started_at 毫秒时间用于展示 |
| log | ts、message | ts 为有效 UTC Unix 毫秒整数，message 为文字；可选 level 作为文字标签。顺序由 SQL 的 ORDER BY 决定 |

折线和柱图使用现有 ECharts SVG 方式，提示框按文本绘制。折线时间轴使用服务端返回的 params.from/to。状态格、表格和日志保留深浅色样式，示例涵盖全部七种类型。每卡独立连接、独立刷新，一卡慢查询不占用其他卡片的连接。

## 常用只读视图

010 迁移创建以下视图，已有数据保留。普通只读查询可以使用，事件时间 SQL 规则禁止读取当前状态视图，防止历史回放读取未来数据。

| 视图 | 列与语义 |
|---|---|
| v_target_current | collector_id、target_id、ts、state、detail；每个采集器/目标的最后一条已观测状态，不合成尚未采样的目标 |
| v_alerts_active | id、rule_id、collector_id、target_id、state、severity、started_at、resolved_at、last_notified_at、summary；只包含 firing，排除恢复与关闭 |
| v_collectors | collector_id、hostname、first_seen、last_seen、version；已登记主机与持久化的最后心跳，不宣称它们当前仍在线 |
| v_probe_certificates | collector_id、target_id、probe、ts、tls_not_after、tls_subject；最近一次带证书的原始观测，无证书失败不会擦除，随原始数据保留 |

## 读取与参数

以下接口均为 read，本机自动会话也能使用：

- GET /api/v1/dashboards：按 id 排序的摘要，包括 file、version、panelCount、error。
- GET /api/v1/dashboards/<id>：完整定义，含 defaultRange、每卡 parameters/error。version 是成功文件原始内容 SHA-256 前 16 位；文件损坏时保留旧定义及旧版本，附上当前文件错误。首次加载就坏的文件仍在列表中，读取定义返回 422。
- GET /api/v1/dashboards/<id>/panels/<panel>/data：服务端按当前定义执行查询，返回 columns、rows、truncated、version、params。列契约不符返回 422 panel_columns；SQL 错误为 400 invalid_query；参数错误为 400 invalid_params；超时为 408 query_timeout。

数据请求可写 ?range=6h&collector=fake-host，或 ?from=1000&to=2000。POST /api/v1/query 通过 params 对象使用相同规则，响应也带实际 params；CLI 对应 --range、--from、--to、--collector。

| 提交时间参数 | 实际 from / to |
|---|---|
| from、to | 使用两者 |
| range，可加 to | to 或服务端现在，from 为 to 减 range |
| 只有 from | from 到服务端现在 |
| 只有 to | to 减默认范围，到 to |
| 都省略 | 服务端现在减默认范围，到服务端现在 |

面板默认范围来自 defaultRange；通用 query/CLI 使用 1h。range 与 from 不能同时使用；range 须为正时长，from 须早于 to。时间均为 UTC Unix 毫秒整数。相对范围只提交 range，浏览器不计算起止时间；固定范围由本人填写。

collector 须为监控器认识的采集器 ID，省略时绑定 NULL，表示全部主机。因此查询应写成：

```sql
SELECT collector_id, target_id, state
FROM v_target_current
WHERE (:collector IS NULL OR collector_id = :collector)
```

只写 collector_id = :collector 会在“全部主机”时返回空结果。三个示例均使用正确的 NULL 写法。from/to/collector 每次全部通过 sql.Named 绑定，绝不替换 SQL 字符串；加载阶段会忽略字符串和注释中的假参数，拒绝 :host 等其他参数。

## 热加载与错误

文件逐页加载；损坏文件不阻止健康页、采集或告警配置更新。保留旧版本时网页显示文件名和行号。卡片 SQL 或参数写错只给该卡片 error，其他卡片继续显示。CLI config validate 会报告这些诊断；运行时不会因为一个面板文件有错停止整个配置加载。

配置变动通过 SSE dashboard 事件发送 id/version，删除附带 deleted: true。即使损坏文件保留旧 version，错误变动仍发事件让网页重读。数据响应的 version 与手中定义不同也会重读。离开页面或改变筛选条件时取消旧请求，已启动请求不会覆盖新的页面条件。

## 自定义 HTML

自定义 HTML 页面沿用此目录、选择器与热加载，参见 [HTML 面板与 lgmon.js](html-dashboards.md)。使用同 ID 的 .yaml/.html 组合，html 与 panels 二选一；源码和 SDK 都只能通过隔离容器读取数据。examples/dashboards 的 custom-overview 是新增的完整示例，不改变前述三个声明式示例。

## 本人验收

在本人使用的监控 home 放入三个示例，打开面板，核对桌面/手机深浅色、主机和时间选择、七类内容、热加载与错误提示。此步骤不会由 Agent 操作已有服务或替本人确认视觉验收；自动化截图使用虚构数据。
