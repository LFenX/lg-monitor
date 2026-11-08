import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { beforeEach, expect, it, vi } from 'vitest'
import { API } from './api'
import { AlertEventDetails } from './Alerts'
import type { Alert, AlertEvents } from './types'

let api: API
vi.mock('./state', () => ({ useApp: () => ({ api }), useResource: vi.fn() }))
const alert: Alert = {
  id: 'fake-alert',
  ruleId: 'down',
  collectorId: 'fake-host',
  targetId: 'fake-app',
  state: 'firing',
  severity: 'warning',
  startedAt: 1000000,
  resolvedAt: 0,
  summary: '虚构进程退出',
}
function page(message: string, cursor: string | null): AlertEvents {
  return {
    alertId: alert.id,
    from: 940000,
    to: 1060000,
    events: [
      {
        ts: 1000000,
        channel: 'Application',
        provider: 'Application Error',
        eventId: 1000,
        level: 2,
        recordId: message === '首条事件' ? '9007199254740993' : message,
        subject: 'fake.exe',
        message,
      },
    ],
    nextCursor: cursor,
  }
}
beforeEach(() => {
  HTMLDialogElement.prototype.showModal = function () {
    this.setAttribute('open', '')
  }
  HTMLDialogElement.prototype.close = function () {
    this.removeAttribute('open')
  }
})
it('paginates the full window with a read token, preserves record precision and refreshes late events', async () => {
  const requests: string[] = []
  let first = 0
  api = new API(
    'fake-read',
    vi.fn(async (url, init) => {
      requests.push(String(url))
      expect((init?.headers as Record<string, string>).Authorization).toBe('Bearer fake-read')
      if (String(url).includes('cursor=')) return Response.json(page('<script>第二页</script>', null))
      return Response.json(first++ ? page('迟到事件', null) : page('首条事件', 'cursor+safe/='))
    }),
  )
  const user = userEvent.setup()
  render(<AlertEventDetails alert={alert} close={vi.fn()} />)
  expect(await screen.findByText('首条事件')).toBeVisible()
  expect(screen.getByText(/记录 9007199254740993/)).toBeVisible()
  await user.click(screen.getByRole('button', { name: '加载更多事件' }))
  expect(await screen.findByText('<script>第二页</script>')).toBeVisible()
  expect(document.querySelector('.eventlog-list script')).toBeNull()
  expect(screen.getByText('首条事件')).toBeVisible()
  expect(requests[1]).toContain('cursor=cursor%2Bsafe%2F%3D')
  await user.click(screen.getByRole('button', { name: '刷新事件' }))
  expect(await screen.findByText('迟到事件')).toBeVisible()
  expect(screen.queryByText('首条事件')).not.toBeInTheDocument()
  expect(screen.queryByText('<script>第二页</script>')).not.toBeInTheDocument()
  expect(requests[2]).not.toContain('cursor=')
})
it('shows a legacy empty window and aborts requests when the detail dialog closes', async () => {
  let signal: AbortSignal | null | undefined
  api = new API(
    'fake-read',
    vi.fn(async (_url, init) => {
      signal = init?.signal
      return Response.json({ alertId: alert.id, from: null, to: null, events: [], nextCursor: null })
    }),
  )
  const view = render(<AlertEventDetails alert={alert} close={vi.fn()} />)
  expect(await screen.findByText('此告警没有进程或服务故障的原始采样时间，无法确定事件窗口。')).toBeVisible()
  await waitFor(() => expect(screen.getByRole('button', { name: '刷新事件' })).toBeEnabled())
  view.unmount()
  expect(signal?.aborted).toBe(true)
})
