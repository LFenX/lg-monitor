import { fireEvent, render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, beforeEach, expect, it, vi } from 'vitest'
import { App } from './App'
import { Beats } from './status'
import type { AgentSession, BoardView, Monitor, MonitorDetail } from './types'

let bootstrap: HTMLScriptElement
let scope = 'read'
let board: BoardView
let posted: { path: string; body: unknown }[]
let hasBoards: boolean
let createError: string
let monitorsFail: boolean
let monitors: Monitor[]
let agents: AgentSession[]

function monitor(id: string, name: string, extra: Partial<Monitor> = {}): Monitor {
  return {
    id,
    name,
    watching: ['端口 3000'],
    state: 'up',
    beatSlots: Array.from({ length: 48 }, (_, index) => ({
      from: index * 1800000,
      state: 'up' as const,
      uptime: 100,
      segments: [{ from: index * 1800000, to: (index + 1) * 1800000, state: 'up' as const }],
    })),
    overall: 'up',
    hosts: [{ collectorId: 'fake-host', hostname: '测试主机', state: 'up', reason: '正常', ts: Date.now() }],
    rules: [],
    firing: 0,
    beats: Array(48).fill('up'),
    uptime: 100,
    latencyMs: 12,
    kind: 'port',
    children: [],
    depth: 0,
    boards: [],
    ...extra,
  }
}

function boardWith(items: Monitor[]): BoardView {
  return {
    id: 'main',
    name: '我的服务',
    description: '测试面板',
    monitors: items.filter((item) => !item.depth).map((item) => item.id),
    summary: { services: 2, children: 1, up: 2, down: 1, unknown: 0, uptime: 99.5, state: 'down' },
    url: 'http://fake/#/boards/main',
    items,
    unassigned: [],
  }
}

