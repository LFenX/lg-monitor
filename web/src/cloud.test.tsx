/* Copyright (c) 2026 LFen (https://lfen.ink) — lg-monitor */

import { render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, beforeEach, expect, it, vi } from 'vitest'
import { App } from './App'
import type { BoardView, CloudDeviceSummary, CloudStatus } from './types'

let bootstrap: HTMLScriptElement
let cloudStatus: CloudStatus
let cloudDevices: CloudDeviceSummary[]
let calls: { method: string; path: string; body?: unknown }[]
let registered: Set<string>

const ownBoard: BoardView = {
  id: 'main',
  name: '本机核心',
  monitors: ['local-api'],
  summary: { services: 1, children: 0, up: 1, down: 0, unknown: 0, uptime: 100, state: 'up' },
  url: '',
  unassigned: [],
  items: [
    {
      id: 'local-api',
      name: '本机接口',
      watching: ['端口 3000'],
      state: 'up',
      overall: 'up',
      hosts: [],
      rules: [],
      firing: 0,
      beats: Array(48).fill('up'),
      uptime: 100,
      latencyMs: 5,
      kind: 'port',
      children: [],
      depth: 0,
    },
  ],
}

const remoteCloudBoard: BoardView = {
  id: 'home-main',
  name: '家庭服务',
  description: 'NAS 与下载服务',
  monitors: ['nas'],
  summary: { services: 1, children: 0, up: 0, down: 1, unknown: 0, uptime: 98.5, state: 'down' },
  url: '',
  unassigned: [],
  items: [
    {
      id: 'nas',
      name: '群晖 NAS',
      watching: ['URL https://nas.local'],
      state: 'down',
      overall: 'down',
      issue: '连接超时',
      hosts: [],
      rules: [],
      firing: 1,
      beats: [...Array(47).fill('up'), 'down'],
      uptime: 98.5,
      latencyMs: null,
      kind: 'url',
      children: [],
      depth: 0,
      hasDoc: true,
      description: '家庭存储节点',
    },
  ],
}

const syncedJob = {
  id: 'daily',
  name: '每日上传',
  schedule: '每天 02:00（北京时间）',
  stages: [],
  source: 'api' as const,
  production: ['nas'],
}

const dailyJob = {
  ...remoteCloudBoard.items[0],
  id: 'daily',
  name: '每日上传',
  watching: [],
  state: 'up' as const,
  overall: 'up' as const,
  issue: undefined,
  firing: 0,
  kind: 'job' as const,
  hasDoc: false,
  job: { date: '2026-11-21', status: 'success' as const, summary: '远端全部阶段成功' },
}

const homeDevice = (): CloudDeviceSummary => ({
  id: 'dev-home',
  name: '家里电脑',
  avatar: 'lg-cat-1',
  isCurrent: false,
  online: true,
  lastSeen: Date.now(),
  stateUpdated: Date.now(),
  totalCount: 1,
  downCount: 1,
  boards: [remoteCloudBoard],
})

