import { useEffect, useRef, useState } from 'react'
import type { ReactNode } from 'react'
import { Glyph } from './glyphs'
import { ago, useCopy } from './kit'
import { useApp } from './state'

export function Dialog({
  title,
  close,
  children,
  footer,
  action,
  wide = false,
  full = false,
}: {
  title: string
  close: () => void
  children: ReactNode
  footer?: ReactNode
  action?: ReactNode
  wide?: boolean
  /* Nearly the whole window, for writing at length: the body becomes a column that fills the height. */
  full?: boolean
}) {
  const box = useRef<HTMLDivElement>(null)
  // Callers pass a fresh close on every render; keyed on it, the focus below ran on each keystroke and live
  // refresh and pulled the caret back to the first field. The latest close lives in a ref instead.
  const closing = useRef(close)
  useEffect(() => {
    closing.current = close
  })
  useEffect(() => {
    const key = (event: KeyboardEvent) => event.key === 'Escape' && closing.current()
    window.addEventListener('keydown', key)
    return () => window.removeEventListener('keydown', key)
  }, [])
  useEffect(() => {
    if (box.current?.contains(document.activeElement)) return
    const first = box.current?.querySelector<HTMLElement>(
      'input, select, textarea, button.lm-kind, button.primary',
    )
    first?.focus({ preventScroll: true })
  }, [])
  return (
    <div className="lm-overlay" onMouseDown={(event) => event.target === event.currentTarget && close()}>
      <div
        className={'lm-dialog' + (wide ? ' wide' : '') + (full ? ' full' : '')}
        role="dialog"
        aria-modal="true"
        aria-label={title}
        ref={box}
      >
        <header>
          <h2>{title}</h2>
          {action && <div className="lm-dialog-action">{action}</div>}
          <button className="lm-btn ghost icon" onClick={close} aria-label="关闭">
            <Glyph name="close" />
          </button>
        </header>
        <div className="body">{children}</div>
        {footer && <footer>{footer}</footer>}
      </div>
    </div>
  )
}

export function Menu({ children, label = '更多操作' }: { children: ReactNode; label?: string }) {
  const [open, setOpen] = useState(false)
  const box = useRef<HTMLDivElement>(null)
  useEffect(() => {
    if (!open) return
    const outside = (event: MouseEvent) => {
      if (!box.current?.contains(event.target as Node)) setOpen(false)
    }
    const key = (event: KeyboardEvent) => event.key === 'Escape' && setOpen(false)
    document.addEventListener('mousedown', outside)
    window.addEventListener('keydown', key)
    return () => {
      document.removeEventListener('mousedown', outside)
      window.removeEventListener('keydown', key)
    }
  }, [open])
  return (
    <div className="lm-menu" ref={box}>
      <button
        className="lm-btn icon"
        aria-label={label}
        title={label}
        aria-expanded={open}
        onClick={() => setOpen((value) => !value)}
      >
        <Glyph name="more" />
      </button>
      {open && (
        <div className="lm-menu-list" role="menu" onClick={() => setOpen(false)}>
          {children}
        </div>
      )}
    </div>
  )
}

export function CopyButton({
  text,
  label = '复制',
  primary = false,
}: {
  text: string
  label?: string
  primary?: boolean
}) {
  const { copied, copy } = useCopy()
  return (
    <button className={'lm-btn small' + (primary ? ' primary' : '')} onClick={() => copy(text)}>
      <Glyph name={copied ? 'check' : 'copy'} size={15} />
      {copied ? '已复制' : label}
    </button>
  )
}