beforeEach(() => {
  sessionStorage.clear()
  localStorage.clear()
  window.location.hash = '#/'
  scope = 'read'
  posted = []
  monitorsFail = false
  monitors = []
  agents = []
  hasBoards = true
  createError = ''
  bootstrap = document.createElement('script')
  bootstrap.id = 'lgmon-session'
  bootstrap.type = 'application/json'
  bootstrap.textContent = JSON.stringify({ token: 'fake-local', scope: 'read' })
  document.head.append(bootstrap)
  board = boardWith([
    monitor('blog', '博客网站', {
      children: ['tunnel'],
      groupBeats: [...Array(47).fill('up'), 'down'],
      groupUptime: 98,
      overall: 'down',
      issue: '子服务 隧道 异常',
    }),
    monitor('tunnel', '隧道', {
      parent: 'blog',
      depth: 1,
      state: 'down',
      overall: 'down',
      kind: 'service',
      watching: ['服务 cloudflared'],
    }),
    monitor('api', '接口'),
  ])
  vi.stubGlobal(
    'fetch',
    vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const path = String(input)
      const headers = { 'X-Lgmon-Scope': scope }
      if (init?.method === 'POST' && path.endsWith('/license/margin'))
        return Response.json(
          { url: 'https://margin.lfen.ink/lgmon#ticket=fake-body.fake-signature' },
          { headers },
        )
      if (init?.method === 'POST' && path.endsWith('/boards')) {
        const input = JSON.parse(String(init.body))
        posted.push({ path, body: input })
        if (createError) return Response.json({ error: { message: createError } }, { status: 409, headers })
        board = { ...boardWith([]), id: 'created', name: input.name, description: input.description }
        hasBoards = true
        return Response.json(board, { headers })
      }
      if (init?.method === 'PUT' && path.endsWith('/boards/main')) {
        posted.push({ path, body: JSON.parse(String(init.body)) })
        return Response.json(board, { headers })
      }
      if (init?.method === 'PUT' && path.endsWith('/monitors/tunnel')) {
        posted.push({ path, body: JSON.parse(String(init.body)) })
        return Response.json(monitor('tunnel', '外网隧道'), { headers })
      }
      if (init?.method === 'POST' && path.endsWith('/monitors')) {
        posted.push({ path, body: JSON.parse(String(init.body)) })
        return Response.json(
          { monitor: monitor('new', '新服务'), created: true, monitorUrl: '', next: [] },
          { headers },
        )
      }
      if (path.endsWith('/stream'))
        return new Response(new ReadableStream(), {
          headers: { ...headers, 'Content-Type': 'text/event-stream' },
        })
      if (path.endsWith('/boards')) return Response.json(hasBoards ? [board] : [], { headers })
      if (path.endsWith('/boards/' + board.id)) return Response.json(board, { headers })
      if (path.includes('/explore'))
        return Response.json(
          {
            collector: { id: 'fake-host', hostname: '测试主机', online: true },
            kind: 'ports',
            collectedAt: Date.now(),
            total: 2,
            matched: 2,
            counts: {},
            offset: 0,
            limit: 80,
            items: [
              {
                kind: 'port',
                name: '3000',
                port: 3000,
                process: 'node.exe',
                address: '0.0.0.0',
                monitoredBy: 'blog',
              },
              {
                kind: 'port',
                name: '8080',
                port: 8080,
                process: 'java.exe',
                address: '127.0.0.1',
                suggest: { name: 'java :8080', port: 8080 },
              },
            ],
          },
          { headers },
        )
      if (path.endsWith('/monitors/gone'))
        return Response.json(
          { error: { code: 'not_found', message: '监控 gone 不存在，先调用 list_monitors 核对 ID' } },
          { status: 404, headers },
        )
      if (path.endsWith('/monitors/tunnel'))
        return Response.json(
          {
            monitor: board.items[1],
            lineage: [{ id: 'blog', name: '博客网站', overall: 'down' }],
            children: [],
            boards: [{ id: 'main', name: '我的服务' }],
            slots: Array.from({ length: 96 }, (_, index) => ({
              from: index * 900000,
              state: index === 95 ? 'down' : 'up',
              uptime: index === 95 ? 98.2 : 100,
            })),
            days: Array.from({ length: 30 }, (_, index) => ({
              from: index * 86400000,
              state: index === 29 ? 'down' : 'up',
              uptime: index === 29 ? 98.2 : 100,
            })),
            uptime24h: 99,
            uptime7d: 99.5,
            uptime30d: 99.9,
            latency: [],
            latencyNow: null,
            latencyAvg: null,
            events: [{ ts: Date.now(), kind: 'down', text: '出现异常', host: '测试主机' }],
            incidents: [
              {
                monitorId: 'tunnel',
                monitorName: '隧道',
                depth: 0,
                kind: 'down',
                from: Date.now() - 45000,
                to: 0,
                durationMs: 45000,
                reason: '虚构服务已停止',
                host: '测试主机',
                alerts: [],
              },
              ...Array.from({ length: 4 }, (_, index) => ({
                monitorId: 'tunnel',
                monitorName: '隧道',
                depth: 0,
                kind: 'down' as const,
                from: new Date(2026, 10, 15, 22 - index, 4, 22).getTime(),
                to: new Date(2026, 10, 15, 22 - index, 7, 34).getTime(),
                durationMs: 192000,
                reason: '虚构服务已停止',
                host: '测试主机',
                alerts:
                  index === 0
                    ? [{ startedAt: 1, resolvedAt: 2, severity: 'critical', summary: '虚构隧道服务停止' }]
                    : [],
              })),
            ],
            rules: [
              {
                id: 'tunnel-service-stopped',
                type: 'service_stopped',
                severity: 'critical',
                text: 'Windows 服务停止持续 20s 后告警',
                notify: ['本机 Windows 通知'],
              },
            ],
          } satisfies MonitorDetail,
          { headers },
        )
      if (path.endsWith('/collectors'))
        return Response.json(
          [{ id: 'fake-host', hostname: '测试主机', online: true, connected: true, lastSeen: Date.now() }],
          { headers },
        )
      if (monitorsFail && path.endsWith('/monitors'))
        return Response.json(
          { error: { code: 'internal_error', message: '接口执行失败' } },
          { status: 500, headers },
        )
      if (path.endsWith('/monitors') && monitors.length) return Response.json(monitors, { headers })
      if (path.endsWith('/agents')) return Response.json(agents, { headers })
      if (
        path.endsWith('/rule-errors') ||
        path.endsWith('/alerts') ||
        path.endsWith('/targets') ||
        path.endsWith('/monitors') ||
        path.endsWith('/dashboards')
      )
        return Response.json([], { headers })
      if (path.endsWith('/config/targets')) return Response.json({ targets: [] }, { headers })
      if (path.endsWith('/config/notifiers')) return Response.json({ notifiers: [] }, { headers })
      if (path.endsWith('/notifications/pause'))
        return Response.json({ paused: false, until: null }, { headers })
      if (path.endsWith('/query'))
        return Response.json({ columns: [], rows: [], truncated: false }, { headers })
      if (path.endsWith('/guide'))
        return Response.json(
          {
            steps: [],
            state: { agentSeenAt: 0 },
            install: { executable: 'C:\\Fake\\lgmon.exe', home: 'C:\\Fake Home', personal: true, url: '' },
            collectors: [{ id: 'fake-host', hostname: '测试主机', online: true }],
          },
          { headers },
        )
      return Response.json({}, { headers })
    }),
  )
})
afterEach(() => {
  bootstrap.remove()
  vi.unstubAllGlobals()
})