beforeEach(() => {
  sessionStorage.clear()
  localStorage.clear()
  window.location.hash = '#/'
  calls = []
  registered = new Set()
  cloudStatus = {
    loggedIn: false,
    deviceId: 'dev-self',
    deviceName: '公司主机',
    serverUrl: 'https://sync.lfen.ink',
    paused: false,
    syncing: false,
    devicesCount: 0,
    stats: { bytesSent: 0, bytesReceived: 0, pushCount: 0, heartbeatCount: 0, pullCount: 0, docUploads: 0 },
  }
  cloudDevices = []

  bootstrap = document.createElement('script')
  bootstrap.id = 'lgmon-session'
  bootstrap.type = 'application/json'
  bootstrap.textContent = JSON.stringify({ token: 'fake-local', scope: 'config' })
  document.head.append(bootstrap)

  vi.stubGlobal(
    'fetch',
    vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
      const path = String(input).replace('/api/v1', '')
      const method = init?.method || 'GET'
      calls.push({ method, path, body: init?.body ? JSON.parse(String(init.body)) : undefined })
      const headers = { 'X-Lgmon-Scope': 'config' }

      if (path === '/stream') {
        return new Response(new ReadableStream(), {
          headers: { ...headers, 'Content-Type': 'text/event-stream' },
        })
      }
      if (path === '/cloud/status') return Response.json(cloudStatus, { headers })
      if (path === '/cloud/devices') return Response.json(cloudDevices, { headers })
      if (path === '/cloud/auth/otp/request' && method === 'POST') {
        const { email, purpose } = JSON.parse(String(init?.body))
        if (purpose === 'login' && !registered.has(email)) {
          return Response.json(
            { error: { code: 'account_not_found', message: '这个邮箱还没有注册，请先注册' } },
            { status: 404, headers },
          )
        }
        if (purpose === 'register' && registered.has(email)) {
          return Response.json(
            { error: { code: 'account_exists', message: '这个邮箱已经注册过了，请直接登录' } },
            { status: 409, headers },
          )
        }
        return Response.json({ ok: true, cooldownSec: 60, expiresInSec: 600 }, { headers })
      }
      if (
        path === '/cloud/auth/otp/verify' &&
        method === 'POST' &&
        JSON.parse(String(init?.body)).code !== '123456'
      ) {
        // The local API turns the sync service's 401 into 400, so the panel keeps its own session.
        return Response.json(
          { error: { code: 'otp_mismatch', message: '验证码不正确' } },
          { status: 400, headers },
        )
      }
      if ((path === '/cloud/auth/otp/verify' || path === '/cloud/auth/register') && method === 'POST') {
        registered.add(JSON.parse(String(init?.body)).email)
        cloudStatus = {
          ...cloudStatus,
          loggedIn: true,
          email: 'user@example.com',
          hasPassword: false,
          devicesCount: 2,
        }
        cloudDevices = [
          {
            id: 'dev-self',
            name: '公司主机',
            isCurrent: true,
            online: true,
            lastSeen: Date.now(),
            totalCount: 1,
            downCount: 0,
            boards: [ownBoard],
          },
          {
            id: 'dev-home',
            name: '家里电脑',
            avatar: 'lg-cat-1',
            isCurrent: false,
            online: true,
            lastSeen: Date.now(),
            stateUpdated: Date.now(),
            totalCount: 1,
            downCount: 1,
            boards: [remoteCloudBoard],
          },
        ]
        return Response.json(cloudStatus, { headers })
      }
      if (path === '/cloud/devices/dev-home/boards/home-main') {
        return Response.json(
          {
            deviceId: 'dev-home',
            deviceName: '家里电脑',
            deviceAvatar: 'lg-cat-1',
            online: true,
            lastSeen: Date.now(),
            stateUpdated: Date.now(),
            board: remoteCloudBoard,
          },
          { headers },
        )
      }
      if (path === '/cloud/devices/dev-home/monitors/nas') {
        return Response.json(
          {
            monitor: remoteCloudBoard.items[0],
            lineage: [],
            children: [],
            boards: [{ id: 'home-main', name: '家庭服务' }],
            slots: Array.from({ length: 96 }, (_, i) => ({ from: i * 900000, state: 'up', uptime: 100 })),
            days: [],
            uptime24h: 98.5,
            latency: [],
            latencyNow: null,
            events: [],
            incidents: [],
            rules: [],
            historyNotice: '云同步展示最近 24 小时状态摘要',
          },
          { headers },
        )
      }
      if (path === '/cloud/devices/dev-home/monitors/daily') {
        return Response.json(
          {
            monitor: dailyJob,
            lineage: [],
            children: [],
            boards: [{ id: 'home-main', name: '家庭服务' }],
            slots: [],
            days: [],
            uptime24h: null,
            latency: [],
            latencyNow: null,
            events: [],
            incidents: [],
            rules: [],
          },
          { headers },
        )
      }
      if (path === '/cloud/devices/dev-home/jobs/daily/days?days=30') {
        return Response.json(
          {
            job: syncedJob,
            days: [
              {
                date: '2026-11-21',
                status: 'success',
                summary: '远端全部阶段成功',
                enabled: true,
                attempts: [],
              },
            ],
          },
          { headers },
        )
      }
      if (path.startsWith('/cloud/devices/dev-home/jobs/daily/days/')) {
        return Response.json(
          {
            job: syncedJob,
            day: {
              date: path.split('/').pop(),
              status: 'pending',
              summary: '计划 02:00 运行',
              enabled: true,
              attempts: [],
            },
            production: [{ id: 'nas', name: '群晖 NAS', state: 'down', issue: '连接超时' }],
          },
          { headers },
        )
      }
      if (path === '/cloud/devices/dev-home/monitors/nas/doc') {
        return Response.json({ id: 'nas', markdown: '# 群晖 NAS\n\n云端同步的部署文档' }, { headers })
      }
      if (path === '/boards') return Response.json([ownBoard], { headers })
      if (path === '/boards/main') return Response.json(ownBoard, { headers })
      if (path === '/monitors') return Response.json(ownBoard.items, { headers })
      if (path === '/lan') {
        return Response.json(
          {
            visible: false,
            name: '公司主机',
            avatar: 'fiora-3',
            hostname: 'pc',
            fingerprint: 'AA',
            addresses: [],
            listening: false,
            pending: 0,
            viewers: 0,
          },
          { headers },
        )
      }
      if (path === '/guide') {
        return Response.json(
          {
            version: '0.17.0',
            steps: [],
            state: { agentSeenAt: 0 },
            install: { executable: 'lgmon.exe', home: 'C:\\lgmon', personal: true, url: '' },
            collectors: [],
          },
          { headers },
        )
      }
      if (path === '/notifications/pause') return Response.json({ paused: false, until: null }, { headers })
      if (path === '/config/targets') return Response.json({ targets: [] }, { headers })
      if (path === '/config/notifiers') return Response.json({ notifiers: [] }, { headers })
      if (
        path === '/collectors' ||
        path === '/targets' ||
        path === '/alerts' ||
        path === '/rule-errors' ||
        path === '/dashboards' ||
        path === '/agents' ||
        path === '/lan/remote'
      ) {
        return Response.json([], { headers })
      }
      if (path === '/query') return Response.json({ columns: [], rows: [], truncated: false }, { headers })
      return Response.json({}, { headers })
    }),
  )
})

