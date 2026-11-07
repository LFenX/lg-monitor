import { act, render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, beforeEach, expect, it, vi } from 'vitest'
import { App } from './App'

let bootstrap: HTMLScriptElement
let requests: { path: string; init?: RequestInit }[]

it.each([
  { offset: 40000, message: '这台电脑的时钟比监控器慢 40 秒，请同步时间' },
  { offset: -300000, message: '这台电脑的时钟比监控器快 300 秒，请同步时间' },
])('shows and clears the host clock warning for $offset milliseconds', async ({ offset, message }) => {
  const user = userEvent.setup(),
    previous = globalThis.fetch
  let currentOffset = offset
  vi.stubGlobal(
    'fetch',
    vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const response = await previous(input, init)
      if (String(input).endsWith('/collectors')) {
        const collectors = await response.json()
        collectors[0].clockOffsetMs = currentOffset
        return Response.json(collectors, { headers: response.headers })
      }
      return response
    }),
  )
  render(<App />)
  await screen.findByText(message)
  currentOffset = 0
  await user.click(screen.getByRole('button', { name: '刷新数据' }))
  await waitFor(() => expect(screen.queryByText(message)).not.toBeInTheDocument())
})

it('does not switch credentials if the user closes a pending login', async () => {
  const user = userEvent.setup(),
    previous = globalThis.fetch
  let release: (() => void) | undefined, signal: AbortSignal | null | undefined
  vi.stubGlobal(
    'fetch',
    vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
      if (String((init?.headers as Record<string, string>)?.Authorization).includes('fake-pending')) {
        signal = init?.signal
        return new Promise<Response>((resolve) => {
          release = () => resolve(Response.json([], { headers: { 'X-Lgmon-Scope': 'config' } }))
        })
      }
      return previous(input, init)
    }),
  )
  render(<App />)
  await screen.findByRole('heading', { name: '运行概览' })
  await user.click(screen.getByRole('button', { name: '只读' }))
  await user.type(screen.getByLabelText('访问令牌'), 'fake-pending')
  await user.click(screen.getByRole('button', { name: '连接监控器' }))
  await waitFor(() => expect(release).toBeDefined())
  await user.click(screen.getByRole('button', { name: '关闭' }))
  expect(signal?.aborted).toBe(true)
  await act(async () => release!())
  expect(screen.getByRole('button', { name: '只读' })).toBeVisible()
  expect(sessionStorage.getItem('lgmon-token')).toBeNull()
})
beforeEach(() => {
  sessionStorage.clear()
  localStorage.clear()
  window.location.hash = '#/'
  bootstrap = document.createElement('script')
  bootstrap.id = 'lgmon-session'
  bootstrap.type = 'application/json'
  bootstrap.textContent = JSON.stringify({ token: 'fake-local-read', scope: 'read' })
  document.head.append(bootstrap)
  Object.defineProperties(HTMLDialogElement.prototype, {
    showModal: {
      configurable: true,
      value: function (this: HTMLDialogElement) {
        this.setAttribute('open', '')
      },
    },
    close: {
      configurable: true,
      value: function (this: HTMLDialogElement) {
        this.removeAttribute('open')
      },
    },
  })
  requests = []
  vi.stubGlobal(
    'fetch',
    vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const path = String(input)
      requests.push({ path, init })
      const editable = String((init?.headers as Record<string, string>)?.Authorization).includes(
          'fake-config',
        ),
        headers = { 'X-Lgmon-Scope': editable ? 'config' : 'read' }
      if (path.endsWith('/stream'))
        return new Response(new ReadableStream(), {
          headers: { ...headers, 'Content-Type': 'text/event-stream' },
        })
      if (path.endsWith('/collectors'))
        return Response.json(
          [
            {
              id: 'fake-host',
              hostname: '测试主机',
              version: 'fake-v1',
              online: true,
              connected: true,
              lastSeen: Date.now(),
            },
          ],
          { headers },
        )
      if (path.endsWith('/config/targets'))
        return Response.json({ targets: [{ id: 'web', name: '测试网站' }] }, { headers })
      if (path.endsWith('/config/notifiers'))
        return Response.json({ feishuApps: [], notifiers: [] }, { headers })
      if (path.endsWith('/notifications/pause'))
        return Response.json(
          { paused: false, startedAt: null, until: null, reason: null, by: null, suppressed: null },
          { headers },
        )
      if (path.endsWith('/targets'))
        return Response.json(
          [
            {
              collectorId: 'fake-host',
              targetId: 'web',
              ts: Date.now(),
              state: 'up',
              detail: { reason: '进程与端口正常' },
            },
          ],
          { headers },
        )
      if (path.includes('/alerts') && !path.endsWith('/ack'))
        return Response.json(
          [
            {
              id: 'fake-alert',
              ruleId: 'fake-rule',
              collectorId: 'fake-host',
              targetId: 'web',
              state: 'firing',
              severity: 'critical',
              summary: '虚构测试告警',
              startedAt: Date.now(),
              resolvedAt: 0,
            },
          ],
          { headers },
        )
      if (path.endsWith('/query'))
        return Response.json(
          {
            columns: ['collector_id', 'ts', 'cpu', 'memory_percent', 'memory_used', 'memory_total', 'uptime'],
            rows: [['fake-host', Date.now(), 0, 40, 1024, 4096, 10]],
            truncated: false,
          },
          { headers },
        )
      return Response.json({ ok: true, message: '成功' }, { headers })
    }),
  )
})
afterEach(() => {
  bootstrap.remove()
  vi.unstubAllGlobals()
})

