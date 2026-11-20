import { render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, beforeEach, expect, it, vi } from 'vitest'
import { App } from './App'
import { avatarSrc } from './avatar'
import type { BoardView, LANOutgoing } from './types'

let bootstrap: HTMLScriptElement
let calls: { method: string; path: string; body?: unknown }[]
let outgoing: LANOutgoing[]
let revoked: boolean
let avatar: string
let remoteSlow: Promise<void> | undefined

const remoteBoard: BoardView = {
  id: 'main',
  name: '对方的服务',
  monitors: ['site'],
  summary: { services: 1, children: 0, up: 1, down: 0, unknown: 0, uptime: 100, state: 'up' },
  url: '',
  unassigned: [],
  items: [
    {
      id: 'site',
      name: '对方网站',
      watching: ['端口 80'],
      state: 'up',
      overall: 'up',
      hosts: [],
      rules: [],
      firing: 0,
      beats: Array(48).fill('up'),
      uptime: 100,
      latencyMs: 3,
      kind: 'port',
      children: [],
      depth: 0,
      depends: [{ id: 'db', relation: 'data' }],
    },
    {
      id: 'db',
      name: '对方数据库',
      watching: ['服务 fake-db'],
      state: 'up',
      overall: 'up',
      hosts: [],
      rules: [],
      firing: 0,
      beats: Array(48).fill('up'),
      uptime: 100,
      latencyMs: 2,
      kind: 'service',
      children: [],
      depth: 0,
      role: 'database',
      dependents: [{ id: 'site', relation: 'data' }],
    },
  ],
}