export function quote(value: string) {
  return "'" + value.replace(/'/g, "''") + "'"
}

/*
 * Messages for the agent never ask the person to fill in a blank: the agent asks what it needs to know, so the
 * whole exchange happens in conversation with it.
 */
const askAndFind =
  '然后问我想监控哪些服务（网站、端口、Windows 服务、进程或计划任务），以及它们有没有附属的部分（例如隧道、网关、数据库）。' +
  '不要让我自己去查端口或进程名：用 explore 在这台电脑上找到它们，跟我确认后用 add_monitor 添加，附属的部分也用 add_monitor，并用 parent 挂在主服务下面。'

export function boardPrompt(board: { id: string; name: string }, setup = '') {
  return (
    setup +
    '请用 lgmon 帮我往面板「' +
    board.name +
    '」（board: ' +
    board.id +
    '）里加服务。\n\n先调用 start_here 看现状，' +
    askAndFind +
    '\n\n加好后把面板地址发给我。'
  )
}

// A concrete task (from the examples on the Connect page) replaces the question about what to monitor.
export function firstPrompt(name = '我的服务', description = '', setup = '', task = '') {
  const board =
    '还没有面板时用 create_board 建一个，name 使用 ' +
    JSON.stringify(name.trim() || '我的服务') +
    (description.trim() ? '，description 使用 ' + JSON.stringify(description.trim()) : '') +
    '，不用让我自己建；之后用它的 id 作为 board。'
  return (
    setup +
    (task
      ? '请用 lgmon 帮我：' +
        task +
        '\n\n先调用 start_here 看现状。' +
        board +
        '需要添加服务时用 explore 找到对象，用 add_monitor 添加，附属的部分用 parent 挂在主服务下面；有不确定的地方先问我。\n\n做完告诉我结果和面板地址。'
      : '请用 lgmon 帮我监控这台电脑上的服务。\n\n先调用 start_here 看现状，' +
        askAndFind +
        board +
        '\n\n加好后把面板地址发给我，再问我要不要把告警发到手机（飞书）。')
  )
}

// The message in lgmon's small window: once services exist, the agent asks what this visit is for.
export function startPrompt(setup = '', hasServices = false) {
  if (!hasServices) return firstPrompt('我的服务', '', setup)
  return (
    setup +
    '请用 lgmon 帮我照看这台电脑上的服务。\n\n' +
    '先调用 start_here 看现状，告诉我现在有没有异常，然后问我这次想做什么：加服务、改告警、把告警发到飞书，或者查某个服务出过什么问题。' +
    '加服务时用 explore 在这台电脑上找到它，跟我确认后用 add_monitor 添加，附属的部分（隧道、网关、数据库）用 parent 挂在主服务下面。\n\n' +
    '做完告诉我结果和面板地址。'
  )
}

export function agentSetup(
  install?: { executable: string; home: string; url: string },
  tools: 'essential' | 'all' = 'all',
) {
  if (!install?.executable || !install.home) return ''
  const command =
    '& ' +
    quote(install.executable) +
    ' agent connect --home ' +
    quote(install.home) +
    (install.url ? ' --url ' + quote(install.url) : '') +
    (tools === 'all' ? ' --tools all' : '') +
    ' --elevate --json'
  return (
    (tools === 'all'
      ? '如果 lgmon MCP 尚未接入，或当前工具集中没有本任务需要的管理工具，请你先在这台电脑执行以下 PowerShell 命令接入完整工具集，不要让我手动运行：\n'
      : '如果 lgmon MCP 尚未接入，请你先在这台电脑执行以下 PowerShell 命令完成接入，不要让我手动运行：\n') +
    command +
    '\n' +
    '需要管理员时，程序会弹出 Windows 授权窗口，由我本人确认一次；取消时停止接入并说明原因，不要反复弹窗。' +
    '仅在内存中读取 JSON 接入结果，令牌不得回显到聊天、截图、日志或写入仓库。' +
    '能动态连接 MCP 时使用返回的配置，并通过子进程环境变量传递令牌；当前会话不能动态加载时，用返回的 http 地址和令牌请求头直接调用 MCP（initialize、tools/list、tools/call），不要要求我重开会话或手动配置，也不要把令牌放进命令行参数。' +
    '已经接入时复用当前连接，不重复创建令牌。接入后继续完成下面的任务。\n\n'
  )
}

export function useAgentSetup(tools: 'essential' | 'all' = 'all') {
  return agentSetup(useApp().guide?.install, tools)
}

export function feishuPrompt(setup = '', apps: string[] = []) {
  return (
    setup +
    '请用 lgmon 的完整 MCP 工具帮我接入飞书告警通知。\n\n' +
    '先问我两件事：告警要发到哪个飞书群，以及哪些服务或告警规则要发过去（我没想好时默认全部）。不要让我去查群 ID。\n\n' +
    (apps.length
      ? '已在本机保存凭证的应用 ID：' + JSON.stringify(apps) + '；优先复用，多个应用无法唯一确定时先问我。\n'
      : '还没有已保存凭证的应用。先指导我创建自建应用、启用机器人，并在本机安全表单保存应用凭证；等待我完成，不要向聊天索取 App Secret，也不要读取密钥库。\n') +
    '先调用 start_here，再读取 get_config(notifiers) 和 get_config(rules)，保留已有渠道、筛选条件和规则。用 feishu_check 检查机器人、权限和接入状态；需要我开通权限、发布版本、审批或把机器人拉进群时，只给出必要操作和官方链接，由我配合完成，你继续检查结果。\n' +
    '用 feishu_list_chats 找到我明确指定的群；群未指定、有重名或机器人不在群内时先向我确认，不要自行猜测、建群或改群成员。\n' +
    '我授权你向我明确指定的告警群调用 feishu_test_chat 发送接入测试卡片，遵守同一目的地十秒一次的限制，不绕过。发送成功不等于收到；等待我明确回复已收到后，才调用 feishu_save_chat 保存渠道，并通过 rules 追加到我确认过的规则，不删除或覆盖原通知。\n' +
    '最后重新读取配置，核对群渠道和规则关联，告诉我已完成项、需要我配合的未完成项，以及通知页面地址。整个流程由你执行可自动完成的步骤，不要求我手动运行命令或填写群 ID；应用密钥只在本机输入，不经聊天、截图、日志或仓库。'
  )
}

export function childPrompt(monitor: { id: string; name: string }, setup = '') {
  return (
    setup +
    '请用 lgmon 帮我给「' +
    monitor.name +
    '」（id: ' +
    monitor.id +
    '）添加子服务。\n\n' +
    '先用 get_monitor 看它现在的情况，再问我它有哪些附属的部分要一起看（例如给它做外网穿透的隧道、网关、数据库、计划任务）。' +
    '用 explore 在这台电脑上找到它们，跟我确认后用 add_monitor 添加，并传 parent: "' +
    monitor.id +
    '"。\n\n加好后告诉我结果。'
  )
}

export function changePrompt(monitor: { id: string; name: string }, setup = '') {
  return (
    setup +
    '请用 lgmon 帮我修改监控「' +
    monitor.name +
    '」（id: ' +
    monitor.id +
    '）。\n\n' +
    '先用 get_monitor 看它现在的设置，用一两句话告诉我它在检查什么、异常多久才告警，再问我想怎么改。' +
    '按我的回答用 add_monitor 传同一个 id 更新，改完告诉我结果。'
  )
}

/* Lets an agent work out who uses whom on a board, with descriptions, roles and, where useful, documents. */
export function dependencyPrompt(board: { id: string; name: string }, setup = '') {
  return (
    setup +
    '请用 lgmon 帮我梳理面板「' +
    board.name +
    '」（id: ' +
    board.id +
    '）里服务之间的依赖关系，让别人一眼看懂这些服务怎么连在一起。\n\n' +
    '先用 get_board 看这个面板，再用 list_monitors 看全部服务（依赖可以跨面板）。根据名字、端口、进程、网址和你在这台电脑上能查到的信息，判断每个服务用到了谁：' +
    'calls 调用接口、data 读写数据（数据库、缓存、存储）、via 经由隧道或网关对外、runs 由计划任务或守护程序启动、sends 发送消息，说不清的用 uses。' +
    '把你的判断列给我确认，确认后用 edit_monitor 给每个服务写上一句 description、合适的 role，并用 depends 写出依赖（写在使用的一方）。' +
    '有部署、启动方式或排障步骤值得记下来的服务，用 put_monitor_doc 写一篇简短的文档，里面用 [[服务ID]] 引用其他服务。不要写密钥。\n\n' +
    '做完告诉我改了哪些服务。'
  )
}

/* Lets an agent write one service's document. */
export function docPrompt(monitor: { id: string; name: string }, setup = '') {
  return (
    setup +
    '请用 lgmon 帮我给服务「' +
    monitor.name +
    '」（id: ' +
    monitor.id +
    '）写一篇文档。\n\n' +
    '先用 get_monitor 和 get_monitor_doc 看它现在的情况和已有文档，再在这台电脑上查清楚它是什么、部署在哪、怎么启动和停止、依赖哪些服务、出问题时先查什么；不清楚的问我。' +
    '用 put_monitor_doc 写成 Markdown：开头一段话说清它是做什么的，然后用 ## 分节（概览、依赖、启动与停止、排障），' +
    '提到其他服务时写 [[服务ID]]，加一个 ```deps 代码块画出它的上下游；需要提醒的写成 > **注意** 开头的引用。没写过说明的话，也用 edit_monitor 补一句 description。不要写密钥、令牌和密码。\n\n' +
    '写好后告诉我。'
  )
}

/* How an agent gets the MCP tools; the command uses this install's real program and data paths. */
export function ConnectSteps({
  compact = false,
  last = '把上面那段话发给它。',
}: {
  compact?: boolean
  last?: string
}) {
  const { guide, now } = useApp()
  if (!guide) return null
  const seen = guide.state.agentSeenAt
  const command =
    '& ' + quote(guide.install.executable) + ' agent connect --home ' + quote(guide.install.home)
  if (seen && compact)
    return <p className="lm-muted lm-small">Agent 已接入，最近一次调用在 {ago(seen, now())}。</p>
  return (
    <details className="lm-steps-wrap">
      <summary>手动接入（可选）</summary>
      <ol className="lm-steps">
        <li>
          <div>
            <span>
              在这台电脑的 PowerShell 里运行
              {guide.install.personal ? '' : '（需要管理员窗口）'}：
            </span>
            <div className="lm-code">
              <code>{command}</code>
              <CopyButton text={command} />
            </div>
          </div>
        </li>
        <li>
          <div>把输出的那段配置交给你的 Agent（Claude Code、Codex、Cursor 等），它就有了 lgmon 的工具。</div>
        </li>
        <li>
          <div>{last}</div>
        </li>
      </ol>
      {seen ? <p className="lm-muted lm-small">已有 Agent 接入过，最近一次在 {ago(seen, now())}。</p> : null}
    </details>
  )
}
