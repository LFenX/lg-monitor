/* Copyright (c) 2026 LFen (https://lfen.ink) — lg-monitor */

import { fireEvent, render, screen, waitFor, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, beforeEach, expect, it, vi } from 'vitest'
import { App } from './App'
import type { BoardView, CloudDeviceSummary, CloudStatus, Device, DeviceAlerts } from './types'

let bootstrap: HTMLScriptElement
let cloudStatus: CloudStatus
let cloudDevices: CloudDeviceSummary[]
let calls: { method: string; path: string; body?: unknown }[]
let registered: Set<string>
let binding: CloudStatus['binding']
let devices: Device[]
let deviceAlerts: DeviceAlerts

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
  os: 'Debian 12',
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
  binding = undefined
  cloudStatus = {
    loggedIn: false,
    deviceId: 'dev-self',
    deviceName: '公司主机',
    os: 'Windows 11 24H2',
    serverUrl: 'https://sync.lfen.ink',
    paused: false,
    syncing: false,
    devicesCount: 0,
    stats: { bytesSent: 0, bytesReceived: 0, pushCount: 0, heartbeatCount: 0, pullCount: 0, docUploads: 0 },
  }
  cloudDevices = []
  devices = []
  deviceAlerts = { memory: { threshold: 92, for: '2m' }, notify: [] }

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
            os: 'Debian 12',
            isCurrent: false,
            online: true,
            lastSeen: Date.now(),
            stateUpdated: Date.now(),
            totalCount: 1,
            downCount: 1,
            boards: [remoteCloudBoard],
          },
        ]
        return Response.json({ ...cloudStatus, binding }, { headers })
      }
      if (path === '/cloud/settings' && method === 'PUT') {
        const order = JSON.parse(String(init?.body)).deviceOrder as string[]
        cloudDevices = order.map((id) => cloudDevices.find((dev) => dev.id === id)!)
        return Response.json(cloudStatus, { headers })
      }
      if (path === '/cloud/devices/dev-old/merge' && method === 'POST') {
        cloudDevices = cloudDevices.filter((dev) => !dev.isCurrent)
        cloudDevices = cloudDevices.map((dev) =>
          dev.id === 'dev-old' ? { ...dev, isCurrent: true, online: true } : dev,
        )
        return Response.json({ ok: true }, { headers })
      }
      if (path.startsWith('/cloud/devices/') && method === 'DELETE') {
        cloudDevices = cloudDevices.filter((dev) => path !== '/cloud/devices/' + dev.id)
        return Response.json({ ok: true }, { headers })
      }
      if (path === '/cloud/devices/dev-home/boards/home-main') {
        return Response.json(
          {
            deviceId: 'dev-home',
            deviceName: '家里电脑',
            deviceAvatar: 'lg-cat-1',
            deviceOs: 'Debian 12',
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
      if (path === '/devices') return Response.json(devices, { headers })
      if (path === '/devices/alerts') {
        if (method === 'PUT') deviceAlerts = { ...JSON.parse(String(init?.body)), notify: ['desktop'] }
        return Response.json(deviceAlerts, { headers })
      }
      if (path.startsWith('/metrics?')) return Response.json({ metrics: [] }, { headers })
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
  await userEvent.click(within(dialog).getByRole('button', { name: '注册并登录' }))
  expect(await within(dialog).findByText('新密码长度至少 8 位')).toBeVisible()
  await userEvent.clear(within(dialog).getByLabelText('设置密码（可选，至少 8 位）'))
  await userEvent.click(within(dialog).getByRole('button', { name: '注册并登录' }))
  expect(calls.find((call) => call.path === '/cloud/auth/register')?.body).toEqual({
    email: 'user@example.com',
    code: '123456',
    deviceName: '公司主机',
    remember: true,
  })

  await waitFor(() =>
    expect(screen.queryByRole('dialog', { name: '登录并同步多设备' })).not.toBeInTheDocument(),
  )

  // 4. Sidebar now groups boards by device: 本机置顶 + 家里电脑 card with live down badge and OS badges.
  const homeDeviceGroup = await within(boardsNav).findByRole('group', { name: '家里电脑 的面板' })
  const selfDeviceGroup = within(boardsNav).getByRole('group', { name: '公司主机 的面板' })
  expect(selfDeviceGroup).toBeVisible()
  expect(within(selfDeviceGroup).getByText('Windows 11')).toBeVisible()
  expect(within(homeDeviceGroup).getByText('Debian 12')).toBeVisible()
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
  await userEvent.click(within(dialog).getByRole('button', { name: '登录' }))
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
  // Other devices start folded: the card alone says how each one is doing.
  expect(home).toHaveAttribute('aria-expanded', 'false')
  expect(home).toHaveTextContent('1 异常')
  expect(home.querySelector('.lm-start-cloud-bar i[data-state="down"]')).not.toBeNull()
  expect(within(section).queryByRole('link', { name: /家庭服务/ })).toBeNull()
  await userEvent.click(home)
  expect(within(section).getByRole('link', { name: /家庭服务/ })).toHaveAttribute(
    'href',
    '#/cloud/dev-home/boards/home-main',
  )
  // Its only board opens with its services, each leading to that device's service page.
  const services = await within(section).findByRole('list', { name: '家庭服务' })
  expect(await within(services).findByRole('link', { name: /群晖 NAS/ })).toHaveAttribute(
    'href',
    '#/cloud/dev-home/monitors/nas',
  )
  const lab = within(section).getByRole('button', { name: '展开或收起 实验室 的面板' })
  expect(lab).toHaveTextContent('离线')
  expect(lab).toHaveTextContent('3 小时前在线')
  // Local service card shows this computer's name, avatar, and OS badge.
  const localCard = screen.getByRole('region', { name: '公司主机' })
  expect(within(localCard).getByText('公司主机')).toBeVisible()
  expect(within(localCard).getByText('Windows 11 24H2')).toBeVisible()
  expect(localCard.querySelector('.lm-avatar')).not.toBeNull()
  // One chip per other device, named and with its face and OS badge.
  const homeChip = screen.getByRole('button', { name: /^我的设备 家里电脑/ })
  expect(homeChip).toHaveTextContent('家里电脑')
  expect(homeChip).toHaveTextContent('Debian 12')
  expect(homeChip).toHaveTextContent('1 个异常')
  expect(homeChip.querySelector('.lm-avatar')).not.toBeNull()
  expect(screen.getByRole('button', { name: /^我的设备 实验室/ })).toHaveTextContent('离线')
  // The account menu says which email this computer is signed in with.
  await userEvent.click(screen.getByRole('button', { name: /^我的账户：/ }))
  expect(within(screen.getByRole('menu', { name: '我的账户' })).getByText('user@example.com')).toBeVisible()
})

it('folds more than two other devices into one chip', async () => {
  cloudStatus = { ...cloudStatus, loggedIn: true, email: 'user@example.com', devicesCount: 4 }
  cloudDevices = ['甲', '乙', '丙'].map((name, index) => ({
    id: 'dev-' + index,
    name,
    isCurrent: false,
    online: true,
    lastSeen: Date.now(),
    totalCount: 2,
    downCount: index === 1 ? 1 : 0,
    boards: [],
  }))
  window.location.hash = '#/start'
  render(<App />)
  const chip = await screen.findByRole('button', { name: /我的其他设备 · 3 台/ })
  expect(chip).toHaveTextContent('1 个异常')
  expect(chip.querySelectorAll('.lm-avatar')).toHaveLength(3)
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

const signedInWithHome = () => {
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
  ]
}

it('shares a board of another synced device with LAN guests from the LAN page', async () => {
  signedInWithHome()
  let shared: Record<string, string[]> = {}
  const lanPuts: unknown[] = []
  const base = vi.mocked(fetch).getMockImplementation()!
  vi.mocked(fetch).mockImplementation(async (input, init) => {
    const path = String(input).replace('/api/v1', '')
    const headers = { 'X-Lgmon-Scope': 'config' }
    if (path === '/lan') {
      if (init?.method === 'PUT') {
        const body = JSON.parse(String(init.body)) as { webDeviceBoards?: Record<string, string[]> }
        lanPuts.push(body)
        shared = { ...shared, ...body.webDeviceBoards }
      }
      return Response.json(
        {
          visible: false,
          web: true,
          webDeviceBoards: shared,
          name: '公司主机',
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
    if (path === '/lan/requests') return Response.json({ incoming: [], outgoing: [] }, { headers })
    if (path === '/lan/grants' || path === '/departments' || path.startsWith('/lan/discover'))
      return Response.json([], { headers })
    return base(input, init)
  })
  window.location.hash = '#/lan'
  render(<App />)
  const device = await screen.findByRole('group', { name: '向内网开放 家里电脑 的面板' }, { timeout: 3000 })
  const box = within(device).getByRole('checkbox', { name: '开放 家庭服务' })
  expect(box).not.toBeChecked()
  await userEvent.click(box)
  await waitFor(() => expect(box).toBeChecked())
  // Only that board of that device is shared; nothing else from it is sent.
  await waitFor(() => expect(shared).toEqual({ 'dev-home': ['home-main'] }))
  // The opened board narrows to some services the same way a board of this PC does.
  await userEvent.click(within(device).getByRole('button', { name: '选择服务：家庭服务' }))
  const all = await within(device).findByRole('checkbox', { name: '开放此面板所有服务（含以后新增）' })
  expect(all).toBeChecked()
  const nas = await within(device).findByRole('checkbox', { name: '群晖 NAS' })
  await userEvent.click(nas)
  await waitFor(() =>
    expect(lanPuts.at(-1)).toEqual({ webDeviceServices: { 'dev-home': { 'home-main': [] } } }),
  )
}, 15000)

it('shows a LAN guest the owner as a device card, with the boards shared from other devices', async () => {
  bootstrap.textContent = JSON.stringify({ token: 'fake-guest', scope: 'read', guest: true })
  // The host lists only the other devices' boards it shares with guests.
  cloudDevices = [homeDevice()]
  render(<App />)
  const boardsNav = await screen.findByRole('navigation', { name: '面板' })
  const owner = await within(boardsNav).findByRole('group', { name: '公司主机 的面板' })
  expect(within(owner).getByRole('link', { name: /本机核心/ })).toBeVisible()
  expect(within(owner).queryByText('本机')).not.toBeInTheDocument()
  const home = await within(boardsNav).findByRole('group', { name: '家里电脑 的面板' })
  await userEvent.click(within(home).getByRole('link', { name: /家庭服务/ }))
  expect(await screen.findByRole('heading', { name: '家庭服务' })).toBeVisible()
  expect(calls.some((call) => call.path === '/cloud/status')).toBe(false)
}, 15000)

it('shows each computer’s figures apart from the boards and switches on the out-of-memory warning', async () => {
  devices = [
    {
      id: 'fake-host',
      hostname: '虚构服务器',
      online: true,
      lastSeen: Date.now(),
      system: {
        ts: Date.now(),
        cpuPercent: 10.4,
        cpuCores: 4,
        corePercent: [4, 5, 3, 30],
        load1: 0.79,
        load5: 1.02,
        load15: 1.44,
        memoryTotal: 16 << 30,
        memoryUsed: 15 << 30,
        memoryAvailable: 1 << 30,
        memoryPercent: 94,
        swapTotal: 4 << 30,
        swapUsed: 3 << 30,
        swapPercent: 75,
        uptimeSeconds: 90000,
        disks: [{ path: '/', total: 100, used: 50, free: 50, usedPercent: 50 }],
        networks: [],
      },
      usage: {
        ts: Date.now(),
        processCount: 479,
        top: [
          { name: 'postgres', count: 50, cpuPercent: 1, rss: 474 << 20 },
          {
            name: 'node',
            detail: 'task-worker.ts',
            displayName: '任务处理',
            count: 3,
            cpuPercent: 2,
            rss: 100 << 20,
          },
        ],
        containers: [
          {
            id: 'abc',
            name: 'fake-web',
            cpuPercent: 1,
            memoryUsed: 5 << 30,
            memoryLimit: 5.5 * 2 ** 30,
            memoryPercent: 91,
            pids: 12,
          },
        ],
      },
    },
  ]
  window.location.hash = '#/devices'
  render(<App />)
  const host = await screen.findByRole('region', { name: '虚构服务器' })
  // Memory over 92% reads as about to run out; the load and the cores are there for a Linux server.
  expect(within(host).getByText('即将耗尽')).toBeVisible()
  expect(host).toHaveTextContent('0.79 / 1.02 / 1.44')
  expect(host).toHaveTextContent('94.0%')
  const usage = within(host).getByRole('region', { name: '内存去向' })
  expect(within(usage).getByText('postgres')).toBeVisible()
  expect(usage).toHaveTextContent('50 个进程')
  // A program shows the name it was given, with the process and the script it runs beneath.
  expect(within(usage).getByText('任务处理')).toBeVisible()
  expect(within(usage).getByText('node · task-worker.ts')).toBeVisible()
  await userEvent.click(within(usage).getByRole('button', { name: '改显示名：任务处理' }))
  const rename = await screen.findByRole('dialog', { name: '程序显示名' })
  await userEvent.clear(within(rename).getByLabelText('显示名'))
  await userEvent.type(within(rename).getByLabelText('显示名'), '任务 worker')
  await userEvent.click(within(rename).getByRole('button', { name: '保存' }))
  await waitFor(() =>
    expect(
      calls.find((call) => call.method === 'PUT' && call.path === '/devices/program-names')?.body,
    ).toEqual({
      process: 'node',
      detail: 'task-worker.ts',
      name: '任务 worker',
    }),
  )
  await userEvent.click(within(usage).getByRole('button', { name: /容器/ }))
  expect(within(usage).getByText('fake-web').closest('tr')).toHaveAttribute('data-tone', 'bad')
  // The page lives in the sidebar apart from the boards.
  expect(screen.getByRole('link', { name: /设备/ })).toHaveAttribute('href', '#/devices')

  await userEvent.click(screen.getByRole('button', { name: /预警设置/ }))
  const dialog = await screen.findByRole('dialog', { name: '设备预警' })
  await userEvent.click(within(dialog).getByRole('checkbox', { name: /内存即将耗尽/ }))
  await userEvent.click(within(dialog).getByRole('button', { name: '保存' }))
  await waitFor(() =>
    expect(
      calls.find((call) => call.method === 'PUT' && call.path === '/devices/alerts')?.body,
    ).toMatchObject({
      memory: { enabled: true, threshold: 92, for: '2m' },
    }),
  )
})

it("puts Devices in each device card and shows another device's synced sample", async () => {
  signedInWithHome()
  const base = vi.mocked(fetch).getMockImplementation()!
  vi.mocked(fetch).mockImplementation(async (input, init) => {
    const path = String(input).replace('/api/v1', '')
    if (path === '/cloud/devices/dev-home/system')
      return Response.json(
        {
          deviceId: 'dev-home',
          deviceName: '家里电脑',
          online: true,
          lastSeen: Date.now(),
          devices: [
            {
              id: 'local',
              hostname: '家里的 NAS 主机',
              history: {
                series: [
                  {
                    span: '6h',
                    from: Date.now() - 600_000,
                    step: 300_000,
                    cpu: [10, -1, 30],
                    mem: [50, 55, 60],
                  },
                ],
              },
              online: true,
              lastSeen: Date.now(),
              system: {
                ts: Date.now(),
                cpuPercent: 37.5,
                memoryTotal: 8 << 30,
                memoryUsed: 4 << 30,
                memoryAvailable: 4 << 30,
                memoryPercent: 50,
                uptimeSeconds: 3600,
                disks: [],
                networks: [],
              },
            },
          ],
        },
        { headers: { 'X-Lgmon-Scope': 'read' } },
      )
    return base(input, init)
  })
  window.location.hash = '#/'
  render(<App />)
  const boardsNav = await screen.findByRole('navigation', { name: '面板' })
  const home = await within(boardsNav).findByRole('group', { name: '家里电脑 的面板' })
  const entry = within(home).getByRole('link', { name: '设备' })
  expect(entry).toHaveAttribute('href', '#/cloud/dev-home/devices')
  // The bottom navigation no longer repeats it; this computer's card has its own entry.
  expect(
    within(screen.getByRole('navigation', { name: '主导航' })).queryByRole('link', { name: /设备/ }),
  ).toBeNull()
  await userEvent.click(entry)
  expect(await screen.findByRole('heading', { name: '设备 · 家里电脑' })).toBeVisible()
  expect(await screen.findByRole('region', { name: '家里的 NAS 主机' })).toBeVisible()
  // The curves come with the synced sample, so another device's page can draw them too.
  const history = await screen.findByRole('region', { name: '历史趋势' })
  expect(within(history).queryByText('这段时间还没有数据')).toBeNull()
}, 15000)

it('sums up cross-device dependencies above shared boards and opens them all full screen', async () => {
  signedInWithHome()
  const end = (
    device: string,
    deviceName: string,
    id: string,
    name: string,
    state = 'up',
    current = false,
  ) => ({
    device,
    deviceName,
    current,
    online: true,
    id,
    name,
    state,
  })
  const relations = {
    devices: 2,
    problems: 1,
    relations: [
      {
        from: end('dev-self', '公司主机', 'core', '本机核心', 'up', true),
        to: { ...end('dev-home', '家里电脑', 'nas', '群晖 NAS', 'down'), issue: '虚构的超时' },
        relation: 'backup',
      },
      {
        from: end('dev-self', '公司主机', 'web', '官网', 'up', true),
        to: end('dev-home', '家里电脑', 'db', '数据库'),
      },
      {
        from: end('dev-home', '家里电脑', 'nas', '群晖 NAS', 'down'),
        to: end('dev-self', '公司主机', 'core', '本机核心', 'up', true),
      },
      {
        from: end('dev-home', '家里电脑', 'photos', '相册', 'up'),
        to: end('dev-self', '公司主机', 'web', '官网', 'up', true),
      },
    ],
  }
  const base = vi.mocked(fetch).getMockImplementation()!
  vi.mocked(fetch).mockImplementation(async (input, init) => {
    const path = String(input).replace('/api/v1', '')
    if (path === '/cloud/relations') return Response.json(relations, { headers: { 'X-Lgmon-Scope': 'read' } })
    return base(input, init)
  })
  window.location.hash = '#/'
  render(<App />)
  const card = await screen.findByRole('region', { name: '跨设备依赖' })
  expect(card).toHaveTextContent('4 条关系 · 2 台设备')
  expect(card).toHaveTextContent('1 个异常')
  const rows = within(card).getAllByRole('link')
  expect(rows).toHaveLength(3)
  // The direction is spelled out, so the arrow cannot be read the wrong way round.
  expect(rows[0]).toHaveAccessibleName('本机核心（本机） 依赖 群晖 NAS（家里电脑）：虚构的超时')
  expect(rows[0]).toHaveAttribute('href', '#/monitors/core')
  await userEvent.click(within(card).getByRole('button', { name: '还有 1 条 · 查看全部' }))
  const full = screen.getByRole('dialog', { name: '跨设备依赖' })
  expect(within(full).getAllByRole('listitem')).toHaveLength(4)
  await userEvent.type(within(full).getByLabelText('搜索服务或设备'), '相册')
  expect(within(full).getAllByRole('listitem')).toHaveLength(1)
  expect(within(full).getByRole('link', { name: /官网/ })).toHaveAttribute('href', '#/monitors/web')
  await userEvent.keyboard('{Escape}')
  expect(screen.queryByRole('dialog', { name: '跨设备依赖' })).toBeNull()
}, 15000)

it('says whether signing in recognised this computer, and lets the owner decide whether to stay signed in', async () => {
  registered.add('user@example.com')
  binding = 'known'
  cloudStatus = { ...cloudStatus, lastEmail: 'user@example.com' }
  render(<App />)
  await userEvent.click(await screen.findByRole('button', { name: '登录同步多设备' }))
  const dialog = await screen.findByRole('dialog', { name: '登录并同步多设备' })

  expect(within(dialog).getByLabelText('邮箱地址')).toHaveValue('user@example.com')
  expect(within(dialog).getByText(/这台电脑已在 user@example\.com 名下/)).toBeVisible()
  await userEvent.type(within(dialog).getByLabelText('邮箱地址'), '.cn')
  expect(within(dialog).getByText(/账号里已有它的记录就沿用/)).toBeVisible()
  await userEvent.clear(within(dialog).getByLabelText('邮箱地址'))
  await userEvent.type(within(dialog).getByLabelText('邮箱地址'), 'user@example.com')

  const remember = within(dialog).getByRole('checkbox', { name: /保持登录/ })
  expect(remember).toBeChecked()
  await userEvent.click(remember)
  expect(within(dialog).getByText(/重启、更新后需要重新登录/)).toBeVisible()

  await userEvent.type(within(dialog).getByLabelText('6 位验证码'), '123456')
  await userEvent.click(within(dialog).getByRole('button', { name: '登录' }))
  expect(await within(dialog).findByText(/沿用原来的设备/)).toBeVisible()
  expect(calls.find((call) => call.path === '/cloud/auth/otp/verify')?.body).toMatchObject({
    remember: false,
  })
  await userEvent.click(within(dialog).getByRole('button', { name: '完成' }))
  expect(screen.queryByRole('dialog')).not.toBeInTheDocument()
}, 15000)

it('merges a record this computer left behind and deletes other devices', async () => {
  window.location.hash = '#/settings/account'
  const row = (id: string, name: string, patch: Partial<CloudDeviceSummary> = {}): CloudDeviceSummary => ({
    id,
    name,
    isCurrent: false,
    online: false,
    lastSeen: Date.now() - 3 * 3600_000,
    totalCount: 1,
    downCount: 0,
    boards: [],
    ...patch,
  })
  cloudStatus = {
    ...cloudStatus,
    loggedIn: true,
    email: 'user@example.com',
    deviceName: '公司主机',
    devicesCount: 4,
    remember: true,
  }
  cloudDevices = [
    row('dev-old', '公司主机'),
    row('dev-lab', '实验室'),
    row('dev-renamed', '旧名字', { sameMachine: true }),
    row('dev-self', '公司主机', { isCurrent: true, online: true }),
  ]
  render(<App />)
  const list = await screen.findByRole('list', { name: '账号下的设备' })
  const rows = within(list).getAllByRole('listitem')
  expect(rows[3]).toHaveTextContent('本机')
  expect(rows.map((item) => !!within(item).queryByRole('button', { name: '合并到本机' }))).toEqual([
    true,
    false,
    true,
    false,
  ])

  await userEvent.click(within(rows[1]).getByRole('button', { name: '删除' }))
  const confirm = await screen.findByRole('dialog', { name: '删除设备「实验室」？' })
  await userEvent.click(within(confirm).getByRole('button', { name: '删除' }))
  expect(await screen.findByText('已删除设备「实验室」和它在云端的数据。')).toBeVisible()
  expect(calls.some((call) => call.method === 'DELETE' && call.path === '/cloud/devices/dev-lab')).toBe(true)
  await waitFor(() => expect(within(list).queryByText('实验室')).not.toBeInTheDocument())

  await userEvent.click(within(list).getAllByRole('button', { name: '合并到本机' })[0])
  const merge = await screen.findByRole('dialog', { name: '把「公司主机」合并到本机？' })
  await userEvent.click(within(merge).getByRole('button', { name: '合并到本机' }))
  expect(await screen.findByText('已合并，本机沿用原来的设备记录。')).toBeVisible()
  expect(calls.some((call) => call.method === 'POST' && call.path === '/cloud/devices/dev-old/merge')).toBe(
    true,
  )
  await waitFor(() => expect(within(list).getAllByText('公司主机')).toHaveLength(1))
}, 15000)

it('folds the sidebar to a strip of icons and remembers it', async () => {
  cloudStatus = { ...cloudStatus, loggedIn: true, email: 'user@example.com', devicesCount: 2 }
  cloudDevices = [homeDevice()]
  const first = render(<App />)
  await screen.findByRole('group', { name: '家里电脑 的面板' })
  await userEvent.click(screen.getByRole('button', { name: '收起侧边栏' }))

  const boards = screen.getByRole('navigation', { name: '面板' })
  expect(within(boards).getByRole('link', { name: /本机核心/ })).toHaveAttribute('href', '#/boards/main')
  expect(within(boards).getByRole('link', { name: /^家里电脑 · 在线/ })).toHaveAttribute(
    'href',
    '#/cloud/dev-home/boards/home-main',
  )
  expect(within(boards).getByRole('link', { name: '设备' })).toHaveAttribute('href', '#/devices')
  expect(screen.queryByRole('group', { name: '家里电脑 的面板' })).not.toBeInTheDocument()
  const main = screen.getByRole('navigation', { name: '主导航' })
  expect(within(main).getByRole('link', { name: '设置' })).toBeVisible()
  expect(within(main).getByRole('link', { name: 'user@example.com' })).toHaveAttribute(
    'href',
    '#/settings/account',
  )
  expect(screen.getAllByRole('button', { name: '搜索' }).length).toBeGreaterThan(0)

  first.unmount()
  render(<App />)
  await userEvent.click(await screen.findByRole('button', { name: '展开侧边栏' }))
  expect(await screen.findByRole('group', { name: '家里电脑 的面板' })).toBeVisible()
  expect(localStorage.getItem('lgmon-side-rail')).toBe('')
}, 15000)

it('reorders the device cards, this computer included, by keyboard and by dragging', async () => {
  cloudStatus = { ...cloudStatus, loggedIn: true, email: 'user@example.com', devicesCount: 3 }
  const self = { ...homeDevice(), id: 'dev-self', name: '公司主机', isCurrent: true, boards: [] }
  const lab = { ...homeDevice(), id: 'dev-lab', name: '实验室', boards: [] }
  cloudDevices = [self, homeDevice(), lab]
  render(<App />)
  const nav = await screen.findByRole('navigation', { name: '面板' })
  await within(nav).findByRole('group', { name: '实验室 的面板' })
  const names = () =>
    within(nav)
      .getAllByRole('group')
      .map((card) => card.getAttribute('aria-label')!.replace(' 的面板', ''))
  expect(names()).toEqual(['公司主机', '家里电脑', '实验室'])

  // Arrow keys on the grip move a card one place.
  within(nav).getByRole('button', { name: '移动设备 实验室 的位置' }).focus()
  await userEvent.keyboard('{ArrowUp}')
  await waitFor(() => expect(names()).toEqual(['公司主机', '实验室', '家里电脑']))
  expect(calls.filter((call) => call.path === '/cloud/settings').map((call) => call.body)).toEqual([
    { deviceOrder: ['dev-self', 'dev-lab', 'dev-home'] },
  ])

  // Dragging this computer's header below the last card; jsdom has no layout, so the cards get one.
  const place = () =>
    nav.querySelectorAll<HTMLElement>('[data-device]').forEach((card, index) => {
      card.getBoundingClientRect = () =>
        ({ top: index * 100, height: 90, bottom: index * 100 + 90 }) as DOMRect
    })
  place()
  const head = nav.querySelector<HTMLElement>('.lm-dev-head-static')!
  fireEvent.pointerDown(head, { button: 0, pointerType: 'mouse', clientX: 20, clientY: 20 })
  fireEvent.pointerMove(window, { clientX: 20, clientY: 23 })
  expect(nav.querySelector('[data-dragging]')).toBeNull()
  fireEvent.pointerMove(window, { clientX: 20, clientY: 280 })
  expect(nav.querySelector('[data-dragging]')).toHaveAttribute('data-device', 'dev-self')
  expect(nav.querySelector('[data-drop="after"]')).toHaveAttribute('data-device', 'dev-home')
  fireEvent.keyDown(window, { key: 'Escape' })
  expect(nav.querySelector('[data-dragging]')).toBeNull()
  expect(names()).toEqual(['公司主机', '实验室', '家里电脑'])

  fireEvent.pointerDown(head, { button: 0, pointerType: 'mouse', clientX: 20, clientY: 20 })
  fireEvent.pointerMove(window, { clientX: 20, clientY: 280 })
  fireEvent.pointerUp(window)
  await waitFor(() => expect(names()).toEqual(['实验室', '家里电脑', '公司主机']))
  await waitFor(() =>
    expect(calls.filter((call) => call.path === '/cloud/settings').at(-1)?.body).toEqual({
      deviceOrder: ['dev-lab', 'dev-home', 'dev-self'],
    }),
  )
}, 15000)