beforeEach(() => {
  sessionStorage.clear()
  localStorage.clear()
  window.location.hash = '#/lan'
  calls = []
  outgoing = []
  revoked = false
  avatar = 'fiora-3'
  remoteSlow = undefined
  bootstrap = document.createElement('script')
  bootstrap.id = 'lgmon-session'
  bootstrap.type = 'application/json'
  bootstrap.textContent = JSON.stringify({ token: 'fake-local', scope: 'read' })
  document.head.append(bootstrap)
  vi.stubGlobal(
    'fetch',
    vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const path = String(input).replace('/api/v1', '')
      const method = init?.method || 'GET'
      calls.push({ method, path, body: init?.body ? JSON.parse(String(init.body)) : undefined })
      const headers = { 'X-Lgmon-Scope': 'config' }
      if (path === '/stream')
        return new Response(new ReadableStream(), {
          headers: { ...headers, 'Content-Type': 'text/event-stream' },
        })
      if (path === '/lan')
        return Response.json(
          {
            visible: true,
            name: '我的电脑',
            avatar,
            hostname: 'fake-host',
            fingerprint: 'AAAA-BBBB-CCCC-DDDD',
            addresses: ['https://192.0.2.10:7445'],
            listening: true,
            pending: 1,
            viewers: 0,
          },
          { headers },
        )
      if (path === '/lan/remote' && remoteSlow) await remoteSlow
      if (path === '/lan/remote')
        return Response.json(
          outgoing.filter((item) => item.status === 'approved'),
          { headers },
        )
      if (path === '/lan/remote/lanout-1' && revoked)
        return Response.json(
          { error: { code: 'lan_forbidden', message: '对方已撤销授权，不能再查看这个面板；需要时重新申请' } },
          { status: 403, headers },
        )
      if (path === '/lan/remote/lanout-1/monitors/site')
        return Response.json(
          {
            monitor: { ...remoteBoard.items[0], hasDoc: true, description: '对方的官网' },
            lineage: [],
            children: [],
            boards: [{ id: 'main', name: '对方的服务' }],
            slots: Array.from({ length: 96 }, (_, index) => ({
              from: index * 900000,
              state: 'up',
              uptime: 100,
            })),
            days: Array.from({ length: 30 }, (_, index) => ({
              from: index * 86400000,
              state: 'up',
              uptime: 100,
            })),
            uptime24h: 100,
            uptime7d: 100,
            uptime30d: 100,
            latency: [],
            latencyNow: null,
            latencyAvg: null,
            events: [],
            incidents: [],
            rules: [
              {
                id: 'down',
                type: 'port_down',
                severity: 'critical',
                text: '端口不通持续 1 分钟后告警',
                notify: [],
              },
            ],
          },
          { headers },
        )
      if (path === '/lan/remote/lanout-1/monitors/site/doc')
        return Response.json({ id: 'site', markdown: '# 对方网站\n\n对方写的部署说明' }, { headers })
      if (path === '/lan/remote/lanout-1')
        return Response.json(
          {
            id: 'lanout-1',
            ownerName: '小王',
            ownerAvatar: 'huaji-18',
            address: 'https://192.0.2.20:7445',
            board: remoteBoard,
          },
          { headers },
        )
      if (path === '/lan/requests' && method === 'GET')
        return Response.json(
          {
            incoming: [
              {
                id: 'lanreq-1',
                boardId: 'main',
                boardName: '我的服务',
                deviceName: '小王',
                avatar: 'fiora-7',
                hostname: 'wang-pc',
                address: '192.0.2.20',
                code: '123 456',
                status: 'pending',
                createdAt: Date.now(),
              },
            ],
            outgoing,
          },
          { headers },
        )
      if (path === '/lan/requests' && method === 'POST') {
        outgoing = [
          {
            id: 'lanout-2',
            address: 'https://192.0.2.20:7445',
            ownerName: '小王',
            fingerprint: '1111222233334444' + '0'.repeat(48),
            boardId: 'web',
            boardName: '网站',
            code: '654 321',
            status: 'pending',
            createdAt: Date.now(),
          },
        ]
        return Response.json(outgoing[0], { headers })
      }
      if (path === '/lan/discover')
        return Response.json(
          [
            {
              name: '小王',
              address: 'https://192.0.2.20:7445',
              fingerprint: '1111222233334444',
              directory: {
                name: '小王',
                avatar: 'huaji-18',
                fingerprint: '1111222233334444' + '0'.repeat(48),
                boards: [{ id: 'web', name: '网站', description: '公司官网', services: 2, children: 1 }],
              },
            },
          ],
          { headers },
        )
      if (path === '/departments')
        return Response.json(
          {
            me: 'KEY-ME',
            pending: {},
            hosts: [
              {
                id: 'HOSTA',
                name: '小王',
                departments: [
                  {
                    id: 'd1',
                    name: '虚构研发部',
                    people: [
                      { key: 'KEY-WANG', name: '小王', role: 'leader' },
                      { key: 'KEY-ME', name: '我的电脑', role: 'leader' },
                    ],
                  },
                  {
                    id: 'd2',
                    name: '虚构前端组',
                    parent: 'HOSTA:d1',
                    people: [{ key: 'KEY-WANG', name: '小王', role: 'leader' }],
                  },
                ],
              },
            ],
          },
          { headers },
        )
      if (path.startsWith('/departments/') && path.endsWith('/requests'))
        return Response.json(
          { requests: [{ id: 'r1', department: 'd1', key: 'KEY-LI', name: '小李', createdAt: 1 }] },
          { headers },
        )
      if (path.startsWith('/departments/')) return Response.json({ role: 'pending' }, { headers })
      if (path === '/lan/grants') return Response.json([], { headers })
      if (path === '/profile' && method === 'PUT') {
        avatar = 'fiora-4'
        return Response.json({ nickname: '我的电脑', avatar, createdAt: 1, updatedAt: 2 }, { headers })
      }
      if (path.endsWith('/approve')) return Response.json({ id: 'lanreq-1', status: 'approved' }, { headers })
      if (
        path === '/collectors' ||
        path === '/targets' ||
        path === '/alerts' ||
        path === '/rule-errors' ||
        path === '/monitors' ||
        path === '/dashboards' ||
        path === '/boards'
      )
        return Response.json([], { headers })
      if (path === '/config/targets') return Response.json({ targets: [] }, { headers })
      if (path === '/config/notifiers') return Response.json({ notifiers: [] }, { headers })
      if (path === '/notifications/pause') return Response.json({ paused: false, until: null }, { headers })
      if (path === '/query') return Response.json({ columns: [], rows: [], truncated: false }, { headers })
      return Response.json({}, { headers })
    }),
  )
})
afterEach(() => {
  bootstrap.remove()
  vi.unstubAllGlobals()
})

