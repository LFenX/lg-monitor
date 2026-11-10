import { useEffect, useMemo, useRef, useState } from 'react'
import ReactMarkdown from 'react-markdown'
import remarkGfm from 'remark-gfm'
import { APIError, readEvents } from './api'
import { AssistantMark } from './brand'
import { CopyBox } from './kit'
import { useApp, useResource } from './state'
import type { AssistantProvider, AssistantStatus } from './types'
import { Icon } from './ui'
import './assistant.css'

type Step = { id: string; label: string; detail?: string; state: 'running' | 'done' | 'failed' }
type Turn = { question: string; answer: string; steps: Step[]; error?: string; at: number }

const storageKey = 'lgmon-assistant-turns'
const starters = [
  { kind: '状态', tone: 'sky', text: '现在整体情况怎么样？有没有需要我处理的？' },
  { kind: '服务', tone: 'mint', text: '哪些 Windows 服务设为自动启动却停着？' },
  { kind: '资源', tone: 'yellow', text: '内存占用最大的 5 个进程是什么？' },
  { kind: '告警', tone: 'rose', text: '最近有什么告警？原因是什么？' },
]
const regions: [AssistantProvider['region'], string][] = [
  ['global', '国际'],
  ['china', '国内'],
  ['local', '本机运行'],
]

