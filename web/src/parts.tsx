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

export function boardPrompt(board: { id: string; name: string }) {
  return (
    '请用 lgmon 的 MCP 工具帮我加监控：【把这里换成你要监控的服务，例如：本机 3000 端口上的博客网站，以及给它做外网穿透的 cloudflared 隧道】。\n\n' +
    '放进面板「' +
    board.name +
    '」（board: ' +
    board.id +
    '）。先调用 start_here 看现状，再用 explore 找到对应的端口、Windows 服务、进程或计划任务，用 add_monitor 添加；' +
    '附属的部分（隧道、网关、数据库、计划任务）也用 add_monitor 添加，并用 parent 挂在主服务下面。加好后把面板地址告诉我。'
  )
}

export function firstPrompt() {
  return (
    '请用 lgmon 的 MCP 工具帮我加监控：【把这里换成你要监控的服务，例如：本机 3000 端口上的博客网站，以及给它做外网穿透的 cloudflared 隧道】。\n\n' +
    '先调用 start_here 看现状，再用 explore 找到对应的端口、Windows 服务、进程或计划任务，用 add_monitor 添加（还没有面板时会自动建好“我的服务”）；' +
    '附属的部分（隧道、网关、数据库、计划任务）也用 add_monitor 添加，并用 parent 挂在主服务下面。加好后把面板地址告诉我。'
  )
}

export function childPrompt(monitor: { id: string; name: string }) {
  return (
    '请用 lgmon 的 MCP 工具给「' +
    monitor.name +
    '」（id: ' +
    monitor.id +
    '）添加子服务：【把这里换成附属的服务，例如：给它做外网穿透的 cloudflared 隧道】。\n\n' +
    '用 explore 找到它，再用 add_monitor 添加，并传 parent: "' +
    monitor.id +
    '"。加好后告诉我结果。'
  )
}

export function changePrompt(monitor: { id: string; name: string }) {
  return (
    '请用 lgmon 的 MCP 工具修改监控「' +
    monitor.name +
    '」（id: ' +
    monitor.id +
    '）：【把这里换成你想改的，例如：改成检查 https://blog.example.com/health，连续失败 1 分钟再告警】。\n\n' +
    '先用 get_monitor 看它现在的设置，再用 add_monitor 传同一个 id 更新，改完告诉我。'
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
    <div className="lm-steps-wrap">
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
    </div>
  )
}
