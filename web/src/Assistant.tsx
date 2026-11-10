import { useEffect, useRef, useState } from 'react'
import ReactMarkdown from 'react-markdown'
import remarkGfm from 'remark-gfm'
import { APIError, readEvents } from './api'
import { CopyBox } from './kit'
import { useApp, useResource } from './state'
import type { AssistantStatus } from './types'
import { ErrorNotice, Icon } from './ui'

type Tool = { id: string; label: string; detail?: string; state: 'running' | 'done' | 'failed' }
type Message = { role: 'user' | 'assistant'; text: string; tools?: Tool[]; error?: string }

const storageKey = 'lgmon-assistant-chat'
const suggestions = [
  '现在整体情况怎么样？',
  '有哪些设为自动启动却停着的服务？',
  '这台电脑上哪些端口对外开放？',
  '最近一小时 CPU 最高是多少？',
  '内存占用最大的 5 个进程是什么？',
  '最近有什么告警？原因是什么？',
]

function restore(): Message[] {
  try {
    const value = JSON.parse(sessionStorage.getItem(storageKey) || '[]')
    return Array.isArray(value)
      ? value
          .filter(
            (item) =>
              item && (item.role === 'user' || item.role === 'assistant') && typeof item.text === 'string',
          )
          .slice(-40)
      : []
  } catch {
    return []
  }
}

