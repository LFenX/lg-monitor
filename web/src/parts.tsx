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
}: {
  title: string
  close: () => void
  children: ReactNode
  footer?: ReactNode
}) {
  const box = useRef<HTMLDivElement>(null)
  useEffect(() => {
    const key = (event: KeyboardEvent) => event.key === 'Escape' && close()
    window.addEventListener('keydown', key)
    const first = box.current?.querySelector<HTMLElement>(
      'input, select, textarea, button.lm-kind, button.primary',
    )
    first?.focus()
    return () => window.removeEventListener('keydown', key)
  }, [close])
  return (
    <div className="lm-overlay" onMouseDown={(event) => event.target === event.currentTarget && close()}>
      <div className="lm-dialog" role="dialog" aria-modal="true" aria-label={title} ref={box}>
        <header>
          <h2>{title}</h2>
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

/* The part in 【】 is the one thing the person replaces, so it stands out. */
export function PromptBox({ text }: { text: string }) {
  const pieces = text.split(/(【[^】]*】)/)
  return (
    <div className="lm-prompt">
      {pieces.map((piece, index) => (piece.startsWith('【') ? <mark key={index}>{piece}</mark> : piece))}
    </div>
  )
}

export function quote(value: string) {
  return "'" + value.replace(/'/g, "''") + "'"
}

export function boardPrompt(board: { id: string; name: string }, setup = '') {
  return (
    setup +
    ('请用 lgmon 的 MCP 工具帮我加监控：【把这里换成你要监控的服务，例如：本机 3000 端口上的博客网站，以及给它做外网穿透的 cloudflared 隧道】。\n\n' +
      '放进面板「' +
      board.name +
      '」（board: ' +
      board.id +
      '）。先调用 start_here 看现状，再用 explore 找到对应的端口、Windows 服务、进程或计划任务，用 add_monitor 添加；' +
      '附属的部分（隧道、网关、数据库、计划任务）也用 add_monitor 添加，并用 parent 挂在主服务下面。加好后把面板地址告诉我。')
  )
}

export function firstPrompt(
  name = '我的服务',
  description = '',
  setup = '',
  wish = '【把这里换成你要监控的服务，例如：本机 3000 端口上的博客网站，以及给它做外网穿透的 cloudflared 隧道】',
) {
  return (
    setup +
    ('请用 lgmon 的 MCP 工具帮我加监控：' +
      wish +
      '。\n\n' +
      '先调用 start_here 和 list_boards 看现状；还没有目标面板时，直接用 create_board 创建面板，name 使用 ' +
      JSON.stringify(name.trim() || '我的服务') +
      (description.trim() ? '，description 使用 ' + JSON.stringify(description.trim()) : '') +
      '，不需要让我手动创建。使用返回的面板 id 作为 board，再用 explore 找到对应的端口、Windows 服务、进程或计划任务，用 add_monitor 添加；' +
      '附属的部分（隧道、网关、数据库、计划任务）也用 add_monitor 添加，并用 parent 挂在主服务下面。加好后把面板地址告诉我。')
  )
}

export function agentSetup(
  install?: { executable: string; home: string; url: string },
  tools: 'essential' | 'all' = 'essential',
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

export function useAgentSetup(tools: 'essential' | 'all' = 'essential') {
  return agentSetup(useApp().guide?.install, tools)
}

export function feishuPrompt(setup = '', apps: string[] = []) {
  return (
    setup +
    '请用 lgmon 的完整 MCP 工具帮我接入飞书告警通知。\n\n' +
    '接收告警的群：【填写群名称，未填写时先帮我查找并向我确认】。通知范围：【填写要关联的监控或规则；未明确时先向我确认】。\n\n' +
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
    ('请用 lgmon 的 MCP 工具给「' +
      monitor.name +
      '」（id: ' +
      monitor.id +
      '）添加子服务：【把这里换成附属的服务，例如：给它做外网穿透的 cloudflared 隧道】。\n\n' +
      '用 explore 找到它，再用 add_monitor 添加，并传 parent: "' +
      monitor.id +
      '"。加好后告诉我结果。')
  )
}

export function changePrompt(monitor: { id: string; name: string }, setup = '') {
  return (
    setup +
    ('请用 lgmon 的 MCP 工具修改监控「' +
      monitor.name +
      '」（id: ' +
      monitor.id +
      '）：【把这里换成你想改的，例如：改成检查 https://blog.example.com/health，连续失败 1 分钟再告警】。\n\n' +
      '先用 get_monitor 看它现在的设置，再用 add_monitor 传同一个 id 更新，改完告诉我。')
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
