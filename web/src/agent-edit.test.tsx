/* Copyright (c) 2026 LFen (https://lfen.ink) — lg-monitor */

import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { AgentCard } from './AgentCard'
import { API } from './api'
import { BoardMap } from './Board'
import { I18nProvider } from './i18n'
import { ServiceDoc, ServiceRelations } from './ServiceDocs'
import { State } from './state'
import type { BoardView, Monitor, MonitorDetail } from './types'

function sampleMonitor(id: string, name: string, extra: Partial<Monitor> = {}): Monitor {
  return {
    id,
    name,
    watching: [],
    state: 'up',
    overall: 'up',
    hosts: [],
    rules: [],
    firing: 0,
    beats: [],
    uptime: 100,
    latencyMs: null,
    kind: 'port',
    ...extra,
  }
}

function mockAPI(docMarkdown = '# 虚构服务\n\n已写好的文档内容。') {
  const json = (body: unknown) =>
    new Response(JSON.stringify(body), { status: 200, headers: { 'Content-Type': 'application/json' } })
  const fetchFn: typeof fetch = async (input) => {
    const url = typeof input === 'string' ? input : input instanceof URL ? input.toString() : input.url
    if (url.endsWith('/api/v1/stream')) {
      return new Response(new ReadableStream(), { headers: { 'Content-Type': 'text/event-stream' } })
    }
    if (url.includes('/doc')) return json({ markdown: docMarkdown })
    if (url.endsWith('/api/v1/ui/lang')) return json({ lang: 'zh' })
    if (url.endsWith('/api/v1/guide')) return json({ install: {}, state: {} })
    if (url.endsWith('/api/v1/lan')) return json({ visible: false, web: false, name: 'LFen', hostname: 'PC' })
    if (url.endsWith('/api/v1/cloud/status')) return json({ loggedIn: false })
    if (url.endsWith('/api/v1/notifications/pause')) return json({ active: false })
    if (url.endsWith('/api/v1/config/targets')) return json({ targets: [] })
    if (url.endsWith('/api/v1/config/notifiers')) return json({ feishuApps: [], notifiers: [] })
    if (url.endsWith('/api/v1/query')) return json({ columns: [], rows: [] })
    return json([])
  }
  vi.stubGlobal('fetch', fetchFn)
  return new API('token', fetchFn)
}

describe('Bohemian agent prompt icons and persistent edit widgets', () => {
  afterEach(() => {
    vi.unstubAllGlobals()
  })

  it('uses the Bohemian mark by default on quiet AgentCard and renders Antigravity, Pi, Muse, and Grok icons', () => {
    const api = mockAPI()
    const { container } = render(
      <I18nProvider>
        <State api={api} scope="config" theme="light" keep={false}>
          <AgentCard quiet status={false} title="把这段话发给你的 Agent" lead="说明文字" text="测试提示词" />
        </State>
      </I18nProvider>,
    )

    expect(container.querySelector('.agent-mark.agent-persona span.bh svg')).toBeInTheDocument()
    for (const kind of ['claude-code', 'codex', 'cursor', 'antigravity', 'gemini', 'pi', 'muse', 'grok', 'windsurf', 'copilot', 'trae', 'qwen']) {
      expect(container.querySelector(`.agent-works .agent-icon[data-kind="${kind}"]`)).toBeInTheDocument()
    }
  })

  it('keeps a collapsible Agent edit-prompt widget on an already-written service document', async () => {
    const api = mockAPI('# 订单接口\n\n已经写好的服务文档。')
    const monitor = sampleMonitor('orders', '订单接口', { hasDoc: true })
    render(
      <I18nProvider>
        <State api={api} scope="config" theme="light" keep={false}>
          <ServiceDoc monitor={monitor} />
        </State>
      </I18nProvider>,
    )

    const toggle = await screen.findByRole('button', { name: /让 Agent 修改文档/ })
    expect(toggle).toHaveAttribute('aria-expanded', 'false')
    expect(screen.queryByText('让 Agent 修改这篇文档')).not.toBeInTheDocument()

    await userEvent.click(toggle)
    expect(toggle).toHaveAttribute('aria-expanded', 'true')
    expect(screen.getByText('让 Agent 修改这篇文档')).toBeVisible()
    await userEvent.click(screen.getByRole('button', { name: '显示这段话' }))
    expect(screen.getByText(/get_monitor_doc/)).toBeVisible()
    expect(screen.getByText(/put_monitor_doc/)).toBeVisible()
  })

  it('keeps a collapsible Agent edit-prompt widget on populated service and board dependency maps', async () => {
    const api = mockAPI()
    const db = sampleMonitor('db', '订单库', { role: 'database', dependents: [{ id: 'orders', relation: 'data' }] })
    const orders = sampleMonitor('orders', '订单接口', {
      role: 'api',
      depends: [{ id: 'db', relation: 'data', note: 'orders' }],
    })
    const detail = {
      monitor: orders,
      lineage: [],
      children: [],
      boards: [],
      slots: [],
      days: [],
      uptime24h: 100,
      uptime7d: 100,
      uptime30d: 100,
      latency: [],
      latencyNow: null,
      latencyAvg: null,
      events: [],
      probes: [],
    } as unknown as MonitorDetail
    const board: BoardView = {
      id: 'core',
      name: '核心业务',
      monitors: ['orders', 'db'],
      url: '#/boards/core',
      items: [orders, db],
      unassigned: [],
      summary: { services: 2, children: 0, up: 2, down: 0, unknown: 0, uptime: 100, state: 'up' },
    }

    render(
      <I18nProvider>
        <State api={api} scope="config" theme="light" keep={false}>
          <ServiceRelations detail={detail} />
          <BoardMap board={board} />
        </State>
      </I18nProvider>,
    )

    // ServiceRelations compact toggle expands inline edit-dependency AgentCard.
    const svcToggle = screen.getByRole('button', { name: /让 Agent 修改依赖/ })
    await userEvent.click(svcToggle)
    expect(screen.getByText('让 Agent 修改「订单接口」的依赖关系')).toBeVisible()

    // BoardMap compact collapsible bar stays compact until expanded.
    expect(screen.getByText('需要增删或调整依赖图？让 Agent 来改')).toBeVisible()
    expect(screen.queryByText('让 Agent 补全和核对依赖关系')).not.toBeInTheDocument()
    const expandBtn = screen.getByRole('button', { name: '展开' })
    await userEvent.click(expandBtn)
    await waitFor(() => expect(screen.getByText('让 Agent 补全和核对依赖关系')).toBeVisible())
  })
})