it('shows the pairing code of an incoming request and approves it', async () => {
  render(<App />)
  const section = await screen.findByRole('region', { name: '收到的申请' })
  expect(within(section).getByLabelText('配对码 123 456')).toBeVisible()
  expect(within(section).getByText('小王')).toBeVisible()
  // People are their nickname and avatar; the computer's name stays out of sight.
  expect(within(section).getByText(/192\.0\.2\.20/)).toBeVisible()
  expect(screen.queryByText(/wang-pc|fake-host/)).not.toBeInTheDocument()
  expect(screen.getByRole('switch', { name: '让内网看到我的面板' })).toHaveAttribute('aria-checked', 'true')
  expect(screen.getByText('https://192.0.2.10:7445')).toBeVisible()
  await userEvent.click(within(section).getByRole('button', { name: '配对码一致，同意' }))
  await waitFor(() =>
    expect(
      calls.some((call) => call.method === 'POST' && call.path === '/lan/requests/lanreq-1/approve'),
    ).toBe(true),
  )
  // The sidebar shows how many requests wait.
  expect(within(screen.getByRole('navigation', { name: '主导航' })).getByText('1')).toBeVisible()
})

it('finds a nearby board and asks to view it with the fingerprint seen in discovery', async () => {
  render(<App />)
  const nearby = await screen.findByRole('region', { name: '附近的电脑' })
  const peer = await within(nearby).findByRole('article', { name: '小王' })
  expect(within(peer).getByText('网站')).toBeVisible()
  expect(peer.querySelector('img.lm-avatar')).toHaveAttribute('src', avatarSrc('huaji-18'))
  expect(within(peer).getByText(/公司官网/)).toBeVisible()
  await userEvent.click(within(peer).getByRole('button', { name: '申请查看' }))
  await waitFor(() =>
    expect(calls.find((call) => call.method === 'POST' && call.path === '/lan/requests')?.body).toEqual({
      address: 'https://192.0.2.20:7445',
      boardId: 'web',
      fingerprint: '1111222233334444',
    }),
  )
  expect(await within(peer).findByLabelText('等待对方同意，配对码 654 321')).toBeVisible()
})

it('shows an approved remote board read-only from the sidebar', async () => {
  outgoing = [
    {
      id: 'lanout-1',
      address: 'https://192.0.2.20:7445',
      ownerName: '小王',
      ownerAvatar: 'huaji-18',
      fingerprint: 'F'.repeat(64),
      boardId: 'main',
      boardName: '对方的服务',
      code: '111 222',
      status: 'approved',
      createdAt: Date.now(),
    },
  ]
  window.location.hash = '#/lan/remote/lanout-1'
  render(<App />)
  expect(await screen.findByRole('heading', { name: '对方的服务' })).toBeVisible()
  expect(screen.getByText(/来自 小王 · 只读/)).toBeVisible()
  // Other people's boards sit under the person, so a long name never runs into the board's.
  const others = await screen.findByRole('navigation', { name: '别人的面板' })
  const person = within(others).getByRole('group', { name: '小王 的面板' })
  expect(person.querySelector('img.lm-avatar')).toHaveAttribute('src', avatarSrc('huaji-18'))
  expect(within(person).getByRole('link', { name: /对方的服务/ })).toHaveAttribute(
    'href',
    '#/lan/remote/lanout-1',
  )
  const list = screen.getByRole('list', { name: '服务列表' })
  // Shared services open their read-only pages here, not the owner's own pages.
  expect(within(list).getByRole('link', { name: /对方网站/ })).toHaveAttribute(
    'href',
    '#/lan/remote/lanout-1/monitors/site',
  )
  // The viewer sees how the shared services depend on each other.
  await userEvent.click(screen.getByRole('button', { name: '关系' }))
  const map = await screen.findByRole('figure', { name: '对方的服务 的依赖关系' })
  expect(within(map).getByRole('link', { name: /对方数据库/ })).toHaveAttribute(
    'href',
    '#/lan/remote/lanout-1/monitors/db',
  )
  expect(map.querySelectorAll('.dm-label').length).toBeGreaterThan(0)
  await userEvent.click(screen.getByRole('button', { name: '列表' }))
  // The sidebar folds the shared board open to its services and their health.
  await userEvent.click(within(person).getByRole('button', { name: '展开对方的服务的服务' }))
  const folded = await within(person).findByRole('list', { name: '对方的服务 的服务' })
  expect(within(folded).getByRole('link', { name: /对方网站/ })).toHaveTextContent('100%')
  // After the owner revokes, the last copy disappears instead of lingering under an error.
  revoked = true
  await userEvent.click(screen.getByRole('button', { name: '刷新数据' }))
  expect(await screen.findByText(/对方已撤销授权/)).toBeVisible()
  expect(screen.queryByRole('heading', { name: '对方的服务' })).not.toBeInTheDocument()
  expect(screen.queryByText('对方网站')).not.toBeInTheDocument()
})

