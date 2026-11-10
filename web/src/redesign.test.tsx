import { fireEvent, render, screen, waitFor, within } from '@testing-library/react'
import { beforeEach, expect, it, vi } from 'vitest'
import { API } from './api'
import { Assistant } from './Assistant'
import { DashboardPanel } from './DashboardPanel'
import type { DashboardPanelDefinition } from './dashboard'
import { display } from './format'
import { AddMonitorDialog } from './Monitors'

let api: API,
  scope = 'config'
const refresh = vi.fn()
vi.mock('./state', async (original) => ({
  ...(await original<typeof import('./state')>()),
  useApp: () => ({ api, scope, refresh, version: 1, now: () => 1000, theme: 'dark' }),
}))
vi.mock('./Chart', () => ({ Chart: ({ title }: { title: string }) => <div role="img" aria-label={title} /> }))

beforeEach(() => {
  sessionStorage.clear()
  refresh.mockClear()
  HTMLDialogElement.prototype.showModal = function () {
    this.setAttribute('open', '')
  }
  HTMLDialogElement.prototype.close = function () {
    this.removeAttribute('open')
  }
  Element.prototype.scrollTo = () => {}
})

const card = (type: DashboardPanelDefinition['type'], extra: Partial<DashboardPanelDefinition> = {}) =>
  ({
    id: type,
    type,
    title: '卡片 ' + type,
    query: 'SELECT 1',
    width: 6,
    ...extra,
  }) as DashboardPanelDefinition

it('formats units the way people read them', () => {
  expect(display(52.345, 'percent')).toBe('52.3%')
  expect(display(1536, 'bytes')).toBe('1.5 KB')
  expect(display(2048, 'bytes_per_sec')).toBe('2.0 KB/s')
  expect(display(1500, 'ms')).toBe('1.5 秒')
  expect(display(7200, 'seconds')).toBe('2 小时')
  expect(display(null, 'percent')).toBe('—')
  expect(display('12 / 13', undefined)).toBe('12 / 13')
})

it('renders gauges, rankings, rings and timelines with warning levels', () => {
  const { container } = render(
    <>
      <DashboardPanel
        panel={card('gauge', { unit: 'percent', warn: 75, critical: 90 })}
        result={{ columns: ['value'], rows: [[93.21]], truncated: false }}
      />
      <DashboardPanel
        panel={card('toplist', { unit: 'percent', warn: 80 })}
        result={{
          columns: ['label', 'value'],
          rows: [
            ['C:', 85],
            ['D:', 40],
          ],
          truncated: false,
        }}
      />
      <DashboardPanel
        panel={card('pie')}
        result={{
          columns: ['label', 'value'],
          rows: [
            ['运行中', 3],
            ['已停止', 1],
          ],
          truncated: false,
        }}
      />
      <DashboardPanel
        panel={card('timeline')}
        params={{ from: 0, to: 100, collector: null }}
        result={{
          columns: ['ts', 'label', 'state'],
          rows: [
            [0, '主机甲', 'up'],
            [50, '主机甲', 'down'],
          ],
          truncated: false,
        }}
      />
      <DashboardPanel
        panel={card('trend', { unit: 'ms' })}
        result={{
          columns: ['ts', 'value'],
          rows: [
            [2, 30],
            [1, 10],
          ],
          truncated: false,
        }}
      />
      <DashboardPanel
        panel={card('alerts')}
        result={{ columns: ['summary', 'severity', 'state'], rows: [], truncated: false }}
      />
    </>,
  )
  expect(screen.getByRole('img', { name: '卡片 gauge 93.2%' }).closest('[data-level]')).toHaveAttribute(
    'data-level',
    'critical',
  )
  const ranking = container.querySelector('[data-type="toplist"]')!
  expect(
    within(ranking as HTMLElement)
      .getByText('85%')
      .closest('li'),
  ).toHaveAttribute('data-level', 'warn')
  expect(screen.getByText('合计')).toBeInTheDocument()
  expect(screen.getByText(/· 75%/)).toBeInTheDocument()
  const strip = screen.getByRole('img', { name: '主机甲 状态时间线' })
  expect(strip.querySelectorAll('.seg-up')).toHaveLength(1)
  expect(strip.querySelectorAll('.seg-down')).toHaveLength(1)
  // The trend shows the latest reading after sorting by time, not the last row returned.
  expect(container.querySelector('[data-type="trend"] .viz-stat')).toHaveTextContent('30ms')
  expect(screen.getByText('没有告警')).toBeVisible()
})

