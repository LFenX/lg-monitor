import { fireEvent, render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { expect, it, vi } from 'vitest'
import { API } from './api'
import { LicenseGate, useTripleClick } from './license'

function Mark() {
  const triple = useTripleClick()
  return <button onClick={triple}>标志</button>
}

function service(state: string, sent: { path: string; body?: Record<string, unknown> }[]) {
  return new API(
    'fake',
    vi.fn(async (url: unknown, init?: RequestInit) => {
      const path = String(url).replace('/api/v1', '')
      const body = init?.body ? JSON.parse(String(init.body)) : undefined
      sent.push({ path, body })
      if (path === '/license' && !body) return Response.json({ enforced: state !== 'off', state })
      if (path === '/license')
        return body.key === 'LGMON-AAAAA-BBBBB-CCCCC-DDDDD'
          ? Response.json({ enforced: true, state: 'active', label: '虚构同事' })
          : Response.json(
              { error: { code: 'license_rejected', message: '这个内测密钥不存在，请核对后重新输入' } },
              { status: 400 },
            )
      if (path === '/license/admin' && body.action === 'login')
        return body.password === '虚构管理员密码123'
          ? Response.json({ session: 'fake-session', expiresAt: 1 })
          : Response.json({ error: { message: '密码不对' } }, { status: 401 })
      if (path === '/license/admin' && body.action === 'keys')
        return Response.json({
          keys: [
            {
              id: 'k_1',
              label: '小王的笔记本',
              createdAt: 1,
              expiresAt: 0,
              maxDevices: 1,
              revokedAt: 0,
              state: 'active',
              devices: [{ id: 'dev-1', name: '小王', version: '0.13.2', firstSeen: 1, lastSeen: 2 }],
            },
          ],
        })
      if (path === '/license/admin' && body.action === 'create')
        return Response.json({ key: 'LGMON-NEW01-NEW02-NEW03-NEW04', record: {} })
      return Response.json({})
    }) as unknown as typeof fetch,
  )
}

it('asks for a beta key in a gated build and lets the app through once it is accepted', async () => {
  const sent: { path: string; body?: Record<string, unknown> }[] = []
  const reload = vi.fn()
  Object.defineProperty(window, 'location', { configurable: true, value: { ...window.location, reload } })
  render(
    <LicenseGate api={service('missing', sent)}>
      <p>面板内容</p>
    </LicenseGate>,
  )
  const card = await screen.findByRole('main', { name: '输入内测密钥' })
  expect(screen.queryByText('面板内容')).not.toBeInTheDocument()
  const field = within(card).getByLabelText('内测密钥')
  // Whatever is typed or pasted is shaped into the key's form.
  await userEvent.type(field, 'lgmon aaaaa bbbbb ccccc ddddd')
  expect(field).toHaveValue('LGMON-AAAAA-BBBBB-CCCCC-DDDDD')
  await userEvent.click(within(card).getByRole('button', { name: '激活这台电脑' }))
  await waitFor(() => expect(reload).toHaveBeenCalled())
  expect(sent.at(-1)).toEqual({ path: '/license', body: { key: 'LGMON-AAAAA-BBBBB-CCCCC-DDDDD' } })
})

it('stays out of the way in an ordinary build, and opens the console on a triple click', async () => {
  const sent: { path: string; body?: Record<string, unknown> }[] = []
  render(
    <LicenseGate api={service('off', sent)}>
      <Mark />
    </LicenseGate>,
  )
  const mark = await screen.findByRole('button', { name: '标志' })
  fireEvent.click(mark)
  fireEvent.click(mark)
  expect(screen.queryByRole('dialog', { name: '内测管理' })).not.toBeInTheDocument()
  fireEvent.click(mark)
  const console = await screen.findByRole('dialog', { name: '内测管理' })
  await userEvent.type(within(console).getByLabelText('管理员密码'), '错误')
  await userEvent.click(within(console).getByRole('button', { name: '登录' }))
  expect(await within(console).findByRole('alert')).toHaveTextContent('密码不对')
  await userEvent.clear(within(console).getByLabelText('管理员密码'))
  await userEvent.type(within(console).getByLabelText('管理员密码'), '虚构管理员密码123')
  await userEvent.click(within(console).getByRole('button', { name: '登录' }))
  expect(await within(console).findByText('小王的笔记本')).toBeVisible()
  await userEvent.type(within(console).getByPlaceholderText('例如：小王的笔记本'), '小李')
  await userEvent.click(within(console).getByRole('button', { name: '生成密钥' }))
  expect(await within(console).findByText('LGMON-NEW01-NEW02-NEW03-NEW04')).toBeVisible()
  expect(sent.find((item) => item.body?.action === 'create')?.body).toEqual({
    session: 'fake-session',
    action: 'create',
    label: '小李',
    days: 7,
    maxDevices: 1,
  })
  await userEvent.click(within(console).getByRole('button', { name: '电脑' }))
  expect(within(console).getByText('小王')).toBeVisible()
})
