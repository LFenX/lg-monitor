/* Copyright (c) 2026 LFen (https://lfen.ink) — lg-monitor */

import type { Lang } from './i18n'

/*
 * Every message lgmon hands to an agent, written the same way: a one-line goal, then short titled sections
 * (connect, first step, how, rules, done when) with the exact tool calls and argument shapes, so an agent does
 * the job in a few calls instead of trying things. They never ask the person to fill a blank: the agent asks.
 */

export function quote(value: string) {
  return "'" + value.replace(/'/g, "''") + "'"
}

type Install = { executable: string; home: string; url: string }

export function agentSetup(install?: Install, tools: 'essential' | 'all' = 'all', lang: Lang = 'zh') {
  if (!install?.executable || !install.home) return ''
  const command =
    '& ' +
    quote(install.executable) +
    ' agent connect --home ' +
    quote(install.home) +
    (install.url ? ' --url ' + quote(install.url) : '') +
    (tools === 'all' ? ' --tools all' : '') +
    ' --elevate --json'
  if (lang === 'en') {
    return [
      '## 0. Connect to lgmon (skip if you already have lgmon MCP tools)',
      tools === 'all'
        ? 'If not connected yet, or if your current toolset lacks the tools for this task, run this command yourself in PowerShell on this computer (do not ask me to run it manually):'
        : 'If not connected yet, run this command yourself in PowerShell on this computer (do not ask me to run it manually):',
      command,
      '- If administrator privileges are needed, Windows will prompt me once; if I cancel, stop and explain instead of re-prompting.',
      '- Read the returned JSON in memory only; never echo tokens into chat, screenshots, logs, CLI args, or git.',
      '- Use the returned MCP config if you can attach dynamically (pass the token via env); otherwise call the returned HTTP MCP endpoint directly with the Authorization header (initialize, tools/list, tools/call) without asking me to restart the session.',
      '- Reuse an existing connection instead of creating duplicate tokens.',
      '',
      '',
    ].join('\n')
  }
  return [
    '## 0. 接入 lgmon（已经能调用 lgmon 的 MCP 工具就跳过）',
    tools === 'all'
      ? '没有接入、或当前工具集缺少这个任务要用的工具时，你自己在这台电脑的 PowerShell 里运行（不要让我手动运行）：'
      : '没有接入时，你自己在这台电脑的 PowerShell 里运行（不要让我手动运行）：',
    command,
    '- 需要管理员时会弹出 Windows 授权窗口，由我确认一次；我取消就停下并说明原因，不要反复弹窗。',
    '- 只在内存里读取返回的 JSON；令牌不要出现在聊天、截图、日志、命令行参数或仓库里。',
    '- 能动态连接 MCP 就用返回的配置（令牌通过子进程环境变量传）；不能时用返回的 http 地址和 Authorization 请求头直接发 MCP 请求（initialize、tools/list、tools/call），不要让我重开会话。',
    '- 已经接入就复用，不重复创建令牌。',
    '',
    '',
  ].join('\n')
}

const lines = (...parts: (string | false | undefined)[]) =>
  parts.filter((part) => part !== false && part !== undefined).join('\n')

/* What every task shares: how to add a service in one go, so no agent has to discover it. */
const addRecipeZh = [
  '- 找对象：`explore {"kind":"ports"}`（也可以是 services、processes、tasks，带 `"q":"关键词"` 缩小范围），结果里的 `suggest` 原样用作参数。不要让我自己查端口或进程名。',
  '- 跟我确认名字和面板后，一次调用建好监控、告警和通知：`add_monitor {"name":"订单接口","port":8080,"board":"<面板 id>","role":"api","description":"一两句话：做什么、谁在用"}`。port、process、service、url、task 只给一个。',
  '- 附属部分（隧道、网关、数据库、计划任务）也用 add_monitor，加 `"parent":"<主服务 id>"`。',
  '- 我说了怎么访问它（内网 IP、域名、带端口的网址）就同时写 `"entry":"http://10.0.0.5:8080"`；端口服务写同事能打开的地址，不写 127.0.0.1。',
]

const addRecipeEn = [
  '- Discover targets: `explore {"kind":"ports"}` (or `services`, `processes`, `tasks`, optionally with `"q":"keyword"`), and pass `suggest` fields directly. Do not ask me to look up ports or process names.',
  '- After confirming the name and board with me, create monitor + alert rule + notification in one call: `add_monitor {"name":"Orders API","port":8080,"board":"<board id>","role":"api","description":"Short summary of what it does"}`. Provide only one of port, process, service, url, or task.',
  '- Attach supporting parts (tunnels, gateways, databases, scheduled tasks) with `add_monitor` and `"parent":"<parent id>"`.',
  '- If I share how to reach it (LAN IP, domain, URL), include `"entry":"http://10.0.0.5:8080"` using an address teammates can open instead of 127.0.0.1.',
]

const rulesZh = [
  '## 规则',
  '- 出错时先读 error.message，它会说缺什么、怎么改；ID 用 list_boards、list_monitors 查，不要猜。',
  '- 不要写密钥、令牌和密码；不确定的地方先问我，不要试探着改配置。',
]

const rulesEn = [
  '## Rules',
  '- On error, read `error.message` first; look up IDs with `list_boards` / `list_monitors` instead of guessing.',
  '- Never write secrets, tokens, or passwords; ask me when unsure instead of modifying config by trial and error.',
]

export function boardPrompt(board: { id: string; name: string }, setup = '', lang: Lang = 'zh') {
  if (lang === 'en') {
    return lines(
      'Please use lgmon to add services to board "' + board.name + '" (board: ' + board.id + ').',
      '',
      setup + '## 1. Check current state',
      'Call `start_here`, then `get_board {"id":"' + board.id + '"}` to see what is already on this board.',
      '',
      '## 2. Ask me',
      'Ask which services I want to monitor (websites, ports, Windows services, processes, or scheduled tasks) and whether they have supporting parts (tunnels, gateways, databases).',
      '',
      '## 3. How',
      ...addRecipeEn,
      '',
      ...rulesEn,
      '',
      '## When done',
      'Send me the board URL.',
    )
  }
  return lines(
    '请用 lgmon 帮我往面板「' + board.name + '」（board: ' + board.id + '）里加服务。',
    '',
    setup + '## 1. 先看现状',
    '调用 `start_here`，再用 `get_board {"id":"' + board.id + '"}` 看面板里已经有什么。',
    '',
    '## 2. 问我',
    '问我想监控哪些服务（网站、端口、Windows 服务、进程或计划任务），以及它们有没有附属的部分（例如隧道、网关、数据库）。',
    '',
    '## 3. 做法',
    ...addRecipeZh,
    '',
    ...rulesZh,
    '',
    '## 完成时',
    '把面板地址发给我。',
  )
}

// A concrete task (from the examples on the Connect page) replaces the question about what to monitor.
export function firstPrompt(name = '我的服务', description = '', setup = '', task = '', lang: Lang = 'zh') {
  if (lang === 'en') {
    const boardName = name.trim() || 'My Services'
    const board =
      'If no board exists yet, create one with `create_board` using name ' +
      JSON.stringify(boardName) +
      (description.trim() ? ' and description ' + JSON.stringify(description.trim()) : '') +
      '; then use its id as `board`.'
    return lines(
      task
        ? 'Please use lgmon to help me: ' + task
        : 'Please use lgmon to monitor services on this computer.',
      '',
      setup + '## 1. Check current state',
      'Call `start_here`. ' + board,
      '',
      !task && '## 2. Ask me',
      !task &&
        'Ask which services I want to monitor (websites, ports, Windows services, processes, or scheduled tasks) and whether they have supporting parts (tunnels, gateways, databases).',
      !task && '',
      '## ' + (task ? '2' : '3') + '. How',
      ...addRecipeEn,
      '',
      ...rulesEn,
      '',
      '## When done',
      task
        ? 'Share the result and board URL.'
        : 'Send me the board URL, and ask whether I want alerts sent to Feishu.',
    )
  }
  const board =
    '还没有面板时用 `create_board` 建一个，name 使用 ' +
    JSON.stringify(name.trim() || '我的服务') +
    (description.trim() ? '，description 使用 ' + JSON.stringify(description.trim()) : '') +
    '，不用让我自己建；之后用它的 id 作为 board。'
  return lines(
    task ? '请用 lgmon 帮我：' + task : '请用 lgmon 帮我监控这台电脑上的服务。',
    '',
    setup + '## 1. 先看现状',
    '调用 `start_here`。' + board,
    '',
    !task && '## 2. 问我',
    !task &&
      '问我想监控哪些服务（网站、端口、Windows 服务、进程或计划任务），以及它们有没有附属的部分（例如隧道、网关、数据库）。',
    !task && '',
    '## ' + (task ? '2' : '3') + '. 做法',
    ...addRecipeZh,
    '',
    ...rulesZh,
    '',
    '## 完成时',
    task ? '告诉我结果和面板地址。' : '把面板地址发给我，再问我要不要把告警发到手机（飞书）。',
  )
}

// The message in lgmon's small window: once services exist, the agent asks what this visit is for.
export function startPrompt(setup = '', hasServices = false, lang: Lang = 'zh') {
  if (!hasServices) return firstPrompt(lang === 'en' ? 'My Services' : '我的服务', '', setup, '', lang)
  if (lang === 'en') {
    return lines(
      'Please use lgmon to help me look after the services on this computer.',
      '',
      setup + '## 1. Check current state',
      'Call `start_here` and tell me if anything is currently down (which service and since when).',
      '',
      '## 2. Ask what I want to do',
      'Add/edit services, tune alerts, send alerts to Feishu, map dependencies, write/update service docs, or investigate an outage.',
      '',
      '## 3. How',
      ...addRecipeEn,
      '- Investigate issues: `get_monitor {"id":"..."}` for 24h/30d history and events, `list_alerts {"state":"all"}` for alert history.',
      '',
      ...rulesEn,
      '',
      '## When done',
      'Tell me what changed and share the board URL.',
    )
  }
  return lines(
    '请用 lgmon 帮我照看这台电脑上的服务。',
    '',
    setup + '## 1. 先看现状',
    '调用 `start_here`，告诉我现在有没有异常（有就先说是哪个服务、从什么时候开始）。',
    '',
    '## 2. 问我这次要做什么',
    '加服务、改告警、把告警发到飞书、梳理依赖关系、写服务文档，或者查某个服务出过什么问题。',
    '',
    '## 3. 做法',
    ...addRecipeZh,
    '- 查问题：`get_monitor {"id":"…"}` 看 24 小时、30 天和事件，`list_alerts {"state":"all"}` 看告警记录。',
    '',
    ...rulesZh,
    '',
    '## 完成时',
    '告诉我结果和面板地址。',
  )
}

export function feishuPrompt(setup = '', apps: string[] = [], lang: Lang = 'zh') {
  if (lang === 'en') {
    return lines(
      'Please use lgmon MCP tools to set up Feishu alert notifications.',
      '',
      setup + '## 1. Ask me two things first',
      'Which Feishu group should receive alerts, and which services or alert rules should send there (default to all if unsure). Do not ask me to look up chat IDs.',
      '',
      '## 2. App credentials',
      apps.length
        ? 'Locally saved app IDs: ' +
            JSON.stringify(apps) +
            '; reuse them when possible, and ask if multiple apps exist.'
        : 'No app credentials saved yet. Guide me to create a Feishu app, enable the bot, and save credentials in the local form; never ask for the App Secret in chat.',
      '',
      '## 3. How',
      '- Call `start_here`, then read `get_config(notifiers)` and `get_config(rules)` to preserve existing channels and rules.',
      '- Use `feishu_check` to verify permissions and bot status; use `feishu_list_chats` to find the group.',
      '- Send a test card with `feishu_test_chat` once authorized; only after I confirm receipt, call `feishu_save_chat` and attach to confirmed rules.',
      '',
      '## When done',
      'Re-read the config to verify the channel and rules, summarize what was done, and share the notifications page URL.',
    )
  }
  return lines(
    '请用 lgmon 的完整 MCP 工具帮我接入飞书告警通知。',
    '',
    setup + '## 1. 先问我两件事',
    '告警要发到哪个飞书群，以及哪些服务或告警规则要发过去（我没想好时默认全部）。不要让我去查群 ID。',
    '',
    '## 2. 应用凭证',
    apps.length
      ? '已在本机保存凭证的应用 ID：' + JSON.stringify(apps) + '；优先复用，多个应用无法唯一确定时先问我。'
      : '还没有已保存凭证的应用。先指导我创建自建应用、启用机器人，并在本机安全表单保存应用凭证；等我完成，不要向聊天索取 App Secret，也不要读取密钥库。',
    '',
    '## 3. 做法',
    '- 先调用 `start_here`，再读 `get_config(notifiers)` 和 `get_config(rules)`，保留已有渠道、筛选条件和规则。',
    '- 用 `feishu_check` 检查机器人、权限和接入状态；需要我开通权限、发布版本、审批或把机器人拉进群时，只给出必要操作和官方链接，由我完成，你继续检查结果。',
    '- 用 `feishu_list_chats` 找到我指定的群；群未指定、有重名或机器人不在群内时先问我，不要猜、建群或改群成员。',
    '- 我授权你向我指定的群调用 `feishu_test_chat` 发接入测试卡片（同一目的地十秒一次，不要绕过）。发送成功不等于收到；等我回复已收到，才 `feishu_save_chat` 保存渠道，并在 rules 里追加到我确认过的规则，不删除或覆盖原通知。',
    '',
    '## 完成时',
    '重新读取配置核对群渠道和规则关联，告诉我已完成的、需要我配合的，以及通知页面地址。应用密钥只在本机输入，不经聊天、截图、日志或仓库。',
  )
}

export function childPrompt(monitor: { id: string; name: string }, setup = '', lang: Lang = 'zh') {
  if (lang === 'en') {
    return lines(
      'Please use lgmon to add sub-services under "' + monitor.name + '" (id: ' + monitor.id + ').',
      '',
      setup + '## 1. Check current state',
      'Call `get_monitor {"id":"' + monitor.id + '"}` to see its current setup.',
      '',
      '## 2. Ask me',
      'Ask which supporting parts belong under it (tunnels, gateways, databases, scheduled tasks).',
      '',
      '## 3. How',
      ...addRecipeEn.map((line) => line.replace('<parent id>', monitor.id)),
      '- Pass `"parent":"' + monitor.id + '"` for every sub-service.',
      '',
      ...rulesEn,
      '',
      '## When done',
      'List the sub-services that were added.',
    )
  }
  return lines(
    '请用 lgmon 帮我给「' + monitor.name + '」（id: ' + monitor.id + '）添加子服务。',
    '',
    setup + '## 1. 先看现状',
    '`get_monitor {"id":"' + monitor.id + '"}` 看它现在的情况。',
    '',
    '## 2. 问我',
    '它有哪些附属的部分要一起看（例如给它做外网穿透的隧道、网关、数据库、计划任务）。',
    '',
    '## 3. 做法',
    ...addRecipeZh.map((line) => line.replace('<主服务 id>', monitor.id)),
    '- 每个子服务都传 `"parent":"' + monitor.id + '"`。',
    '',
    ...rulesZh,
    '',
    '## 完成时',
    '告诉我加了哪些子服务。',
  )
}

export function changePrompt(monitor: { id: string; name: string }, setup = '', lang: Lang = 'zh') {
  if (lang === 'en') {
    return lines(
      'Please use lgmon to edit monitor "' + monitor.name + '" (id: ' + monitor.id + ').',
      '',
      setup + '## 1. Check current state',
      'Call `get_monitor {"id":"' +
        monitor.id +
        '"}`, summarize in one sentence what it checks, its alert delay, and notifications, then ask what I want to change.',
      '',
      '## 2. How',
      '- Name, description, role, entry URL, parent, dependencies: `edit_monitor {"id":"' +
        monitor.id +
        '", ...changed fields only}`; `depends` replaces the whole list, so merge with existing ones first.',
      '- Check target, alert delay (`for`), severity, notifications: call `add_monitor` with the same `"id":"' +
        monitor.id +
        '"`.',
      '',
      ...rulesEn,
      '',
      '## When done',
      'Summarize what was updated.',
    )
  }
  return lines(
    '请用 lgmon 帮我修改监控「' + monitor.name + '」（id: ' + monitor.id + '）。',
    '',
    setup + '## 1. 先看现状',
    '`get_monitor {"id":"' +
      monitor.id +
      '"}`，用一两句话告诉我它在检查什么、异常多久才告警、通知到哪里，再问我想怎么改。',
    '',
    '## 2. 做法',
    '- 名字、说明、角色、访问入口、挂在哪个服务下、依赖：`edit_monitor {"id":"' +
      monitor.id +
      '", …只传要改的字段}`；depends 是整体替换，先读出已有的再合并。',
    '- 检查方式、告警等待时间（for）、严重程度、通知：`add_monitor` 传同一个 `"id":"' +
      monitor.id +
      '"` 更新。',
    '',
    ...rulesZh,
    '',
    '## 完成时',
    '告诉我改了什么。',
  )
}

/*
 * The relation map is only worth something when its links are right, so this one is a full guide: gather
 * evidence first, pick the relation that says what really happens, show the table, then write it.
 */
export function dependencyPrompt(board: { id: string; name: string }, setup = '', lang: Lang = 'zh') {
  if (lang === 'en') {
    return lines(
      'Please use lgmon to map accurate service dependencies for board "' +
        board.name +
        '" (board: ' +
        board.id +
        ').',
      '',
      'Goal: a reader should immediately see where traffic enters, which services it passes through, where data lives, what runs on a schedule, and blast radius on failure.',
      '',
      setup + '## 1. Gather evidence (read-only)',
      '- Call `get_board {"id":"' + board.id + '"}` and `list_monitors` (dependencies may cross boards).',
      '- Verify real connections from process configs/DSNs/upstreams, `explore {"kind":"ports"}` / `Get-NetTCPConnection -State Established`, `explore {"kind":"tasks"}`, and `get_monitor_doc`.',
      '- Never guess unverified links; list them as "to confirm" and ask me.',
      '',
      '## 2. Choose accurate relations (on caller A: `depends` lists B)',
      '- `calls`, `data` / `reads` / `writes`, `via`, `forwards`, `runs`, `sends` / `consumes`, `auth`, `syncs`, `replicates` / `backup`, `fallback`, `monitors`, `uses`.',
      '- Add `"optional": true` if A degrades gracefully when B fails, and a concrete `note` (DB name, port, queue, domain).',
      '- If B runs on another of my computers (same lgmon cloud account), do not monitor it again here: find it with `list_external_services` and write `{"target":"<its id>","device":"<its device id>"}` in depends. It shows in the board’s External dependencies block and never alerts on this computer.',
      '',
      '## 3. Show me first',
      'Present a table of Caller -> Target, relation, optional, note, evidence source, and proposed role/description. Wait for my confirmation.',
      '',
      '## 4. Write updates',
      'Call `edit_monitor` per service (merging with existing `depends`), and optionally add short runbooks via `put_monitor_doc` with `[[serviceId]]` links and a ```deps block.',
      '',
      ...rulesEn,
      '- Only edit descriptions, roles, dependencies, and docs; do not change probe/alert settings.',
      '',
      '## When done',
      'List updated services and share the board Map view URL.',
    )
  }
  return lines(
    '请用 lgmon 帮我把面板「' + board.name + '」（board: ' + board.id + '）的依赖关系图画完整、画准确。',
    '',
    '目标：第一次看的人从图上就能看懂请求从哪里进来、经过谁、数据落在哪、谁在后台按时跑，以及谁坏了会连累谁。',
    '',
    setup + '## 1. 收集事实（只读，不改任何东西）',
    '- `get_board {"id":"' +
      board.id +
      '"}` 看这个面板的服务和子服务；`list_monitors` 看全部服务（依赖可以跨面板）。',
    '- 对每个服务查清它实际连了谁，证据按这个顺序找：',
    '  1. 进程命令行和配置文件里的连接串、上游地址（数据库 DSN、API base URL、nginx/caddy 的 upstream、cloudflared 的 ingress）。',
    '  2. 网络连接：`explore {"kind":"ports"}` 看谁在监听；需要时只读地运行 `Get-NetTCPConnection -State Established` 对照进程，看谁连向谁的端口。',
    '  3. 计划任务和守护程序：`explore {"kind":"tasks"}` 看谁按时启动谁。',
    '  4. 已有文档：`get_monitor_doc {"id":"…"}`。',
    '- 找不到证据的关系不要猜，列为“待确认”问我。',
    '',
    '## 2. 选对关系（写在使用的一方：A 的 depends 里写 B，表示 A 用到 B）',
    '| relation | 什么时候用 | 例子 |',
    '|---|---|---|',
    '| calls | A 调用 B 的接口 | 前端 → 后端 API |',
    '| data / reads / writes | A 读写 / 只读 / 只写 B 的数据 | API → 订单库（data）；报表 → 只读库（reads） |',
    '| via | A 经由 B 才能被访问或出网 | 网站 → cloudflared 隧道；服务 → 网关 |',
    '| forwards | A 把请求转发给 B（A 是网关或反向代理） | nginx → 应用 |',
    '| runs | A 由 B 启动或按时触发 | 采集脚本 → 计划任务 |',
    '| sends / consumes | A 往 B 发消息 / A 从 B 取消息 | 下单 → 队列（sends）；worker → 队列（consumes） |',
    '| auth | A 的登录走 B | 应用 → 统一登录 |',
    '| syncs | A 和 B 双向同步 | 两地的文件同步 |',
    '| replicates / backup | A 复制到 / 备份到 B | 主库 → 从库；数据库 → NAS |',
    '| fallback | A 出故障时切换到 B | 主接口 → 备用接口 |',
    '| monitors | A 检查 B | 健康检查脚本 → 网站 |',
    '| uses | 以上都不贴切 | |',
    '- B 坏了 A 还能部分工作的，加 `"optional": true`。',
    '- `note` 写一句具体内容：库名或表、端口、队列名、域名，例如 `"订单库 orders，5432"`。',
    '- 只为一个服务存在的附属部分（它专用的隧道、数据库）用 `parent` 挂成子服务；被多个服务共用的不要挂，用 depends 连。',
    '- 面板外的第三方依赖（云接口、短信服务）可以 `add_monitor` 成 `"role":"external"` 的服务再连上。',
    '- 用到我另一台电脑（同一个 lgmon 云账号）上的服务时，不要在这台重复加监控：`list_external_services` 找到它，在 depends 里写 `{"target":"那台的服务ID","device":"那台的设备ID"}`。它会显示在面板的「外部依赖」区，不参与本机告警。',
    '',
    '## 3. 先给我看',
    '列一张表：使用方 → 被使用方、relation、optional、note、证据来源，以及每个服务打算写的 role 和 description。等我确认或修改。',
    '',
    '## 4. 写入（每个服务一次调用）',
    '`edit_monitor {"id":"site","role":"web","description":"客服每天用的前端","depends":[{"target":"api","relation":"calls","note":"/api/*"},{"target":"tunnel","relation":"via"}]}`',
    '- depends 是整体替换：先 `get_monitor` 读出已有的依赖，合并后再写，不要丢掉原来的。',
    '- role 只能是 web、api、app、database、cache、queue、storage、tunnel、gateway、worker、scheduler、ai、mcp、external。',
    '- 值得记下部署、启动和排障的服务，用 `put_monitor_doc` 写一篇短文档，用 [[服务ID]] 引用其他服务，放一个 ```deps 代码块画它的上下游。',
    '',
    '## 5. 核对',
    '再 `get_board` 一遍：每个服务都有 role 和 description，没有孤立的服务（真的独立就说明为什么）。我可以在关系图右上角点“Markdown”把整张图复制给你核对。',
    '',
    ...rulesZh,
    '- 只改说明、角色、依赖和文档；不改监控方式、告警和通知。',
    '',
    '## 完成时',
    '告诉我改了哪些服务，以及面板“关系”视图的地址。',
  )
}

/* Lets an agent write one service's document. */
export function docPrompt(monitor: { id: string; name: string }, setup = '', lang: Lang = 'zh') {
  if (lang === 'en') {
    return lines(
      'Please use lgmon to write or update the service document for "' +
        monitor.name +
        '" (id: ' +
        monitor.id +
        ').',
      '',
      setup + '## 1. Check current state',
      'Call `get_monitor {"id":"' +
        monitor.id +
        '"}` and `get_monitor_doc {"id":"' +
        monitor.id +
        '"}` to inspect the service and any existing doc.',
      '',
      '## 2. Investigate',
      'Check on this computer what it does, where it is deployed, how to start/stop it, its dependencies, and first troubleshooting steps; ask me for anything you cannot verify.',
      '',
      '## 3. Write',
      'Call `put_monitor_doc {"id":"' + monitor.id + '","markdown":"..."}` using Markdown:',
      '- Opening summary, then `## Overview`, `## Dependencies`, `## Start & Stop`, `## Troubleshooting`.',
      '- Reference other services with `[[serviceId]]` and include a ```deps code block for upstream/downstream.',
      '- Use `> [!NOTE]` / `> [!WARNING]` for callouts and fenced code blocks for commands.',
      '- If missing a short description, update it with `edit_monitor`.',
      '',
      ...rulesEn,
      '',
      '## When done',
      'Let me know it is saved and share the service page URL.',
    )
  }
  return lines(
    '请用 lgmon 帮我给服务「' + monitor.name + '」（id: ' + monitor.id + '）写一篇文档。',
    '',
    setup + '## 1. 先看现状',
    '`get_monitor {"id":"' +
      monitor.id +
      '"}` 和 `get_monitor_doc {"id":"' +
      monitor.id +
      '"}` 看它的情况和已有文档。',
    '',
    '## 2. 查清楚',
    '在这台电脑上查清它是什么、部署在哪、怎么启动和停止、依赖哪些服务、出问题先查什么；查不到的问我。',
    '',
    '## 3. 写',
    '`put_monitor_doc {"id":"' + monitor.id + '","markdown":"…"}`，Markdown 结构：',
    '- 开头一段话说清它是做什么的；然后 `## 概览`、`## 依赖`、`## 启动与停止`、`## 排障`。',
    '- 提到其他服务写 [[服务ID]]；在“依赖”里放一个 ```deps 代码块画上下游。',
    '- 需要提醒的写成 `> [!NOTE]` 或 `> **注意** …`；命令放进代码块。',
    '- 还没有说明的话，用 `edit_monitor` 补一句 description。',
    '',
    ...rulesZh,
    '',
    '## 完成时',
    '告诉我写好了，以及服务页面地址。',
  )
}

/* Writing or updating service documentation across a board (used in "Other uses" sheet). */
export function docsPrompt(setup = '', board?: { id: string; name: string }, lang: Lang = 'zh') {
  if (lang === 'en') {
    return lines(
      board
        ? 'Please use lgmon to write or update service documentation for board "' +
            board.name +
            '" (board: ' +
            board.id +
            ').'
        : 'Please use lgmon to write or update service documentation.',
      '',
      setup + '## 1. Check current state',
      board
        ? 'Call `get_board {"id":"' +
            board.id +
            '"}` to list services and see which ones already have docs (`hasDoc`).'
        : 'Call `start_here` and `list_monitors` to list services and see which ones already have docs (`hasDoc`).',
      'Ask me which service(s) to write or update (or whether to draft docs for all undocumented services).',
      '',
      '## 2. Investigate & edit',
      '- Call `get_monitor {"id":"..."}` and `get_monitor_doc {"id":"..."}` first so existing notes are preserved when updating.',
      '- Inspect deployment path, startup/stop commands, ports, and dependencies on this computer (read-only); ask me if unsure.',
      '- Save with `put_monitor_doc {"id":"...","markdown":"..."}` using `## Overview`, `## Dependencies` (with `[[serviceId]]` and ```deps block), `## Start & Stop`, and `## Troubleshooting`.',
      '- Use `> [!NOTE]` / `> [!TIP]` / `> [!WARNING]` for callouts.',
      '',
      ...rulesEn,
      '',
      '## When done',
      'List the services whose docs were written or updated, along with their page URLs.',
    )
  }
  return lines(
    board
      ? '请用 lgmon 帮我给面板「' + board.name + '」（board: ' + board.id + '）里的服务写文档或修改已有文档。'
      : '请用 lgmon 帮我给服务写文档或修改已有文档。',
    '',
    setup + '## 1. 先看现状',
    board
      ? '调用 `get_board {"id":"' + board.id + '"}` 查看面板里的服务以及哪些已有文档（`hasDoc`）。'
      : '调用 `start_here` 和 `list_monitors` 查看所有服务以及哪些已有文档（`hasDoc`）。',
    '问我要给哪个（或哪几个）服务写文档、还是修改哪篇已有文档。',
    '',
    '## 2. 查清楚并写入',
    '- 先调用 `get_monitor {"id":"…"}` 和 `get_monitor_doc {"id":"…"}` 读出已有文档，修改时保留原有的有效内容。',
    '- 在这台电脑上只读地核实部署目录、启动/停止命令、端口和依赖关系；查不到的先问我。',
    '- 用 `put_monitor_doc {"id":"…","markdown":"…"}` 保存：建议包含 `## 概览`、`## 依赖`（用 `[[服务ID]]` 引用服务并放 ```deps 代码块）、`## 启动与停止`、`## 排障`；提醒项用 `> [!NOTE]` / `> [!WARNING]`。',
    '',
    ...rulesZh,
    '',
    '## 完成时',
    '列出写好或修改过的服务文档，以及对应的服务页面地址。',
  )
}

/* Editing existing services or attaching sub-services (used in "Other uses" sheet). */
export function editServicesPrompt(setup = '', board?: { id: string; name: string }, lang: Lang = 'zh') {
  if (lang === 'en') {
    return lines(
      board
        ? 'Please use lgmon to edit existing services or add sub-services on board "' +
            board.name +
            '" (board: ' +
            board.id +
            ').'
        : 'Please use lgmon to edit existing services or add sub-services.',
      '',
      setup + '## 1. Check current state',
      board
        ? 'Call `get_board {"id":"' +
            board.id +
            '"}` and ask me which service to modify or which sub-service to attach.'
        : 'Call `start_here` and `list_monitors`, then ask me which service to modify or which sub-service to attach.',
      '',
      '## 2. How',
      '- Edit name, description, role, entry URL, parent, or depends: `edit_monitor {"id":"...", ...changed fields}` (merge existing `depends` first).',
      '- Attach a sub-service (tunnel, gateway, database, scheduled task): `add_monitor` with `"parent":"<parent id>"`.',
      '- Delete a monitor: confirm with me first before calling `remove_monitor`.',
      '',
      ...rulesEn,
      '',
      '## When done',
      'Summarize what was changed.',
    )
  }
  return lines(
    board
      ? '请用 lgmon 帮我修改面板「' + board.name + '」（board: ' + board.id + '）里已有的服务或添加子服务。'
      : '请用 lgmon 帮我修改已有的服务或添加子服务。',
    '',
    setup + '## 1. 先看现状',
    board
      ? '调用 `get_board {"id":"' +
          board.id +
          '"}`，问我想改哪个服务（改名、简介、角色、访问地址、挂载子服务）或删哪个服务。'
      : '调用 `start_here` 和 `list_monitors`，问我想改哪个服务或给哪个主服务挂子服务。',
    '',
    '## 2. 做法',
    '- 改名称、说明、角色、访问入口、parent、依赖：`edit_monitor {"id":"…", …只传要改的字段}`（改 depends 前先读已有依赖合并）。',
    '- 挂载子服务（隧道、网关、数据库、计划任务）：`add_monitor` 并传 `"parent":"<主服务 id>"`。',
    '- 删除服务前必须先向我确认，再调用 `remove_monitor`。',
    '',
    ...rulesZh,
    '',
    '## 完成时',
    '告诉我修改了哪些服务。',
  )
}

/* LAN sharing & viewing colleagues' boards (used in "Other uses" sheet). */
export function lanPrompt(setup = '', lang: Lang = 'zh') {
  if (lang === 'en') {
    return lines(
      "Please use lgmon to help me set up LAN sharing or view a teammate's shared board.",
      '',
      setup + '## 1. Check current state',
      'Call `lan_status`, `lan_requests`, and `lan_grants`, summarize whether LAN discovery / read-only LAN web viewing is on, and ask what I want to do.',
      '',
      '## 2. How',
      '- Enable/disable LAN discovery or read-only LAN browser access: `lan_configure {"visible":true}` or `{"web":true}`.',
      '- Discover teammates: `lan_discover`; request access: `lan_request` (always show me the 6-digit pairing code to verify with them).',
      '- Approve or revoke access: confirm explicitly with me first before `lan_approve` / `lan_revoke`.',
      '',
      ...rulesEn,
    )
  }
  return lines(
    '请用 lgmon 帮我配置内网共享，或申请查看同事的监控面板。',
    '',
    setup + '## 1. 先看现状',
    '调用 `lan_status`、`lan_requests` 和 `lan_grants`，告诉我当前内网可见与内网只读网页是否开启、有哪些申请或授权，再问我这次想做什么。',
    '',
    '## 2. 做法',
    '- 开启/关闭内网可见或内网浏览器只读访问：`lan_configure {"visible":true}` 或 `{"web":true}`。',
    '- 发现附近同事：`lan_discover`；申请查看面板：`lan_request`（把 6 位配对码发给我核对）。',
    '- 同意申请或撤销授权前必须经我明确同意，再调用 `lan_approve` / `lan_revoke`。',
    '',
    ...rulesZh,
  )
}

/* Looking into a service that is failing, or failed earlier. */
export function diagnosePrompt(setup = '', lang: Lang = 'zh') {
  if (lang === 'en') {
    return lines(
      'Please use lgmon to investigate why a service is failing.',
      '',
      setup + '## 1. Check current state',
      'Call `start_here` and `list_alerts {"state":"all"}`; ask which service to investigate (or pick the currently failing one if unspecified).',
      '',
      '## 2. Investigate',
      '- Call `get_monitor {"id":"..."}` for 24h/30d history, events, and sub-services to find when the issue began.',
      '- For process/service exits, call `get_alert_events {"id":"<alert id>"}` to inspect surrounding Windows events.',
      '- Inspect logs, ports, processes, and upstream dependencies on this computer (read-only) to identify root cause.',
      '',
      '## 3. Report back',
      'Explain what happened, since when, concrete evidence, and recommended fix. Ask me before restarting services or changing config.',
      '',
      ...rulesEn,
    )
  }
  return lines(
    '请用 lgmon 帮我查清楚服务为什么出问题。',
    '',
    setup + '## 1. 先看现状',
    '`start_here`；`list_alerts {"state":"all"}` 看最近的告警；问我是哪个服务（我没说就挑正在异常的那个）。',
    '',
    '## 2. 查',
    '- `get_monitor {"id":"…"}`：24 小时和 30 天的状态、事件、子服务，找出第一次异常的时间。',
    '- 进程或服务退出的告警：`get_alert_events {"id":"<告警 id>"}` 看前后 60 秒的系统事件。',
    '- 在这台电脑上只读地看日志、端口和进程，说明根因；依赖关系图里它用到的服务也一起看。',
    '',
    '## 3. 告诉我',
    '发生了什么、从什么时候开始、根因的证据、建议怎么修。需要重启或改配置时先问我，不要自己动手。',
    '',
    ...rulesZh,
  )
}

/* Changing when and how alerts fire. */
export function alertPrompt(setup = '', lang: Lang = 'zh') {
  if (lang === 'en') {
    return lines(
      'Please use lgmon to tune my alert rules.',
      '',
      setup + '## 1. Check current state',
      "Call `start_here` and `list_monitors`, and summarize each service's alert delay and notification channel.",
      '',
      '## 2. Ask me',
      'Which services are too noisy or too slow to alert, where notifications should go, and any quiet/maintenance windows.',
      '',
      '## 3. How',
      '- Update delay (`for`), severity, or channels: call `add_monitor` with the same id.',
      '- Silence during maintenance: `silence`.',
      '- Test before saving: `test_rule`.',
      '',
      ...rulesEn,
      '',
      '## When done',
      'List which alert rules were changed.',
    )
  }
  return lines(
    '请用 lgmon 帮我调整告警。',
    '',
    setup + '## 1. 先看现状',
    '`start_here` 和 `list_monitors`，用一两句话告诉我每个服务现在异常多久告警、通知到哪里。',
    '',
    '## 2. 问我',
    '哪些服务太吵或太迟钝、要发到哪里、什么时间不想被打扰。',
    '',
    '## 3. 做法',
    '- 等待时间、严重程度、通知渠道：`add_monitor` 传同一个 id 和要改的 `for`、`severity`、`notify`。',
    '- 维护期间不想收通知：`silence`。',
    '- 改之前先 `test_rule` 试一下会不会触发。',
    '',
    ...rulesZh,
    '',
    '## 完成时',
    '列出改了哪些规则。',
  )
}

/*
 * A skill the person can hand to an agent once: the same recipes as above in one file, so later sessions start
 * knowing them instead of reading the long guide or trying tools out.
 */
export function skillText(setup = '', lang: Lang = 'zh') {
  if (lang === 'en') {
    return lines(
      'Please save the lgmon handbook below as your skill so future lgmon tasks follow it directly (for Claude Code: `%USERPROFILE%\\.claude\\skills\\lgmon\\SKILL.md`; for Codex or other agents: your global rules/memory). Tell me where you saved it.',
      '',
      '````markdown',
      '---',
      'name: lgmon',
      'description: Manage lgmon service monitoring on this Windows computer via MCP: add/edit services, check status & alerts, map dependencies, write/update service docs, tune alerts & Feishu notifications, and LAN sharing.',
      '---',
      '',
      '# lgmon Handbook',
      '',
      'lgmon has three levels: Board -> Service -> Sub-service. Always start with `start_here` and use MCP tools directly.',
      '',
      setup.trim()
        ? setup
            .trim()
            .replace(
              '## 0. Connect to lgmon (skip if you already have lgmon MCP tools)',
              '## Connect (skip if you already have lgmon MCP tools)',
            )
        : "## Connect\nCopy the connect command from lgmon's Agent settings and run it on this computer.",
      '',
      '## Add services',
      ...addRecipeEn,
      '',
      '## Edit & docs',
      '- Edit metadata/dependencies: `edit_monitor {"id":"...", ...changed fields}`.',
      '- Docs: `get_monitor_doc` to read, `put_monitor_doc {"id":"...","markdown":"..."}` to save (use `[[serviceId]]` and ```deps blocks).',
      '- Status & troubleshooting: `list_monitors`, `get_board`, `get_monitor`, `list_alerts {"state":"all"}`, `get_alert_events`.',
      '- LAN sharing: `lan_status`, `lan_configure`, `lan_discover`, `lan_request`, `lan_grants`.',
      '````',
    )
  }
  return lines(
    '请把下面这份 lgmon 操作手册保存为你的 skill，以后处理 lgmon 的事都按它来：Claude Code 保存为 `%USERPROFILE%\\.claude\\skills\\lgmon\\SKILL.md`；Codex 等其他 Agent 放进它的全局规则或记忆文件。保存后告诉我位置。',
    '',
    '````markdown',
    '---',
    'name: lgmon',
    'description: 操作这台 Windows 电脑上的 lgmon 服务监控（MCP 工具）：加服务、看状态和告警、写依赖关系和服务文档、调告警和飞书通知、内网共享。用户提到 lgmon、监控、服务挂了、告警、依赖关系图或服务文档时使用。',
    '---',
    '',
    '# lgmon 操作手册',
    '',
    'lgmon 只有三层：面板 → 服务 → 子服务（主服务和子服务各按自身探测判定状态）。所有操作都用 lgmon 的 MCP 工具完成；每次开始先调用一次 `start_here`，不用先读长文档。',
    '',
    setup.trim()
      ? setup
          .trim()
          .replace(
            '## 0. 接入 lgmon（已经能调用 lgmon 的 MCP 工具就跳过）',
            '## 接入（已经能调用 lgmon 的 MCP 工具就跳过）',
          )
      : '## 接入\n在 lgmon 的“接入 Agent”页面复制接入命令，在这台电脑上运行。',
    '',
    '## 加服务',
    ...addRecipeZh,
    '',
    '## 改服务',
    '- 名字、说明、角色、访问入口、parent、依赖：`edit_monitor {"id":"…", …只传要改的字段}`。',
    '- 检查方式、告警等待（for）、严重程度、通知：`add_monitor` 传同一个 id。',
    '- 删除：`remove_monitor`（有子服务时要 `"children":true`，先问用户）。',
    '',
    '## 依赖关系（关系图）',
    '- 写在使用的一方：`edit_monitor {"id":"A","depends":[{"target":"B","relation":"calls","note":"…","optional":false}]}`；depends 整体替换，先读后合并。',
    '- relation：calls 调用接口、data/reads/writes 读写数据、via 经由隧道或网关、forwards 网关转发给、runs 由它启动、sends/consumes 消息、auth 登录、syncs 同步、replicates/backup 复制备份、fallback 故障切换、monitors 检查、uses 其他。',
    '- 先查证据（配置里的连接串、Get-NetTCPConnection、计划任务），列表给用户确认后再写。',
    '',
    '## 文档',
    '`get_monitor_doc` 读、`put_monitor_doc {"id":"…","markdown":"…"}` 整篇替换；## 分节，[[服务ID]] 引用服务，```deps 代码块画上下游，`> **注意**` 写提醒。',
    '',
    '## 看状态和查问题',
    '`list_monitors` / `get_board` 看全部；`get_monitor` 看单个的 24 小时、30 天和事件；`list_alerts {"state":"all"}` 看告警历史；`get_alert_events` 看进程退出前后的系统事件。',
    '',
    '## 内网',
    '`lan_discover` 找同事的面板，`lan_request` 申请（把配对码告诉用户核对），`lan_requests` 看申请，`lan_grants` 看谁能看我的面板，`lan_remote_board`、`lan_remote_monitor`、`lan_remote_doc` 只读查看别人的；`profile` / `profile_update` 是本机的昵称和头像。同意申请、撤销授权都要用户明确同意。',
    '',
    '## 少走弯路',
    '- 出错先读 error.message；ID 用 list_boards、list_monitors 查，不要猜。',
    '- 一次 add_monitor 就建好规则和通知，不需要再写 targets/rules。',
    '- 不让用户自己查端口、进程名或群 ID；不确定先问。',
    '- 令牌、密钥不要出现在聊天、日志和命令行参数里。',
    '````',
  )
}

/* The purposes the prompt sheet offers, each with what it is for. */
export type PromptChoice = { id: string; title: string; lead: string; text: string }

export function promptChoices(
  setup: string,
  hasServices: boolean,
  board?: { id: string; name: string },
  lang: Lang = 'zh',
) {
  const en = lang === 'en'
  const choices: PromptChoice[] = [
    {
      id: 'start',
      title: hasServices
        ? en
          ? 'Look after services'
          : '照看服务'
        : en
          ? 'Add first service'
          : '加第一个服务',
      lead: hasServices
        ? en
          ? 'Check if anything is down right now, then ask what you want to do.'
          : '看看现在有没有异常，再问你这次要做什么。'
        : en
          ? 'Connect to lgmon and ask which services you want to monitor.'
          : '接入 lgmon，然后问你想监控什么。',
      text: startPrompt(setup, hasServices, lang),
    },
  ]
  if (hasServices)
    choices.push(
      {
        id: 'add',
        title: en ? 'Add services' : '加服务',
        lead: en
          ? 'Discover running services on this computer and add them after confirmation.'
          : '在这台电脑上找到服务，跟你确认后加进面板。',
        text: board
          ? boardPrompt(board, setup, lang)
          : firstPrompt(en ? 'My Services' : '我的服务', '', setup, '', lang),
      },
      {
        id: 'edit',
        title: en ? 'Edit services & sub-services' : '改服务 / 加子服务',
        lead: en
          ? 'Rename, update descriptions or entry URLs, or attach tunnels, databases, and tasks.'
          : '修改服务名称、简介、访问地址，或挂载隧道、数据库、计划任务等子服务。',
        text: editServicesPrompt(setup, board, lang),
      },
      {
        id: 'docs',
        title: en ? 'Write / edit service docs' : '写文档 / 改文档',
        lead: en
          ? 'Investigate deployment, startup, and troubleshooting, then write or update service runbooks.'
          : '核实部署、启停与排障步骤，为服务撰写新文档或更新已有文档。',
        text: docsPrompt(setup, board, lang),
      },
    )
  if (hasServices && board)
    choices.push({
      id: 'relations',
      title: en ? 'Map dependencies' : '梳理依赖关系',
      lead: en
        ? 'Trace callers, databases, and tunnels to build an accurate dependency map.'
        : '查清谁调用谁、读写哪个库、经由哪条隧道，把关系图画完整。',
      text: dependencyPrompt(board, setup, lang),
    })
  if (hasServices)
    choices.push(
      {
        id: 'diagnose',
        title: en ? 'Troubleshoot issues' : '查问题',
        lead: en
          ? 'Find when an outage started and gather root-cause evidence.'
          : '找出服务出问题的时间和根因。',
        text: diagnosePrompt(setup, lang),
      },
      {
        id: 'alerts',
        title: en ? 'Tune alerts' : '调整告警',
        lead: en
          ? 'Change alert delays, notification channels, or silence during maintenance.'
          : '改告警的等待时间、通知去向和静音。',
        text: alertPrompt(setup, lang),
      },
    )
  choices.push(
    {
      id: 'feishu',
      title: en ? 'Send alerts to Feishu' : '告警发到飞书',
      lead: en
        ? 'Connect a Feishu bot and route service alerts to your group chat.'
        : '接入飞书机器人，把告警发到指定的群。',
      text: feishuPrompt(setup, [], lang),
    },
    {
      id: 'lan',
      title: en ? 'LAN sharing & team boards' : '内网共享与同事面板',
      lead: en
        ? 'Turn on LAN sharing or read-only web access, discover peers, and request boards.'
        : '开启内网共享或只读网页，发现附近同事并申请查看面板。',
      text: lanPrompt(setup, lang),
    },
    {
      id: 'skill',
      title: en ? 'Install lgmon skill' : '安装 lgmon skill',
      lead: en
        ? 'Save this handbook as your agent skill so future sessions work faster.'
        : '让 Agent 记住这份操作手册，以后不用再试探，改得更快。',
      text: skillText(setup, lang),
    },
  )
  return choices
}
