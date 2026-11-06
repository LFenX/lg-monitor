# 飞书卡片模板

模板保存在 `<home>/config/card-templates/<id>.yaml`。ID 与文件名一致，只用小写字母、数字和连字符。配置热加载通过 `config.Manager` 发布有效快照；渲染器每次读取最新快照，加载失败继续使用上一份有效配置。

## 命令

```powershell
.\bin\lgmon.exe template list --home .\dev-home --json
.\bin\lgmon.exe template new oncall --from standard --home .\dev-home
.\bin\lgmon.exe template validate --home .\dev-home
.\bin\lgmon.exe template validate oncall --home .\dev-home
.\bin\lgmon.exe template preview oncall --state firing --home .\dev-home
.\bin\lgmon.exe template preview oncall --state resolved --home .\dev-home
.\bin\lgmon.exe template preview digest --state digest --home .\dev-home
```

`list --json` 输出含 `id`、`name`、`preset` 的数组。`new` 带注释生成文件，已有文件不会覆盖。`validate` 对触发、恢复、汇总三个虚构样例分别渲染；任一模板错误退出 1。`preview` 标准输出只有卡片 JSON，回退说明写到标准错误。预览和校验不会发送飞书消息。

## 基于预设修改

内置 `standard`、`compact`、`detailed`、`digest` 四套预设；其 YAML 与自定义配置共用渲染路径。

```yaml
id: oncall
name: 值班群
extends: standard
title: '{{.SeverityIcon}} {{.Rule.Name}} · {{.SeverityText}}'
colors: {critical: carmine, warning: orange, info: blue, resolved: green}
fields: [host, target, startedAt, duration, value]
extraFields:
  - label: 标签
    value: '{{join .Target.Tags "、"}}'
mention:
  critical: all
  warning: [ou_fake_user]
buttons:
  - text: 值班手册
    url: https://example.com/runbook
note: '处理完成后请回复；告警编号：{{.Alert.ID}}'
```

`fields` 控制内置字段的顺序；`extraFields` 接在内置字段之后。`colors` 只覆盖指定状态的颜色。`mention` 按 critical、warning、info、resolved 分别配置，恢复不会沿用 critical 的 @。删除 `mention` 即不 @任何人。示例用户标识是假的，实际值仅保存在本地配置。

标准卡片以两列展示字段，配置了 `DashboardURL` 才显示“打开面板”。简洁卡片没有标题栏；恢复卡片写明持续时长。排查卡片展示调用方提供的进程、默认收起的日志、原因建议和可复制的诊断文本，不自行读取文件或执行命令。汇总最多显示 20 条，超出显示“还有 N 条”。

## 模板变量

模板使用 `text/template`，未知变量报错。时间使用本机时区，格式为 `2006-01-02 15:04:05`；未发生的恢复时间为空。持续时长为 `1小时2分3秒`，触发时计算到当前时间，恢复后计算到恢复时间。

`range` 只允许遍历 `.Alerts`、`.Processes`、`.Target.Tags`（也接受 `$.Alerts` 等根路径），不能嵌套，不能遍历数字、变量或函数结果。不允许 `define`、`template`、`block`。加载配置、保存和预览使用相同语法检查；每次模板执行的输出上限为 64 KiB，超过会停止并报错，卡片整体仍受 28000 字节限制。

| 变量 | 内容 |
| --- | --- |
| `.Alert.ID` | 告警 ID |
| `.Alert.State` | firing / resolved；汇总预览样例为 digest |
| `.Alert.Severity` | critical / warning / info |
| `.Alert.Summary` `.Alert.Detail` | 摘要与详情 |
| `.Alert.StartedAt` `.Alert.ResolvedAt` `.Alert.Duration` | 已格式化的开始时间、恢复时间、持续时长 |
| `.Rule.ID` `.Rule.Name` | 规则标识与名称；无名称时使用标识 |
| `.Target.ID` `.Target.Name` `.Target.Tags` | 目标标识、名称、字符串标签列表 |
| `.Host.Name` `.Host.CollectorID` | 主机名称、采集器标识；无名称时使用采集器标识 |
| `.Value` | 当前值，无值时为空 |
| `.SeverityIcon` `.SeverityText` | 🔴 严重、🟠 警告、🔵 提示、✅ 已恢复 |
| `.DashboardURL` | 此告警的面板地址，无配置时为空 |
| `.Processes` | 进程列表：PID、Name、CPUPercent、RSS（字节）、Memory（MiB 文本） |
| `.LogTail` `.Diagnosis` | 日志末尾、原因和建议 |
| `.Alerts` | 汇总列表，每项具有本表相同的变量（不再嵌套列表），最多 20 项 |