it('lists services with the troubled group first and its children open', async () => {
  board = boardWith([board.items[2], board.items[0], board.items[1]])
  render(<App />)
  const list = await screen.findByRole('list', { name: '服务列表' })
  const rows = within(list).getAllByRole('listitem')
  expect(rows.map((row) => within(row).getByRole('link').textContent)).toEqual(['博客网站', '隧道', '接口'])
  // Previous releases supplied rolled-up fields, including on shared boards.
  expect(within(rows[0]).queryByText('子服务 隧道 异常')).toBeNull()
  expect(within(rows[0]).getByText('100%')).toHaveAttribute('data-state', 'up')
  expect(rows[1]).toHaveAttribute('data-overall', 'down')
  const toggle = within(rows[0]).getByRole('button', { name: '收起博客网站的子服务' })
  await userEvent.click(toggle)
  expect(within(list).queryByText('隧道')).not.toBeInTheDocument()
  await userEvent.click(within(list).getByRole('button', { name: '展开博客网站的子服务' }))
  expect(within(list).getByText('隧道')).toBeVisible()
  expect(screen.getByText('24 小时可用率').nextSibling).toHaveTextContent('99.50%')
})

it('switches to cards and remembers the choice for this board', async () => {
  render(<App />)
  await screen.findByRole('list', { name: '服务列表' })
  await userEvent.click(screen.getByRole('button', { name: /卡片/ }))
  const cards = screen.getByRole('list', { name: '服务卡片' })
  expect(within(cards).getAllByRole('listitem')).toHaveLength(2)
  expect(within(within(cards).getAllByRole('listitem')[0]).getByLabelText('子服务')).toHaveTextContent('隧道')
  expect(localStorage.getItem('lgmon-layout-main')).toBe('cards')
})

it('draws the board as a dependency map and offers the agent the job of linking it up', async () => {
  render(<App />)
  await screen.findByRole('list', { name: '服务列表' })
  await userEvent.click(screen.getByRole('button', { name: /关系/ }))
  const map = await screen.findByRole('figure', { name: '我的服务 的依赖关系' })
  // Only the sub-service line exists yet: the tunnel hangs from the blog.
  expect(within(map).getByRole('link', { name: /隧道/ })).toHaveAttribute('data-state', 'down')
  expect(within(map).getByRole('link', { name: /接口/ })).toHaveAttribute('data-alone')
  const card = screen.getByRole('region', { name: '让 Agent 梳理依赖关系' })
  await userEvent.click(within(card).getByRole('button', { name: '显示这段话' }))
  expect(card).toHaveTextContent('面板「我的服务」（board: main）')
  expect(card).toHaveTextContent('先给我看')
  expect(card).toHaveTextContent('edit_monitor')
  expect(localStorage.getItem('lgmon-layout-main')).toBe('map')
})

it('guides an empty board to an agent message that names the board and asks instead of leaving blanks', async () => {
  board = boardWith([])
  render(<App />)
  const guide = await screen.findByRole('region', { name: '空面板指引' })
  // The message is folded away: the person copies it, the agent reads it.
  expect(within(guide).queryByText(/board: main/)).not.toBeInTheDocument()
  await userEvent.click(within(guide).getByRole('button', { name: '显示这段话' }))
  const message = within(guide).getByText(/board: main/)
  expect(message.textContent).toContain(
    "& 'C:\\Fake\\lgmon.exe' agent connect --home 'C:\\Fake Home' --tools all --elevate --json",
  )
  expect(message.textContent).toContain('问我想监控哪些服务')
  expect(message.textContent).not.toContain('【')
  expect(within(guide).getByRole('status')).toHaveTextContent('等待你的 Agent 接入…')
  // A read-only page keeps manual creation in reach; using it explains how to get permission.
  await userEvent.click(within(guide).getByRole('button', { name: '手动创建' }))
  expect(screen.getByRole('dialog', { name: '需要修改权限' })).toHaveTextContent('手动添加服务需要修改权限')
  expect(screen.queryByRole('group', { name: '要监控什么' })).not.toBeInTheDocument()
})

it('adds a service by picking a running port and hanging it under a parent', async () => {
  scope = 'config'
  render(<App />)
  await screen.findByRole('list', { name: '服务列表' })
  await userEvent.click(screen.getByRole('button', { name: '添加服务' }))
  // The dialog opens on the agent prompt; manual creation sits in its corner.
  const agent = await screen.findByRole('dialog', { name: '添加服务' })
  expect(within(agent).getByRole('button', { name: '复制这段话' })).toBeVisible()
  await userEvent.click(within(agent).getByRole('button', { name: '手动创建' }))
  const dialog = await screen.findByRole('dialog', { name: '添加服务' })
  const taken = await within(dialog).findByText(':3000 node.exe')
  expect(taken.closest('label')).toHaveAttribute('aria-disabled', 'true')
  await userEvent.click(within(dialog).getByText(':8080 java.exe'))
  expect(within(dialog).getByLabelText('名字')).toHaveValue('java :8080')
  await userEvent.selectOptions(within(dialog).getByLabelText('挂在哪个服务下面'), 'blog')
  await userEvent.click(within(dialog).getByRole('button', { name: '添加' }))
  await waitFor(() => expect(posted).toHaveLength(1))
  expect(posted[0].body).toEqual({ name: 'java :8080', board: 'main', parent: 'blog', port: 8080 })
  await waitFor(() => expect(screen.queryByRole('dialog')).not.toBeInTheDocument())
})

