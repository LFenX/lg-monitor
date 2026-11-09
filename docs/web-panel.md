# Web 面板

“通知 → 飞书卡片操作”按已有飞书应用群显示授权和长连接状态，read/config 只读、admin 可显式启用或撤销。页面说明群原消息收件人的两种有限权限，连接状态每五秒刷新，离开即取消请求；真实订阅与真人点击仍 Pending。接入和作用范围见 [卡片动作](card-actions.md)。

构建：Go 1.27、Node 22.22.2+（22.x）和 pnpm 11.21.0。执行 `scripts/build.ps1`，先安装锁定的前端依赖并构建，再以 webui 标签将产物嵌入 `bin/lgmon.exe`。运行编译后的 exe 不需要 Node。前端产物与 node_modules 不提交；pnpm 11 的项目设置在 web/pnpm-workspace.yaml，缓存与状态放在仓库的 dev-home/，不是 .npmrc。

在监控器运行时打开 `http://127.0.0.1:7444/`。只有直接回环访问的首页携带仅限本机的只读会话，远程首页不携带令牌。手机使用监控器允许的地址访问，在页面填写本人创建的令牌；监听和令牌创建见 [CLI](cli.md)。页面不改变监听、防火墙或服务配置。

访问令牌只保存在当前标签页会话，不写 URL、日志或 localStorage。主题偏好保存在 localStorage。服务重启使本机会话失效时重新加载首页；远程令牌失效会回到连接页面。右上角“只读 / 可配置 / 管理员”来自实际认证响应的 X-Lgmon-Scope，不能作为服务端授权依据。read 只能查看，config/admin 才显示可执行的确认与静音；后端仍逐请求检查权限。

使用 http: 且访问主机不属于回环或 Tailscale 网段时，登录表单前及页面顶部显示“正在通过不加密的 HTTP 在局域网提供面板，同一网络里的人可能截获令牌。建议改为只监听 Tailscale 地址”。按浏览器地址判断，不依据后端的 publicBaseUrl；HTTPS、localhost、IPv4/IPv6 回环和 Tailscale 地址不提示，其他 DNS 名称提示。HTTPWarning 组件只展示地址风险，不修改监听配置。

## 页面与数据

有自建或加入团队后，面板页出现“团队共享”分组与“团队与分享”入口。负责人用 admin 管理成员和分享；成员用 read 查看授权卡片，支持时间、主机选择及手机/深浅色，失联或撤销后清空旧数据。首次设备加入可直接打开 `/#/teams/join`，默认游客页面不显示团队入口。详见 [团队共享](teams.md)。

告警行的“诊断包”进入 `/#/alerts/<id>/diagnostics`，read 即可查看、刷新、复制交接文本并下载同一 JSON。手机和深浅色均支持，复制不可用可手动选择文本。只读范围、数据完整性和字段隐私见 [诊断包](diagnostics.md)。

| 页面 | 入口与出口 | 数据与状态 | 阶段 |
|---|---|---|---|
| 总览 | `/`，主机卡进入详情，告警区域进入告警页 | collectors、targets、firing alerts、通知配置；只读 SQL 按索引取各主机最后一条系统指标 | M1-13 |
| 主机详情 | `/#/hosts/<采集器>`，目标行进入目标详情，面包屑返回 | metrics 历史、目标/服务、最近数据缺口，以及只读启动项/计划任务、来源覆盖和最近变化（见 [清单](inventory.md)） | M1-13/M2-04 |
| 目标详情 | `/#/hosts/<采集器>/targets/<目标>`，面包屑返回主机 | 状态、探测耗时、进程 CPU/RSS 历史与时间范围内相关告警 | M1-13 |
| 告警 | `/#/alerts`，可筛选、确认或静音 | alerts；确认调用 ack；静音按规则作用于全部匹配实例，状态继续记录 | M1-13 |
| 通知 | `/#/notifications`，接入入口与返回链接 | 应用、渠道、filter、双状态模板库和规则模板选择 | M1-13/M1-14 |
| 模板编辑 | `/#/notifications/templates/<代号>`，new 新建；返回通知 | extends 基础表单、raw JSON、后端实时预览、校验保存与当前草稿试发 | M1-14 |
| 飞书接入 | `/#/notifications/setup`，返回通知页 | 五步向导：凭据、机器人、权限、多群发现及模板、本人确认收到后保存 | M1-14 |
| 规则通知 | `/#/notifications/rules`，返回通知页 | 修改指定规则的 notify 和模板，保留其他规则字段 | M1-14 |
| 面板 | `/#/boards` 或 `/#/boards/<id>`，可切换页面 | 服务端卡片查询、相对/固定时间、主机选择、独立刷新、七类渲染和热加载错误 | M1-15 |

