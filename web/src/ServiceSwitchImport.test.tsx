import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { beforeEach, expect, it, vi } from 'vitest'
import { API } from './api'
import { ServiceSwitchImport } from './ServiceSwitchImport'
import type { Collector, Scope } from './types'

let api: API
let scope: Scope
const refresh = vi.fn()
vi.mock('./state', () => ({ useApp: () => ({ api, scope, refresh, now: Date.now }) }))
const host = { id: 'fake-host', hostname: '虚构主机' } as Collector
const preview = {
  collector: { id: host.id, hostname: host.hostname },
  sha256: 'a'.repeat(64),
  targetsVersion: 'b'.repeat(64),
  readAt: 2000000000000,
  items: [
    {
      ref: 'app:fake',
      kind: 'app',
      name: '虚构博客',
      autostart: 'off',
      action: 'create',
      targetId: 'fake',
      coveredBy: null,
      reason: null,
      target: { id: 'fake', collector: host.id, match: { port: 3000 } },
    },
    {
      ref: 'app:gone',
      kind: 'app',
      name: '旧目标',
      autostart: '',
      action: 'missing',
      targetId: 'gone',
      coveredBy: null,
      reason: '这次源文件中已不存在，默认保留',
      target: null,
    },
  ],
}
beforeEach(() => {
  scope = 'read'
  refresh.mockClear()
  HTMLDialogElement.prototype.showModal = function () {
    this.setAttribute('open', '')
  }
  HTMLDialogElement.prototype.close = function () {
    this.removeAttribute('open')
  }
})
it('previews with read permission, shows complete targets and keeps saving disabled', async () => {
  const fetcher = vi.fn(async () => Response.json(preview))
  api = new API('fake-read', fetcher)
  const user = userEvent.setup()
  render(<ServiceSwitchImport host={host} />)
  await user.click(screen.getByRole('button', { name: '从服务开关导入' }))
  expect(await screen.findByText('虚构博客')).toBeVisible()
  expect(screen.getByText('开机不自动启动')).toBeVisible()
  expect(screen.getByLabelText('导入 虚构博客')).toBeChecked()
  expect(screen.getByLabelText(/一并删除所有/)).not.toBeChecked()
  await user.click(screen.getByText('查看将保存的完整目标'))
  expect(screen.getByText(/"port": 3000/)).toBeVisible()
  expect(screen.getByRole('button', { name: '确认并保存所选目标' })).toBeDisabled()
  expect(screen.getByText(/当前为只读权限/)).toBeVisible()
  expect(fetcher).toHaveBeenCalledTimes(1)
})
it('saves only explicitly selected refs, retains missing by default, and reloads after success', async () => {
  scope = 'config'
  const bodies: unknown[] = []
  api = new API(
    'fake-config',
    vi.fn(async (_url, init) => {
      if (init?.method === 'POST') {
        bodies.push(JSON.parse(String(init.body)))
        return Response.json({ created: ['fake'], updated: [], removed: [], targetsVersion: 'c'.repeat(64) })
      }
      return Response.json(preview)
    }),
  )
  const user = userEvent.setup()
  render(<ServiceSwitchImport host={host} />)
  await user.click(screen.getByRole('button', { name: '从服务开关导入' }))
  expect(await screen.findByText('虚构博客')).toBeVisible()
  await waitFor(() => expect(screen.getByRole('button', { name: '确认并保存所选目标' })).toBeEnabled())
  await user.click(screen.getByRole('button', { name: '确认并保存所选目标' }))
  await waitFor(() => expect(bodies).toHaveLength(1), { timeout: 4000 })
  expect(bodies[0]).toEqual({
    sha256: preview.sha256,
    targetsVersion: preview.targetsVersion,
    refs: ['app:fake'],
    removeMissing: false,
    publicProbes: true,
  })
  expect(await screen.findByText(/已创建 1 个/)).toBeVisible()
  expect(refresh).toHaveBeenCalledTimes(1)
})
it('shows version conflicts without saving or replacing the preview silently', async () => {
  scope = 'config'
  api = new API(
    'fake-config',
    vi.fn(async (_url, init) =>
      init?.method === 'POST'
        ? Response.json(
            { error: { code: 'config_changed', message: '目标配置已变化，请重新预览并确认' } },
            { status: 409 },
          )
        : Response.json(preview),
    ),
  )
  const user = userEvent.setup()
  render(<ServiceSwitchImport host={host} />)
  await user.click(screen.getByRole('button', { name: '从服务开关导入' }))
  await screen.findByText('虚构博客')
  await waitFor(() => expect(screen.getByRole('button', { name: '确认并保存所选目标' })).toBeEnabled())
  await user.click(screen.getByRole('button', { name: '确认并保存所选目标' }))
  expect(await screen.findByText('目标配置已变化，请重新预览并确认', {}, { timeout: 4000 })).toBeVisible()
  expect(refresh).not.toHaveBeenCalled()
})
