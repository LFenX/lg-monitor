import { render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, beforeEach, expect, it, vi } from 'vitest'
import { App } from './App'
import type { BoardView, LANOutgoing } from './types'

let bootstrap: HTMLScriptElement
let calls: { method: string; path: string; body?: unknown }[]
let outgoing: LANOutgoing[]

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
    },
  ],
}

beforeEach(() => {
  sessionStorage.clear()
  localStorage.clear()
  window.location.hash = '#/lan'
  calls = []
  outgoing = []
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
      if (path === '/lan/remote/lanout-1')
        return Response.json(
          { id: 'lanout-1', ownerName: '小王', address: 'https://192.0.2.20:7445', board: remoteBoard },
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
                deviceName: '小王的电脑',
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
                hostname: 'wang-pc',
                fingerprint: '1111222233334444' + '0'.repeat(48),
                boards: [{ id: 'web', name: '网站', description: '公司官网', services: 2, children: 1 }],
              },
            },
          ],
          { headers },
        )
      if (path === '/lan/grants') return Response.json([], { headers })
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
  expect(within(section).getByText('小王的电脑')).toBeVisible()
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
  const list = screen.getByRole('list', { name: '服务列表' })
  expect(within(list).getByText('对方网站')).toBeVisible()
  expect(within(list).queryByRole('link')).not.toBeInTheDocument()
  expect(await screen.findByRole('navigation', { name: '别人的面板' })).toHaveTextContent('对方的服务')
})