通知提示条在没有配置任何通知渠道时显示“还没有设置告警通知”。已有 Webhook 或 Windows 渠道时不误报未设置，通知页仍保留飞书接入入口。通知配置存在不等于已经验证本人收件。

历史范围为 1 小时、6 小时、24 小时、7 天。前两种用 raw，24 小时用 1m，7 天用 1h；状态仍取原始采样，原始清理后的缺失不由数值汇总伪造。曲线按时间排序、插入观测缺口；不把缺失值画成零，不把最后的目标状态延伸到无采样区间。离线卡片明确显示上次采样。历史过大或读取失败显示中文错误，不静默截断服务或历史列表。

SSE 用带 Authorization 的 fetch，解析分块 UTF-8、CRLF、多行 data、heartbeat 和 reset。reset 或重连触发重新读取快照；最多每秒合并刷新一次，慢请求完成后才处理合并的下一次刷新。页面、身份或筛选条件变化会取消旧请求；相同时间戳下仍通过独立版本计数刷新详情。断线显示重连状态，断线期间每 15 秒补取快照，不把旧数据当成刚更新。图表只在详情加载，离开页面释放实例，数据刷新保持实例与缩放。

## 控件绑定（M1-13）

同一数据驱动控件按采集器/目标/告警 ID 实例化；手机导航是同一目的地的布局变体。以下编号指源码中的控件模板，纯展示控件同样列出来源。

| 编号 | 控件 / 所在源码 | 输入与出口 | 最低权限 | 验证 |
|---|---|---|---|---|
| C-001 | 总览导航，App.navigation | hash 到总览 | read | 浏览器总览 |
| C-002 | 告警导航，App.navigation | hash 到告警列表 | read | 浏览器告警 |
| C-003 | 面板导航，App.navigation | hash 到面板列表与声明式页面 | read | 三示例原生浏览器 |
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
| C-034 | 接入飞书与返回，Notifications / FeishuSetup | 进入五步网页接入，返回通知 | read；操作需 config/admin | 浏览器入口与完整向导 |
| C-035 | 面板入口/空状态，Dashboards | 读取真实列表；无配置显示放置示例的说明 | read | 空配置原套件与三示例 |
| C-036 | 空数据、错误与旧快照提示，ui / App | 无数据不造值；失败保留上次快照并明示 | read | Vitest、浏览器、API 错误测试 |

## M1-14 控件绑定

五步网页向导复用 setup 的检查、测试发送和保存渠道逻辑。首次填凭据需要 admin，权限不足给出管理员令牌和 CLI 接入两种办法；config 可操作已保存应用。凭据验证后清空 Secret，不保存在浏览器存储。机器人和权限自动检查，群每五秒刷新，可同时选择多个群，各选模板、告警范围和规则，规则默认全选。确认收到前不写渠道；“没收到”列排查步骤，十秒后重发。关闭或离开页面丢弃本页收件状态，重新进入要重发。

