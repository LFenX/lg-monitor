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
            avatar: 'fiora-3',
            hostname: 'fake-host',
            fingerprint: 'AAAA-BBBB-CCCC-DDDD',
            addresses: ['https://192.0.2.10:7445'],
            listening: true,
            pending: 1,
            viewers: 0,
          },
          { headers },
        )
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
      if (path === '/lan/grants') return Response.json([], { headers })
      if (path === '/profile' && method === 'PUT')
        return Response.json(
          { nickname: '我的电脑', avatar: 'fiora-4', createdAt: 1, updatedAt: 2 },
          { headers },
        )
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
  expect(within(list).getByText('对方网站')).toBeVisible()
  expect(within(list).queryByRole('link')).not.toBeInTheDocument()
  // The viewer sees how the shared services depend on each other, without links into the owner's pages.
  await userEvent.click(screen.getByRole('button', { name: '关系' }))
  const map = await screen.findByRole('figure', { name: '对方的服务 的依赖关系' })
  expect(within(map).getByText('对方数据库')).toBeVisible()
  expect(within(map).queryByRole('link')).not.toBeInTheDocument()
  expect(map.querySelectorAll('.dm-label').length).toBeGreaterThan(0)
  await userEvent.click(screen.getByRole('button', { name: '列表' }))
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
  // The agent card is signed by 波西米亚 instead of a row of logos.
  const card = screen.getByRole('region', { name: '有事交给 Agent' })
  expect(card.querySelector('.agent-persona .bh')).not.toBeNull()
  expect(card.querySelector('.agent-icon')).toBeNull()
  // This computer's avatar sits in the corner; a click picks another one.
  await userEvent.click(screen.getByRole('button', { name: '随机换一个头像' }))
  await waitFor(() =>
    expect(calls.find((call) => call.method === 'PUT' && call.path === '/profile')?.body).toEqual({
      shuffle: true,
    }),
  )
  expect(screen.queryByText(/fake-host/)).not.toBeInTheDocument()
})