function restore(): Turn[] {
  try {
    const value = JSON.parse(sessionStorage.getItem(storageKey) || '[]')
    return Array.isArray(value)
      ? value
          .filter((item) => item && typeof item.question === 'string' && typeof item.answer === 'string')
          .map((item) => ({ ...item, steps: Array.isArray(item.steps) ? item.steps : [], at: item.at || 0 }))
          .slice(-20)
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

function clock(at: number) {
  return at
    ? new Date(at).toLocaleTimeString('zh-CN', { hour: '2-digit', minute: '2-digit', hour12: false })
    : ''
}

export function Assistant({ close }: { close: () => void }) {
  const { api, version, scope } = useApp()
  // Re-read on every snapshot refresh, so a key saved from the terminal shows up without reloading.
  const status = useResource(
    (signal) => api.request<AssistantStatus>('/assistant', { signal }),
    [api, version],
  )
  const [turns, setTurns] = useState<Turn[]>(restore),
    [input, setInput] = useState(''),
    [busy, setBusy] = useState(false),
    [view, setView] = useState<'chat' | 'settings'>('chat'),
    [scrolled, setScrolled] = useState(false)
  const pending = useRef<AbortController | null>(null),
    list = useRef<HTMLDivElement>(null),
    field = useRef<HTMLTextAreaElement>(null)
  useEffect(() => () => pending.current?.abort(), [])
  useEffect(() => {
    try {
      sessionStorage.setItem(storageKey, JSON.stringify(turns.filter((turn) => turn.answer || turn.error)))
    } catch {
      /* The conversation simply starts over on the next visit. */
    }
    list.current?.scrollTo({ top: list.current.scrollHeight, behavior: 'smooth' })
  }, [turns])
  useEffect(() => {
    const element = field.current
    if (!element) return
    element.style.height = 'auto'
    element.style.height = Math.min(132, element.scrollHeight) + 'px'
  }, [input])
  useEffect(() => {
    const key = (event: KeyboardEvent) => {
      if (event.key === 'Escape' && !busy && !document.querySelector('dialog[open]')) close()
    }
    window.addEventListener('keydown', key)
    return () => window.removeEventListener('keydown', key)
  }, [busy, close])
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
  const update = (change: (last: Turn) => Turn) =>
    setTurns((current) => [...current.slice(0, -1), change(current[current.length - 1])])
  const send = async (text: string) => {
    const question = text.trim()
    if (!question || busy) return
    const history = turns
      .filter((turn) => turn.answer && !turn.error)
      .slice(-8)
      .flatMap((turn) => [
        { role: 'user', text: turn.question },
        { role: 'assistant', text: turn.answer },
      ])
    setTurns((current) => [...current, { question, answer: '', steps: [], at: Date.now() }])
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
        body: JSON.stringify({ messages: [...history, { role: 'user', text: question }] }),
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
          update((last) => ({ ...last, answer: last.answer + data.delta }))
        if (event === 'tool' && typeof data.id === 'string')
          update((last) => {
            const steps = [...last.steps]
            const index = steps.findIndex((step) => step.id === data.id)
            const step: Step = {
              id: data.id,
              label: data.label || data.name,
              detail: data.detail,
              state: data.state as Step['state'],
            }
            if (index >= 0) steps[index] = step
            else steps.push(step)
            return { ...last, steps }
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
        last.answer || last.error
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
  const subtitle = status.value
    ? configured
      ? (status.value.providerName ? status.value.providerName + ' · ' : '') + status.value.model
      : '还没有选模型'
    : '正在读取…'
  return (
    <aside className="ai-panel" aria-label="AI 助手">
      <div className="ai-box" data-scrolled={scrolled || undefined}>
        <div className="ai-head">
          {view === 'settings' ? (
            <button className="icon-button ai-back" aria-label="返回对话" onClick={() => setView('chat')}>
              <Icon name="arrow" size={17} />
            </button>
          ) : (
            <AssistantMark size={28} think={busy} />
          )}
          <div className="ai-head-t">
            <b>{view === 'settings' ? '选择模型' : '监控助手'}</b>
            <span className="ai-head-sub">
              {view === 'settings' ? 'Claude、OpenAI、国内各家兼容接口与本机模型' : subtitle + ' · 只读'}
            </span>
          </div>
          <div className="ai-head-r">
            {view === 'chat' && !!turns.length && (
              <button
                className="icon-button"
                title="新对话"
                aria-label="新对话"
                onClick={() => {
                  pending.current?.abort()
                  setTurns([])
                }}
              >
                <Icon name="plus" size={17} />
              </button>
            )}
            {view === 'chat' && (
              <button
                className="icon-button"
                title="选择模型"
                aria-label="选择模型"
                onClick={() => setView('settings')}
              >
                <Icon name="settings" size={17} />
              </button>
            )}
            <button className="icon-button" title="关闭（Esc）" aria-label="关闭 AI 助手" onClick={close}>
              <Icon name="close" size={17} />
            </button>
          </div>
        </div>
        {view === 'settings' ? (
          <Settings
            status={status.value}
            admin={scope === 'admin'}
            saved={() => {
              setView('chat')
              status.reload()
            }}
          />
        ) : (
          <>
            <div
              className="ai-scroll"
              ref={list}
              onScroll={(event) => setScrolled(event.currentTarget.scrollTop > 4)}
            >
              {status.value && !configured ? (
                <div className="ai-hello">
                  <span className="ai-hello-mark">
                    <AssistantMark size={44} glow />
                  </span>
                  <h2>先给助手选一个模型</h2>
                  <p>
                    助手会实时查询这台电脑的服务、端口、进程、监控和告警，再用大白话告诉你。Claude、OpenAI、DeepSeek、通义千问、Kimi、智谱、Gemini，或者本机的
                    Ollama 都可以。
                  </p>
                  {status.value.problem && <p className="ai-problem">{status.value.problem}</p>}
                  <button className="ai-resume" onClick={() => setView('settings')}>
                    <Icon name="settings" size={16} />
                    <span className="ai-resume-t">
                      <b>选择模型并保存密钥</b>
                    </span>
                    <small>
                      {status.value.providers?.length ? status.value.providers.length + ' 个供应商' : ''}
                    </small>
                  </button>
                  {status.value.setup && (
                    <>
                      <p className="ai-or">
                        也可以在监控器电脑的 PowerShell 里运行（按提示粘贴密钥，输入不显示）：
                      </p>
                      <CopyBox text={status.value.setup} />
                    </>
                  )}
                </div>
              ) : !turns.length ? (
                <div className="ai-hello">
                  <span className="ai-hello-mark">
                    <AssistantMark size={44} glow />
                  </span>
                  <h2>想知道这台电脑的什么？</h2>
                  <p>
                    我会实时查询服务、端口、进程、监控和告警，查完用几句话告诉你结论。我只能看，不会改动任何东西。
                  </p>
                  <div className="ai-starters">
                    {starters.map((item) => (
                      <button
                        key={item.text}
                        className={'st-' + item.tone}
                        disabled={!configured}
                        onClick={() => void send(item.text)}
                      >
                        <span className="ai-st-k">{item.kind}</span>
                        <span className="ai-st-t">{item.text}</span>
                      </button>
                    ))}
                  </div>
                </div>
              ) : (
                turns.map((turn, index) => (
                  <section className="ai-turn" key={index}>
                    {clock(turn.at) && (
                      <div className="ai-when">
                        <span>{clock(turn.at)}</span>
                      </div>
                    )}
                    <div className="ai-q">
                      <span>{turn.question}</span>
                    </div>
                    <div className="ai-a">
                      {!!turn.steps.length && (
                        <ul className="ai-steps">
                          {turn.steps.map((step) => (
                            <li className="ai-step" key={step.id} data-state={step.state}>
                              <Icon
                                name={
                                  step.state === 'failed'
                                    ? 'alert'
                                    : step.state === 'done'
                                      ? 'check'
                                      : 'search'
                                }
                                size={14}
                              />
                              <span className={step.state === 'running' ? 'ai-shimmer' : undefined}>
                                {step.state === 'running'
                                  ? '正在'
                                  : step.state === 'failed'
                                    ? '没查到 · '
                                    : '查看了 '}
                                <b>{step.label}</b>
                                {step.detail && <q>{step.detail}</q>}
                              </span>
                            </li>
                          ))}
                        </ul>
                      )}
                      {turn.answer ? (
                        <div className="ai-text">
                          <Reply text={turn.answer} />
                          {busy && index === turns.length - 1 && <i className="ai-caret" />}
                        </div>
                      ) : (
                        !turn.error && (
                          <div className="ai-skel" aria-label="正在思考">
                            <i />
                            <i />
                            <i />
                          </div>
                        )
                      )}
                      {turn.error && (
                        <div className="ai-error" role="alert">
                          <Icon name="alert" size={16} />
                          <span>{turn.error}</span>
                          {index === turns.length - 1 && !busy && turn.error !== '已停止' && (
                            <button
                              onClick={() => {
                                setTurns((current) => current.slice(0, -1))
                                void send(turn.question)
                              }}
                            >
                              重试
                            </button>
                          )}
                        </div>
                      )}
                    </div>
                  </section>
                ))
              )}
            </div>
            <div className="ai-bottom">
              <form
                className="ai-compose"
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
                  placeholder={configured ? '问问这台电脑的状况…' : '先选一个模型'}
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
                  <button
                    type="button"
                    className="ai-send stop"
                    aria-label="停止"
                    title="停止"
                    onClick={() => pending.current?.abort()}
                  >
                    <Icon name="stop" size={14} />
                  </button>
                ) : (
                  <button
                    className="ai-send"
                    aria-label="发送"
                    title="发送（Enter）"
                    disabled={!configured || !input.trim()}
                  >
                    <Icon name="send" size={16} />
                  </button>
                )}
              </form>
              <p className="ai-foot">Enter 发送 · Shift+Enter 换行 · Ctrl J 打开或收起</p>
            </div>
          </>
        )}
      </div>
    </aside>
  )
}

function Settings({ status, admin, saved }: { status?: AssistantStatus; admin: boolean; saved: () => void }) {
  const { api, guide } = useApp()
  const providers = useMemo(() => status?.providers || [], [status])
  const [provider, setProvider] = useState(status?.provider || 'anthropic'),
    [model, setModel] = useState(status?.configured ? status.model : ''),
    [address, setAddress] = useState(''),
    [key, setKey] = useState(''),
    [busy, setBusy] = useState(false),
    [error, setError] = useState(''),
    [done, setDone] = useState('')
  const form = useRef<HTMLFormElement>(null)
  const chosen = providers.find((item) => item.id === provider)
  const current = status?.configured && status.provider === provider
  const quote = (value: string) => "'" + value.replaceAll("'", "''") + "'"
  const command =
    '& ' +
    quote(guide?.install.executable || 'lgmon.exe') +
    ' assistant set --provider ' +
    provider +
    (model ? ' --model ' + quote(model) : '') +
    (address ? ' --base-url ' + quote(address) : '') +
    ' --home ' +
    quote(guide?.install.home || '<home>')
  return (
    <div className="ai-scroll ai-settings">
      {regions.map(([region, label]) => {
        const items = providers.filter((item) => item.region === region)
        if (!items.length) return null
        return (
          <div key={region}>
            <p className="ai-label">{label}</p>
            <div className="ai-providers">
              {items.map((item) => (
                <button
                  key={item.id}
                  aria-pressed={provider === item.id}
                  onClick={() => {
                    setProvider(item.id)
                    setModel(status?.configured && status.provider === item.id ? status.model : '')
                    setAddress('')
                    setError('')
                    setDone('')
                    // The form sits under the provider grid; bring it up so the choice visibly lands somewhere.
                    requestAnimationFrame(() =>
                      form.current?.scrollIntoView?.({ block: 'nearest', behavior: 'smooth' }),
                    )
                  }}
                >
                  <b>{item.name}</b>
                  <small>{item.protocol === 'anthropic' ? 'Anthropic API' : 'OpenAI 兼容'}</small>
                  {status?.configured && status.provider === item.id && <i className="ai-current">使用中</i>}
                </button>
              ))}
            </div>
          </div>
        )
      })}
      {chosen && (
        <form
          ref={form}
          className="ai-form"
          onSubmit={(event) => {
            event.preventDefault()
            setBusy(true)
            setError('')
            setDone('')
            void api
              .request<AssistantStatus>('/assistant', {
                method: 'PUT',
                body: JSON.stringify({ provider, model, baseUrl: address, apiKey: key }),
              })
              .then((result) => {
                setKey('')
                setDone('已连通 ' + result.providerName + ' · ' + result.model + '，可以提问了。')
                setTimeout(saved, 900)
              })
              .catch((err: unknown) => setError(err instanceof Error ? err.message : '保存失败'))
              .finally(() => setBusy(false))
          }}
        >
          <label>
            模型
            <input
              value={model}
              list={'models-' + chosen.id}
              placeholder={chosen.model ? '默认 ' + chosen.model : '填写模型 ID'}
              onChange={(event) => setModel(event.target.value)}
              spellCheck={false}
            />
            <datalist id={'models-' + chosen.id}>
              {chosen.models.map((value) => (
                <option key={value} value={value} />
              ))}
            </datalist>
          </label>
          <label>
            接口地址
            <input
              value={address}
              placeholder={chosen.baseUrl || 'https://你的网关/v1'}
              onChange={(event) => setAddress(event.target.value)}
              spellCheck={false}
            />
          </label>
          {admin && (chosen.needsKey || chosen.id === 'custom') && (
            <label>
              API Key
              <input
                type="password"
                value={key}
                autoComplete="off"
                placeholder={current ? '已保存，留空则沿用' : '粘贴密钥，只保存在这台电脑的加密密钥库'}
                onChange={(event) => setKey(event.target.value)}
              />
            </label>
          )}
          {chosen.keyUrl && (
            <a className="ai-keylink" href={chosen.keyUrl} target="_blank" rel="noreferrer">
              去 {chosen.name} 申请 API Key <Icon name="external" size={13} />
            </a>
          )}
          {error && (
            <div className="ai-error" role="alert">
              <Icon name="alert" size={16} />
              <span>{error}</span>
            </div>
          )}
          {done && <div className="ai-ok">{done}</div>}
          {admin ? (
            <button className="button primary full-width" disabled={busy}>
              {busy ? '正在验证…' : '验证并保存'}
            </button>
          ) : (
            <div className="ai-cli">
              <p>
                保存密钥需要管理员权限。在监控器电脑的 PowerShell 里运行这一条，按提示粘贴密钥（输入不显示）：
              </p>
              <CopyBox text={command} />
            </div>
          )}
        </form>
      )}
    </div>
  )
}