it('welcomes with the agent first and the board name it should use, building by hand right below', async () => {
  scope = 'config'
  hasBoards = false
  window.location.hash = '#/'
  render(<App />)
  await screen.findByRole('heading', { name: '欢迎使用 lgmon' })
  const name = screen.getByRole('textbox', { name: '面板名称' })
  await userEvent.clear(name)
  await userEvent.type(name, '部门服务')
  const card = screen.getByRole('region', { name: '让 Agent 帮你开始' })
  await userEvent.click(within(card).getByRole('button', { name: '显示这段话' }))
  const message = within(card).getByText(/create_board/)
  expect(message.textContent).toContain('name 使用 "部门服务"')
  expect(message.textContent).toContain('--elevate --json')
  expect(message.textContent).toContain('不用让我自己建')
  expect(message.textContent).toContain('问我想监控哪些服务')
  const manual = name.closest('form') as HTMLFormElement
  expect(within(manual).getByRole('button', { name: '新建面板' })).toBeVisible()
})

it('offers the entered name to the agent and closes the dialog after successful creation', async () => {
  scope = 'config'
  render(<App />)
  await screen.findByRole('list', { name: '服务列表' })
  await userEvent.click(screen.getByRole('button', { name: '新建面板' }))
  const dialog = screen.getByRole('dialog', { name: '新建面板' })
  await userEvent.type(within(dialog).getByRole('textbox', { name: '名称' }), '数据应用部')
  await userEvent.type(within(dialog).getByRole('textbox', { name: '简介（可选）' }), '业务服务')
  const prompt = within(dialog).getByRole('region', { name: '交给 Agent 创建' })
  await userEvent.click(within(prompt).getByRole('button', { name: '显示这段话' }))
  expect(prompt).toHaveTextContent('name 使用 "数据应用部"，description 使用 "业务服务"')
  expect(within(prompt).getByRole('button', { name: '复制这段话' })).toBeVisible()
  await userEvent.click(within(dialog).getByRole('button', { name: '新建' }))
  await waitFor(() => expect(screen.queryByRole('dialog', { name: '新建面板' })).not.toBeInTheDocument())
  expect(posted[0].body).toEqual({ name: '数据应用部', description: '业务服务' })
  await screen.findByRole('heading', { name: '数据应用部' })
  const empty = await screen.findByRole('region', { name: '空面板指引' })
  expect(within(empty).getByRole('button', { name: '复制这段话' })).toBeVisible()
  await userEvent.click(within(empty).getByRole('button', { name: '显示这段话' }))
  expect(empty).toHaveTextContent('board: created')
})

it('retains the board dialog and inputs when creation fails and permits a successful retry', async () => {
  scope = 'config'
  createError = '名称已存在，请换一个名称'
  render(<App />)
  await screen.findByRole('list', { name: '服务列表' })
  const initialHash = window.location.hash
  await userEvent.click(screen.getByRole('button', { name: '新建面板' }))
  const dialog = screen.getByRole('dialog', { name: '新建面板' })
  const name = within(dialog).getByRole('textbox', { name: '名称' })
  await userEvent.type(name, '业务看板')
  await userEvent.click(within(dialog).getByRole('button', { name: '新建' }))
  expect(await within(dialog).findByText('名称已存在，请换一个名称')).toBeVisible()
  expect(name).toHaveValue('业务看板')
  expect(window.location.hash).toBe(initialHash)
  createError = ''
  await userEvent.click(within(dialog).getByRole('button', { name: '新建' }))
  await waitFor(() => expect(screen.queryByRole('dialog', { name: '新建面板' })).not.toBeInTheDocument())
  expect(posted.filter((item) => item.path.endsWith('/boards'))).toHaveLength(2)
})

it('starts adding from one dialog: the agent prompt first, nothing left under the list', async () => {
  render(<App />)
  await screen.findByRole('list', { name: '服务列表' })
  expect(screen.queryByRole('group', { name: '面板 Agent 提示词' })).not.toBeInTheDocument()
  expect(screen.queryByText('交给 Agent 添加服务')).not.toBeInTheDocument()
  await userEvent.click(screen.getByRole('button', { name: '添加服务' }))
  const dialog = screen.getByRole('dialog', { name: '添加服务' })
  expect(within(dialog).getByRole('button', { name: '复制这段话' })).toBeVisible()
  await userEvent.click(within(dialog).getByRole('button', { name: '显示这段话' }))
  expect(dialog).toHaveTextContent('board: main')
  expect(dialog).toHaveTextContent('--elevate --json')
  expect(within(dialog).getByRole('button', { name: '手动创建' })).toBeVisible()
})