afterEach(() => {
  bootstrap.remove()
  vi.unstubAllGlobals()
})

it('registers a new address first, then groups synced devices in the sidebar and opens a synced doc', async () => {
  render(<App />)

  // 1. Logged-out users see their own boards immediately without any forced login gate.
  const boardsNav = await screen.findByRole('navigation', { name: '面板' })
  expect(within(boardsNav).getByRole('link', { name: /本机核心/ })).toBeVisible()

  // 2. The dialog opens on 登录 with an email code by default.
  await userEvent.click(await screen.findByRole('button', { name: '登录同步多设备' }))
  const dialog = await screen.findByRole('dialog', { name: '登录并同步多设备' })
  expect(within(dialog).getByRole('tab', { name: '登录' })).toHaveAttribute('aria-selected', 'true')
  expect(within(dialog).getByRole('tab', { name: '注册' })).toHaveAttribute('aria-selected', 'false')
  expect(within(dialog).getByLabelText('6 位验证码')).toBeVisible()

  // 3. An address with no account is sent to 注册, keeping what was typed, and gets a register code.
  await userEvent.type(within(dialog).getByLabelText('邮箱地址'), 'user@example.com')
  await userEvent.click(within(dialog).getByRole('button', { name: '获取验证码' }))
  expect(await within(dialog).findByText(/这个邮箱还没注册/)).toBeVisible()
  expect(within(dialog).getByRole('tab', { name: '注册' })).toHaveAttribute('aria-selected', 'true')
  expect(within(dialog).getByLabelText('邮箱地址')).toHaveValue('user@example.com')
  await userEvent.click(within(dialog).getByRole('button', { name: '获取验证码' }))
  expect(await within(dialog).findByText(/注册邮件已发往 user@example\.com/)).toBeVisible()
  expect(calls.filter((call) => call.path === '/cloud/auth/otp/request').map((call) => call.body)).toEqual([
    { email: 'user@example.com', purpose: 'login', lang: 'zh' },
    { email: 'user@example.com', purpose: 'register', lang: 'zh' },
  ])
  await userEvent.type(within(dialog).getByLabelText('6 位验证码'), '123456')
  await userEvent.type(within(dialog).getByLabelText('设置密码（可选，至少 8 位）'), 'short')
  await userEvent.click(within(dialog).getByRole('button', { name: '注册并绑定本机' }))
  expect(await within(dialog).findByText('新密码长度至少 8 位')).toBeVisible()
  await userEvent.clear(within(dialog).getByLabelText('设置密码（可选，至少 8 位）'))
  await userEvent.click(within(dialog).getByRole('button', { name: '注册并绑定本机' }))
  expect(calls.find((call) => call.path === '/cloud/auth/register')?.body).toEqual({
    email: 'user@example.com',
    code: '123456',
    deviceName: '公司主机',
  })

  await waitFor(() =>
    expect(screen.queryByRole('dialog', { name: '登录并同步多设备' })).not.toBeInTheDocument(),
  )

  // 4. Sidebar now groups boards by device: 本机置顶 + 家里电脑 card with live down badge.
  const homeDeviceGroup = await within(boardsNav).findByRole('group', { name: '家里电脑 的面板' })
  expect(within(boardsNav).getByRole('group', { name: '公司主机 的面板' })).toBeVisible()
  const homeBoardLink = within(homeDeviceGroup).getByRole('link', { name: /家庭服务/ })
  expect(homeBoardLink).toHaveAttribute('href', '#/cloud/dev-home/boards/home-main')

  // 5. Open synced remote board and service with on-demand Markdown doc.
  await userEvent.click(homeBoardLink)
  expect(await screen.findByRole('heading', { name: '家庭服务' })).toBeVisible()
  expect(screen.getByText(/来自我的设备「家里电脑」· 云同步/)).toBeVisible()

  await userEvent.click(screen.getByRole('link', { name: /群晖 NAS/ }))
  expect(await screen.findByRole('heading', { name: '群晖 NAS' })).toBeVisible()
  expect(await screen.findByText('云端同步的部署文档', {}, { timeout: 3000 })).toBeVisible()
}, 15000)