保存先重新确认群成员关系，再合并渠道与规则。全部告警在新建时省略 filter，重置已有 filter 时显式传 {}；未修改的自定义筛选条件省略以保留。所选规则级别不在范围内时要求取消勾选，不静默删规则。规则写入失败显示已保存渠道的真实状态，链接到规则通知设置补充。服务端不记录网页确认状态，配置令牌原本就能写配置。

模板库逐个显示触发和恢复预览；预设另存为自定义模板，不能覆盖预设。基础模式与高级模式对应互斥的 extends/raw 写法，分别保留本页草稿，只保存当前模式，不声称能无损转换两种定义。基础表单支持标题、四种状态颜色、内置字段选择及顺序、附加字段、成员提醒、按钮和备注；简洁/汇总的固定布局不使用内置字段，简洁没有标题栏，对应控件禁用并说明。成员输入保留逗号和空格，不在输入过程中删掉分隔符。

前端使用锁定的 [react-markdown](https://github.com/remarkjs/react-markdown) 10.1.0 渲染文本，原始 HTML 作为文本处理，链接与图片由受控组件替换；不加载外部图片，不执行卡片按钮。支持标题栏/颜色、Markdown、两列字段、分割线、按钮和折叠面板，未知组件明确提示，深层嵌套明确停止预览。实际飞书显示仍由本人验收。

| 编号 | 控件 / 源码 | 输入与出口 | 最低权限 | 当前证据 |
|---|---|---|---|---|
| C-037 | 刷新模板，TemplateLibrary | 重新读取列表及双状态预览 | read | 浏览器请求计数 |
| C-038 | 新建/自定义/编辑，TemplateLibrary | 编辑 hash 路由 | read；保存需 config | 浏览器进入编辑器 |
| C-039 | 双状态模板卡，CardPreview | preview API 的真实 card | read | 浏览器双状态，Markdown/折叠测试 |
| C-040 | 模板名称、代号，TemplateEditor | 草稿与代号校验 | read；保存需 config | 浏览器保存后 GET 核对 |
| C-041 | 基础/高级模式，TemplateEditor | extends/raw，不隐式转换 | read | 组件与浏览器 JSON 错误 |
| C-042 | 基础预设，BasicEditor | 读取预设定义，保留覆盖 | read | 源码绑定与真实读取 |
| C-043 | 标题与颜色，BasicEditor | 草稿预览，字段错误 | read | 组件与浏览器修改 |
| C-044 | 字段勾选、上/下移，BasicEditor | fields 数组与顺序 | read | 浏览器重排、取消全部，HTTP 往返 |
| C-045 | 附加字段新增/修改/移除，BasicEditor | extraFields 数组 | read | 新增行浏览器；索引绑定源码 |
| C-046 | 状态与提醒对象，BasicEditor | mention；所有人或成员列表 | read | 浏览器所有人；逐字输入多名成员 |
| C-047 | 按钮新增/修改/移除，BasicEditor | buttons 数组，后端检查 URL | read | 浏览器保存、预览禁用；索引绑定 |
| C-048 | 底部备注，BasicEditor | note 与后端渲染 | read | 浏览器保存后 GET 核对 |
| C-049 | raw JSON，TemplateEditor | 本地语法与后端模板校验 | read | JSON/未知变量浏览器测试 |
| C-050 | 变量说明与插入，TemplateEditor | 说明；高级模式按光标插入 | read | 浏览器指定光标插入主机变量，并核对实际预览 |
| C-051 | 触发/恢复切换，TemplateEditor | 已校验的两份结果 | read | 库双状态；高级编辑器切换两份 raw 卡片 |
| C-052 | 校验并保存，TemplateEditor | preview 后 PUT，再次校验 | config | 组件调用顺序、浏览器保存 |
| C-053 | 返回通知，TemplateEditor | hash 返回，取消预览 | read | 路由与 effect 清理 |
| C-054 | 规则模板选择，RuleTemplates | 最新规则，只换所选 template | config | 浏览器整份规则前后比较 |
| C-055 | 应用选择，FeishuSetup | 实际应用检查，切换取消并清空选群 | config | API/组件与浏览器 |
| C-056 | 重新检查/自动刷新，FeishuInspection | GET feishu/check，五秒且不重叠 | config | 组件取消、浏览器状态转换 |
| C-057 | 机器人/权限按钮，FeishuInspection | 仅官方 HTTPS，新标签页 | config | 浏览器 URL/target，组件拒绝其他来源 |
| C-058 | 权限、群、详情，FeishuInspection | 中文用途、人数、新群；标识在折叠详情 | config | 自动发现、默认隐藏测试 |
| C-059 | App ID/Secret、平台、验证保存，FeishuSetup | POST feishu/apps；验证后进入检查，清空 Secret | admin | 从零完整浏览器 |
| C-060 | 凭据权限提示、后台链接，FeishuSetup | 管理员令牌/CLI 说明，官方 HTTPS 新标签页 | read | 浏览器只读初始态 |
| C-061 | 群勾选，FeishuSetup | 自动发现群列表，多群独立设置 | config | 双群浏览器 |
| C-062 | 群模板、范围、规则勾选，GroupSetup | template/filter/rules；规则默认全选 | config | 双群保存后真实配置核对 |
| C-063 | 测试发送与倒计时，GroupSetup | POST feishu/test；十秒限频，不保存 | config | 未确认配置不变，真实十秒等待 |
| C-064 | 收到了/没收到，GroupSetup | 仅成功试发后出现；确认才 POST feishu/notifiers | config | 浏览器拒收/收件保存，API 部分失败 |
| C-065 | 查看、继续修改、补规则，GroupSetup | 通知/规则路由或恢复表单 | config | 成功浏览器，部分保存接口测试与绑定 |
| C-066 | 试发入口与模态关闭，TemplateEditor / FeishuTrial | 捕获当前草稿，默认已配置群，可选择专用群 | config | 真实 API 浏览器；假发送端核对 |
| C-067 | 试发方式/群/发送，FeishuTrial | app+chat+draft+当前状态，或自定义机器人 draft | config | 浏览器配置不变，HTTP Webhook 草稿 |
| C-068 | 管理规则通知、渠道勾选/保存，RuleNotifications | fresh GET 后只改指定规则 notify，后端完整校验 | config | 浏览器实际规则前后比较 |

GET feishu/check 额外返回可选 botName，来自原有 BotInfo，网页不解析中文 message 来提取名称。模板 JSON 保留 fields=[] 与省略 fields 的区别：前者隐藏内置字段，后者继承；预览、保存、重新读取再编辑保持一致。读取/预览套件检查飞书发送、Webhook 构造和 Windows 通知次数均为零；独立向导/试发套件只向假客户端发送，并核对当前未保存草稿的内容与状态，未操作真实飞书。

## 开发与验证

M1-17 增加全局通知暂停提示。C-077 对应 App.NotificationPauseBanner：读取 GET notifications/pause，在 SSE pause 后重新读取快照；显示服务端截止时间。恢复按钮 DELETE 同一路径，需要 config/admin；read 禁用，等待中禁用重复提交，失败保留横幅并显示中文错误，可重试。State 卸载取消请求，SSE 断开时沿用现有轮询。桌面及 390 像素手机真实 Edge 用例验证 SSE 开始、只读权限、配置令牌恢复和无横向溢出；前端单测验证失败及重试。

本轮截图使用独立环境变量 `LGMON_M117_SCREENSHOT_DIR`，文件 [桌面暂停提示](m1-17-pause-desktop.png)、[手机暂停提示](m1-17-pause-mobile.png)。真实 Windows 托盘的人工验收步骤见 [命令行](cli.md#全局通知暂停与托盘m1-17) 及 PLAN M1-17 执行记录；网页截图不代替托盘显示确认。

M1-15 的绑定见 [dashboards.md](dashboards.md)。以下控件都只读，不修改面板文件或通知配置。

| 编号 | 控件 / 源码 | 输入与出口 | 验证 |
|---|---|---|---|
| C-069 | 刷新列表、页面选择，Dashboards | GET dashboards；hash 选择与书签 | 三示例切换、删除 |
| C-070 | 刷新面板，DashboardView | 重读定义并重查所有卡片 | 浏览器真实数据请求 |
| C-071 | 时间方式、相对范围，DashboardView | range 原样提交，按服务端时间计算 | 浏览器时钟改为 2099 年仍正确 |
| C-072 | 固定起止输入，DashboardView | UTC 毫秒 from/to；无效时不发请求 | 固定时间请求断言 |
| C-073 | 主机选择，DashboardView | 只在卡片声明 collector 时显示；空值为全部 | 主机参数与 NULL 查询 |
| C-074 | 七类卡片，DashboardPanel | 每卡 GET 数据，统一服务端校验、独立错误 | 实际 SQLite、ECharts SVG、错误隔离 |
| C-075 | 面板详情展开，DashboardView | 文件、版本和已保存 SQL | 页面绑定；不执行 HTML |
| C-076 | SSE 与轮询，state / LivePanel | 按 id 重读，版本不符重读；卸载取消 | 热更新/损坏/删除浏览器 |

```powershell
pnpm --dir web install --frozen-lockfile
pnpm --dir web test
pnpm --dir web build
go test -tags webui ./web ./internal/api ./internal/app
go test -count=1 -tags 'integration webui' ./internal/app -run 'TestNative(WebPanel|FeishuWizard)Browser' -v
go test -count=1 -tags 'integration webui' ./internal/app -run '^TestNativeDashboardBrowser$' -v
```

浏览器验证使用已安装的 Edge，Go 测试创建临时 SQLite、HTTP API 和虚构数据，Node 子进程收到握手后才启动浏览器；进程属于测试自己的 Windows Job，结束和取消均清理子进程，不连接真实监控 home 或飞书。M1-14 截图使用 `$env:LGMON_M114_SCREENSHOT_DIR = Join-Path (Get-Location) 'docs'`；M1-13 的 LGMON_SCREENSHOT_DIR 仅用于原阶段证据，避免覆盖历史截图。Playwright 单独运行时必须提供测试创建的回环地址，不能对远程环境运行确认/静音或发送测试。

无 webui 标签的 Go 检查用于后端开发，不包含页面，访问根目录明确报 503。开发时 `pnpm --dir web dev` 提供 Vite，代理 /api 到默认监控器；开发入口不自动注入本机会话，需要自行填写开发监控器的令牌。正式程序统一由构建脚本产出。Web 验证不安装服务，不修改防火墙，不自动发送消息。

主机卡片显示超过正负十秒的时钟偏差提示：正数为采集器慢，负数为采集器快；同步以后随刷新或实时快照更新移除提示。告警数据仍显示原始时间，时钟不准会影响故障时间及 SQL 规则的数据可见时间。

顶部在采集器配置提示旁显示当前规则错误，包含旧采集器缺少证书上报能力和 SQL 执行错误。初次加载及 SSE 重连读取 GET /api/v1/rule-errors；ruleErrors 事件直接更新整个数组。最多列三条，更多时显示总数，恢复后移除提示。桌面和 390 像素手机的真实浏览器用例为 TestNativeRuleErrorsBrowser，截图变量为 LGMON_R502_SCREENSHOT_DIR。

主机卡片增加缓存用量、写入速度和预计可缓存小时数，使用十进制 MB，与 cache.maxSize 一致。启动不足五分钟显示估算中，旧采集器没有数据时不伪造数值；预计不足 24 小时显示调小数据量或调大本机上限的提示。新心跳随现有刷新/15 秒轮询更新。真实桌面/390 像素手机用例为 TestNativeCacheCapacityBrowser，截图变量为 LGMON_M122_SCREENSHOT_DIR。
