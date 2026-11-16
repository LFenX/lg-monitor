import { useEffect, useRef, useState } from 'react'
import ReactMarkdown from 'react-markdown'
import remarkGfm from 'remark-gfm'
import { APIError, readEvents } from './api'
import { assistantPrompt } from './AssistantSettings'
import { BohemiaMark } from './bohemia'
import { CopyBox } from './kit'
import { CopyButton, PromptBox, useAgentSetup } from './parts'
import { useApp, useResource } from './state'
import type { AssistantStatus } from './types'
import { Icon } from './ui'
import './assistant.css'

type Step = { id: string; label: string; detail?: string; state: 'running' | 'done' | 'failed' }
type Turn = { question: string; answer: string; steps: Step[]; error?: string; at: number }

const storageKey = 'lgmon-assistant-turns'
const starters = [
  { kind: '现状', tone: 'sky', text: '现在整体情况怎么样？有没有需要我处理的？' },
  { kind: '排查', tone: 'rose', text: '最近 7 天哪个服务出过问题？持续了多久，为什么？' },
  { kind: '怎么用', tone: 'mint', text: '怎么给网站加一个 Cloudflare 隧道子服务？' },
  { kind: '怎么用', tone: 'yellow', text: '卡片样式有哪些？分别适合什么服务？' },
]

/* What the assistant still needs, said as two steps with one place to do them. */
function SetupCard({ status, close }: { status: AssistantStatus; close: () => void }) {
  const setup = useAgentSetup('all')
  const prompt = assistantPrompt
  return (
    <div className="ai-hello">
      <span className="ai-hello-mark">
        <BohemiaMark size={48} />
      </span>
      <h2>先给波西米亚接上模型</h2>
      <p>
        我是波西米亚在 lgmon 里的分身：帮你看懂服务状况，也回答 lgmon 怎么用。回答问题用
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
      <details className="ai-agent-setup">
        <summary>让 Agent 帮我配置</summary>
        <PromptBox text={prompt} preface={setup} />
        <CopyButton text={setup + prompt} primary label="复制这段话" />
      </details>
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
  const { api, version } = useApp()
  // Re-read on every snapshot refresh, so a key saved from the terminal shows up without reloading.
  const status = useResource(
    (signal) => api.request<AssistantStatus>('/assistant', { signal }),
    [api, version],
  )
  const [turns, setTurns] = useState<Turn[]>(restore),
    [input, setInput] = useState(''),
    [busy, setBusy] = useState(false),
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
        if (event === 'error') update((last) => ({ ...last, error: data.message || '波西米亚出错了' }))
      })
    } catch (err) {
      if (!controller.signal.aborted)
        update((last) => ({ ...last, error: err instanceof Error ? err.message : '连接波西米亚失败' }))
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
      ? 'lgmon 分身 · ' + status.value.model + ' · 只读'
      : 'lgmon 分身 · 还没有接上模型'
    : '正在读取…'
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
        <>
          <div
            className="ai-scroll"
            ref={list}
            onScroll={(event) => setScrolled(event.currentTarget.scrollTop > 4)}
          >
            {status.value && !configured ? (
              <SetupCard status={status.value} close={close} />
            ) : !turns.length ? (
              <div className="ai-hello">
                <span className="ai-hello-mark">
                  <BohemiaMark size={48} />
                </span>
                <h2>你好，我是波西米亚</h2>
                <p>
                  我是波西米亚在 lgmon 里的分身。问我服务现在怎么样、哪里出过问题，或者 lgmon
                  怎么用：我会先查监控数据和说明，再用几句话告诉你。我只能看，不会改动任何东西。
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
                                step.state === 'failed' ? 'alert' : step.state === 'done' ? 'check' : 'search'
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
                placeholder={configured ? '问问服务的状况，或者 lgmon 怎么用…' : '先在设置里接上模型'}
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
      </div>
    </aside>
  )
}