it('shows paused status as read-only and keeps a failed resume visible before retry', async () => {
  const user = userEvent.setup(),
    previous = globalThis.fetch
  let paused = true,
    failed = false
  vi.stubGlobal(
    'fetch',
    vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      if (String(input).endsWith('/notifications/pause')) {
        const editable = String((init?.headers as Record<string, string>)?.Authorization).includes(
          'fake-config',
        )
        const headers = { 'X-Lgmon-Scope': editable ? 'config' : 'read' }
        if (init?.method === 'DELETE') {
          expect(editable).toBe(true)
          if (!failed) {
            failed = true
            return Response.json({ error: { message: '虚构恢复失败' } }, { status: 503, headers })
          }
          paused = false
        }
        return Response.json(
          {
            paused,
            startedAt: Date.now(),
            until: Date.now() + 3600000,
            reason: '',
            by: 'fake-config',
            suppressed: 2,
          },
          { headers },
        )
      }
      return previous(input, init)
    }),
  )
  render(<App />)
  const restore = await screen.findByRole('button', { name: '恢复通知' })
  expect(restore).toBeDisabled()
  await user.click(screen.getByRole('button', { name: '只读' }))
  await user.type(screen.getByLabelText('访问令牌'), 'fake-config')
  await user.click(screen.getByRole('button', { name: '连接监控器' }))
  await waitFor(() => expect(screen.getByRole('button', { name: '恢复通知' })).toBeEnabled())
  await user.click(screen.getByRole('button', { name: '恢复通知' }))
  await screen.findByText('虚构恢复失败')
  expect(screen.getByRole('button', { name: '恢复通知' })).toBeEnabled()
  await user.click(screen.getByRole('button', { name: '恢复通知' }))
  await waitFor(() => expect(screen.queryByRole('button', { name: '恢复通知' })).not.toBeInTheDocument())
})

it('shows real zero values, notification setup and a functioning theme control', async () => {
  const user = userEvent.setup()
  render(<App />)
  expect(await screen.findByRole('heading', { name: '运行概览' })).toBeVisible()
  expect(screen.getByRole('link', { name: /还没有设置告警通知/ })).toHaveAttribute(
    'href',
    '#/notifications/setup',
  )
  expect(screen.getByText('0.0%')).toBeVisible()
  expect(screen.getByRole('heading', { name: '测试主机' })).toBeVisible()
  await user.click(screen.getByRole('button', { name: '切换到深色' }))
  expect(document.documentElement.dataset.theme).toBe('dark')
  expect(requests.every((request) => !request.path.includes('fake-local-read'))).toBe(true)
})

it('keeps local read access read-only and enables actions only after a config credential is verified', async () => {
  const user = userEvent.setup()
  render(<App />)
  await screen.findByRole('heading', { name: '运行概览' })
  await user.click(
    within(screen.getByRole('navigation', { name: '主导航' })).getByRole('link', { name: /告警/ }),
  )
  const action = await screen.findByRole('button', { name: '确认' })
  expect(action).toBeDisabled()
  await user.click(screen.getByRole('button', { name: '只读' }))
  await user.type(screen.getByLabelText('访问令牌'), 'fake-config')
  await user.click(screen.getByRole('button', { name: '连接监控器' }))
  await waitFor(() => expect(screen.queryByRole('dialog')).not.toBeInTheDocument())
  await waitFor(() => expect(screen.getByRole('button', { name: '确认' })).toBeEnabled())
  await user.click(screen.getByRole('button', { name: '确认' }))
  await screen.findByText('已确认告警；本次故障的重复提醒已停止。')
  expect(
    requests.some(
      (request) => request.path === '/api/v1/alerts/fake-alert/ack' && request.init?.method === 'POST',
    ),
  ).toBe(true)
  expect(sessionStorage.getItem('lgmon-token')).toBe('fake-config')
})