it('shows one service with its parent, history, events and alert rules', async () => {
  window.location.hash = '#/monitors/tunnel'
  render(<App />)
  expect(await screen.findByRole('heading', { name: '隧道' })).toBeVisible()
  const crumbs = screen.getByRole('navigation', { name: '位置' })
  expect(within(crumbs).getByRole('link', { name: '博客网站' })).toHaveAttribute('href', '#/monitors/blog')
  expect(screen.getByRole('img', { name: '最近 24 小时状态' }).children.length).toBeGreaterThan(3)
  expect(screen.getByRole('img', { name: '最近 30 天状态' }).children).toHaveLength(30)
  for (const label of ['最近 24 小时状态', '最近 30 天状态']) {
    const latest = screen.getByRole('img', { name: label }).lastElementChild as HTMLElement
    expect(latest).toHaveAttribute('data-state', 'down')
    expect(latest.style.getPropertyValue('--beat-up')).toBe('98.2%')
    expect(latest.title).toContain('正常 98.2% · 异常 1.8%')
  }
  expect(screen.getByText('Windows 服务停止持续 20s 后告警')).toBeVisible()
  expect(screen.getByText('通知：本机 Windows 通知')).toBeVisible()
  const events = screen.getByRole('region', { name: '事件' })
  expect(events).toHaveTextContent('最近 7 天 5 次异常')
  const group = within(events).getByRole('region', { name: '隧道 的事件' })
  expect(within(group).getByText('进行中')).toBeVisible()
  expect(within(group).getByText('2026-11-15 22:04:22')).toBeVisible()
  expect(within(group).getByText('→ 22:07:34 · 测试主机')).toBeVisible()
  expect(within(group).getAllByText('持续 3 分 12 秒')).toHaveLength(2)
  expect(within(group).getByText('严重告警：虚构隧道服务停止（已恢复）')).toBeVisible()
  // Three entries show; the rest fold until asked.
  await userEvent.click(within(group).getByRole('button', { name: '展开其余 2 条' }))
  expect(within(group).getAllByText('持续 3 分 12 秒')).toHaveLength(4)
  expect(screen.queryByRole('region', { name: '响应时间' })).not.toBeInTheDocument()
})

it('keeps complete failures, healthy samples, unknown periods and missing data distinct', () => {
  render(
    <Beats
      size="day"
      slots={[
        { from: 0, span: 1, state: 'down', uptime: 0 },
        { from: 1, span: 1, state: 'up', uptime: 100 },
        { from: 2, span: 1, state: 'down', uptime: 25 },
        { from: 3, span: 1, state: 'unknown', uptime: 100 },
        { from: 4, span: 1, state: '', uptime: null },
        { from: 5, span: 1, state: 'down' },
        { from: 6, span: 1, state: 'up', uptime: Number.NaN },
      ]}
    />,
  )
  const bars = Array.from(screen.getByRole('img', { name: '状态历史' }).children) as HTMLElement[]
  expect(bars.slice(0, 3).map((bar) => bar.style.getPropertyValue('--beat-up'))).toEqual([
    '0%',
    '100%',
    '25%',
  ])
  for (const bar of bars.slice(3)) expect(bar).not.toHaveAttribute('data-uptime')
  expect(bars[3]).toHaveAttribute('data-state', 'unknown')
  expect(bars[4]).toHaveAttribute('data-state', 'none')
  expect(bars[5]).toHaveAttribute('data-state', 'down')
})

it('places clipped failure segments at their own timestamps rather than at a fixed edge', () => {
  render(
    <Beats
      size="large"
      slots={[
        {
          from: 0,
          span: 1000000,
          state: 'down',
          uptime: 99.9,
          segments: [{ from: 100000, to: 101000, state: 'down' }],
        },
        {
          from: 1000000,
          span: 1000000,
          state: 'down',
          uptime: 99.9,
          segments: [{ from: 1800000, to: 1801000, state: 'down' }],
        },
      ]}
    />,
  )
  const bars = screen.getByRole('img', { name: '状态历史' }).querySelectorAll<HTMLElement>('.lm-beat-segment')
  expect(bars[0].style.getPropertyValue('--segment-left')).toBe('10%')
  expect(bars[1].style.getPropertyValue('--segment-left')).toBe('80%')
  for (const bar of bars) {
    expect(bar.title).toContain(' · 1 秒')
    expect(bar.style.getPropertyValue('--segment-width')).toBe('0.1%')
  }
})

it.each(['row', 'large', 'day'] as const)(
  'shows the latest bar fully red during a live failure and restores history after recovery (%s)',
  (size) => {
    const slots = [
      {
        from: 0,
        span: 100000,
        state: 'up' as const,
        uptime: 100,
        segments: [{ from: 0, to: 100000, state: 'up' as const }],
      },
    ]
    const { rerender } = render(<Beats size={size} slots={slots} liveState="down" />)
    let latest = screen.getByRole('img', { name: '状态历史' }).lastElementChild!
    expect(latest).toHaveAttribute('data-live', 'true')
    expect(latest).toHaveAttribute('data-state', 'down')
    expect(latest).toHaveAttribute('data-history-state', 'up')
    rerender(<Beats size={size} slots={slots} liveState="up" />)
    latest = screen.getByRole('img', { name: '状态历史' }).lastElementChild!
    expect(latest).not.toHaveAttribute('data-live')
    expect(latest).toHaveAttribute('data-state', 'up')
  },
)

