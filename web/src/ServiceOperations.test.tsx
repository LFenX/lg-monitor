import { fireEvent, render, screen, waitFor, within } from '@testing-library/react'
import { beforeEach, expect, it, vi } from 'vitest'
import { API } from './api'
import { ServiceOperations } from './ServiceOperations'

let api: API,
  scope = 'admin'
vi.mock('./state', async (original) => ({
  ...(await original<typeof import('./state')>()),
  useApp: () => ({ api, scope }),
}))
const version = 'a'.repeat(64)
const policy = {
  enabled: true,
  version,
  services: [{ id: 'restart-demo', service: 'FakeService' }],
  reason: '虚构本机授权',
}

beforeEach(() => {
  sessionStorage.clear()
  HTMLDialogElement.prototype.showModal = function () {
    this.setAttribute('open', '')
  }
  HTMLDialogElement.prototype.close = function () {
    this.removeAttribute('open')
  }
})

it('requires an exact-service review and explicit per-request confirmation before the one POST', async () => {
  let body:
    { requestId: string; grantId: string; version: string; reason: string; confirmed: boolean } | undefined
  const fetcher = vi.fn(async (url: unknown, init?: RequestInit) => {
    if (init?.method === 'POST') {
      body = JSON.parse(String(init.body))
      return Response.json({
        ...body,
        collectorId: 'fake-host',
        service: 'FakeService',
        state: 'succeeded',
        startedAt: 1,
        finishedAt: 2,
        message: '夹具核对运行',
      })
    }
    return Response.json(
      String(url).includes('/collectors/')
        ? policy
        : {
            requestId: body?.requestId,
            state: 'succeeded',
            service: 'FakeService',
            startedAt: 1,
            finishedAt: 2,
            message: '夹具核对运行',
          },
    )
  })
  scope = 'admin'
  api = new API('fake-admin', fetcher as typeof fetch)
  const { unmount } = render(<ServiceOperations collector="fake-host" hostname="虚构主机" />)
  fireEvent.click(await screen.findByRole('button', { name: '准备重启 FakeService' }))
  const modal = within(screen.getByRole('dialog', { name: '确认重启服务' }))
  expect(modal.getByText('虚构主机', { exact: true })).toBeVisible()
  expect(modal.getByText('FakeService', { exact: true })).toBeVisible()
  expect(modal.getByRole('button', { name: '确认执行本次重启' })).toBeDisabled()
  fireEvent.change(modal.getByLabelText('服务重启原因'), { target: { value: '本人确认的虚构重启' } })
  expect(modal.getByRole('button', { name: '确认执行本次重启' })).toBeDisabled()
  fireEvent.click(modal.getByRole('checkbox'))
  fireEvent.click(modal.getByRole('button', { name: '确认执行本次重启' }))
  await screen.findByText('已核对运行')
  expect(body).toEqual({
    requestId: expect.stringMatching(/^[a-f0-9]{32}$/),
    grantId: 'restart-demo',
    version,
    reason: '本人确认的虚构重启',
    confirmed: true,
  })
  expect(Object.keys(body!)).not.toContain('service')
  expect(fetcher.mock.calls.filter(([, init]) => init?.method === 'POST')).toHaveLength(1)
  fireEvent.click(screen.getByRole('button', { name: '只读查询操作结果' }))
  await waitFor(() =>
    expect(fetcher.mock.calls.filter(([, init]) => init?.method === 'POST')).toHaveLength(1),
  )
  unmount()
})

it('keeps read access read only and never runs a disabled policy', async () => {
  scope = 'read'
  const fetcher = vi.fn(async () => Response.json(policy))
  api = new API('fake-read', fetcher as typeof fetch)
  const { unmount } = render(<ServiceOperations collector="fake-host" hostname="虚构主机" />)
  expect(await screen.findByRole('button', { name: '准备重启 FakeService' })).toBeDisabled()
  expect(fetcher.mock.calls).toHaveLength(1)
  unmount()
  scope = 'admin'
  api = new API('fake-admin', (async () =>
    Response.json({ enabled: false, services: [], version, reason: '远程服务操作未启用' })) as typeof fetch)
  const next = render(<ServiceOperations collector="fake-host" hostname="虚构主机" />)
  await screen.findByText('远程服务操作未启用')
  expect(screen.queryByRole('button', { name: '准备重启 FakeService' })).not.toBeInTheDocument()
  next.unmount()
})

it('retains the original request ID on an uncertain response and only queries when requested', async () => {
  let submitted = ''
  const fetcher = vi.fn(async (url: unknown, init?: RequestInit) => {
    if (init?.method === 'POST') {
      submitted = JSON.parse(String(init.body)).requestId
      return Response.json(
        { error: { code: 'operation_unknown', message: '结果未知，请查询原请求' } },
        { status: 409 },
      )
    }
    return Response.json(
      String(url).includes('/collectors/')
        ? policy
        : {
            requestId: submitted,
            state: 'unknown',
            service: 'FakeService',
            message: '需要本人核对',
            startedAt: 1,
            finishedAt: 2,
          },
    )
  })
  scope = 'admin'
  api = new API('fake-admin', fetcher as typeof fetch)
  const { unmount } = render(<ServiceOperations collector="fake-host" hostname="虚构主机" />)
  fireEvent.click(await screen.findByRole('button', { name: '准备重启 FakeService' }))
  fireEvent.change(screen.getByLabelText('服务重启原因'), { target: { value: '虚构确认' } })
  fireEvent.click(screen.getByRole('checkbox'))
  fireEvent.click(screen.getByRole('button', { name: '确认执行本次重启' }))
  await screen.findByText('结果未知，请查询原请求')
  fireEvent.click(within(screen.getByRole('dialog')).getByRole('button', { name: '关闭' }))
  expect(screen.getByLabelText('服务操作请求 ID')).toHaveValue(submitted)
  fireEvent.click(screen.getByRole('button', { name: '只读查询操作结果' }))
  await screen.findByText('结果未知，需核对')
  expect(fetcher.mock.calls.filter(([, init]) => init?.method === 'POST')).toHaveLength(1)
  unmount()
})
