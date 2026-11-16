import { useEffect, useState } from 'react'
import { Glyph } from './glyphs'
import { ago, useCopy } from './kit'
import { useApp } from './state'
import type { Guide } from './types'
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

/* Whether an agent has called lgmon yet. It asks on its own so the line changes while the person waits. */
export function AgentStatus() {
  const { api, guide, now } = useApp()
  const [fresh, setFresh] = useState<Guide['state'] | undefined>()
  const state = fresh || guide?.state
  const seen = state?.agentSeenAt || 0
  useEffect(() => {
    let active = true
    const ask = () =>
      api
        .request<Guide>('/guide')
        .then((value) => active && value?.state && setFresh(value.state))
        .catch(() => {})
    const timer = setInterval(ask, seen ? 15000 : 3000)
    return () => {
      active = false
      clearInterval(timer)
    }
  }, [api, seen])
  return (
    <p className="agent-status" data-seen={seen ? '' : undefined} role="status">
      <i aria-hidden="true" />
      {seen
        ? 'Agent 已接入' +
          (state?.agentName ? '（' + state.agentName + '）' : '') +
          ' · 最近一次 ' +
          ago(seen, now())
        : '等待你的 Agent 接入…'}
    </p>
  )
}

export function AgentCard({
  title,
  lead,
  text,
  status = true,
  art = true,
  quiet = false,
}: {
  title: string
  lead: string
  text: string
  status?: boolean
  art?: boolean
  // quiet steps back when something else on the page is the main action: left-aligned, outlined button, no art.
  quiet?: boolean
}) {
  const [shown, setShown] = useState(false)
  const { copied, copy } = useCopy()
  return (
    <section className="agent-card" data-quiet={quiet || undefined} aria-label={title}>
      {art && !quiet && <AgentArt />}
      <h2>{title}</h2>
      <p className="agent-lead">{lead}</p>
      <button className="agent-copy" onClick={() => copy(text)}>
        <Glyph name={copied ? 'check' : 'copy'} size={16} />
        {copied ? '已复制，发给你的 Agent 吧' : '复制这段话'}
      </button>
      <button className="agent-toggle" aria-expanded={shown} onClick={() => setShown((value) => !value)}>
        {shown ? '收起这段话' : '显示这段话'}
      </button>
      {shown && <div className="agent-message">{text.trim()}</div>}
      {status && <AgentStatus />}
    </section>
  )
}