function Reply({ text }: { text: string }) {
  return (
    <ReactMarkdown
      remarkPlugins={[remarkGfm]}
      skipHtml
      components={{
        a: ({ href, children }) => {
          const safe = typeof href === 'string' && /^(https?:\/\/|#\/)/.test(href)
          return safe ? (
            <a href={href} target={href.startsWith('#') ? undefined : '_blank'} rel="noreferrer">
              {children}
            </a>
          ) : (
            <span>{children}</span>
          )
        },
        table: ({ children }) => (
          <div className="table-wrap">
            <table>{children}</table>
          </div>
        ),
        img: () => null,
      }}
    >
      {text}
    </ReactMarkdown>
  )
}

export function Assistant({ close }: { close: () => void }) {
  const { api, version } = useApp()
  // Re-read on every snapshot refresh, so a key saved from the terminal shows up without reloading.
  const status = useResource(
    (signal) => api.request<AssistantStatus>('/assistant', { signal }),
    [api, version],
  )
  const [messages, setMessages] = useState<Message[]>(restore),
    [input, setInput] = useState(''),
    [busy, setBusy] = useState(false)
  const pending = useRef<AbortController | null>(null),
    list = useRef<HTMLDivElement>(null),
    field = useRef<HTMLTextAreaElement>(null)
  useEffect(() => () => pending.current?.abort(), [])
  useEffect(() => {
    try {
      sessionStorage.setItem(
        storageKey,
        JSON.stringify(messages.filter((item) => item.text).map(({ role, text }) => ({ role, text }))),
      )
    } catch {
      /* The conversation simply starts over on the next visit. */
    }
    list.current?.scrollTo({ top: list.current.scrollHeight, behavior: 'smooth' })
  }, [messages])
  useEffect(() => {
    const element = field.current
    if (!element) return
    element.style.height = 'auto'
    element.style.height = Math.min(140, element.scrollHeight) + 'px'
  }, [input])
  const configured = status.value?.configured
  const [draft, setDraft] = useState('')
  useEffect(() => {
    // The search palette leaves a question here when the user picks "ask AI".
    const take = () => {
      try {
        const value = sessionStorage.getItem('lgmon-assistant-draft') || ''
        sessionStorage.removeItem('lgmon-assistant-draft')
        if (value) setDraft(value)
      } catch {
        /* Nothing to take over. */
      }
    }
    take()
    window.addEventListener('lgmon-ask', take)
    return () => window.removeEventListener('lgmon-ask', take)
  }, [])
  const update = (change: (last: Message) => Message) =>
    setMessages((current) => [...current.slice(0, -1), change(current[current.length - 1])])
  const send = async (text: string) => {
    const question = text.trim()
    if (!question || busy) return
    const history = [
      ...messages.filter((item) => item.text && !item.error),
      { role: 'user' as const, text: question },
    ].slice(-20)
    setMessages((current) => [
      ...current,
      { role: 'user', text: question },
      { role: 'assistant', text: '', tools: [] },
    ])
    setInput('')
    setBusy(true)
    const controller = new AbortController()
    pending.current = controller
    try {
      const response = await api.fetcher('/api/v1/assistant/chat', {
        method: 'POST',
        signal: controller.signal,
        redirect: 'error',
        cache: 'no-store',
        credentials: 'omit',
        headers: { Authorization: 'Bearer ' + api.token, 'Content-Type': 'application/json' },
        body: JSON.stringify({ messages: history.map(({ role, text }) => ({ role, text })) }),
      })
      if (!response.ok || !response.body) {
        let message = 'AI 助手暂时不可用（HTTP ' + response.status + '）'
        try {
          const value = await response.json()
          if (typeof value?.error?.message === 'string') message = value.error.message
        } catch {
          /* The status line is enough. */
        }
        throw new APIError(message, response.status)
      }
      await readEvents(response.body, controller.signal, (event, raw) => {
        let data: Record<string, string> = {}
        try {
          data = JSON.parse(raw)
        } catch {
          return
        }
        if (event === 'text' && typeof data.delta === 'string')
          update((last) => ({ ...last, text: last.text + data.delta }))
        if (event === 'tool' && typeof data.id === 'string')
          update((last) => {
            const tools = [...(last.tools || [])]
            const index = tools.findIndex((tool) => tool.id === data.id)
            const tool: Tool = {
              id: data.id,
              label: data.label || data.name,
              detail: data.detail,
              state: data.state as Tool['state'],
            }
            if (index >= 0) tools[index] = tool
            else tools.push(tool)
            return { ...last, tools }
          })
        if (event === 'error') update((last) => ({ ...last, error: data.message || 'AI 助手出错了' }))
      })
    } catch (err) {
      if (!controller.signal.aborted)
        update((last) => ({ ...last, error: err instanceof Error ? err.message : '连接 AI 助手失败' }))
    } finally {
      if (pending.current === controller) pending.current = null
      setBusy(false)
      update((last) =>
        last.text || last.error
          ? last
          : { ...last, error: controller.signal.aborted ? '已停止' : '没有收到回答' },
      )
    }
  }
  useEffect(() => {
    if (!draft || status.loading) return
    setDraft('')
    if (configured) void send(draft)
    else setInput(draft)
    // send reads the latest state when it runs, so it is deliberately not a dependency.
  }, [draft, configured, status.loading])
  return (
    <aside className="assistant" aria-label="AI 助手">
      <div className="assistant-head">
        <span className="assistant-spark">
          <Icon name="sparkles" size={17} />
        </span>
        <strong>
          AI 助手
          <small>
            {configured ? status.value?.model + ' · 只读，不会改动任何东西' : '读取监控数据回答问题'}
          </small>
        </strong>
        {!!messages.length && (
          <button
            className="icon-button"
            title="新对话"
            aria-label="新对话"
            onClick={() => {
              pending.current?.abort()
              setMessages([])
            }}
          >
            <Icon name="refresh" size={17} />
          </button>
        )}
        <button className="icon-button" title="关闭" aria-label="关闭 AI 助手" onClick={close}>
          <Icon name="close" size={18} />
        </button>
      </div>
      <div className="assistant-messages" ref={list}>
        {status.value && !configured ? (
          <div className="assistant-welcome">
            <span className="assistant-spark">
              <Icon name="plug" size={17} />
            </span>
            <h3>先给助手配一个 API Key</h3>
            <p>
              助手使用 Claude 模型读取这里的监控数据来回答问题。在监控器电脑的 PowerShell
              里运行下面这条命令，按提示粘贴你的 Anthropic API Key（输入时不显示）：
            </p>
            <CopyBox text={status.value.setup || 'lgmon secret set assistant-api-key'} />
            <p style={{ marginTop: 14 }}>
              保存后点一下右上角的刷新即可使用。需要换模型或走兼容网关时，在 settings.yaml 里写：
            </p>
            <CopyBox text={'assistant:\n  model: claude-opus-5-5\n  baseUrl: https://你的网关地址'} />
          </div>
        ) : !messages.length ? (
          <div className="assistant-welcome">
            <span className="assistant-spark">
              <Icon name="sparkles" size={17} />
            </span>
            <h3>想知道什么，直接问</h3>
            <p>我会实时查询这台电脑的服务、端口、进程、监控和告警，再用大白话告诉你。</p>
            <ul className="prompt-list">
              {suggestions.map((text) => (
                <li key={text}>
                  <button onClick={() => void send(text)} disabled={!configured}>
                    <Icon name="send" size={14} />
                    <span>{text}</span>
                  </button>
                </li>
              ))}
            </ul>
          </div>
        ) : (
          messages.map((message, index) =>
            message.role === 'user' ? (
              <div className="bubble user" key={index}>
                {message.text}
              </div>
            ) : (
              <div className="bubble assistant" key={index}>
                {!!message.tools?.length && (
                  <div className="tool-trail">
                    {message.tools.map((tool) => (
                      <span className="tool-chip" key={tool.id} data-state={tool.state}>
                        <i />
                        {tool.label}
                        {tool.detail ? '：' + tool.detail : ''}
                      </span>
                    ))}
                  </div>
                )}
                {message.text ? (
                  <Reply text={message.text} />
                ) : (
                  !message.error && (
                    <span className="typing" aria-label="正在思考">
                      <i />
                      <i />
                      <i />
                    </span>
                  )
                )}
                {message.error && <ErrorNotice message={message.error} />}
              </div>
            ),
          )
        )}
        <ErrorNotice message={status.error} />
      </div>
      <form
        className="assistant-form"
        onSubmit={(event) => {
          event.preventDefault()
          void send(input)
        }}
      >
        <textarea
          ref={field}
          rows={1}
          value={input}
          maxLength={8000}
          disabled={!configured}
          placeholder={configured ? '问问这台电脑的状况…' : '配置 API Key 后即可提问'}
          aria-label="问 AI 助手"
          onChange={(event) => setInput(event.target.value)}
          onKeyDown={(event) => {
            if (event.key === 'Enter' && !event.shiftKey && !event.nativeEvent.isComposing) {
              event.preventDefault()
              void send(input)
            }
          }}
        />
        {busy ? (
          <button type="button" aria-label="停止" title="停止" onClick={() => pending.current?.abort()}>
            <Icon name="close" size={16} />
          </button>
        ) : (
          <button aria-label="发送" title="发送（Enter）" disabled={!configured || !input.trim()}>
            <Icon name="send" size={16} />
          </button>
        )}
      </form>
      <p className="assistant-note">Enter 发送 · Shift+Enter 换行 · 助手只能读取，不能修改配置</p>
    </aside>
  )
}
