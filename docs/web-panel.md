# Web 面板

构建：Go 1.27、Node 22.22.2+（22.x）和 pnpm 11.21.0。执行 `scripts/build.ps1`，先安装锁定的前端依赖并构建，再以 webui 标签将产物嵌入 `bin/lgmon.exe`。运行编译后的 exe 不需要 Node。前端产物与 node_modules 不提交；pnpm 11 的项目设置在 web/pnpm-workspace.yaml，缓存与状态放在仓库的 dev-home/，不是 .npmrc。

在监控器运行时打开 `http://127.0.0.1:7444/`。只有直接回环访问的首页携带仅限本机的只读会话，远程首页不携带令牌。手机使用监控器允许的地址访问，在页面填写本人创建的令牌；监听和令牌创建见 [CLI](cli.md)。页面不改变监听、防火墙或服务配置。

访问令牌只保存在当前标签页会话，不写 URL、日志或 localStorage。主题偏好保存在 localStorage。服务重启使本机会话失效时重新加载首页；远程令牌失效会回到连接页面。右上角“只读 / 可配置 / 管理员”来自实际认证响应的 X-Lgmon-Scope，不能作为服务端授权依据。read 只能查看，config/admin 才显示可执行的确认与静音；后端仍逐请求检查权限。

## 页面与数据

| 页面 | 入口与出口 | 数据与状态 | 阶段 |
|---|---|---|---|
| 总览 | `/`，主机卡进入详情，告警区域进入告警页 | collectors、targets、firing alerts、通知配置；只读 SQL 按索引取各主机最后一条系统指标 | M1-13 |
| 主机详情 | `/#/hosts/<采集器>`，目标行进入目标详情，面包屑返回 | metrics 历史、当前目标、只读 SQL 的 Windows 服务最新状态与最近 100 条数据缺口 | M1-13 |
| 目标详情 | `/#/hosts/<采集器>/targets/<目标>`，面包屑返回主机 | 状态、探测耗时、进程 CPU/RSS 历史与时间范围内相关告警 | M1-13 |
| 告警 | `/#/alerts`，可筛选、确认或静音 | alerts；确认调用 ack；静音按规则作用于全部匹配实例，状态继续记录 | M1-13 |
| 通知 | `/#/notifications`，接入入口与返回链接 | 当前应用、渠道、filter、预设与自定义模板的真实只读列表 | M1-13；网页向导与编辑器由 M1-14 接入 |
| 飞书接入 | `/#/notifications/setup`，返回通知页 | 当前提供已实现的 CLI 向导步骤，不自动执行命令或发消息 | M1-14 的网页向导入口 |
| 面板 | `/#/boards`，可返回总览 | 当前明确显示声明式面板尚未开放，不伪造面板列表或查询结果 | M1-15 接入声明式渲染 |

通知提示条在没有配置任何通知渠道时显示“还没有设置告警通知”。已有 Webhook 或 Windows 渠道时不误报未设置，通知页仍保留飞书接入入口。通知配置存在不等于已经验证本人收件。

历史范围为 1 小时、6 小时、24 小时、7 天。前两种用 raw，24 小时用 1m，7 天用 1h；状态仍取原始采样，原始清理后的缺失不由数值汇总伪造。曲线按时间排序、插入观测缺口；不把缺失值画成零，不把最后的目标状态延伸到无采样区间。离线卡片明确显示上次采样。历史过大或读取失败显示中文错误，不静默截断服务或历史列表。

SSE 用带 Authorization 的 fetch，解析分块 UTF-8、CRLF、多行 data、heartbeat 和 reset。reset 或重连触发重新读取快照；最多每秒合并刷新一次，慢请求完成后才处理合并的下一次刷新。页面、身份或筛选条件变化会取消旧请求；相同时间戳下仍通过独立版本计数刷新详情。断线显示重连状态，断线期间每 15 秒补取快照，不把旧数据当成刚更新。图表只在详情加载，离开页面释放实例，数据刷新保持实例与缩放。

## 控件绑定（M1-13）

同一数据驱动控件按采集器/目标/告警 ID 实例化；手机导航是同一目的地的布局变体。以下编号指源码中的控件模板，纯展示控件同样列出来源。

