/* Copyright (c) 2026 LFen (https://lfen.ink) — lg-monitor */

import { Fragment, useEffect, useLayoutEffect, useMemo, useRef, useState } from 'react'
import { edges, useFrame } from './assistant-window'
import { suggestions } from './assistant-suggest'
import { dropChat, keepChat, newChatID, readChats } from './assistant-history'
import type { SavedChat } from './assistant-history'
import ReactMarkdown from 'react-markdown'
import type { Components } from 'react-markdown'
import remarkGfm from 'remark-gfm'
import { APIError, readEvents } from './api'
import { assistantPrompt } from './AssistantSettings'
import { BohemiaMark } from './bohemia'
import { useI18n } from './i18n'
import { Avatar } from './avatar'
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
const chatKey = 'lgmon-assistant-chat'

/* What the assistant still needs, said as two steps with one place to do them. */
function SetupCard({ status, close }: { status: AssistantStatus; close: () => void }) {
  const { t } = useI18n()
  const setup = useAgentSetup('all')
  return (
    <div className="ai-hello">
      <span className="ai-hello-mark">
        <BohemiaMark size={48} />
      </span>
      <h2>{t('aiSetupTitle')}</h2>
      <p>{t('aiSetupBody')}</p>
      <ol className="ai-setup">
        <li data-done={status.deepseekKey || undefined}>
          <b>{t('aiDeepseekKey')}</b>
          <span>{status.deepseekKey ? t('aiKeySaved') : t('aiDeepseekHint')}</span>
        </li>
        <li data-done={status.dashscopeKey || undefined}>
          <b>{t('aiDashscopeKey')}</b>
          <span>{status.dashscopeKey ? t('aiKeySaved') : t('aiDashscopeHint')}</span>
        </li>
      </ol>
      {/* The panel would cover the form it points to, so it steps aside. */}
      <a className="ai-resume" href="#/settings/assistant" onClick={close}>
        <Icon name="settings" size={16} />
        <span className="ai-resume-t">
          <b>{t('aiGoSettings')}</b>
        </span>
        <small>{t('aiSettingsPath')}</small>
      </a>
      <AgentCard
        quiet
        art={false}
        status={false}
        title={t('aiSetupAgentTitle')}
        lead={t('aiSetupAgentLead')}
        text={setup + assistantPrompt}
      />
      <p className="ai-or">{t('aiSetupPsOr')}</p>
      <CopyBox text={status.setup} />
    </div>
  )
}

/* Turns read back from storage, with whatever was still waiting at the time closed off. */
function restored(value: unknown): Turn[] {
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
        .slice(-30)
    : []
}

function restore(): Turn[] {
  try {
    return restored(JSON.parse(sessionStorage.getItem(storageKey) || '[]'))
  } catch {
    return []
  }
}

function currentChat() {
  try {
    const id = sessionStorage.getItem(chatKey)
    if (id) return id
  } catch {
    /* A fresh id below. */
  }
  return newChatID()
}

function when(at: number, lang: 'zh' | 'en', t: ReturnType<typeof useI18n>['t']) {
  const locale = lang === 'en' ? 'en-US' : 'zh-CN'
  const date = new Date(at),
    now = new Date()
  const time = date.toLocaleTimeString(locale, { hour: '2-digit', minute: '2-digit' })
  if (date.toDateString() === now.toDateString()) return t('aiToday') + ' ' + time
  const yesterday = new Date(now.getFullYear(), now.getMonth(), now.getDate() - 1)
  if (date.toDateString() === yesterday.toDateString()) return t('aiYesterday') + ' ' + time
  return date.toLocaleDateString(locale, { month: 'numeric', day: 'numeric' }) + ' ' + time
}

/* The last conversations, newest first; one click picks one up where it stopped. */
function History({
  chats,
  current,
  pick,
  drop,
  close,
}: {
  chats: SavedChat<Turn>[]
  current: string
  pick: (chat: SavedChat<Turn>) => void
  drop: (id: string) => void
  close: () => void
}) {
  const { lang, t } = useI18n()
  return (
    <div className="ai-history" role="dialog" aria-label={t('aiHistoryTitle')}>
      <header>
        <b>{t('aiHistoryTitle')}</b>
        <small>
          {chats.length > 0 ? t('aiHistoryNote', { count: chats.length }) : t('aiHistoryNoteEmpty')}
        </small>
        <button className="icon-button" aria-label={t('aiHistoryClose')} onClick={close}>
          <Icon name="close" size={15} />
        </button>
      </header>
      {chats.length === 0 ? (
        <p className="ai-history-empty">{t('aiHistoryEmpty')}</p>
      ) : (
        <ul>
          {chats.map((chat) => (
            <li key={chat.id} aria-current={chat.id === current || undefined}>
              <button className="ai-history-pick" onClick={() => pick(chat)}>
                <span className="ai-history-t">{chat.title || t('aiHistoryUntitled')}</span>
                <small>
                  {when(chat.at, lang, t)} · {t('aiHistoryQuestions', { count: chat.turns.length })}
                  {chat.id === current ? ' · ' + t('aiHistoryCurrent') : ''}
                </small>
              </button>
              <button
                className="icon-button"
                aria-label={chat.title || t('aiHistoryUntitled')}
                onClick={() => drop(chat.id)}
              >
                <Icon name="trash" size={14} />
              </button>
            </li>
          ))}
        </ul>
      )}
    </div>
  )
}

