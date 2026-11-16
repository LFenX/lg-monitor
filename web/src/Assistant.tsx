import { Fragment, useEffect, useLayoutEffect, useRef, useState } from 'react'
import ReactMarkdown from 'react-markdown'
import remarkGfm from 'remark-gfm'
import { APIError, readEvents } from './api'
import { assistantPrompt } from './AssistantSettings'
import { BohemiaMark } from './bohemia'
import { CopyBox } from './kit'
import { AgentCard } from './AgentCard'
import { useAgentSetup } from './parts'
import { useApp, useResource } from './state'
import type { AssistantStatus } from './types'
import { Icon } from './ui'
import './assistant.css'

type Step = {
  id: string
  label: string
  detail?: string
  state: 'running' | 'done' | 'failed' | 'declined'
  change?: boolean
}
type Change = { id: string; label: string; subject?: string; href?: string }
type Ask = {
  id: string
  label: string
  subject?: string
  consequence?: string
  state: 'waiting' | 'sending' | 'approved' | 'declined' | 'expired'
  problem?: string
}
type Turn = {
  question: string
  answer: string
  steps: Step[]
  changes: Change[]
  asks: Ask[]
  error?: string
  at: number
  ms?: number
}

const storageKey = 'lgmon-assistant-turns'
const starters = [
  { kind: '现状', tone: 'sky', text: '现在整体情况怎么样？有没有需要我处理的？' },
  { kind: '排查', tone: 'rose', text: '最近 7 天哪个服务出过问题？持续了多久，为什么？' },
  { kind: '动手', tone: 'mint', text: '把这个面板的卡片换成可用率环' },
  { kind: '分享', tone: 'yellow', text: '怎么让同事也能看到我的面板？' },
]

/* What the assistant still needs, said as two steps with one place to do them. */
function SetupCard({ status, close }: { status: AssistantStatus; close: () => void }) {
  const setup = useAgentSetup('all')
  return (
    <div className="ai-hello">
      <span className="ai-hello-mark">
        <BohemiaMark size={48} />
      </span>
      <h2>先给波西米亚接上模型</h2>
      <p>
        我是波西米亚在 lgmon 里的分身：帮你看懂服务状况，也能替你改改名字、换卡片样式、处理告警。回答问题用
        DeepSeek，查说明用阿里云百炼的向量模型和 rerank，两把密钥都要。
      </p>
      <ol className="ai-setup">
        <li data-done={status.deepseekKey || undefined}>
          <b>DeepSeek 的 API Key</b>
          <span>{status.deepseekKey ? '已保存' : '回答问题用，在 DeepSeek 开放平台创建'}</span>
        </li>
        <li data-done={status.dashscopeKey || undefined}>
          <b>百炼的 API Key</b>
          <span>
            {status.dashscopeKey ? '已保存' : '查说明用（向量模型和 rerank），在阿里云百炼控制台创建'}
          </span>
        </li>
      </ol>
      {/* The panel would cover the form it points to, so it steps aside. */}
      <a className="ai-resume" href="#/settings/assistant" onClick={close}>
        <Icon name="settings" size={16} />
        <span className="ai-resume-t">
          <b>去设置里填写</b>
        </span>
        <small>设置 → 波西米亚</small>
      </a>
      <AgentCard
        art={false}
        status={false}
        title="让 Agent 帮我配置"
        lead="它会告诉你去哪里创建这两把密钥，并替你保存和测试。"
        text={setup + assistantPrompt}
      />
      <p className="ai-or">也可以在这台电脑的 PowerShell 里运行（按提示粘贴密钥，输入不显示）：</p>
      <CopyBox text={status.setup} />
    </div>
  )
}

