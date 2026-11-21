/* Copyright (c) 2026 LFen (https://lfen.ink) — lg-monitor */

import { useEffect, useRef, useState } from 'react'
import type { PromptChoice } from './prompts'
import { AgentIcon, AgentSignals, agentName, useAgents } from './agents'
import { BohemiaMark } from './bohemia'
import { Glyph } from './glyphs'
import { useI18n } from './i18n'
import { ago, useCopy } from './kit'
import { useApp } from './state'
import './agent-card.css'

/*
 * The one thing a person does with lgmon: copy a message and send it to their agent. The message stays folded
 * away (the agent reads it, the person doesn't have to), and a live line says whether the agent has arrived.
 */

/* lgmon's tile plugged into an agent's chat bubble. */
export function AgentArt() {
  return (
    <svg className="agent-art" viewBox="0 0 168 104" aria-hidden="true" focusable="false">
      <ellipse cx="84" cy="96" rx="62" ry="5" className="agent-art-shadow" />
      <path className="agent-art-cord" d="M56 62 C 70 92, 96 92, 104 64" />
      <g className="agent-art-tile">
        <rect x="14" y="20" width="52" height="52" rx="16" className="agent-art-brand" />
        {[0.45, 0.65, 0.85, 1].map((opacity, index) => (
          <rect key={index} x={25 + index * 8} y="34" width="4.5" height="24" rx="2.25" opacity={opacity} />
        ))}
      </g>
      <g className="agent-art-bubble">
        <path d="M104 18 h34 a16 16 0 0 1 16 16 v20 a16 16 0 0 1 -16 16 h-20 l-10 9 v-9 h-4 a16 16 0 0 1 -16 -16 v-20 a16 16 0 0 1 16 -16 z" />
        {[0, 1, 2].map((index) => (
          <circle key={index} cx={113 + index * 11} cy="44" r="3.6" />
        ))}
      </g>
      <circle cx="104" cy="64" r="4.5" className="agent-art-plug" />
      <circle cx="56" cy="62" r="4.5" className="agent-art-plug" />
    </svg>
  )
}

/*
 * Who is connected. Each connected agent is a chip with its logo and a live signal; with none, the line says
 * who was here last, or that lgmon is still waiting for the first one.
 */
export function AgentStatus() {
  const { guide, now } = useApp()
  const { t } = useI18n()
  const agents = useAgents()
  const connected = agents?.filter((agent) => agent.connected) || []
  if (connected.length)
    return (
      <div className="agent-status" data-seen="" data-live="" role="status">
        <AgentSignals list={connected} />
        <span className="agent-status-note">
          {connected.length === 1
            ? t('agentConnected')
            : t('agentConnectedMany', { count: connected.length })}
        </span>
      </div>
    )
  const last = agents?.[0]
  const seen = last?.lastSeenAt || guide?.state?.agentSeenAt || 0
  const name = last ? agentName(last) : guide?.state?.agentName
  return (
    <p className="agent-status" data-seen={seen ? '' : undefined} role="status">
      {last ? <AgentIcon kind={last.kind} size={18} /> : <i aria-hidden="true" />}
      {seen
        ? name
          ? t('agentDisconnectedNamed', { name, ago: ago(seen, now()) })
          : t('agentDisconnected', { ago: ago(seen, now()) })
        : t('agentWaiting')}
    </p>
  )
}

/* Logos of agents known to take the message as it is; the list is a hint, any agent that runs commands works. */
const works = ['claude-code', 'codex', 'cursor', 'gemini', 'trae', 'qwen']

