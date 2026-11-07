import { act, render, screen, waitFor } from '@testing-library/react'
import { expect, it, vi } from 'vitest'
import { API } from './api'
import { FeishuInspection, officialFeishuURL } from './FeishuInspection'

let api: API
vi.mock('./state', async (original) => ({
  ...(await original<typeof import('./state')>()),
  useApp: () => ({ api }),
}))

it('polls missing permissions and discovers a newly visible group without showing technical identifiers', async () => {
  let checks = 0
  const app = { id: 'fake-app', domain: 'feishu' }
  const fetcher = vi.fn(async () => {
    checks++
    return Response.json({
      botName: '虚构机器人',
      steps: [
        { step: 'credentials', status: 'ok', message: '有效' },
        { step: 'bot', status: 'ok', message: '已启用' },
        {
          step: 'permissions',
          status: checks === 1 ? 'action_required' : 'ok',
          url: 'https://open.feishu.cn/app/cli_fake/auth?q=fake',
          permissions: [
            {
              name: 'im:message:send_as_bot',
              description: '发送告警卡片',
              required: true,
              granted: checks > 1,
            },
            { name: 'im:chat:read', description: '读取群', required: true, granted: checks > 1 },
          ],
        },
        checks === 1
          ? { step: 'chat', status: 'action_required' }
          : {
              step: 'chat',
              status: 'ok',
              chats: [{ id: 'oc_fake_hidden', name: '虚构新群', memberCount: null }],
            },
      ],
    })
  })
  api = new API('fake-config', fetcher as typeof fetch)
  const { container, unmount } = render(<FeishuInspection app={app} enabled />)
  const publicText = () =>
    [...container.querySelectorAll('.setup-inspection > .panel')].map((panel) => panel.textContent).join('')
  const button = await screen.findByRole('link', { name: /一键开通权限/ })
  expect(button).toHaveAttribute('target', '_blank')
  expect(button).toHaveAttribute('href', 'https://open.feishu.cn/app/cli_fake/auth?q=fake')
  expect(screen.getByText('虚构机器人', { exact: true })).toBeVisible()
  expect(publicText()).not.toContain('im:message:send_as_bot')
  expect(container.querySelector('.inspection-details pre')).not.toBeVisible()
  await waitFor(() => expect(screen.getByText('虚构新群')).toBeVisible(), { timeout: 7000 })
  expect(screen.getByText('新发现')).toBeVisible()
  expect(screen.getByText('人数未知')).toBeVisible()
  expect(publicText()).not.toContain('oc_fake_hidden')
  expect(container.querySelector('.inspection-details pre')).not.toBeVisible()
  unmount()
  expect(fetcher.mock.calls).toHaveLength(2)
}, 10000)

it('does not inspect using read access and aborts an active request when leaving', async () => {
  let signal: AbortSignal | null | undefined
  const fetcher = vi.fn((_input: RequestInfo | URL, init?: RequestInit) => {
    signal = init?.signal
    return new Promise<Response>((_resolve, reject) => {
      signal?.addEventListener('abort', () => reject(new DOMException('cancelled', 'AbortError')), {
        once: true,
      })
    })
  })
  api = new API('fake-read', fetcher as typeof fetch)
  const readonly = render(<FeishuInspection app={{ id: 'fake-app', domain: 'feishu' }} enabled={false} />)
  expect(fetcher).not.toHaveBeenCalled()
  expect(screen.getByRole('button', { name: '重新检查' })).toBeDisabled()
  readonly.unmount()
  const active = render(<FeishuInspection app={{ id: 'fake-app', domain: 'feishu' }} enabled />)
  expect(fetcher).toHaveBeenCalledOnce()
  await act(async () => active.unmount())
  expect(signal?.aborted).toBe(true)
  expect(officialFeishuURL('javascript:alert(1)')).toBeUndefined()
  expect(officialFeishuURL('https://example.com/fake')).toBeUndefined()
  expect(officialFeishuURL('http://open.feishu.cn/app')).toBeUndefined()
})