/*
 * Defined once: components made anew on each render are new element types to React, which then rebuilds every
 * table and link of the answer whenever the panel renders, while text streams in and after.
 */
const markdown: Components = {
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
}
const plugins = [remarkGfm]

function Reply({ text }: { text: string }) {
  return (
    <ReactMarkdown remarkPlugins={plugins} skipHtml components={markdown}>
      {text}
    </ReactMarkdown>
  )
}

function clock(at: number) {
  return at
    ? new Date(at).toLocaleTimeString('zh-CN', { hour: '2-digit', minute: '2-digit', hour12: false })
    : ''
}

function seconds(ms: number, t: ReturnType<typeof useI18n>['t']) {
  const n = ms < 10_000 ? (ms / 1000).toFixed(1) : String(Math.round(ms / 1000))
  return t('aiSec', { n })
}

/* While it works: one line per step, the current one shimmering. Afterwards: one quiet line that opens. */
function Steps({ turn, live }: { turn: Turn; live: boolean }) {
  const { t } = useI18n()
  const [open, setOpen] = useState(false)
  if (!turn.steps.length) {
    return live && !turn.answer ? (
      <div className="ai-trace">
        <div className="ai-tr now">
          <BohemiaMark size={14} think />
          <span className="ai-shimmer">
            {turn.asks.some((ask) => ask.state === 'waiting') ? t('aiWaitingConfirm') : t('aiThinking')}
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
        {step.state === 'declined'
          ? t('aiDeclinedPrefix')
          : step.state === 'failed'
            ? t('aiFailedPrefix')
            : ''}
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
    looked ? t('aiCheckedCount', { count: looked }) : '',
    changed ? t('aiChangedCount', { count: changed }) : '',
    missed ? t('aiFailedCount', { count: missed }) : '',
    turn.ms ? seconds(turn.ms, t) : '',
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
  const { t } = useI18n()
  const settled = ask.state === 'approved' || ask.state === 'declined' || ask.state === 'expired'
  return (
    <div
      className="ai-ask"
      data-state={ask.state}
      role="group"
      aria-label={t('aiConfirmNeeded', { label: ask.label })}
    >
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
            ? t('aiConfirmed')
            : ask.state === 'declined'
              ? t('aiDeclined')
              : t('aiExpired')}
        </div>
      ) : (
        <div className="ai-ask-b">
          <button
            type="button"
            className="ai-ask-no"
            disabled={ask.state === 'sending'}
            onClick={() => answer(false)}
          >
            {t('cancel')}
          </button>
          <button
            type="button"
            className="ai-ask-yes"
            disabled={ask.state === 'sending'}
            onClick={() => answer(true)}
          >
            {t('aiConfirmAction', { label: ask.label })}
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
  const { api, version, monitors = [], boardList = [], lan, remoteBoards = [], guest } = useApp()
  const { lang, t } = useI18n()
  const frame = useFrame()
  // Starters are chosen when the panel opens and on "换一批"; they do not reshuffle while someone reads them.
  const [round, setRound] = useState(0)
  const starters = useMemo(
    () => suggestions({ monitors, boardList, lan, remoteBoards, page: location.hash || '#/', lang, guest }),
    // eslint-disable-next-line react-hooks/exhaustive-deps
    [round, monitors.length > 0, lang, guest],
  )
  // Re-read on every snapshot refresh, so a key saved from the terminal shows up without reloading.
  const status = useResource(
    (signal) => api.request<AssistantStatus>('/assistant', { signal }),
    [api, version],
  )
  const [turns, setTurns] = useState<Turn[]>(restore),
    [chat, setChat] = useState(currentChat),
    [chats, setChats] = useState(() => readChats<Turn>()),
    [history, setHistory] = useState(false),
    [input, setInput] = useState(''),
    [busy, setBusy] = useState(false),
    [scrolled, setScrolled] = useState(false),
    [copied, setCopied] = useState(-1)
  // Enter while an answer is coming stops it and asks the new question at once; Ctrl+Enter queues it for after.
  const [queue, setQueue] = useState<string[]>([])
  const interject = useRef(''),
    latest = useRef<Turn[]>([])
  useEffect(() => {
    latest.current = turns
  })
  const pending = useRef<AbortController | null>(null),
    list = useRef<HTMLDivElement>(null),
    flow = useRef<HTMLDivElement>(null),
    stick = useRef(true),
    field = useRef<HTMLTextAreaElement>(null)
  useEffect(() => () => pending.current?.abort(), [])
  useEffect(() => {
    try {
      sessionStorage.setItem(storageKey, JSON.stringify(turns.filter((turn) => turn.answer || turn.error)))
      sessionStorage.setItem(chatKey, chat)
    } catch {
      /* The conversation simply starts over on the next visit. */
    }
  }, [turns, chat])
  // The finished conversation joins the last five once an answer is complete, not on every streamed word.
  useEffect(() => {
    if (busy) return
    const done = turns.filter((turn) => turn.answer || turn.error)
    if (done.length) setChats(keepChat(chat, done))
  }, [turns, chat, busy])
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
  const send = async (text: string, earlier: Turn[] = latest.current) => {
    const question = text.trim()
    if (!question || pending.current) return
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
        // 波西米亚 opened another page for the person; only lgmon's own pages arrive here.
        if (event === 'navigate' && /^#\/[\w./:-]*$/.test(str('hash') || ''))
          window.location.hash = str('hash')!
      })
    } catch (err) {
      if (!controller.signal.aborted)
        update((last) => ({ ...last, error: err instanceof Error ? err.message : t('aiConnectFailed') }))
    } finally {
      if (pending.current === controller) pending.current = null
      setBusy(false)
      // A question that cut in goes first, before anything queued with Ctrl+Enter.
      const next = interject.current
      interject.current = ''
      if (next) setQueue((value) => [next, ...value])
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
              ? t('aiStopped')
              : t('aiNoReply')),
      }))
    }
  }
  // A queued question goes as soon as the answer before it is finished.
  useEffect(() => {
    if (busy || !queue.length || !configured) return
    const [next, ...rest] = queue
    setQueue(rest)
    void send(next)
    // send reads the latest state when it runs, so it is deliberately not a dependency.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [busy, queue, configured])
  const submit = (later: boolean) => {
    const text = input.trim()
    if (!text) return
    if (!busy) return void send(text)
    setInput('')
    if (later) setQueue((value) => [...value, text])
    else {
      interject.current = text
      pending.current?.abort()
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
      ? t('aiSubModel', { model: status.value.model }) + (status.value.sharedBy ? t('aiSubShared') : '')
      : t('aiSubNoModel')
    : t('loading')
  const copy = (index: number, text: string) => {
    void navigator.clipboard?.writeText(text).then(() => {
      setCopied(index)
      setTimeout(() => setCopied(-1), 1400)
    })
  }
  const placed = frame.frame
  return (
    <aside
      className={'ai-panel' + (placed ? ' placed' : '') + (frame.moving ? ' moving' : '')}
      aria-label={t('bohemia')}
      style={
        placed
          ? {
              left: placed.x,
              top: placed.y,
              width: placed.w,
              height: placed.h,
              right: 'auto',
              bottom: 'auto',
            }
          : undefined
      }
    >
      <div className="ai-box" data-scrolled={scrolled || undefined}>
        <div
          className={'ai-head' + (busy ? ' busy' : '') + (frame.desk ? ' handle' : '')}
          onPointerDown={(event) => {
            if (!(event.target as HTMLElement).closest('button, a')) frame.begin(event, 'move')
          }}
          onDoubleClick={(event) => {
            if (!(event.target as HTMLElement).closest('button, a')) frame.toggle()
          }}
        >
          <BohemiaMark size={30} think={busy} />
          <div className="ai-head-t">
            <b>{t('bohemia')}</b>
            <span className="ai-head-sub">{guest ? t('bohemiaReadOnly') : subtitle}</span>
          </div>
          <div className="ai-head-r">
            <button
              className="icon-button"
              title={t('aiHistoryTitle')}
              aria-label={t('aiHistoryTitle')}
              aria-expanded={history}
              onClick={() => setHistory((value) => !value)}
            >
              <Icon name="clock" size={16} />
            </button>
            {!!turns.length && (
              <button
                className="icon-button"
                title={t('aiNewChat')}
                aria-label={t('aiNewChat')}
                onClick={() => {
                  pending.current?.abort()
                  setTurns([])
                  setChat(newChatID())
                  setHistory(false)
                }}
              >
                <Icon name="plus" size={17} />
              </button>
            )}
            {frame.desk && (
              <button
                className="icon-button"
                title={frame.expanded ? t('aiRestoreSize') : t('aiMaximize')}
                aria-label={frame.expanded ? t('aiRestoreSize') : t('aiMaximize')}
                onClick={frame.toggle}
              >
                <Icon name={frame.expanded ? 'minimize' : 'maximize'} size={16} />
              </button>
            )}
            {!guest && (
              <a
                className="icon-button"
                title={t('settings')}
                aria-label={t('aiSettingsAria')}
                href="#/settings/assistant"
                onClick={close}
              >
                <Icon name="settings" size={17} />
              </a>
            )}
            <button
              className="icon-button"
              title={t('aiCloseEsc')}
              aria-label={t('aiCloseAria')}
              onClick={close}
            >
              <Icon name="close" size={17} />
            </button>
          </div>
          <i className="ai-head-line" aria-hidden="true" />
        </div>
        {history && (
          <History
            chats={chats}
            current={chat}
            close={() => setHistory(false)}
            pick={(saved) => {
              pending.current?.abort()
              setQueue([])
              setTurns(restored(saved.turns))
              setChat(saved.id)
              setHistory(false)
            }}
            drop={(id) => {
              setChats(dropChat<Turn>(id))
              if (id === chat) {
                pending.current?.abort()
                setTurns([])
                setChat(newChatID())
              }
            }}
          />
        )}
        {status.value?.sharedBy && (
          <p className="ai-shared" title={t('aiSharedModelTip')}>
            <Avatar value={status.value.sharedBy.avatar} seed={status.value.sharedBy.name} size={20} />
            <span>
              {t('aiSharedModelLine', {
                name: status.value.sharedBy.name,
                model: status.value.sharedBy.model,
              })}
            </span>
          </p>
        )}
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
                <h2>{t('aiHelloTitle')}</h2>
                <p>{guest ? t('aiHelloGuestBody') : t('aiHelloBody')}</p>
                <div className="ai-starters" aria-label={t('aiStartersAria')}>
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
                <button className="ai-reroll" onClick={() => setRound((value) => value + 1)}>
                  <Icon name="refresh" size={13} />
                  {t('aiReroll')}
                </button>
                <p className="ai-note">{guest ? t('aiHelloGuestNote') : t('aiHelloNote')}</p>
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
                          {last && !busy && turn.error !== t('aiStopped') && (
                            <button onClick={() => void send(turn.question, turns.slice(0, -1))}>
                              {t('aiRetry')}
                            </button>
                          )}
                        </div>
                      )}
                      {!live && turn.answer && !turn.error && (
                        <div className="ai-acts">
                          <button
                            type="button"
                            className="icon-button"
                            title={copied === index ? t('copied') : t('copy')}
                            aria-label={t('aiCopyAnswer')}
                            onClick={() => copy(index, turn.answer)}
                          >
                            <Icon name={copied === index ? 'check' : 'copy'} size={15} />
                          </button>
                          {last && !busy && (
                            <button
                              type="button"
                              className="icon-button"
                              title={t('aiRegenerate')}
                              aria-label={t('aiRegenerate')}
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
          {queue.length > 0 && (
            <ul className="ai-queue" aria-label={t('aiQueueAria')}>
              {queue.map((item, index) => (
                <li key={index}>
                  <span>{item}</span>
                  <button
                    aria-label={t('aiRemoveQueue')}
                    onClick={() => setQueue((value) => value.filter((_, at) => at !== index))}
                  >
                    <Icon name="close" size={12} />
                  </button>
                </li>
              ))}
            </ul>
          )}
          <form
            className="ai-compose"
            onSubmit={(event) => {
              event.preventDefault()
              submit(false)
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
                  ? busy
                    ? t('aiPlaceholderBusy')
                    : turns.length
                      ? t('aiPlaceholderFollow')
                      : guest
                        ? t('aiPlaceholderGuest')
                        : t('aiPlaceholderFirst')
                  : t('aiPlaceholderSetup')
              }
              aria-label={t('aiInputAria')}
              onChange={(event) => setInput(event.target.value)}
              onKeyDown={(event) => {
                if (event.key === 'Enter' && !event.shiftKey && !event.nativeEvent.isComposing) {
                  event.preventDefault()
                  submit(event.ctrlKey || event.metaKey)
                }
              }}
            />
            {busy && !input.trim() ? (
              <button
                type="button"
                className="ai-send stop"
                aria-label={t('aiStop')}
                title={t('aiStop')}
                onClick={() => pending.current?.abort()}
              >
                <Icon name="stop" size={14} />
              </button>
            ) : (
              <button
                className="ai-send"
                aria-label={t('aiSend')}
                title={busy ? t('aiSendTitleBusy') : t('aiSendTitleIdle')}
                disabled={!configured || !input.trim()}
              >
                <Icon name="send" size={16} />
              </button>
            )}
          </form>
        </div>
      </div>
      {frame.desk &&
        edges.map((edge) => (
          <span
            key={edge}
            className={'ai-rz ai-rz-' + edge}
            aria-hidden="true"
            onPointerDown={(event) => frame.begin(event, edge)}
            onDoubleClick={frame.reset}
          />
        ))}
    </aside>
  )
}
