# 自定义 HTML 面板

在监控器 home/config/dashboards 放入同 ID 的 YAML 和 HTML，随后在网页“面板”选择它。现有声明式页面不变；两种页面都通过已认证的 HTTP/MCP 读取。

```yaml
id: custom-overview
title: 自定义运行图谱
refresh: 10s
defaultRange: 1h
html: custom-overview.html
```

html 和 panels 二选一。HTML 必须在同目录、文件名等于 `<id>.html`，不接受绝对路径、子目录或其他文件；最多 1 MiB、UTF-8、没有 NUL。两个文件保存后自动加载，只改 HTML 也会更新版本。错误时只给这一页提示并保留上次成功版本，修复后恢复；删除 YAML 才移除页面。完整示例为 [描述文件](../examples/dashboards/custom-overview.yaml) 和 [HTML](../examples/dashboards/custom-overview.html)，复制这两个文件到实际监控 home 后运行 `lgmon config validate --json`。

HTML 在独立的只读 iframe 内运行，可以使用内联 HTML/CSS/JavaScript、SVG 和 Canvas。内置 lgmon.js 在用户脚本前自动注入，脚本执行时内容已装入 DOM，可直接读取页面元素。主题通过 CSS 变量 `--lgmon-bg`、`--lgmon-text`、`--lgmon-border` 提供，html 的 data-theme 为 light 或 dark。内置 viewport 支持手机；布局由自己的 CSS 控制。

```javascript
async function load() {
  const result = await lgmon.query('SELECT count(*) AS value FROM collectors')
  console.log(result.columns, result.rows, result.truncated, result.params)
}
load().catch(console.error)
const stop = lgmon.subscribe('target', (data, event) => console.log(event, data))
stop()
```

query 返回与 POST /api/v1/query 一致的 columns/rows/truncated/params。省略 params 使用页面时间和主机选择；显式 range 或 from/to 会替换上下文的整组时间参数，collector 可以是已登记 ID 或 null（全部）。最终范围由服务器解析，SQL 必须自行使用 `:from`、`:to`、`:collector` 条件。支持第三个参数 `{signal: controller.signal}` 取消；结果仍是只读数据，不能使用 SQL 写入、PRAGMA 或其他操作。

每个容器最多四个并发查询，传给页面的结果上限 16 MiB；服务器沿用五秒、一万行限制。主动取消、超时和离开页面都会终止请求。descriptor 的 refresh 只作为上下文提供，HTML 作者通过 query/subscribe 管理更新，宿主页不增加轮询。示例合并短时间内的事件，避免每条采样重复查询。

subscribe 支持 target、alert、sample、dashboard、pause、ruleErrors 和 `*`，回调为 `(data,event)`，data 与既有 SSE JSON 相同，返回函数取消这一个回调。多个回调共享一次订阅；所有 HTML 使用宿主页原有的一条 SSE。context 订阅会立即送当前上下文并在时间/主机/主题改变时更新，connection 会送 connecting/connected/reconnecting 状态；断线恢复后应重新查询当前值。

`lgmon.context` 包含 title、theme、refresh、params，没有令牌。事件处理最多允许 64 条未确认消息；脚本阻塞导致超限时停止该页订阅并提示刷新，其他页面继续。脚本或查询错误显示纯文本，不插入宿主页 DOM。

HTML 没有父页面 DOM、存储或 API 令牌访问权，不能直连 API、加载外部脚本/图片、提交表单、开弹窗、下载或跳转顶层。数据只经过 query/subscribe 代理。容器采用 iframe 与响应 CSP 两层 sandbox allow-scripts，没有 allow-same-origin；主页面的脚本策略保持不变，子导航限制在本站。设计依据为 [WHATWG sandbox](https://html.spec.whatwg.org/multipage/iframe-embed-object.html) 与 [W3C CSP](https://www.w3.org/TR/CSP3/#directive-frame-src)，实际隔离行为由 Edge 集成测试验证。

用户源码只在 read 的 GET /api/v1/dashboards/{id} 的 `html:{file,source}` 中返回；HTML 页 panels 为 []，列表有 html:true。MCP get_dashboard 返回相同内容。内置 /lgmon-frame.html 和 /lgmon.js 仅是通用框架资源，不含用户源码、数据或会话；直接打开框架不能查询。当前没有网页/HTTP/MCP 保存面板入口，Agent 直接写本机文件；没有文件权限时提供完整文件和保存位置，由本人保存，不宣称已经写入。

HTML 草稿可与 YAML 一同提交给 validate_config，只读校验不执行脚本、不写文件。团队共享面板仍仅针对声明式面板，M2-05 的真实双机开工条件和 M2 整体验收条件保持。
