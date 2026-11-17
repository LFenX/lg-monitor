import { render, screen, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { beforeEach, expect, it, vi } from 'vitest'
import { API } from './api'
import { FeishuSetup } from './FeishuSetup'

let scope = 'config'
const fetcher = vi.fn(async (input: RequestInfo | URL) =>
  Response.json(String(input).endsWith('/config/rules') ? { rules: [] } : []),
)
const api = new API('fake', fetcher as typeof fetch)
vi.mock('./state', async (original) => ({
  ...(await original<typeof import('./state')>()),
  useApp: () => ({
    api,
    scope,
    version: 1,
    refresh: vi.fn(),
    data: { notifiers: { feishuApps: [], notifiers: [] } },
    guide: {
      install: { executable: 'C:\\Fake\\lgmon.exe', home: 'C:\\Fake Home', url: 'http://127.0.0.1:17444' },
    },
  }),
}))

beforeEach(() => {
  scope = 'config'
  fetcher.mockClear()
})

it('shows the full agent workflow and human actions without mounting the manual setup', async () => {
  render(<FeishuSetup />)
  const prompt = screen.getByRole('region', { name: '交给 Agent 接入飞书' })
  // The message is folded away until someone wants to read it.
  expect(prompt).not.toHaveTextContent('feishu_check')
  await userEvent.click(within(prompt).getByRole('button', { name: '显示这段话' }))
  expect(prompt).toHaveTextContent('--tools all --elevate --json')
  expect(prompt).toHaveTextContent('先问我两件事')
  expect(prompt).not.toHaveTextContent('【')
  for (const tool of ['feishu_check', 'feishu_list_chats', 'feishu_test_chat', 'feishu_save_chat'])
    expect(prompt).toHaveTextContent(tool)
  expect(prompt).toHaveTextContent('等待我明确回复已收到后')
  expect(prompt).toHaveTextContent('不删除或覆盖原通知')
  expect(within(prompt).getByRole('button', { name: '复制这段话' })).toBeVisible()
  expect(screen.getByRole('region', { name: '需要你配合的操作' })).toHaveTextContent(
    '应用密钥只在本机安全表单填写',
  )
  expect(screen.queryByLabelText('App Secret')).not.toBeInTheDocument()
  // Only the connected-agents line asks the server; the manual setup is not mounted.
  expect(fetcher.mock.calls.map(([input]) => new URL(String(input), 'http://fake').pathname)).toEqual(
    fetcher.mock.calls.length ? ['/api/v1/agents'] : [],
  )
})

it('keeps credential entry in the optional local manual form and excludes secret values from prompts', async () => {
  scope = 'admin'
  render(<FeishuSetup />)
  await userEvent.click(screen.getByRole('button', { name: '手动填写凭证或手动接入（可选）' }))
  const secret = await screen.findByLabelText('App Secret')
  await userEvent.type(secret, 'FAKE_LOCAL_SECRET')
  expect(screen.getByRole('region', { name: '交给 Agent 接入飞书' })).not.toHaveTextContent(
    'FAKE_LOCAL_SECRET',
  )
  await userEvent.click(screen.getByRole('button', { name: '收起手动接入' }))
  expect(screen.queryByLabelText('App Secret')).not.toBeInTheDocument()
})
