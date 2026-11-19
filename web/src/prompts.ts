/*
 * Every message lgmon hands to an agent, written the same way: a one-line goal, then short titled sections
 * (connect, first step, how, rules, done when) with the exact tool calls and argument shapes, so an agent does
 * the job in a few calls instead of trying things. They never ask the person to fill a blank: the agent asks.
 */

export function quote(value: string) {
  return "'" + value.replace(/'/g, "''") + "'"
}

type Install = { executable: string; home: string; url: string }

export function agentSetup(install?: Install, tools: 'essential' | 'all' = 'all') {
  if (!install?.executable || !install.home) return ''
  const command =
    '& ' +
    quote(install.executable) +
    ' agent connect --home ' +
    quote(install.home) +
    (install.url ? ' --url ' + quote(install.url) : '') +
    (tools === 'all' ? ' --tools all' : '') +
    ' --elevate --json'
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
const addRecipe = [
  '- 找对象：`explore {"kind":"ports"}`（也可以是 services、processes、tasks，带 `"q":"关键词"` 缩小范围），结果里的 `suggest` 原样用作参数。不要让我自己查端口或进程名。',
  '- 跟我确认名字和面板后，一次调用建好监控、告警和通知：`add_monitor {"name":"订单接口","port":8080,"board":"<面板 id>","role":"api","description":"一两句话：做什么、谁在用"}`。port、process、service、url、task 只给一个。',
  '- 附属部分（隧道、网关、数据库、计划任务）也用 add_monitor，加 `"parent":"<主服务 id>"`。',
  '- 我说了怎么访问它（内网 IP、域名、带端口的网址）就同时写 `"entry":"http://10.0.0.5:8080"`；端口服务写同事能打开的地址，不写 127.0.0.1。',
]

const rules = [
  '## 规则',
  '- 出错时先读 error.message，它会说缺什么、怎么改；ID 用 list_boards、list_monitors 查，不要猜。',
  '- 不要写密钥、令牌和密码；不确定的地方先问我，不要试探着改配置。',
]

export function boardPrompt(board: { id: string; name: string }, setup = '') {
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
    ...addRecipe,
    '',
    ...rules,
    '',
    '## 完成时',
    '把面板地址发给我。',
  )
}

// A concrete task (from the examples on the Connect page) replaces the question about what to monitor.
export function firstPrompt(name = '我的服务', description = '', setup = '', task = '') {
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
    ...addRecipe,
    '',
    ...rules,
    '',
    '## 完成时',
    task ? '告诉我结果和面板地址。' : '把面板地址发给我，再问我要不要把告警发到手机（飞书）。',
  )
}

// The message in lgmon's small window: once services exist, the agent asks what this visit is for.
export function startPrompt(setup = '', hasServices = false) {
  if (!hasServices) return firstPrompt('我的服务', '', setup)
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
    ...addRecipe,
    '- 查问题：`get_monitor {"id":"…"}` 看 24 小时、30 天和事件，`list_alerts {"state":"all"}` 看告警记录。',
    '',
    ...rules,
    '',
    '## 完成时',
    '告诉我结果和面板地址。',
  )
}

export function feishuPrompt(setup = '', apps: string[] = []) {
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

export function childPrompt(monitor: { id: string; name: string }, setup = '') {
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
    ...addRecipe.map((line) => line.replace('<主服务 id>', monitor.id)),
    '- 每个子服务都传 `"parent":"' + monitor.id + '"`。',
    '',
    ...rules,
    '',
    '## 完成时',
    '告诉我加了哪些子服务。',
  )
}

export function changePrompt(monitor: { id: string; name: string }, setup = '') {
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
    ...rules,
    '',
    '## 完成时',
    '告诉我改了什么。',
  )
}

/*
 * The relation map is only worth something when its links are right, so this one is a full guide: gather
 * evidence first, pick the relation that says what really happens, show the table, then write it.
 */
export function dependencyPrompt(board: { id: string; name: string }, setup = '') {
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
    ...rules,
    '- 只改说明、角色、依赖和文档；不改监控方式、告警和通知。',
    '',
    '## 完成时',
    '告诉我改了哪些服务，以及面板“关系”视图的地址。',
  )
}

/* Lets an agent write one service's document. */
export function docPrompt(monitor: { id: string; name: string }, setup = '') {
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
    '- 需要提醒的写成 `> **注意** …`；命令放进代码块。',
    '- 还没有说明的话，用 `edit_monitor` 补一句 description。',
    '',
    ...rules,
    '',
    '## 完成时',
    '告诉我写好了，以及服务页面地址。',
  )
}

/* Looking into a service that is failing, or failed earlier. */
export function diagnosePrompt(setup = '') {
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
    ...rules,
  )
}

/* Changing when and how alerts fire. */
export function alertPrompt(setup = '') {
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
    ...rules,
    '',
    '## 完成时',
    '列出改了哪些规则。',
  )
}

/*
 * A skill the person can hand to an agent once: the same recipes as above in one file, so later sessions start
 * knowing them instead of reading the long guide or trying tools out.
 */
export function skillText(setup = '') {
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
    'lgmon 只有三层：面板 → 服务 → 子服务（子服务异常时主服务整体算异常）。所有操作都用 lgmon 的 MCP 工具完成；每次开始先调用一次 `start_here`，不用先读长文档。',
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
    ...addRecipe,
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

export function promptChoices(setup: string, hasServices: boolean, board?: { id: string; name: string }) {
  const choices: PromptChoice[] = [
    {
      id: 'start',
      title: hasServices ? '照看服务' : '加第一个服务',
      lead: hasServices ? '看看现在有没有异常，再问你这次要做什么。' : '接入 lgmon，然后问你想监控什么。',
      text: startPrompt(setup, hasServices),
    },
  ]
  if (hasServices)
    choices.push({
      id: 'add',
      title: '加服务',
      lead: '在这台电脑上找到服务，跟你确认后加进面板。',
      text: board ? boardPrompt(board, setup) : firstPrompt('我的服务', '', setup),
    })
  if (hasServices && board)
    choices.push({
      id: 'relations',
      title: '梳理依赖关系',
      lead: '查清谁调用谁、读写哪个库、经由哪条隧道，把关系图画完整。',
      text: dependencyPrompt(board, setup),
    })
  if (hasServices)
    choices.push(
      { id: 'diagnose', title: '查问题', lead: '找出服务出问题的时间和根因。', text: diagnosePrompt(setup) },
      {
        id: 'alerts',
        title: '调整告警',
        lead: '改告警的等待时间、通知去向和静音。',
        text: alertPrompt(setup),
      },
    )
  choices.push(
    {
      id: 'feishu',
      title: '告警发到飞书',
      lead: '接入飞书机器人，把告警发到指定的群。',
      text: feishuPrompt(setup),
    },
    {
      id: 'skill',
      title: '安装 lgmon skill',
      lead: '让 Agent 记住这份操作手册，以后不用再试探，改得更快。',
      text: skillText(setup),
    },
  )
  return choices
}