it('sends a registered address back to 登录, and a wrong code keeps the panel signed in', async () => {
  registered.add('user@example.com')
  render(<App />)
  await userEvent.click(await screen.findByRole('button', { name: '登录同步多设备' }))
  const dialog = await screen.findByRole('dialog', { name: '登录并同步多设备' })

  await userEvent.click(within(dialog).getByRole('tab', { name: '注册' }))
  await userEvent.type(within(dialog).getByLabelText('邮箱地址'), 'user@example.com')
  await userEvent.click(within(dialog).getByRole('button', { name: '获取验证码' }))
  expect(await within(dialog).findByText('这个邮箱已经注册过了，直接登录就行。')).toBeVisible()
  expect(within(dialog).getByRole('tab', { name: '登录' })).toHaveAttribute('aria-selected', 'true')

  await userEvent.click(within(dialog).getByRole('button', { name: '获取验证码' }))
  expect(await within(dialog).findByText(/验证码已发往 user@example\.com/)).toBeVisible()
  await userEvent.type(within(dialog).getByLabelText('6 位验证码'), '000000')
  await userEvent.click(within(dialog).getByRole('button', { name: '登录并绑定本机' }))
  expect(await within(dialog).findByText('验证码不正确')).toBeVisible()
  expect(screen.queryByRole('dialog', { name: /访问|令牌/ })).not.toBeInTheDocument()

  await userEvent.click(within(dialog).getByRole('button', { name: '用密码登录' }))
  expect(within(dialog).queryByLabelText('6 位验证码')).not.toBeInTheDocument()
  expect(within(dialog).getByLabelText('登录密码')).toBeVisible()
  expect(within(dialog).getByRole('button', { name: '忘记密码？用验证码重置' })).toBeVisible()
}, 15000)

it('lists the account’s other devices in the start window, with a summary chip beside the headline', async () => {
  cloudStatus = { ...cloudStatus, loggedIn: true, email: 'user@example.com', devicesCount: 2 }
  cloudDevices = [
    {
      id: 'dev-self',
      name: '公司主机',
      isCurrent: true,
      online: true,
      lastSeen: Date.now(),
      totalCount: 1,
      downCount: 0,
      boards: [ownBoard],
    },
    homeDevice(),
    {
      id: 'dev-lab',
      name: '实验室',
      isCurrent: false,
      online: false,
      lastSeen: Date.now() - 3 * 3600_000,
      totalCount: 0,
      downCount: 0,
      boards: [],
    },
  ]
  window.location.hash = '#/start'
  render(<App />)
  const section = await screen.findByRole('region', { name: '我的其他设备' })
  // This computer is the page itself; only the others are listed.
  expect(within(section).queryByText('公司主机')).not.toBeInTheDocument()
  const home = within(section).getByRole('button', { name: '展开或收起 家里电脑 的面板' })
  expect(home).toHaveAttribute('aria-expanded', 'true')
  expect(home).toHaveTextContent('1 异常')
  expect(within(section).getByRole('link', { name: /家庭服务/ })).toHaveAttribute(
    'href',
    '#/cloud/dev-home/boards/home-main',
  )
  const lab = within(section).getByRole('button', { name: '展开或收起 实验室 的面板' })
  expect(lab).toHaveTextContent('离线')
  expect(lab).toHaveTextContent('3 小时前在线')
  const chip = screen.getByRole('button', { name: /我的设备 · 2 台/ })
  expect(chip).toHaveTextContent('1 个异常')
})

it('reads a synced batch job’s daily records through the cloud, linking its production path back to that device', async () => {
  cloudStatus = { ...cloudStatus, loggedIn: true, email: 'user@example.com', devicesCount: 2 }
  cloudDevices = [homeDevice()]
  window.location.hash = '#/cloud/dev-home/monitors/daily'
  render(<App />)
  expect(await screen.findByRole('heading', { name: '每日上传' })).toBeVisible()
  expect(
    await screen.findByText('远端全部阶段成功', { selector: '.lm-job-days *' }, { timeout: 3000 }),
  ).toBeVisible()
  expect(screen.queryByText(/只在 .* 的电脑上查看/)).not.toBeInTheDocument()
  expect(calls.some((call) => call.path === '/cloud/devices/dev-home/jobs/daily/days?days=30')).toBe(true)
  expect(calls.some((call) => call.path.startsWith('/jobs/'))).toBe(false)
})