it('gives this computer an account: click the avatar for another one, or rename it', async () => {
  render(<App />)
  const account = await screen.findByRole('region', { name: '我的账户' })
  expect(within(account).getByText('我的电脑')).toBeVisible()
  expect(account.querySelector('img.lm-avatar')).toHaveAttribute('src', avatarSrc('fiora-3'))
  await userEvent.click(within(account).getByRole('button', { name: '随机换一个头像' }))
  await waitFor(() =>
    expect(calls.find((call) => call.method === 'PUT' && call.path === '/profile')?.body).toEqual({
      shuffle: true,
    }),
  )
  await userEvent.click(within(account).getByRole('button', { name: '改昵称' }))
  const field = within(account).getByRole('textbox', { name: '昵称' })
  await userEvent.clear(field)
  await userEvent.type(field, '小李')
  await userEvent.click(within(account).getByRole('button', { name: '保存' }))
  await waitFor(() =>
    expect(calls.filter((call) => call.method === 'PUT' && call.path === '/profile').at(-1)?.body).toEqual({
      nickname: '小李',
    }),
  )
})

it('shows a new avatar as soon as it is saved, without waiting for the boards of colleagues', async () => {
  render(<App />)
  const account = await screen.findByRole('region', { name: '我的账户' })
  await waitFor(() =>
    expect(account.querySelector('img.lm-avatar')).toHaveAttribute('src', avatarSrc('fiora-3')),
  )
  // Reading other people's boards goes over the network and can take seconds; the avatar must not wait for it.
  let answer = () => {}
  remoteSlow = new Promise((resolve) => (answer = resolve))
  await userEvent.click(within(account).getByRole('button', { name: '随机换一个头像' }))
  await waitFor(
    () => expect(account.querySelector('img.lm-avatar')).toHaveAttribute('src', avatarSrc('fiora-4')),
    {
      timeout: 300,
    },
  )
  // Nowhere on the page still shows the old one.
  expect(document.querySelector(`img.lm-avatar[src="${avatarSrc('fiora-3')}"]`)).toBeNull()
  answer()
})

it('shows the small window the boards other people share, under their avatar, with live health', async () => {
  outgoing = [
    {
      id: 'lanout-1',
      address: 'https://192.0.2.20:7445',
      ownerName: '小王',
      ownerAvatar: 'huaji-18',
      fingerprint: 'F'.repeat(64),
      boardId: 'main',
      boardName: '对方的服务',
      code: '111 222',
      status: 'approved',
      createdAt: Date.now(),
    },
  ]
  window.location.hash = '#/start'
  render(<App />)
  const shared = await screen.findByRole('region', { name: '别人的面板' })
  expect(within(shared).getByText('小王')).toBeVisible()
  expect(shared.querySelector('img.lm-avatar')).toHaveAttribute('src', avatarSrc('huaji-18'))
  const row = within(shared).getByRole('link', { name: /对方的服务/ })
  expect(row).toHaveAttribute('href', '#/lan/remote/lanout-1')
  expect(row).toHaveAttribute('target', '_blank')
  expect(await within(row).findByText('100%')).toBeVisible()
  // 波西米亚 is the card's mark on the left; the agents that take the message stay on the right.
  const card = screen.getByRole('region', { name: '有事交给 Agent' })
  expect(card.querySelector('.agent-head .agent-mark .bh')).not.toBeNull()
  expect(card.querySelectorAll('.agent-works .agent-icon').length).toBeGreaterThan(3)
  // This computer's avatar sits in the corner; a click opens who this is and what can be done.
  await userEvent.click(screen.getByRole('button', { name: '我的账户：我的电脑' }))
  const menu = screen.getByRole('menu', { name: '我的账户' })
  expect(menu).toHaveTextContent('我的电脑')
  expect(await within(menu).findByText('虚构研发部')).toBeVisible()
  expect(menu).toHaveTextContent('内网可见')
  expect(within(menu).getByRole('menuitem', { name: /内网与部门/ })).toHaveAttribute('href', '#/lan')
  expect(calls.some((call) => call.method === 'PUT' && call.path === '/profile')).toBe(false)
  await userEvent.click(within(menu).getByRole('menuitem', { name: '随机换一个头像' }))
  await waitFor(() =>
    expect(calls.find((call) => call.method === 'PUT' && call.path === '/profile')?.body).toEqual({
      shuffle: true,
    }),
  )
  expect(screen.queryByText(/fake-host/)).not.toBeInTheDocument()
})