it('adds a monitor in one request and points read-only viewers at their agent', async () => {
  let body: unknown
  api = new API(
    'fake',
    vi.fn(async (_url: unknown, init?: RequestInit) => {
      body = JSON.parse(String(init?.body))
      return Response.json({
        monitor: {
          id: 'port-3000',
          name: '博客',
          watching: ['端口 3000'],
          state: 'unknown',
          hosts: [],
          rules: [],
          firing: 0,
        },
        created: true,
        dashboard: 'port-3000',
        next: [],
      })
    }) as unknown as typeof fetch,
  )
  const close = vi.fn()
  const { unmount } = render(<AddMonitorDialog close={close} />)
  fireEvent.change(screen.getByLabelText('端口号'), { target: { value: '3000' } })
  fireEvent.change(screen.getByLabelText('名字'), { target: { value: '博客' } })
  fireEvent.click(screen.getByRole('button', { name: '添加监控' }))
  expect(await screen.findByRole('link', { name: /打开面板/ })).toHaveAttribute('href', '#/boards/port-3000')
  expect(body).toEqual({ name: '博客', port: 3000 })
  expect(refresh).toHaveBeenCalled()
  unmount()
  scope = 'read'
  render(<AddMonitorDialog close={close} preset={{ name: 'Tailscale', service: 'Tailscale' }} />)
  expect(screen.queryByRole('button', { name: '添加监控' })).not.toBeInTheDocument()
  expect(screen.getByText(/add_monitor 监控Windows 服务 Tailscale/)).toBeVisible()
  scope = 'config'
})

function stream(events: [string, unknown][]) {
  const text = events
    .map(([name, data]) => 'event: ' + name + '\ndata: ' + JSON.stringify(data) + '\n\n')
    .join('')
  return new Response(new TextEncoder().encode(text), { headers: { 'Content-Type': 'text/event-stream' } })
}

it('streams answers with the tools the assistant used and renders tables safely', async () => {
  let sent: { messages: { role: string; text: string }[] } | undefined
  api = new API(
    'fake',
    vi.fn(async (url: unknown, init?: RequestInit) => {
      if (String(url).endsWith('/assistant'))
        return Response.json({ configured: true, model: 'claude-opus-5-5' })
      sent = JSON.parse(String(init?.body))
      return stream([
        [
          'tool',
          { id: 't1', name: 'search_computer', label: '搜索这台电脑', detail: 'services', state: 'running' },
        ],
        [
          'tool',
          { id: 't1', name: 'search_computer', label: '搜索这台电脑', detail: 'services', state: 'done' },
        ],
        [
          'text',
          {
            delta:
              '有 1 个服务需要留意：\n\n| 服务 | 状态 |\n|---|---|\n| <b>虚构</b> | 已停止 |\n\n[打开探索](#/explore) [坏链接](javascript:alert(1))',
          },
        ],
        ['done', { ok: true }],
      ])
    }) as unknown as typeof fetch,
  )
  const { container } = render(<Assistant close={() => {}} />)
  const field = await screen.findByLabelText('问 AI 助手')
  await waitFor(() => expect(field).toBeEnabled())
  fireEvent.change(field, { target: { value: '哪些服务停着？' } })
  fireEvent.keyDown(field, { key: 'Enter' })
  expect(await screen.findByRole('table')).toBeVisible()
  // Raw HTML from the model is dropped, never rendered.
  expect(within(screen.getByRole('table')).getByText('已停止')).toBeVisible()
  expect(container.querySelector('table b')).toBeNull()
  expect(screen.getByText(/搜索这台电脑/).closest('[data-state]')).toHaveAttribute('data-state', 'done')
  expect(screen.getByRole('link', { name: '打开探索' })).toHaveAttribute('href', '#/explore')
  expect(screen.queryByRole('link', { name: '坏链接' })).not.toBeInTheDocument()
  expect(sent?.messages).toEqual([{ role: 'user', text: '哪些服务停着？' }])
})

