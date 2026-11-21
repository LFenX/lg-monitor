/* Copyright (c) 2026 LFen (https://lfen.ink) — lg-monitor */

import { useEffect, useId, useState } from 'react'
import type { API } from '../api'
import { Glyph } from '../glyphs'
import { useApp } from '../state'
import type { AgentSession } from '../types'
import amp from './icons/amp-color.svg?raw'
import antigravity from './icons/antigravity-color.svg?raw'
import cherry from './icons/cherrystudio-color.svg?raw'
import claude from './icons/claude-color.svg?raw'
import claudeCode from './icons/claudecode-color.svg?raw'
import cline from './icons/cline.svg?raw'
import codebuddy from './icons/codebuddy-color.svg?raw'
import codex from './icons/codex-color.svg?raw'
import cursor from './icons/cursor.svg?raw'
import deepseek from './icons/deepseek-color.svg?raw'
import gemini from './icons/geminicli-color.svg?raw'
import copilot from './icons/githubcopilot.svg?raw'
import goose from './icons/goose.svg?raw'
import kilo from './icons/kilocode.svg?raw'
import kimi from './icons/kimi-color.svg?raw'
import opencode from './icons/opencode.svg?raw'
import qoder from './icons/qoder-color.svg?raw'
import qwen from './icons/qwen-color.svg?raw'
import roo from './icons/roocode.svg?raw'
import trae from './icons/trae-color.svg?raw'
import windsurf from './icons/windsurf.svg?raw'
import zai from './icons/zai.svg?raw'
import './agents.css'

/*
 * Which agents are talking to lgmon right now. lgmon mcp names its agent (Claude Code, Codex, Cursor…) and
 * reports in every 30 seconds, so the page can show one signal per connected agent, each with its own logo.
 */

// Keyed by the kind ids in internal/agents.
const icons: Record<string, string> = {
  'claude-code': claudeCode,
  claude,
  codex,
  cursor,
  opencode,
  kimi,
  dsh: deepseek,
  zcode: zai,
  gemini,
  copilot,
  vscode: copilot,
  windsurf,
  trae,
  qwen,
  codebuddy,
  qoder,
  cline,
  roo,
  kilo,
  amp,
  antigravity,
  goose,
  cherry,
}

export function agentName(agent: Pick<AgentSession, 'name' | 'token'>) {
  return agent.name || agent.token || 'Agent'
}

/* An agent's logo on a small tile, so dark and light logos both read on either theme. */
export function AgentIcon({ kind, size = 22, title }: { kind?: string; size?: number; title?: string }) {
  // Logos with gradients carry an id; each copy on the page needs its own.
  const prefix = 'a' + useId().replace(/[^a-zA-Z0-9]/g, '')
  const svg = kind ? icons[kind] : undefined
  return (
    <span
      className="agent-icon"
      data-kind={svg ? kind : 'other'}
      style={{ fontSize: Math.round(size * 0.62), width: size, height: size }}
      role={title ? 'img' : undefined}
      aria-label={title}
      aria-hidden={title ? undefined : true}
      title={title}
    >
      {svg ? (
        <span
          className="agent-icon-art"
          dangerouslySetInnerHTML={{
            __html: svg
              .replace(/<title>[^<]*<\/title>/, '')
              .replaceAll('lobe-icons-', prefix + '-lobe-icons-'),
          }}
        />
      ) : (
        <Glyph name="agent" size={Math.round(size * 0.62)} />
      )}
    </span>
  )
}

/* Three rising bars, lgmon's own mark, standing for a live connection. */
export function Signal({ live = true }: { live?: boolean }) {
  return (
    <span className="agent-signal" data-live={live || undefined} aria-hidden="true">
      <i />
      <i />
      <i />
    </span>
  )
}

type Shared = { list?: AgentSession[]; listeners: Set<(list?: AgentSession[]) => void>; timer?: number }
const shared = new WeakMap<API, Shared>()

// One poll for the whole page, however many places show the agents.
function subscribe(api: API, listener: (list?: AgentSession[]) => void) {
  let entry = shared.get(api)
  if (!entry) {
    entry = { listeners: new Set() }
    shared.set(api, entry)
  }
  const state = entry
  const ask = () => {
    if (typeof document !== 'undefined' && document.visibilityState === 'hidden') return
    api
      .request<AgentSession[]>('/agents')
      .then((value) => {
        if (!Array.isArray(value)) return
        state.list = value
        state.listeners.forEach((notify) => notify(value))
      })
      .catch(() => {})
  }
  state.listeners.add(listener)
  if (state.list) listener(state.list)
  if (state.listeners.size === 1) {
    ask()
    state.timer = window.setInterval(ask, 5000)
  }
  return () => {
    state.listeners.delete(listener)
    if (state.listeners.size === 0) {
      clearInterval(state.timer)
      state.timer = undefined
    }
  }
}

/* The agents lgmon knows, connected first; undefined until the first answer. */
export function useAgents() {
  const { api } = useApp()
  const [list, setList] = useState<AgentSession[] | undefined>(() => shared.get(api)?.list)
  useEffect(() => subscribe(api, setList), [api])
  return list
}

export type AgentGroup = { key: string; kind?: string; name: string; count: number; version?: string }

/* Two Claude Code windows are one signal with ×2, not two identical chips. */
export function groupAgents(list: AgentSession[]) {
  const groups: AgentGroup[] = []
  for (const agent of list) {
    const name = agentName(agent)
    const key = agent.kind || 'name:' + name
    const found = groups.find((group) => group.key === key)
    if (found) found.count++
    else groups.push({ key, kind: agent.kind, name, count: 1, version: agent.version })
  }
  return groups
}

/* The connected agents as a row of signals, each with its logo and name. */
export function AgentSignals({ list }: { list: AgentSession[] }) {
  return (
    <ul className="agent-signals" aria-label="已连接的 Agent">
      {groupAgents(list).map((group) => (
        <li key={group.key} title={group.name + (group.version ? ' ' + group.version : '') + ' 已连接'}>
          <AgentIcon kind={group.kind} size={20} />
          <span className="name">{group.name}</span>
          {group.count > 1 && <span className="count">×{group.count}</span>}
          <Signal />
        </li>
      ))}
    </ul>
  )
}

/* The overlapping logos of the connected agents, for tight spots such as the side bar. */
export function AgentStack({ list, max = 4 }: { list: AgentSession[]; max?: number }) {
  const groups = groupAgents(list)
  const shown = groups.slice(0, max)
  return (
    <span className="agent-stack">
      {shown.map((group) => (
        <AgentIcon key={group.key} kind={group.kind} size={20} />
      ))}
      {groups.length > max && <span className="agent-stack-more">+{groups.length - max}</span>}
    </span>
  )
}
