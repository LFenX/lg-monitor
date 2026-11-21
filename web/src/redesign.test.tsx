/* Copyright (c) 2026 LFen (https://lfen.ink) — lg-monitor */

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
  // Once answered, the steps fold into one line that opens.
  fireEvent.click(await screen.findByRole('button', { name: /查了 1 项/ }))
  expect(screen.getByText(/搜索这台电脑/).closest('[data-state]')).toHaveAttribute('data-state', 'done')
  expect(screen.getByRole('link', { name: '打开探索' })).toHaveAttribute('href', '#/explore')
  expect(screen.queryByRole('link', { name: '坏链接' })).not.toBeInTheDocument()
  expect(sent?.messages).toEqual([{ role: 'user', text: '哪些服务停着？' }])
  // It says which page the person is on, so it does not send them to the page they are looking at.
  expect((sent as unknown as { page: { path: string } }).page.path).toMatch(/^#/)
  // No line of hints under the box.
  expect(screen.queryByText(/Enter 发送/)).not.toBeInTheDocument()
  expect(screen.getByRole('button', { name: '复制回答' })).toBeVisible()
})

it('keeps the last five conversations on this computer and picks one up again', async () => {
  localStorage.clear()
  window.location.hash = '#/'
  let asked = 0
  api = new API(
    'fake',
    vi.fn(async (url: unknown, init?: RequestInit) => {
      if (String(url).endsWith('/assistant'))
        return Response.json({ ...status, configured: true, deepseekKey: true, dashscopeKey: true })
      const question = JSON.parse(String(init?.body)).messages.at(-1).text as string
      asked++
      return stream([
        ...(question.includes('权限')
          ? ([['navigate', { hash: '#/settings/access' }]] as [string, unknown][])
          : []),
        ['text', { delta: '回答' + asked }],
        ['done', { ok: true }],
      ])
    }) as unknown as typeof fetch,
  )
  render(<Assistant close={() => {}} />)
  const field = await screen.findByLabelText('问波西米亚')
  await waitFor(() => expect(field).toBeEnabled())
  const ask = async (text: string) => {
    fireEvent.change(field, { target: { value: text } })
    fireEvent.keyDown(field, { key: 'Enter' })
    expect(await screen.findByText('回答' + asked)).toBeVisible()
  }
  // Asked to go somewhere, 波西米亚 moves the page there itself.
  await ask('带我去权限页面')
  expect(window.location.hash).toBe('#/settings/access')
  for (let index = 2; index <= 7; index++) {
    fireEvent.click(screen.getByRole('button', { name: '新对话' }))
    await ask('第 ' + index + ' 个问题')
  }
  fireEvent.click(screen.getByRole('button', { name: '最近的对话' }))
  const list = screen.getByRole('dialog', { name: '最近的对话' })
  const titles = [...list.querySelectorAll('.ai-history-t')].map((item) => item.textContent)
  // Newest first, five at most: the first two are gone.
  expect(titles).toEqual(['第 7 个问题', '第 6 个问题', '第 5 个问题', '第 4 个问题', '第 3 个问题'])
  fireEvent.click(within(list).getByRole('button', { name: /^第 4 个问题/ }))
  expect(screen.queryByRole('dialog', { name: '最近的对话' })).not.toBeInTheDocument()
  expect(screen.getByText('回答4')).toBeVisible()
  expect(screen.queryByText('回答7')).not.toBeInTheDocument()
  // Picked up, it goes on with that conversation's history.
  await ask('接着问')
  const sent = JSON.parse(String(vi.mocked(api.fetcher).mock.calls.at(-1)?.[1]?.body)).messages
  expect(sent.map((message: { text: string }) => message.text)).toEqual(['第 4 个问题', '回答4', '接着问'])
  fireEvent.click(screen.getByRole('button', { name: '最近的对话' }))
  expect(screen.getByRole('dialog', { name: '最近的对话' }).querySelector('.ai-history-t')).toHaveTextContent(
    '第 4 个问题',
  )
})