| 编号 | 控件 / 所在源码 | 输入与出口 | 最低权限 | 验证 |
|---|---|---|---|---|
| C-001 | 总览导航，App.navigation | hash 到总览 | read | 浏览器总览 |
| C-002 | 告警导航，App.navigation | hash 到告警列表 | read | 浏览器告警 |
| C-003 | 面板导航，App.navigation | hash 到能力说明；返回总览 | read | 浏览器入口 |
| C-004 | 通知导航，App.navigation | hash 到应用/渠道/模板列表 | read | 浏览器入口 |
| C-005 | 品牌入口，App.Brand | 返回总览 | read | 浏览器导航 |
| C-006 | 刷新，App.Shell | 重新读取真实快照 | read | SSE/reset 与慢请求测试 |
| C-007 | 主题切换，App.Shell | 深浅色与本机偏好 | read | Vitest、手机浏览器 |
| C-008 | 权限入口与关闭，App / ui.Modal | 打开/关闭令牌表单，不改变已有身份 | read | 取消未完成登录测试 |
| C-009 | 令牌输入，App.Login | 隐藏输入；取消后中止请求 | 无；凭据由后端核验 | Vitest 取消与只读隔离 |
| C-010 | 连接，App.Login | GET collectors 成功才切换身份 | 所提供令牌至少 read | Vitest、真实 API 浏览器 |
| C-011 | 获取令牌说明，App.Login | 展开 CLI 操作说明 | 无 | 组件与源码核对 |
| C-012 | 连接、权限与更新时间展示，App.Shell | SSE / scope header / 快照时间 | read | 浏览器连接与只读 |
| C-013 | 未设置通知提示条，Overview | 配置为空时进入飞书接入说明 | read | Vitest 空配置 |
| C-014 | 告警展示与查看全部，Overview.AlertRows | firing alerts；主机/目标/告警链接 | read | 浏览器快照 |
| C-015 | 四项统计，Overview.Stat | 在线主机、正常目标、firing alerts、渠道数量 | read | 浏览器与真实 SQLite |
| C-016 | 主机名与详情链接，Overview.HostCard | 按 collector ID 进入详情 | read | 浏览器详情 |
| C-017 | CPU/内存与目标数，Overview.HostCard | SQL 最新指标与当前目标，未知不为零 | read | Vitest 零值、浏览器 |
| C-018 | 时间范围，ui.Range | 改变历史 from/to/step，取消旧条件请求 | read | 浏览器范围、资源取消测试 |
| C-019 | 系统指标选择，Details.HostDetail | 对应名称的 metrics 查询 | read | 真实 API 与图表 |
| C-020 | 图表与缩放，Chart | ECharts 真实历史、资源分组、缺口；卸载释放 | read | 浏览器 SVG 与状态曲线 |
| C-021 | 目标列表与链接，Details.HostDetail | 当前状态到目标详情 | read | 浏览器详情 |
| C-022 | 服务与缺口展示，Details.HostDetail | 只读 SQL，明确观察状态与缺口 | read | 浏览器服务/缺口 |
| C-023 | 面包屑，Details / Notifications.Setup | 返回上一级 | read | 浏览器路径 |
| C-024 | 状态筛选，Alerts | alerts state；历史范围适用于恢复/全部 | read | API 与浏览器 |
| C-025 | 级别筛选，Alerts | 当前结果按 severity 筛选 | read | 浏览器切换后核对实际结果 |
| C-026 | 主机筛选，Alerts | 当前结果按 collector ID 筛选 | read | 浏览器切换后核对实际结果 |
| C-027 | 搜索，Alerts | 按摘要、目标名称/ID 搜索当前结果 | read | 浏览器输入与清空后核对结果 |
| C-028 | 确认，Alerts | POST ack；成功后重新读库，已确认禁用 | config | Vitest、真实 API 浏览器 |
| C-029 | 静音入口与取消，Alerts / ui.Modal | 打开/关闭规则静音表单 | config | 浏览器静音 |
| C-030 | 静音时长，Alerts.Silence | 15m / 1h / 4h / 24h | config | 浏览器与既有 API 校验 |
| C-031 | 静音原因，Alerts.Silence | 可选输入传给 silences | config | 浏览器核对实际数据库 |
| C-032 | 设置静音，Alerts.Silence | POST silences，成功才提示完成 | config | 浏览器核对实际数据库 |
| C-033 | 应用、渠道、filter 与模板，Notifications | 当前只读配置与模板 API | read | 浏览器真实列表 |
| C-034 | 接入飞书与返回，Notifications / Setup | 当前已实现 CLI 步骤，返回通知 | read | 浏览器入口；M1-14 后继续 |
| C-035 | 面板说明与返回，Notifications.Boards | 说明当前边界，返回总览 | read | 浏览器入口；M1-15 后继续 |
| C-036 | 空数据、错误与旧快照提示，ui / App | 无数据不造值；失败保留上次快照并明示 | read | Vitest、浏览器、API 错误测试 |

## 开发与验证

```powershell
pnpm --dir web install --frozen-lockfile
pnpm --dir web test
pnpm --dir web build
go test -tags webui ./web ./internal/api ./internal/app
go test -count=1 -tags 'integration webui' ./internal/app -run '^TestNativeWebPanelBrowser$' -v
```

浏览器验证使用已安装的 Edge，Go 测试创建临时 SQLite、HTTP API 和虚构数据，Node 子进程收到握手后才启动浏览器；进程属于测试自己的 Windows Job，结束和取消均清理子进程，不连接真实监控 home 或飞书。需要重新生成截图时，在运行该 Go 测试前设置 `$env:LGMON_SCREENSHOT_DIR = Join-Path (Get-Location) 'docs'`。Playwright 单独运行时必须提供测试创建的回环地址，不能对远程环境运行确认/静音测试。

无 webui 标签的 Go 检查用于后端开发，不包含页面，访问根目录明确报 503。开发时 `pnpm --dir web dev` 提供 Vite，代理 /api 到默认监控器；开发入口不自动注入本机会话，需要自行填写开发监控器的令牌。正式程序统一由构建脚本产出。Web 验证不安装服务，不修改防火墙，不自动发送消息。