const providers = [
  {
    id: 'anthropic',
    name: 'Claude',
    protocol: 'anthropic',
    baseUrl: 'https://api.anthropic.com',
    model: 'claude-opus-5-5',
    models: ['claude-opus-5-5'],
    needsKey: true,
    region: 'global',
  },
  {
    id: 'deepseek',
    name: 'DeepSeek',
    protocol: 'openai',
    baseUrl: 'https://api.deepseek.com/v1',
    model: 'deepseek-chat',
    models: ['deepseek-chat'],
    needsKey: true,
    region: 'china',
  },
  {
    id: 'ollama',
    name: 'Ollama',
    protocol: 'openai',
    baseUrl: 'http://127.0.0.1:11434/v1',
    model: '',
    models: [],
    needsKey: false,
    region: 'local',
  },
]
const unconfigured = {
  configured: false,
  provider: 'anthropic',
  providerName: 'Claude',
  model: 'claude-opus-5-5',
  baseUrl: '',
  setup: "& 'lgmon.exe' assistant set --provider anthropic --home 'D:\\fake'",
  providers,
}

it('explains how to configure the assistant instead of failing', async () => {
  scope = 'config'
  api = new API('fake', vi.fn(async () => Response.json(unconfigured)) as unknown as typeof fetch)
  render(<Assistant close={() => {}} />)
  expect(await screen.findByText('先给助手选一个模型')).toBeVisible()
  expect(screen.getByText(/assistant set --provider anthropic/)).toBeVisible()
  expect(screen.getByLabelText('问 AI 助手')).toBeDisabled()

  fireEvent.click(screen.getByRole('button', { name: /选择模型并保存密钥/ }))
  fireEvent.click(await screen.findByRole('button', { name: /DeepSeek/ }))
  expect(screen.getByRole('button', { name: /DeepSeek/ })).toHaveAttribute('aria-pressed', 'true')
  expect(screen.getByText(/assistant set --provider deepseek --home/)).toBeVisible()
  expect(screen.queryByRole('button', { name: '验证并保存' })).not.toBeInTheDocument()
})

it('lets an administrator pick any provider and verifies before saving', async () => {
  scope = 'admin'
  const fetcher = vi.fn(async (_url: string, init?: RequestInit) => {
    if (init?.method === 'PUT')
      return Response.json({
        ...unconfigured,
        configured: true,
        provider: 'deepseek',
        providerName: 'DeepSeek',
        model: 'deepseek-chat',
      })
    return Response.json(unconfigured)
  })
  api = new API('fake', fetcher as unknown as typeof fetch)
  render(<Assistant close={() => {}} />)
  fireEvent.click(await screen.findByRole('button', { name: /选择模型并保存密钥/ }))
  expect(screen.getByText('国内')).toBeVisible()
  fireEvent.click(screen.getByRole('button', { name: /Ollama/ }))
  expect(screen.queryByLabelText('API Key')).not.toBeInTheDocument()
  fireEvent.click(screen.getByRole('button', { name: /DeepSeek/ }))
  fireEvent.change(screen.getByLabelText('API Key'), { target: { value: 'sk-test-only' } })
  fireEvent.click(screen.getByRole('button', { name: '验证并保存' }))
  expect(await screen.findByText(/已连通 DeepSeek · deepseek-chat/)).toBeVisible()
  const put = fetcher.mock.calls.find(([, init]) => init?.method === 'PUT')!
  expect(put[0]).toContain('/api/v1/assistant')
  expect(JSON.parse(String(put[1]!.body))).toEqual({
    provider: 'deepseek',
    model: '',
    baseUrl: '',
    apiKey: 'sk-test-only',
  })
  expect(screen.getByLabelText('API Key')).toHaveValue('')
  scope = 'config'
})
