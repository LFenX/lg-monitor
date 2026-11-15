import { render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, beforeEach, expect, it, vi } from 'vitest'
import { App } from './App'
import { Beats } from './status'
import type { BoardView, Monitor, MonitorDetail } from './types'

let bootstrap: HTMLScriptElement
let scope = 'read'
let board: BoardView
let posted: { path: string; body: unknown }[]
let hasBoards: boolean
let createError: string

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
      if (init?.method === 'POST' && path.endsWith('/boards')) {
        const input = JSON.parse(String(init.body))
        posted.push({ path, body: input })
        if (createError) return Response.json({ error: { message: createError } }, { status: 409, headers })
        board = { ...boardWith([]), id: 'created', name: input.name, description: input.description }
        hasBoards = true
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
  expect(within(rows[0]).getByText('子服务 隧道 异常')).toBeVisible()
  expect(within(rows[0]).getByText('98.0%')).toHaveAttribute('data-state', 'down')
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

it('guides an empty board to an agent prompt that names the board', async () => {
  board = boardWith([])
  render(<App />)
  const guide = await screen.findByRole('region', { name: '空面板指引' })
  expect(within(guide).getByText(/board: main/)).toBeVisible()
  expect(within(guide).getByText(/【把这里换成你要监控的服务/).tagName).toBe('MARK')
  // The connection steps are copied too but folded, so the person reads only their request.
  await userEvent.click(within(guide).getByText(/复制时会在前面附上接入步骤/))
  expect(within(guide).getByText(/--elevate --json/)).toBeVisible()
  await userEvent.click(within(guide).getByText('手动接入（可选）'))
  expect(
    within(guide).getByText("& 'C:\\Fake\\lgmon.exe' agent connect --home 'C:\\Fake Home'", { exact: true }),
  ).toBeVisible()
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
  expect(agent).toHaveTextContent('board: main')
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

it('offers a complete agent setup and named board creation prompt before any board exists', async () => {
  scope = 'config'
  hasBoards = false
  window.location.hash = '#/'
  render(<App />)
  await screen.findByRole('heading', { name: '欢迎使用 lgmon' })
  const name = screen.getByRole('textbox', { name: '面板名称' })
  await userEvent.clear(name)
  await userEvent.type(name, '部门服务')
  expect(screen.getByText(/create_board 创建面板，name 使用 "部门服务"/)).toBeVisible()
  await userEvent.click(screen.getByText(/复制时会在前面附上接入步骤/))
  expect(screen.getByText(/--elevate --json/)).toBeVisible()
  expect(screen.getByText(/不需要让我手动创建/)).toBeVisible()
  expect(screen.getByText('手动接入（可选）')).toBeVisible()
})

it('offers the entered name to the agent and closes the dialog after successful creation', async () => {
  scope = 'config'
  render(<App />)
  await screen.findByRole('list', { name: '服务列表' })
  await userEvent.click(screen.getByRole('button', { name: '新建面板' }))
  const dialog = screen.getByRole('dialog', { name: '新建面板' })
  await userEvent.type(within(dialog).getByRole('textbox', { name: '名称' }), '数据应用部')
  await userEvent.type(within(dialog).getByRole('textbox', { name: '简介（可选）' }), '业务服务')
  const prompt = within(dialog).getByRole('region', { name: '交给 Agent 创建面板' })
  expect(prompt).toHaveTextContent('name 使用 "数据应用部"，description 使用 "业务服务"')
  expect(within(prompt).getByRole('button', { name: '交给 Agent 创建' })).toBeVisible()
  await userEvent.click(within(dialog).getByRole('button', { name: '新建' }))
  await waitFor(() => expect(screen.queryByRole('dialog', { name: '新建面板' })).not.toBeInTheDocument())
  expect(posted[0].body).toEqual({ name: '数据应用部', description: '业务服务' })
  await screen.findByRole('heading', { name: '数据应用部' })
  const empty = await screen.findByRole('region', { name: '空面板指引' })
  expect(empty).toHaveTextContent('board: created')
  expect(within(empty).getByRole('button', { name: '复制这段话' })).toBeVisible()
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
  expect(dialog).toHaveTextContent('board: main')
  expect(dialog).toHaveTextContent('--elevate --json')
  expect(within(dialog).getByRole('button', { name: '复制这段话' })).toBeVisible()
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
  expect(screen.getByText('出现异常')).toBeVisible()
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
  expect(posted).toEqual([{ path: '/api/v1/monitors/tunnel', body: { name: '外网隧道', parent: '' } }])
})