it('tells a person whose link points at a deleted service what happened, not what an agent should call', async () => {
  window.location.hash = '#/monitors/gone'
  render(<App />)
  expect(await screen.findByText('没有找到这个服务，它可能已经被删除了。')).toBeVisible()
  expect(screen.queryByText(/list_monitors/)).not.toBeInTheDocument()
  expect(screen.getByRole('link', { name: '回到面板' })).toHaveAttribute('href', '#/')
})

it('edits the name and the parent by hand and leaves other changes to the agent', async () => {
  scope = 'config'
  window.location.hash = '#/monitors/tunnel'
  render(<App />)
  await screen.findByRole('heading', { name: '隧道' })
  // Agent prompts no longer sit in the service's menu.
  await userEvent.click(screen.getByRole('button', { name: '更多操作' }))
  expect(screen.queryByText('让 Agent 加子服务')).not.toBeInTheDocument()
  expect(screen.queryByText('让 Agent 修改这个监控')).not.toBeInTheDocument()
  await userEvent.click(screen.getByRole('button', { name: '编辑' }))
  const dialog = screen.getByRole('dialog', { name: '编辑服务' })
  const name = within(dialog).getByRole('textbox', { name: '名字' })
  await userEvent.clear(name)
  await userEvent.type(name, '外网隧道')
  await userEvent.selectOptions(within(dialog).getByLabelText('挂在哪个服务下面'), '')
  expect(within(dialog).getByText(/交给 Agent/)).toBeVisible()
  await userEvent.click(within(dialog).getByRole('button', { name: '保存' }))
  await waitFor(() => expect(screen.queryByRole('dialog', { name: '编辑服务' })).not.toBeInTheDocument())
  expect(posted).toEqual([
    {
      path: '/api/v1/monitors/tunnel',
      body: { name: '外网隧道', parent: '', description: '', role: '', entry: '' },
    },
  ])
})

it('opens a web service from its row in a new tab, and never turns a non-web address into a link', async () => {
  board = {
    ...board,
    items: board.items.map((item) =>
      item.id === 'blog'
        ? { ...item, entry: 'https://blog.example.com' }
        : item.id === 'api'
          ? { ...item, entry: 'javascript:alert(1)' }
          : item,
    ),
  }
  render(<App />)
  const list = await screen.findByRole('list', { name: '服务列表' })
  const open = within(list).getByRole('link', { name: '打开 博客网站' })
  expect(open).toHaveAttribute('href', 'https://blog.example.com')
  expect(open).toHaveAttribute('target', '_blank')
  expect(open.getAttribute('rel')).toContain('noopener')
  expect(within(list).queryByRole('link', { name: /^打开 接口/ })).not.toBeInTheDocument()
})
it('shows each service in its own card style and changes one from the card or the style library', async () => {
  scope = 'config'
  board = { ...board, cardStyle: 'ring', cards: { api: 'number' } }
  board.items = board.items.map((item) =>
    item.id === 'blog' ? { ...item, uptime7d: 99.1, uptime30d: 98.7 } : { ...item, latencyMs: 42 },
  )
  localStorage.setItem('lgmon-layout-main', 'cards')
  render(<App />)
  const cards = await screen.findByRole('list', { name: '服务卡片' })
  const blog = within(cards).getByRole('listitem', { name: '博客网站' })
  expect(blog).toHaveAttribute('data-style', 'ring')
  expect(within(blog).getByText('98.7%')).toBeVisible()
  // A child shows as a small tag on its parent's card whatever the style.
  expect(within(blog).getByText('隧道')).toBeVisible()
  const api = within(cards).getByRole('listitem', { name: '接口' })
  expect(api).toHaveAttribute('data-style', 'number')
  expect(within(api).getByText('42 ms')).toBeVisible()
  await userEvent.click(within(blog).getByRole('button', { name: '换卡片样式' }))
  await userEvent.click(screen.getByRole('menuitemradio', { name: '30 天' }))
  // The card takes the new look at once; saving happens behind it.
  expect(within(cards).getByRole('listitem', { name: '博客网站' })).toHaveAttribute('data-style', 'days')
  await waitFor(() =>
    expect(posted).toContainEqual({ path: '/api/v1/boards/main', body: { cards: { blog: 'days' } } }),
  )
  // The library shows every style on sample data and says what each is for.
  await userEvent.click(screen.getByRole('button', { name: '更多操作' }))
  await userEvent.click(screen.getByRole('button', { name: '卡片样式' }))
  const library = screen.getByRole('dialog', { name: '卡片样式' })
  for (const name of ['心跳', '极简', '可用率环', '响应曲线', '30 天', '大数字'])
    expect(within(library).getByRole('region', { name: '样式 ' + name })).toBeVisible()
  expect(within(library).getByText(/适合每天跑一次的计划任务/)).toBeVisible()
  await userEvent.click(
    within(within(library).getByRole('region', { name: '样式 极简' })).getByRole('button', {
      name: '设为默认',
    }),
  )
  await waitFor(() =>
    expect(posted).toContainEqual({ path: '/api/v1/boards/main', body: { cardStyle: 'minimal' } }),
  )
})