function restore(): Turn[] {
  try {
    const value = JSON.parse(sessionStorage.getItem(storageKey) || '[]')
    return Array.isArray(value)
      ? value
          .filter((item) => item && typeof item.question === 'string' && typeof item.answer === 'string')
          .map((item) => ({
            ...item,
            steps: Array.isArray(item.steps) ? item.steps : [],
            changes: Array.isArray(item.changes) ? item.changes : [],
            // A card still waiting when the page closed can no longer be answered.
            asks: (Array.isArray(item.asks) ? item.asks : []).map((ask: Ask) =>
              ask.state === 'waiting' || ask.state === 'sending' ? { ...ask, state: 'expired' } : ask,
            ),
            at: item.at || 0,
          }))
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
          const inside = typeof href === 'string' && href.startsWith('#/')
          const safe = inside || (typeof href === 'string' && /^https?:\/\//.test(href))
          return safe ? (
            <a
              href={href}
              className={inside ? 'ai-link' : undefined}
              target={inside ? undefined : '_blank'}
              rel="noreferrer"
            >
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

function seconds(ms: number) {
  return ms < 10_000 ? (ms / 1000).toFixed(1) + ' 秒' : Math.round(ms / 1000) + ' 秒'
}

/* While it works: one line per step, the current one shimmering. Afterwards: one quiet line that opens. */
function Steps({ turn, live }: { turn: Turn; live: boolean }) {
  const [open, setOpen] = useState(false)
  if (!turn.steps.length) {
    return live && !turn.answer ? (
      <div className="ai-trace">
        <div className="ai-tr now">
          <BohemiaMark size={14} think />
          <span className="ai-shimmer">
            {turn.asks.some((ask) => ask.state === 'waiting') ? '等你确认…' : '在想怎么回答…'}
          </span>
        </div>
      </div>
    ) : null
  }
  const line = (step: Step) => (
    <div className="ai-tr" key={step.id} data-state={step.state}>
      <Icon
        name={
          step.state === 'failed' || step.state === 'declined'
            ? 'alert'
            : step.state === 'running'
              ? 'search'
              : 'check'
        }
        size={13}
      />
      <span className={step.state === 'running' ? 'ai-shimmer' : undefined}>
        {step.state === 'declined' ? '没有执行 · ' : step.state === 'failed' ? '没成功 · ' : ''}
        {step.label}
        {step.detail && <q>{step.detail}</q>}
      </span>
    </div>
  )
  if (live) return <div className="ai-trace">{turn.steps.map(line)}</div>
  const looked = turn.steps.filter((step) => !step.change).length
  const changed = turn.steps.filter((step) => step.change && step.state === 'done').length
  // A step that failed is said on the folded line, not only inside it; a declined one has its own card.
  const missed = turn.steps.filter((step) => step.state === 'failed').length
  const summary = [
    looked ? `查了 ${looked} 项` : '',
    changed ? `改了 ${changed} 处` : '',
    missed ? `${missed} 步没成功` : '',
    turn.ms ? seconds(turn.ms) : '',
  ].filter(Boolean)
  return (
    <div className={'ai-fold' + (open ? ' open' : '')} data-warn={missed || undefined}>
      <button type="button" onClick={() => setOpen((value) => !value)} aria-expanded={open}>
        <Icon name="sparkles" size={13} />
        <span>
          {summary.map((part, index) => (
            <Fragment key={part}>
              {index > 0 && <i aria-hidden="true">·</i>}
              {part}
            </Fragment>
          ))}
        </span>
        <Icon name="chevron" size={13} />
      </button>
      {open && <div className="ai-trace">{turn.steps.map(line)}</div>}
    </div>
  )
}

/* A step that deletes, revokes or reaches outside: the person decides here. */
function AskCard({ ask, answer }: { ask: Ask; answer: (approve: boolean) => void }) {
  const settled = ask.state === 'approved' || ask.state === 'declined' || ask.state === 'expired'
  return (
    <div className="ai-ask" data-state={ask.state} role="group" aria-label={'需要你确认：' + ask.label}>
      <div className="ai-ask-h">
        <span className="ai-ask-ic">
          <Icon name={ask.label.startsWith('删除') ? 'trash' : 'shield'} size={15} />
        </span>
        <span>
          <b>{ask.label}</b>
          {ask.subject && <q>{ask.subject}</q>}
        </span>
      </div>
      {ask.consequence && <p>{ask.consequence}</p>}
      {ask.problem && <p className="ai-ask-problem">{ask.problem}</p>}
      {settled ? (
        <div className="ai-ask-done">
          {ask.state === 'approved'
            ? '已确认'
            : ask.state === 'declined'
              ? '已取消，没有执行'
              : '已过期，没有执行'}
        </div>
      ) : (
        <div className="ai-ask-b">
          <button
            type="button"
            className="ai-ask-no"
            disabled={ask.state === 'sending'}
            onClick={() => answer(false)}
          >
            取消
          </button>
          <button
            type="button"
            className="ai-ask-yes"
            disabled={ask.state === 'sending'}
            onClick={() => answer(true)}
          >
            确认{ask.label}
          </button>
        </div>
      )}
    </div>
  )
}

function Changes({ changes }: { changes: Change[] }) {
  if (!changes.length) return null
  return (
    <div className="ai-changes">
      {changes.map((change) => {
        const body = (
          <>
            <span className="ai-ch-ic">
              <Icon name="check" size={13} />
            </span>
            <span className="ai-ch-t">
              {change.label}
              {change.subject && <b>{change.subject}</b>}
            </span>
            {change.href && <Icon name="arrow" size={14} />}
          </>
        )
        return change.href ? (
          <a key={change.id} className="ai-change" href={change.href}>
            {body}
          </a>
        ) : (
          <div key={change.id} className="ai-change">
            {body}
          </div>
        )
      })}
    </div>
  )
}

export function Assistant({ close }: { close: () => void }) {
  const { api, version } = useApp()
  // Re-read on every snapshot refresh, so a key saved from the terminal shows up without reloading.
  const status = useResource(
    (signal) => api.request<AssistantStatus>('/assistant', { signal }),
    [api, version],
  )
  const [turns, setTurns] = useState<Turn[]>(restore),
    [input, setInput] = useState(''),
    [busy, setBusy] = useState(false),
    [scrolled, setScrolled] = useState(false),
    [copied, setCopied] = useState(-1)
  const pending = useRef<AbortController | null>(null),
    list = useRef<HTMLDivElement>(null),
    flow = useRef<HTMLDivElement>(null),
    stick = useRef(true),
    field = useRef<HTMLTextAreaElement>(null)
  useEffect(() => () => pending.current?.abort(), [])
  useEffect(() => {
    try {
      sessionStorage.setItem(storageKey, JSON.stringify(turns.filter((turn) => turn.answer || turn.error)))
    } catch {
      /* The conversation simply starts over on the next visit. */
    }
  }, [turns])
  // Follows the answer down as it grows, unless the reader has scrolled up to read something.
  useLayoutEffect(() => {
    const element = list.current,
      inner = flow.current
    if (!element || !inner) return
    const follow = () => {
      if (stick.current) element.scrollTop = element.scrollHeight
    }
    follow()
    if (typeof ResizeObserver === 'undefined') return
    const observer = new ResizeObserver(follow)
    observer.observe(inner)
    return () => observer.disconnect()
  }, [])
  useEffect(() => {
    stick.current = true
    if (list.current) list.current.scrollTop = list.current.scrollHeight
  }, [turns.length])
  useLayoutEffect(() => {
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
  const setAsk = (id: string, change: Partial<Ask>) =>
    setTurns((current) =>
      current.map((turn) =>
        turn.asks.some((ask) => ask.id === id)
          ? { ...turn, asks: turn.asks.map((ask) => (ask.id === id ? { ...ask, ...change } : ask)) }
          : turn,
      ),
    )
  const answer = async (id: string, approve: boolean) => {
    setAsk(id, { state: 'sending', problem: undefined })
    try {
      await api.request('/assistant/confirm', { method: 'POST', body: JSON.stringify({ id, approve }) })
      setAsk(id, { state: approve ? 'approved' : 'declined' })
    } catch (err) {
      setAsk(id, { state: 'waiting', problem: err instanceof Error ? err.message : '没有发出去，请再点一次' })
    }
  }
  const send = async (text: string, earlier: Turn[] = turns) => {
    const question = text.trim()
    if (!question || busy) return
    const history = earlier
      .filter((turn) => turn.answer && !turn.error)
      .slice(-8)
      .flatMap((turn) => [
        { role: 'user', text: turn.question },
        { role: 'assistant', text: turn.answer },
      ])
    const started = Date.now()
    setTurns([...earlier, { question, answer: '', steps: [], changes: [], asks: [], at: started }])
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
        body: JSON.stringify({
          messages: [...history, { role: 'user', text: question }],
          page: { path: location.hash || '#/', title: document.title },
        }),
      })
      if (!response.ok || !response.body) {
        let message = '波西米亚暂时不可用（HTTP ' + response.status + '）'
        try {
          const value = await response.json()
          if (typeof value?.error?.message === 'string') message = value.error.message
        } catch {
          /* The status line is enough. */
        }
        throw new APIError(message, response.status)
      }
      await readEvents(response.body, controller.signal, (event, raw) => {
        let data: Record<string, unknown> = {}
        try {
          data = JSON.parse(raw)
        } catch {
          return
        }
        const str = (key: string) => (typeof data[key] === 'string' ? (data[key] as string) : undefined)
        if (event === 'text' && str('delta') !== undefined)
          update((last) => ({ ...last, answer: last.answer + str('delta') }))
        if (event === 'tool' && str('id'))
          update((last) => {
            const steps = [...last.steps]
            const step: Step = {
              id: str('id')!,
              label: str('label') || str('name') || '',
              detail: str('detail'),
              state: str('state') as Step['state'],
              change: data.change === true,
            }
            const index = steps.findIndex((item) => item.id === step.id)
            if (index >= 0) steps[index] = step
            else steps.push(step)
            return { ...last, steps }
          })
        if (event === 'confirm' && str('id'))
          update((last) => ({
            ...last,
            asks: [
              ...last.asks,
              {
                id: str('id')!,
                label: str('label') || '',
                subject: str('subject'),
                consequence: str('consequence'),
                state: 'waiting',
              },
            ],
          }))
        if (event === 'confirmed' && str('id'))
          update((last) => ({
            ...last,
            asks: last.asks.map((ask) =>
              ask.id === str('id') ? { ...ask, state: data.approved ? 'approved' : 'declined' } : ask,
            ),
          }))
        if (event === 'change' && str('id'))
          update((last) => ({
            ...last,
            changes: [
              ...last.changes,
              { id: str('id')!, label: str('label') || '', subject: str('subject'), href: str('href') },
            ],
          }))
        if (event === 'error') update((last) => ({ ...last, error: str('message') || '波西米亚出错了' }))
      })
    } catch (err) {
      if (!controller.signal.aborted)
        update((last) => ({ ...last, error: err instanceof Error ? err.message : '连接波西米亚失败' }))
    } finally {
      if (pending.current === controller) pending.current = null
      setBusy(false)
      update((last) => ({
        ...last,
        ms: Date.now() - started,
        asks: last.asks.map((ask) =>
          ask.state === 'waiting' || ask.state === 'sending' ? { ...ask, state: 'expired' } : ask,
        ),
        error:
          last.error ||
          (last.answer || last.changes.length
            ? undefined
            : controller.signal.aborted
              ? '已停止'
              : '没有收到回答'),
      }))
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
      ? 'lgmon 分身 · ' + status.value.model
      : 'lgmon 分身 · 还没有接上模型'
    : '正在读取…'
  const copy = (index: number, text: string) => {
    void navigator.clipboard?.writeText(text).then(() => {
      setCopied(index)
      setTimeout(() => setCopied(-1), 1400)
    })
  }
  return (
    <aside className="ai-panel" aria-label="波西米亚">
      <div className="ai-box" data-scrolled={scrolled || undefined}>
        <div className={'ai-head' + (busy ? ' busy' : '')}>
          <BohemiaMark size={30} think={busy} />
          <div className="ai-head-t">
            <b>波西米亚</b>
            <span className="ai-head-sub">{subtitle}</span>
          </div>
          <div className="ai-head-r">
            {!!turns.length && (
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
            <a
              className="icon-button"
              title="设置"
              aria-label="波西米亚的设置"
              href="#/settings/assistant"
              onClick={close}
            >
              <Icon name="settings" size={17} />
            </a>
            <button className="icon-button" title="关闭（Esc）" aria-label="关闭波西米亚" onClick={close}>
              <Icon name="close" size={17} />
            </button>
          </div>
          <i className="ai-head-line" aria-hidden="true" />
        </div>
        <div
          className="ai-scroll"
          ref={list}
          onScroll={(event) => {
            const element = event.currentTarget
            stick.current = element.scrollHeight - element.scrollTop - element.clientHeight < 48
            setScrolled(element.scrollTop > 4)
          }}
        >
          <div className="ai-flow" ref={flow}>
            {status.value && !configured ? (
              <SetupCard status={status.value} close={close} />
            ) : !turns.length ? (
              <div className="ai-hello">
                <span className="ai-hello-mark">
                  <BohemiaMark size={56} />
                </span>
                <h2>嗨，我是波西米亚</h2>
                <p>
                  问我服务现在怎么样、哪里出过问题；也可以让我改名字、换卡片样式、整理面板、处理告警。删除和对外发送之前，我会先问你。
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
                <p className="ai-note">添加新服务需要查看这台电脑，请在面板上点“添加服务”交给 Agent。</p>
              </div>
            ) : (
              turns.map((turn, index) => {
                const last = index === turns.length - 1
                const live = busy && last
                const before = turns[index - 1]
                const pause = !before || turn.at - before.at > 30 * 60 * 1000
                return (
                  <section className="ai-turn" key={index} aria-busy={live}>
                    {pause && clock(turn.at) && (
                      <div className="ai-when">
                        <span>{clock(turn.at)}</span>
                      </div>
                    )}
                    <div className="ai-q">
                      <span>{turn.question}</span>
                    </div>
                    <div className="ai-a">
                      <Steps turn={turn} live={live} />
                      {turn.asks.map((ask) => (
                        <AskCard key={ask.id} ask={ask} answer={(approve) => void answer(ask.id, approve)} />
                      ))}
                      {turn.answer && (
                        <div className="ai-text">
                          <Reply text={turn.answer} />
                          {live && <i className="ai-caret" />}
                        </div>
                      )}
                      <Changes changes={turn.changes} />
                      {turn.error && (
                        <div className="ai-error" role="alert">
                          <Icon name="alert" size={16} />
                          <span>{turn.error}</span>
                          {last && !busy && turn.error !== '已停止' && (
                            <button onClick={() => void send(turn.question, turns.slice(0, -1))}>重试</button>
                          )}
                        </div>
                      )}
                      {!live && turn.answer && !turn.error && (
                        <div className="ai-acts">
                          <button
                            type="button"
                            className="icon-button"
                            title={copied === index ? '已复制' : '复制'}
                            aria-label="复制回答"
                            onClick={() => copy(index, turn.answer)}
                          >
                            <Icon name={copied === index ? 'check' : 'copy'} size={15} />
                          </button>
                          {last && !busy && (
                            <button
                              type="button"
                              className="icon-button"
                              title="重新回答"
                              aria-label="重新回答"
                              onClick={() => void send(turn.question, turns.slice(0, -1))}
                            >
                              <Icon name="refresh" size={15} />
                            </button>
                          )}
                        </div>
                      )}
                    </div>
                  </section>
                )
              })
            )}
          </div>
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
              placeholder={
                configured
                  ? turns.length
                    ? '接着问…'
                    : '问问服务的状况，或者让我改点什么…'
                  : '先在设置里接上模型'
              }
              aria-label="问波西米亚"
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
                title="发送（Enter，Shift+Enter 换行）"
                disabled={!configured || !input.trim()}
              >
                <Icon name="send" size={16} />
              </button>
            )}
          </form>
        </div>
      </div>
    </aside>
  )
}