it('asks before a step that deletes, and shows what it changed', async () => {
  let decided: unknown
  let release: () => void = () => {}
  const gate = new Promise<void>((resolve) => (release = resolve))
  const encoder = new TextEncoder()
  const chunk = (name: string, data: unknown) =>
    encoder.encode('event: ' + name + '\ndata: ' + JSON.stringify(data) + '\n\n')
  api = new API(
    'fake',
    vi.fn(async (url: unknown, init?: RequestInit) => {
      const path = String(url)
      if (path.endsWith('/assistant'))
        return Response.json({ ...status, configured: true, deepseekKey: true, dashscopeKey: true })
      if (path.endsWith('/assistant/confirm')) {
        decided = JSON.parse(String(init?.body))
        release()
        return Response.json({ ok: true, message: '已收到' })
      }
      return new Response(
        new ReadableStream({
          async start(controller) {
            controller.enqueue(
              chunk('tool', { id: 'c0', label: '改服务', detail: '官网', state: 'done', change: true }),
            )
            controller.enqueue(
              chunk('change', { id: 'c0', label: '改服务', subject: '官网', href: '#/monitors/web' }),
            )
            controller.enqueue(
              chunk('confirm', {
                id: 'step-1',
                label: '删除服务',
                subject: '旧博客',
                consequence: '删除这个服务和它的告警规则，并从所有面板移除；要恢复只能重新添加。',
              }),
            )
            await gate
            controller.enqueue(chunk('confirmed', { id: 'step-1', approved: true }))
            controller.enqueue(
              chunk('tool', { id: 'c1', label: '删除服务', detail: '旧博客', state: 'done', change: true }),
            )
            controller.enqueue(chunk('change', { id: 'c1', label: '删除服务', subject: '旧博客' }))
            controller.enqueue(chunk('text', { delta: '改好了，旧博客也删了。' }))
            controller.enqueue(chunk('done', { ok: true }))
            controller.close()
          },
        }),
        { headers: { 'Content-Type': 'text/event-stream' } },
      )
    }) as unknown as typeof fetch,
  )
  render(<Assistant close={() => {}} />)
  const field = await screen.findByLabelText('问波西米亚')
  await waitFor(() => expect(field).toBeEnabled())
  fireEvent.change(field, { target: { value: '把 web 改名叫官网，再删掉旧博客' } })
  fireEvent.keyDown(field, { key: 'Enter' })
  const card = await screen.findByRole('group', { name: '需要你确认：删除服务' })
  expect(within(card).getByText(/告警规则/)).toBeVisible()
  expect(within(card).getByText('旧博客')).toBeVisible()
  fireEvent.click(within(card).getByRole('button', { name: '确认删除服务' }))
  expect(await screen.findByText('改好了，旧博客也删了。')).toBeVisible()
  expect(decided).toEqual({ id: 'step-1', approve: true })
  expect(within(card).getByText('已确认')).toBeVisible()
  expect(screen.getByRole('link', { name: /改服务官网/ })).toHaveAttribute('href', '#/monitors/web')
  expect(screen.getByText('旧博客', { selector: '.ai-ch-t b' })).toBeVisible()
  expect(screen.getByRole('button', { name: /改了 2 处/ })).toBeVisible()
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
  const agent = screen.getByRole('region', { name: '让 Agent 帮我配置' })
  fireEvent.click(within(agent).getByRole('button', { name: '显示这段话' }))
  expect(within(agent).getByText(/configure_assistant/)).toBeVisible()
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

it('takes a question while answering: Enter cuts in at once, Ctrl+Enter waits its turn', async () => {
  const asked: string[] = []
  let hold: (() => void) | undefined
  api = new API(
    'fake',
    vi.fn(async (url: unknown, init?: RequestInit) => {
      if (String(url).endsWith('/assistant'))
        return Response.json({ ...status, configured: true, deepseekKey: true, dashscopeKey: true })
      const body = JSON.parse(String(init?.body)) as { messages: { text: string }[] }
      const question = body.messages[body.messages.length - 1].text
      asked.push(question)
      if (question === '第一个问题')
        // The first answer streams a little and then waits, so the next questions arrive while it is busy.
        return new Response(
          new ReadableStream({
            start(controller) {
              controller.enqueue(new TextEncoder().encode('event: text\ndata: {"delta":"正在看…"}\n\n'))
              hold = () => controller.close()
              init?.signal?.addEventListener('abort', () =>
                controller.error(new DOMException('aborted', 'AbortError')),
              )
            },
          }),
          { headers: { 'Content-Type': 'text/event-stream' } },
        )
      return stream([
        ['text', { delta: '回答：' + question }],
        ['done', { ok: true }],
      ])
    }) as unknown as typeof fetch,
  )
  render(<Assistant close={() => {}} />)
  const field = await screen.findByLabelText('问波西米亚')
  await waitFor(() => expect(field).toBeEnabled())
  fireEvent.change(field, { target: { value: '第一个问题' } })
  fireEvent.keyDown(field, { key: 'Enter' })
  expect(await screen.findByText('正在看…')).toBeVisible()
  fireEvent.change(field, { target: { value: '排队的问题' } })
  fireEvent.keyDown(field, { key: 'Enter', ctrlKey: true })
  expect(within(screen.getByRole('list', { name: '排队的问题' })).getByText('排队的问题')).toBeVisible()
  fireEvent.change(field, { target: { value: '插话的问题' } })
  fireEvent.keyDown(field, { key: 'Enter' })
  expect(await screen.findByText('回答：插话的问题')).toBeVisible()
  expect(await screen.findByText('回答：排队的问题')).toBeVisible()
  expect(asked).toEqual(['第一个问题', '插话的问题', '排队的问题'])
  // The cut-in stopped the first answer; its stream is already closed.
  expect(() => hold?.()).toThrow()
})

it('says whose model answers when a colleague shares theirs', async () => {
  api = new API(
    'fake',
    vi.fn(async () =>
      Response.json({
        ...status,
        configured: true,
        model: 'deepseek-chat',
        sharedBy: { name: '小王', avatar: 'fiora-2', model: 'deepseek-chat' },
      }),
    ) as unknown as typeof fetch,
  )
  render(<Assistant close={() => {}} />)
  const note = await screen.findByText(/共享的模型/)
  expect(note.closest('p')).toHaveTextContent('用的是 小王 共享的模型 · deepseek-chat')
  expect(await screen.findByLabelText('问波西米亚')).toBeEnabled()
})
