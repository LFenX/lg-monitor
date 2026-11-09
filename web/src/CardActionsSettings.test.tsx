import { fireEvent, render, screen, waitFor } from '@testing-library/react'
import { expect, it, vi } from 'vitest'
import { API } from './api'
import { CardActionsSettings } from './CardActionsSettings'

let api: API,
  scope = 'admin'
const refresh = vi.fn()
vi.mock('./state', async (original) => ({
  ...(await original<typeof import('./state')>()),
  useApp: () => ({ api, scope, refresh }),
}))
const channels = [{ id: 'fake-channel', type: 'feishu_app', chatName: '虚构通知群' }]

it('keeps personal recipients outside group member delegation', () => {
  const fetcher = vi.fn()
  api = new API('fake-admin', fetcher as typeof fetch)
  const { unmount } = render(
    <CardActionsSettings channels={[{ id: 'personal', type: 'feishu_app', openId: 'ou_fake_person' }]} />,
  )
  expect(screen.getByText('先接入飞书应用通知群')).toBeInTheDocument()
  expect(screen.queryByRole('button', { name: '授权卡片操作' })).not.toBeInTheDocument()
  expect(fetcher).not.toHaveBeenCalled()
  unmount()
})

it('shows delegation scope and uses only the dedicated administrator switch', async () => {
  let enabled = false
  const fetcher = vi.fn(async (_: unknown, init?: RequestInit) => {
    if (init?.method === 'PUT') enabled = JSON.parse(String(init.body)).enabled
    return Response.json({
      notifier: 'fake-channel',
      app: 'fake-app',
      enabled,
      state: enabled ? 'connected' : 'disabled',
      updatedAt: 1,
    })
  })
  scope = 'admin'
  api = new API('fake-admin', fetcher as typeof fetch)
  const { unmount } = render(<CardActionsSettings channels={channels} />)
  await screen.findByText('未授权')
  expect(screen.getByText(/其他告警组不受影响/)).toBeVisible()
  fireEvent.click(screen.getByRole('button', { name: '启用卡片操作' }))
  await screen.findByText('已授权 · 已连接')
  await waitFor(() => expect(screen.getByRole('button', { name: '撤销卡片操作' })).toBeEnabled())
  fireEvent.click(screen.getByRole('button', { name: '撤销卡片操作' }))
  await screen.findByText('未授权')
  expect(
    fetcher.mock.calls.filter(([, init]) => init?.method === 'PUT').map(([, init]) => init?.body),
  ).toEqual(['{"enabled":true}', '{"enabled":false}'])
  expect(fetcher.mock.calls.every(([url]) => url === '/api/v1/notifiers/fake-channel/card-actions')).toBe(
    true,
  )
  unmount()
})

it('keeps read access read only and aborts polling when leaving', async () => {
  scope = 'read'
  const fetcher = vi.fn(async () =>
    Response.json({ notifier: 'fake-channel', enabled: true, state: 'connected' }),
  )
  api = new API('fake-read', fetcher as typeof fetch)
  const { unmount } = render(<CardActionsSettings channels={channels} />)
  await screen.findByText('已授权 · 已连接')
  expect(screen.getByRole('button', { name: '撤销卡片操作' })).toBeDisabled()
  unmount()
  expect(fetcher.mock.calls).toHaveLength(1)
})
