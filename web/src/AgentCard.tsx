import { useState } from 'react'
import { AgentIcon, AgentSignals, agentName, useAgents } from './agents'
import { Glyph } from './glyphs'
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
  const agents = useAgents()
  const connected = agents?.filter((agent) => agent.connected) || []
  if (connected.length)
    return (
      <div className="agent-status" data-seen="" data-live="" role="status">
        <AgentSignals list={connected} />
        <span className="agent-status-note">
          {connected.length === 1 ? '已连接' : connected.length + ' 个 Agent 已连接'}
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
        ? (name ? name + ' 已断开' : 'Agent 已断开') + ' · 最近一次 ' + ago(seen, now())
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