export function AgentCard({
  title,
  lead,
  text,
  status = true,
  art = true,
  quiet = false,
  persona = false,
  choices,
}: {
  title: string
  lead: string
  text: string
  status?: boolean
  art?: boolean
  // quiet steps back when something else on the page is the main action: left-aligned, a small mark, no art.
  quiet?: boolean
  // persona puts 波西米亚, lgmon's own assistant, in the quiet card's mark; the agent logos stay on the right.
  persona?: boolean
  /* Messages for other purposes, offered in a sheet that slides up, so the person picks what fits this visit. */
  choices?: PromptChoice[]
}) {
  const { t } = useI18n()
  const [shown, setShown] = useState(false),
    [sheet, setSheet] = useState(false)
  const { copied, copy } = useCopy()
  return (
    <section
      className="agent-card"
      data-quiet={quiet || undefined}
      data-copied={copied || undefined}
      aria-label={title}
    >
      {art && !quiet && <AgentArt />}
      <div className="agent-head">
        {quiet && (
          <span className={'agent-mark' + (persona ? ' agent-persona' : '')} aria-hidden="true">
            {persona ? <BohemiaMark size={28} /> : <Glyph name="sparkle" size={17} />}
          </span>
        )}
        <div className="agent-head-text">
          <h2>{title}</h2>
          <p className="agent-lead">{lead}</p>
        </div>
      </div>
      <div className="agent-actions">
        <button className="agent-copy" onClick={() => copy(text)}>
          <Glyph name={copied ? 'check' : 'copy'} size={16} />
          {copied ? t('agentCopiedPrompt') : t('agentCopyPrompt')}
        </button>
        <button className="agent-toggle" aria-expanded={shown} onClick={() => setShown((value) => !value)}>
          {shown ? t('agentHidePrompt') : t('agentShowPrompt')}
          <Glyph name="chevron" size={13} />
        </button>
        {choices && choices.length > 1 && (
          <button className="agent-toggle agent-more" aria-haspopup="dialog" onClick={() => setSheet(true)}>
            {t('agentOtherUses')}
            <span className="agent-more-count">{choices.length}</span>
          </button>
        )}
        <span className="agent-works" title={t('agentWorksTitle')}>
          {works.map((kind) => (
            <AgentIcon key={kind} kind={kind} size={22} />
          ))}
        </span>
      </div>
      {shown && <div className="agent-message">{text.trim()}</div>}
      {status && <AgentStatus />}
      {sheet && choices && <PromptSheet choices={choices} close={() => setSheet(false)} />}
    </section>
  )
}

/*
 * The messages for an agent by purpose, in a sheet that slides up from the bottom: each says what it is for,
 * copies in one click and can be read first.
 */
export function PromptSheet({ choices, close }: { choices: PromptChoice[]; close: () => void }) {
  const { t } = useI18n()
  const [open, setOpen] = useState('')
  const { copied, copy } = useCopy()
  const [last, setLast] = useState('')
  const closing = useRef(close)
  useEffect(() => {
    closing.current = close
  })
  useEffect(() => {
    const key = (event: KeyboardEvent) => event.key === 'Escape' && closing.current()
    window.addEventListener('keydown', key)
    return () => window.removeEventListener('keydown', key)
  }, [])
  return (
    <div
      className="agent-sheet-scrim"
      onMouseDown={(event) => event.target === event.currentTarget && close()}
    >
      <div className="agent-sheet" role="dialog" aria-modal="true" aria-label={t('promptSheetAria')}>
        <span className="agent-sheet-handle" aria-hidden="true" />
        <header>
          <h2>{t('promptSheetTitle')}</h2>
          <button className="agent-sheet-close" onClick={close} aria-label={t('close')}>
            <Glyph name="close" size={16} />
          </button>
        </header>
        <ul>
          {choices.map((choice) => (
            <li key={choice.id} data-open={open === choice.id || undefined}>
              <div className="agent-sheet-row">
                <span>
                  <b>{choice.title}</b>
                  <small>{choice.lead}</small>
                </span>
                <button
                  className="agent-sheet-peek"
                  aria-expanded={open === choice.id}
                  aria-label={
                    open === choice.id
                      ? t('promptSheetHideItem', { title: choice.title })
                      : t('promptSheetPeekItem', { title: choice.title })
                  }
                  onClick={() => setOpen((value) => (value === choice.id ? '' : choice.id))}
                >
                  <Glyph name="eye" size={14} />
                </button>
                <button
                  className="agent-sheet-copy"
                  aria-label={t('promptSheetCopyItem', { title: choice.title })}
                  onClick={() => {
                    copy(choice.text)
                    setLast(choice.id)
                  }}
                >
                  <Glyph name={copied && last === choice.id ? 'check' : 'copy'} size={14} />
                  {copied && last === choice.id ? t('copied') : t('copy')}
                </button>
              </div>
              {open === choice.id && <div className="agent-message">{choice.text.trim()}</div>}
            </li>
          ))}
        </ul>
        <p className="agent-sheet-foot">{t('promptSheetFoot')}</p>
      </div>
    </div>
  )
}