辅助函数：

```gotemplate
{{duration "65s"}}                       {{/* 1分5秒；也接受毫秒整数 */}}
{{time "2026-01-02T03:04:05Z"}}          {{/* 转为本地时间；也接受 Unix 毫秒 */}}
{{truncate .Alert.Summary 80}}            {{/* 按 Unicode 字符截取，超出加 … */}}
{{default "暂无" .Value}}
{{join .Target.Tags "、"}}
```

## 完整 JSON 2.0

`extends` 与 `raw` 二选一。优先使用 YAML 对象：每个字符串字段独立执行模板，再由 JSON 编码器编码；因此条件表达式中的引号不需要 JSON 转义。

```yaml
id: raw-demo
name: 自定义 JSON 卡片
raw:
  firing:
    schema: "2.0"
    body:
      elements:
        - tag: markdown
          content: '{{if eq .Alert.State "resolved"}}已恢复{{else}}告警{{end}}：{{.Alert.Summary}}'
  resolved:
    schema: "2.0"
    body:
      elements:
        - tag: markdown
          content: '已恢复：{{.Alert.Summary}}，持续 {{.Alert.Duration}}'
```

没有 `resolved` 时沿用 `firing`。也可以不分状态，直接在 `raw` 下放 `schema` 与 `body`。字符串形式如下；变量放在 JSON 字符串内，渲染器执行 JSON 转义，避免引号或换行破坏结构。需要大量条件和循环时使用上面的对象写法。

```yaml
id: raw-string
raw: |
  {"schema":"2.0","body":{"elements":[{"tag":"markdown","content":"{{.Alert.Summary}}"}]}}
```

可将卡片搭建工具导出的 JSON 改为 `raw`，再替换变量。组件结构参考[官方 JSON 2.0 组件](https://open.feishu.cn/document/feishu-cards/card-json-v2-components/component-json-v2-overview?lang=en-US)，按钮跳转参考[官方发送内容示例](https://open.feishu.cn/document/server-docs/im-v1/message-content-description/create_json?lang=zh-CN)。折叠面板使用 [collapsible_panel](https://open.feishu.cn/document/feishu-cards/card-json-v2-components/containers/collapsible-panel)，搭建工具的可视编辑不支持该组件；实际客户端显示需另行验收。

## 转义、大小与错误处理

数据中的引号、换行由 JSON 编码处理；Markdown 变量中的反斜杠、星号、下划线、方括号、反引号、波浪线、井号、竖线、感叹号和 HTML 特殊字符均转义。模板本身可写 Markdown 格式，但原始模板中的 `<at ...>` 也会被禁用，只有 `mention` 配置能产生 @。链接按钮只接受无用户凭据的 HTTP/HTTPS URL。

卡片限制为 **28,000 字节**。超过后先保留最近 2048、512 个日志字符，再缩减汇总为 10、1 条；仍超出时用有界的简洁卡片，并提示降级。汇总的“还有 N 条”包含被大小限制省略的记录。

模板语法、缺失变量、错误字段、非法 JSON 等导致渲染失败时，运行发送使用标准模板，在底部注明“自定义模板 xxx 出错，已使用标准模板”，并记录不含告警原文的错误日志；`template validate` 则报告错误并退出 1。JSON、基础卡片结构与大小校验通过不等于飞书服务端已接受所有组件；真实发送和显示按开通向导及验收步骤核实。