it('opens a shared service read-only: its details and document, without anything that edits', async () => {
  outgoing = [
    {
      id: 'lanout-1',
      address: 'https://192.0.2.20:7445',
      ownerName: '小王',
      ownerAvatar: 'huaji-18',
      fingerprint: 'F'.repeat(64),
      boardId: 'main',
      boardName: '对方的服务',
      code: '111 222',
      status: 'approved',
      createdAt: Date.now(),
    },
  ]
  window.location.hash = '#/lan/remote/lanout-1/monitors/site'
  render(<App />)
  expect(await screen.findByRole('heading', { name: '对方网站' })).toBeVisible()
  expect(screen.getByRole('navigation', { name: '位置' })).toHaveTextContent('小王 的「对方的服务」')
  expect(screen.getByText('只读')).toBeVisible()
  expect(screen.queryByRole('button', { name: '添加子服务' })).not.toBeInTheDocument()
  expect(screen.queryByRole('button', { name: '更多操作' })).not.toBeInTheDocument()
  expect(await screen.findByText('对方写的部署说明')).toBeVisible()
  expect(screen.queryByRole('button', { name: '编辑' })).not.toBeInTheDocument()
  expect(calls.some((call) => call.path === '/lan/remote/lanout-1/monitors/site/doc')).toBe(true)
})

it('shows departments as a tree on the LAN page, and a leader approves who asks to join', async () => {
  render(<App />)
  const section = await screen.findByRole('region', { name: '部门' })
  expect(await within(section).findByText('我在：虚构研发部')).toBeVisible()
  const child = within(section).getByText('虚构前端组').closest('li')!
  expect(child).toHaveStyle({ '--depth': '1' })
  await userEvent.click(within(child).getByRole('button', { name: '申请加入' }))
  await waitFor(() =>
    expect(calls.some((call) => call.method === 'POST' && call.path === '/departments/HOSTA/d2/join')).toBe(
      true,
    ),
  )
  const mine = within(section).getByText('虚构研发部').closest('li')!
  await userEvent.click(within(mine).getByRole('button', { name: '加入申请' }))
  const asked = await within(mine).findByRole('list', { name: '虚构研发部 的加入申请' })
  await userEvent.click(within(asked).getByRole('button', { name: '同意' }))
  await waitFor(() =>
    expect(calls.find((call) => call.path === '/departments/HOSTA/d1/decide')?.body).toEqual({
      request: 'r1',
      approve: true,
    }),
  )
  // A leader edits the department after it exists: its name, its avatar and where it hangs, never under its own branch.
  await userEvent.click(within(mine).getByRole('button', { name: '编辑' }))
  const dialog = await screen.findByRole('dialog', { name: '编辑部门「虚构研发部」' })
  const under = within(dialog).getByRole('combobox', { name: '上级部门' })
  expect(
    within(under)
      .getAllByRole('option')
      .map((option) => option.textContent),
  ).toEqual(['没有（顶层部门）'])
  await userEvent.clear(within(dialog).getByRole('textbox', { name: '部门名称' }))
  await userEvent.type(within(dialog).getByRole('textbox', { name: '部门名称' }), '虚构研发中心')
  await userEvent.click(within(dialog).getByRole('button', { name: '随机换一个' }))
  await userEvent.click(within(dialog).getByRole('button', { name: '保存' }))
  await waitFor(() => {
    const body = calls.find((call) => call.path === '/departments/HOSTA/d1/edit')?.body as Record<
      string,
      string
    >
    expect(body).toMatchObject({ name: '虚构研发中心', parent: '' })
    expect(body.avatar).toMatch(/^(fiora|huaji)-/)
  })
})

it("arranges other people's boards in the sidebar by the department their owner is in", async () => {
  outgoing = [
    {
      id: 'lanout-1',
      address: 'https://192.0.2.20:7445',
      ownerName: '小王',
      ownerAccount: 'KEY-WANG',
      fingerprint: 'F'.repeat(64),
      boardId: 'main',
      boardName: '对方的服务',
      code: '111 222',
      status: 'approved',
      createdAt: Date.now(),
    },
  ]
  render(<App />)
  const others = await screen.findByRole('navigation', { name: '别人的面板' })
  // 小王 leads both; the deeper department places him.
  expect(await within(others).findByRole('group', { name: '虚构前端组' })).toHaveTextContent('小王')
})