it('opens lgmon on a small start page with the message for the agent folded away', async () => {
  window.location.hash = '#/start'
  render(<App />)
  expect(await screen.findByRole('heading', { name: '还没有在监控服务' })).toBeVisible()
  // The small window stands on its own, without the board's navigation.
  expect(screen.queryByRole('navigation', { name: '面板' })).not.toBeInTheDocument()
  const card = screen.getByRole('region', { name: '让 Agent 帮你加第一个服务' })
  expect(within(card).getByRole('button', { name: '复制这段话' })).toBeVisible()
  expect(within(card).queryByText(/start_here/)).not.toBeInTheDocument()
  await userEvent.click(within(card).getByRole('button', { name: '显示这段话' }))
  const message = within(card).getByText(/start_here/)
  expect(message.textContent).toContain("'C:\\Fake\\lgmon.exe' agent connect --home 'C:\\Fake Home'")
  expect(message.textContent).toContain('问我想监控哪些服务')
  expect(message.textContent).not.toContain('【')
  expect(within(card).getByRole('button', { name: '收起这段话' })).toHaveAttribute('aria-expanded', 'true')
  expect(within(card).getByRole('status')).toHaveTextContent('等待你的 Agent 接入…')
  expect(screen.getByRole('link', { name: '打开看板' })).toHaveAttribute('target', '_blank')
})

it('never tells the small window there are no services when the list could not be read', async () => {
  monitorsFail = true
  window.location.hash = '#/start'
  render(<App />)
  expect(await screen.findByRole('heading', { name: '正在读取…' })).toBeVisible()
  await new Promise((resolve) => setTimeout(resolve, 300))
  expect(screen.queryByRole('heading', { name: '还没有在监控服务' })).not.toBeInTheDocument()
})

it('shows the small window each service with its sub-services and every connected agent by name', async () => {
  monitors = [
    monitor('site', '官网', { overall: 'down', issue: '子服务异常', children: ['tunnel', 'db'] }),
    monitor('tunnel', '隧道', { parent: 'site', depth: 1, state: 'up', uptime: 100 }),
    monitor('db', '数据库', {
      parent: 'site',
      depth: 1,
      state: 'down',
      overall: 'down',
      issue: '端口没有响应',
    }),
    monitor('api', '接口', { state: 'up', uptime: 99.5, children: ['cache', 'queue'] }),
    monitor('cache', '缓存', { parent: 'api', depth: 1, state: 'up', uptime: 100 }),
    monitor('queue', '队列', { parent: 'api', depth: 1, state: 'up', uptime: 100 }),
  ]
  const agent = (id: string, kind: string, name: string, connected = true): AgentSession => ({
    id,
    kind,
    name,
    token: 'fake-token',
    connected,
    since: Date.now() - 60000,
    lastSeenAt: Date.now(),
    calls: 1,
  })
  agents = [
    agent('a', 'claude-code', 'Claude Code'),
    agent('b', 'claude-code', 'Claude Code'),
    agent('c', 'codex', 'Codex'),
    agent('d', 'cursor', 'Cursor', false),
  ]
  window.location.hash = '#/start'
  render(<App />)
  const services = await screen.findByRole('list', { name: '服务' })
  const sub = await within(services).findByRole('list', { name: '官网 的子服务' })
  expect(screen.getByRole('heading', { name: '1 个子服务异常' })).toBeVisible()
  expect(within(services).getByRole('link', { name: /官网/ }).closest('li')).toHaveAttribute(
    'data-state',
    'up',
  )
  // The troubled sub-service comes first, with its own reason.
  const rows = within(sub).getAllByRole('link')
  expect(rows.map((row) => row.textContent)).toEqual(['数据库端口没有响应', '隧道100%'])
  expect(within(services).getByRole('link', { name: /接口/ })).toBeVisible()
  // Healthy parts stay folded behind a count; a failing one opens its service by itself.
  expect(within(services).getByRole('button', { name: '收起官网的子服务' })).toHaveTextContent('1 异常')
  expect(within(services).queryByRole('list', { name: '接口 的子服务' })).not.toBeInTheDocument()
  const fold = within(services).getByRole('button', { name: '展开接口的子服务' })
  expect(fold).toHaveAttribute('aria-expanded', 'false')
  fireEvent.click(fold)
  expect(within(services).getByRole('list', { name: '接口 的子服务' })).toHaveTextContent('缓存')
  fireEvent.click(within(services).getByRole('button', { name: '收起接口的子服务' }))
  expect(within(services).queryByRole('list', { name: '接口 的子服务' })).not.toBeInTheDocument()
  const connected = await screen.findByRole('list', { name: '已连接的 Agent' })
  const chips = within(connected).getAllByRole('listitem')
  expect(chips.map((chip) => chip.textContent)).toEqual(['Claude Code×2', 'Codex'])
  expect(connected.closest('[role="status"]')).toHaveTextContent('3 个 Agent 已连接')
})

