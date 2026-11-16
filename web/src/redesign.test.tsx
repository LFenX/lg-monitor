import { fireEvent, render, screen, waitFor, within } from '@testing-library/react'
import { beforeEach, expect, it, vi } from 'vitest'
import { API } from './api'
import { Assistant } from './Assistant'
import { AssistantSettings } from './AssistantSettings'
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
        board: 'main',
        next: [],
      })
    }) as unknown as typeof fetch,
  )
  const close = vi.fn()
  const { unmount } = render(<AddMonitorDialog close={close} />)
  fireEvent.change(screen.getByLabelText('端口号'), { target: { value: '3000' } })
  fireEvent.change(screen.getByLabelText('名字'), { target: { value: '博客' } })
  fireEvent.click(screen.getByRole('button', { name: '添加监控' }))
  expect(await screen.findByRole('link', { name: /打开面板/ })).toHaveAttribute('href', '#/boards/main')
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

const status = {
  name: '波西米亚',
  configured: false,
  deepseekKey: false,
  model: 'deepseek-chat',
  models: [] as string[],
  dashscopeKey: false,
  region: 'beijing',
  embeddingModel: 'qwen3.7-text-embedding',
  rerankModel: 'qwen3.7-text-rerank',
  docs: 0,
  setup: "& 'lgmon.exe' assistant set --home 'D:\fake'",
}

it('streams answers with the tools the assistant used and renders tables safely', async () => {
  let sent: { messages: { role: string; text: string }[] } | undefined
  api = new API(
    'fake',
    vi.fn(async (url: unknown, init?: RequestInit) => {
      if (String(url).endsWith('/assistant'))
        return Response.json({ ...status, configured: true, deepseekKey: true, dashscopeKey: true })
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
  const field = await screen.findByLabelText('问波西米亚')
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

it('explains what is still missing in one place instead of failing', async () => {
  scope = 'config'
  api = new API(
    'fake',
    vi.fn(async () => Response.json({ ...status, deepseekKey: true })) as unknown as typeof fetch,
  )
  const close = vi.fn()
  render(<Assistant close={close} />)
  expect(await screen.findByText('先给波西米亚接上模型')).toBeVisible()
  const steps = screen.getByRole('list')
  expect(within(steps).getByText('DeepSeek 的 API Key').closest('li')).toHaveAttribute('data-done', 'true')
  expect(within(steps).getByText('百炼的 API Key').closest('li')).not.toHaveAttribute('data-done')
  expect(screen.getByRole('link', { name: /去设置里填写/ })).toHaveAttribute('href', '#/settings/assistant')
  expect(screen.getByText(/assistant set --home/)).toBeVisible()
  expect(screen.getByLabelText('问波西米亚')).toBeDisabled()
  fireEvent.click(screen.getByText('让 Agent 帮我配置'))
  expect(screen.getByText(/configure_assistant/)).toBeVisible()
  // The panel itself no longer carries a provider picker.
  expect(screen.queryByRole('button', { name: /选择模型/ })).not.toBeInTheDocument()
  // Going to settings closes the panel, which would otherwise cover the form.
  fireEvent.click(screen.getByRole('link', { name: /去设置里填写/ }))
  expect(close).toHaveBeenCalledTimes(1)
})

it('configures each part of 波西米亚 from settings, testing before it saves', async () => {
  scope = 'config'
  let saved = { ...status, deepseekKey: false }
  const puts: unknown[] = []
  const fetcher = vi.fn(async (_url: string, init?: RequestInit) => {
    if (init?.method === 'PUT') {
      const body = JSON.parse(String(init.body))
      puts.push(body)
      if (body.deepseekKey === 'sk-wrong')
        return Response.json(
          { error: { message: '没有保存：DeepSeek：DeepSeek 拒绝了这个 API Key' } },
          { status: 400 },
        )
      saved = { ...saved, deepseekKey: true, models: ['deepseek-chat', 'deepseek-reasoner'] }
      return Response.json(saved)
    }
    return Response.json(saved)
  })
  api = new API('fake', fetcher as unknown as typeof fetch)
  render(<AssistantSettings open={() => {}} />)
  const deepseek = await screen.findByLabelText('DeepSeek')
  expect(within(deepseek).getByText('还没有密钥')).toBeVisible()
  // Nothing to test until a key is pasted.
  expect(within(deepseek).getByRole('button', { name: '保存并测试' })).toBeDisabled()
  const key = within(deepseek).getByLabelText('API Key')
  fireEvent.change(key, { target: { value: 'sk-wrong' } })
  fireEvent.click(within(deepseek).getByRole('button', { name: '保存并测试' }))
  expect(await within(deepseek).findByRole('alert')).toHaveTextContent('拒绝了这个 API Key')
  fireEvent.change(key, { target: { value: 'sk-fake-only' } })
  fireEvent.click(within(deepseek).getByRole('button', { name: '保存并测试' }))
  expect(await within(deepseek).findByRole('status')).toHaveTextContent('已连通 DeepSeek')
  expect(key).toHaveValue('')
  expect(puts).toEqual([
    { model: 'deepseek-chat', deepseekKey: 'sk-wrong' },
    { model: 'deepseek-chat', deepseekKey: 'sk-fake-only' },
  ])
  // The 百炼 row keeps its own key, region and models; the models fold away because they rarely change.
  const bailian = screen.getByLabelText('百炼')
  fireEvent.click(within(bailian).getByRole('button', { name: '新加坡' }))
  fireEvent.change(within(bailian).getByLabelText('API Key'), { target: { value: 'fake-dashscope' } })
  fireEvent.click(within(bailian).getByRole('button', { name: '保存并测试' }))
  await waitFor(() =>
    expect(puts[2]).toEqual({
      region: 'singapore',
      embeddingModel: 'qwen3.7-text-embedding',
      rerankModel: 'qwen3.7-text-rerank',
      dashscopeKey: 'fake-dashscope',
    }),
  )
  expect(screen.getByText('让 Agent 帮我配置')).toBeVisible()
  expect(screen.getByText(/assistant set --home/)).toBeInTheDocument()
})