it('folds healthy sub-services of a card into one low strip that opens on request', async () => {
  board = boardWith([
    monitor('site', '官网', { children: ['tunnel', 'db'] }),
    monitor('tunnel', '隧道', { parent: 'site', depth: 1 }),
    monitor('db', '数据库', { parent: 'site', depth: 1, kind: 'service', role: 'database' }),
    monitor('api', '接口'),
  ])
  localStorage.setItem('lgmon-layout-main', 'cards')
  render(<App />)
  const cards = await screen.findByRole('list', { name: '服务卡片' })
  const site = within(cards).getByRole('listitem', { name: '官网' })
  const strip = within(site).getByRole('button', { name: '展开子服务' })
  expect(strip).toHaveTextContent('2 个子服务全部正常')
  expect(within(site).queryByText('数据库')).not.toBeInTheDocument()
  await userEvent.click(strip)
  expect(within(site).getByRole('link', { name: /数据库/ })).toHaveAttribute('href', '#/monitors/db')
  expect(within(site).getByRole('button', { name: '收起子服务' })).toHaveAttribute('aria-expanded', 'true')
})

it('offers a way back to the page the person came from, by name', async () => {
  sessionStorage.clear()
  render(<App />)
  const list = await screen.findByRole('list', { name: '服务列表' })
  // Opened straight from the board, a service names the board to go back to.
  await userEvent.click(within(list).getByRole('link', { name: /隧道/ }))
  await waitFor(() => expect(window.location.hash).toBe('#/monitors/tunnel'))
  const back = await screen.findByRole('button', { name: '返回 我的服务' })
  await userEvent.click(back)
  await waitFor(() => expect(window.location.hash).toBe('#/'))
  expect(screen.queryByRole('navigation', { name: '返回' })).not.toBeInTheDocument()
})

it('offers the agent messages by purpose in a sheet that slides up, including a skill to install', async () => {
  const written: string[] = []
  Object.defineProperty(navigator, 'clipboard', {
    configurable: true,
    value: { writeText: async (text: string) => void written.push(text) },
  })
  monitors = [monitor('site', '官网')]
  window.location.hash = '#/start'
  render(<App />)
  const card = await screen.findByRole('region', { name: '有事交给 Agent' })
  await userEvent.click(within(card).getByRole('button', { name: /其他用途/ }))
  const sheet = screen.getByRole('dialog', { name: '选择要交给 Agent 的事' })
  for (const title of [
    '照看服务',
    '加服务',
    '梳理依赖关系',
    '查问题',
    '调整告警',
    '告警发到飞书',
    '安装 lgmon skill',
  ])
    expect(within(sheet).getByText(title)).toBeVisible()
  await userEvent.click(within(sheet).getByRole('button', { name: '看看「梳理依赖关系」这段话' }))
  expect(sheet).toHaveTextContent('收集事实')
  expect(sheet).toHaveTextContent('| via |')
  await userEvent.click(within(sheet).getByRole('button', { name: '复制「安装 lgmon skill」这段话' }))
  await waitFor(() => expect(written.at(-1)).toContain('name: lgmon'))
  expect(written.at(-1)).toContain('SKILL.md')
  await userEvent.keyboard('{Escape}')
  expect(screen.queryByRole('dialog', { name: '选择要交给 Agent 的事' })).not.toBeInTheDocument()
})

it("signs the panel with the author's name and places, small, in the sidebar and the small window", async () => {
  render(<App />)
  await screen.findByRole('list', { name: '服务列表' })
  const brand = screen.getByRole('link', { name: 'lgmon 首页' })
  expect(brand).toHaveTextContent('by LFen')
  const places = screen.getByRole('navigation', { name: '作者的链接' })
  expect(within(places).getByRole('link', { name: 'GitHub 仓库' })).toHaveAttribute(
    'href',
    'https://github.com/LFenX/lg-monitor',
  )
  expect(within(places).getByRole('link', { name: '个人网站 lfen.ink' })).toHaveAttribute(
    'href',
    'https://lfen.ink/',
  )
  const margin = within(places).getByRole('link', { name: '留白 margin.lfen.ink' })
  expect(margin).toHaveAttribute('href', 'https://margin.lfen.ink/')
  expect(screen.getByText('© 2026 LFen')).toBeVisible()
  // 留白 opens at its lgmon entry with a ticket, in a tab opened during the click so no popup blocker stops it.
  const tab = {
    opener: {} as unknown,
    document: { title: '', body: { textContent: '' } },
    location: { href: '' },
  }
  const open = vi.spyOn(window, 'open').mockReturnValue(tab as unknown as Window)
  await userEvent.click(margin)
  expect(open).toHaveBeenCalledWith('', '_blank')
  expect(tab.opener).toBeNull()
  await waitFor(() =>
    expect(tab.location.href).toBe('https://margin.lfen.ink/lgmon#ticket=fake-body.fake-signature'),
  )
  open.mockRestore()
})
