import { expect, test } from '@playwright/test'
import { useToken } from './access'
import { mkdir } from 'node:fs/promises'
import { join } from 'node:path'
import type { MonitorDetail } from '../src/types'

test.beforeEach(async ({ page }) => {
  await page.clock.setFixedTime(new Date(Number(process.env.LGMON_BROWSER_NOW)))
})

test('HTTP LAN access warns before login and at the top of desktop and phone pages', async ({
  page,
  baseURL,
}) => {
  if (!baseURL) throw new Error('browser test requires its temporary HTTP fixture')
  const origin = 'http://192.0.2.9:7444'
  let localSession = true
  // Proxy only to the test's loopback fixture; the documentation address is never contacted.
  await page.route(origin + '/**', async (route) => {
    const address = new URL(route.request().url())
    if (address.pathname === '/api/v1/stream') {
      await route.fulfill({ contentType: 'text/event-stream', body: ': connected\n\n' })
      return
    }
    const headers = await route.request().allHeaders()
    delete headers.host
    delete headers.origin
    const response = await route.fetch({ url: baseURL + address.pathname + address.search, headers })
    if (!localSession && response.headers()['content-type']?.includes('text/html')) {
      const body = (await response.text()).replace(
        /(<script id="lgmon-session" type="application\/json">)[\s\S]*?(<\/script>)/,
        '$1{}$2',
      )
      await route.fulfill({ response, body })
      return
    }
    await route.fulfill({ response })
  })
  await page.goto(origin)
  await expect(page.getByRole('heading', { name: '欢迎使用 lgmon' })).toBeVisible()
  const warning = page.getByRole('alert').filter({ hasText: '正在通过不加密的 HTTP 在局域网提供面板' })
  await expect(warning).toBeVisible()
  const directory = process.env.LGMON_R402_SCREENSHOT_DIR
  if (directory) {
    await mkdir(directory, { recursive: true })
    await page.screenshot({ path: join(directory, 'r4-02-http-warning-desktop.png'), fullPage: true })
  }
  await page.setViewportSize({ width: 390, height: 844 })
  await expect(warning).toBeVisible()
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(true)
  if (directory) await page.screenshot({ path: join(directory, 'r4-02-http-warning-mobile.png') })
  localSession = false
  await page.evaluate(() => sessionStorage.clear())
  await page.reload()
  await expect(page.getByRole('heading', { name: '连接服务监控' })).toBeVisible()
  await expect(warning).toBeVisible()
})

test('desktop and phone show real API snapshots, and local access stays read-only', async ({ page }) => {
  const failures: string[] = []
  page.on('pageerror', (error) => failures.push(error.message))
  await page.goto('/')
  await expect(page.getByRole('heading', { name: '欢迎使用 lgmon' })).toBeVisible()
  await expect(page.getByRole('button', { name: '刷新数据' })).toContainText('实时')
  // Read-only pages keep the create form; using it explains how to get permission instead.
  await page.locator('.lm-welcome').getByRole('button', { name: '新建面板' }).click()
  const access = page.getByRole('dialog', { name: '需要修改权限' })
  await expect(access).toContainText('新建面板需要修改权限')
  await access.getByRole('button', { name: '关闭' }).click()
  await expect(access).toHaveCount(0)
  await expect(
    page.getByRole('status', { name: '电脑提醒' }).getByText('这台电脑的时钟比监控器慢 40 秒，请同步时间'),
  ).toBeVisible()
  await page.getByRole('navigation', { name: '主导航' }).getByRole('link', { name: '设置' }).click()
  await expect(page.getByRole('heading', { name: '设置', exact: true })).toBeVisible()
  await page.getByRole('navigation', { name: '设置分组' }).getByRole('link', { name: '电脑' }).click()
  await expect(page.getByText('42.5%')).toBeVisible()
  await expect(page.getByRole('heading', { name: '虚构主机' })).toBeVisible()
  const clockDirectory = process.env.LGMON_R401_SCREENSHOT_DIR
  if (clockDirectory) {
    await mkdir(clockDirectory, { recursive: true })
    await page.screenshot({ path: join(clockDirectory, 'r4-01-clock-warning-desktop.png'), fullPage: true })
  }
  const directory = process.env.LGMON_SCREENSHOT_DIR
  if (directory) {
    await mkdir(directory, { recursive: true })
    await page.screenshot({ path: join(directory, 'm1-13-desktop.png'), fullPage: true })
  }
  await page.setViewportSize({ width: 390, height: 844 })
  await expect(page.getByRole('button', { name: '打开导航' })).toBeVisible()
  await expect(page.getByRole('navigation', { name: '主导航' })).toBeHidden()
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(true)
  await expect(
    page.getByRole('status', { name: '电脑提醒' }).getByText('这台电脑的时钟比监控器慢 40 秒，请同步时间'),
  ).toBeVisible()
  if (clockDirectory) await page.screenshot({ path: join(clockDirectory, 'r4-01-clock-warning-mobile.png') })
  if (directory) await page.screenshot({ path: join(directory, 'm1-13-mobile.png') })
  await page.getByRole('heading', { name: '无采样主机' }).scrollIntoViewIfNeeded()
  await expect(page.getByRole('heading', { name: '无采样主机' })).toBeVisible()
  await page.getByRole('button', { name: '打开导航' }).click()
  await page.getByRole('navigation', { name: '主导航' }).getByRole('link', { name: '告警' }).click()
  await expect(page.getByRole('navigation', { name: '主导航' })).toBeHidden()
  await expect(page.getByRole('button', { name: '确认' })).toBeDisabled()
  await page.getByLabel('严重程度').selectOption('warning')
  await expect(page.getByText('没有符合条件的告警', { exact: true })).toBeVisible()
  await page.getByLabel('严重程度').selectOption('')
  await page.getByRole('combobox', { name: '主机', exact: true }).selectOption('fake-pending')
  await expect(page.getByText('没有符合条件的告警', { exact: true })).toBeVisible()
  await page.getByRole('combobox', { name: '主机', exact: true }).selectOption('')
  const alertSearch = page.getByRole('searchbox', { name: '搜索目标或告警内容' })
  await alertSearch.fill('没有这个目标')
  await expect(page.getByText('没有符合条件的告警', { exact: true })).toBeVisible()
  await alertSearch.clear()
  await expect(page.getByRole('button', { name: '确认' })).toBeDisabled()
  await page.getByRole('button', { name: '打开导航' }).click()
  await page.getByRole('complementary', { name: '导航' }).getByRole('button', { name: '切换到深色' }).click()
  await page.locator('.lm-scrim').click({ position: { x: 380, y: 400 } })
  await expect(page.getByRole('navigation', { name: '主导航' })).toBeHidden()
  await expect(page.locator('html')).toHaveAttribute('data-theme', 'dark')
  if (directory)
    await page.screenshot({ path: join(directory, 'm1-13-alerts-mobile-dark.png'), fullPage: true })
  expect(failures).toEqual([])
})

test('host and target details render actual ECharts, service facts and observed gaps', async ({ page }) => {
  await page.goto('/#/hosts/fake-host')
  await expect(page.getByRole('heading', { name: 'Windows 服务' })).toBeVisible()
  await expect(page.getByText('虚构数据库服务')).toBeVisible()
  await expect(page.getByText('虚构断线缺口')).toBeVisible()
  await expect(page.getByRole('img', { name: 'CPU 使用率' }).locator('svg').first()).toBeVisible()
  const directory = process.env.LGMON_SCREENSHOT_DIR
  if (directory) await page.screenshot({ path: join(directory, 'm1-13-host.png'), fullPage: true })
  await page.getByRole('link', { name: '示例网站', exact: true }).click()
  await expect(page.getByRole('heading', { name: '状态时间线' })).toBeVisible()
  await expect.poll(() => page.evaluate(() => window.scrollY)).toBe(0)
  await expect(page.getByRole('img', { name: '探测耗时' }).locator('svg').first()).toBeVisible()
  await expect(page.getByRole('img', { name: '进程内存使用量' }).locator('svg').first()).toBeVisible()
  if (directory) await page.screenshot({ path: join(directory, 'm1-13-target.png'), fullPage: true })
  await page.setViewportSize({ width: 390, height: 844 })
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(true)
  await page.getByLabel('时间范围').selectOption('24')
  await expect(page.getByRole('alert')).toHaveCount(0)
})

test('service history uses each bucket availability while keeping live state and empty history distinct', async ({
  page,
}) => {
  await page.goto('/#/monitors/web')
  await expect(page.getByRole('heading', { name: '示例网站', exact: true })).toBeVisible()
  for (const name of ['最近 24 小时状态', '最近 30 天状态']) {
    const latest = page.getByRole('img', { name }).locator('i').last()
    await expect(latest).toHaveAttribute('data-live', 'true')
    expect(await latest.evaluate((element) => getComputedStyle(element).backgroundImage)).toBe('none')
  }
  // While it is failing the outage is listed at once, with the service's name, the start to the second and how long so far.
  const events = page.getByRole('region', { name: '事件' })
  const outage = events.getByRole('region', { name: '示例网站 的事件' })
  await expect(outage).toContainText('进行中')
  await expect(outage).toContainText('已持续')
  await expect(outage.locator('time').first()).toHaveText(/^\d{4}-\d{2}-\d{2} \d{2}:\d{2}:\d{2}$/)
  expect((await page.request.post('/__fixture/history-state?state=up')).status()).toBe(204)
  await page.reload()
  await expect(page.getByRole('img', { name: '最近 24 小时状态' }).locator('i').last()).not.toHaveAttribute(
    'data-live',
  )
  const detail: MonitorDetail = await page.evaluate(async () => {
    const response = await fetch('/api/v1/monitors/web', {
      headers: { Authorization: 'Bearer fake-read' },
    })
    if (!response.ok) throw new Error('Could not read the isolated history fixture')
    return response.json()
  })
  for (const [label, slots] of [
    ['最近 24 小时状态', detail.slots],
    ['最近 30 天状态', detail.days],
  ] as const) {
    const mixed = slots.findLast(
      (slot) => slot.state === 'down' && slot.uptime != null && slot.uptime > 0 && slot.uptime < 100,
    )
    expect(mixed).toBeDefined()
    const history = page.getByRole('img', { name: label })
    const bar = history.locator('i[data-state="down"][data-uptime]').last()
    await expect(bar).toHaveAttribute('data-uptime', String(mixed!.uptime))
    await expect(bar).toHaveAttribute('data-timed', 'true')
    expect(await bar.evaluate((element) => element.style.getPropertyValue('--beat-up'))).toBe(
      mixed!.uptime + '%',
    )
    await expect(bar).toHaveAttribute('title', /正常 .*% · 异常 .*%/)
    const confirmed = slots
      .flatMap((slot) => slot.segments || [])
      .find((segment) => segment.state === 'down' && segment.to - segment.from === 60000)
    expect(confirmed).toBeDefined()
    expect(
      slots
        .flatMap((slot) => slot.segments || [])
        .some((segment) => segment.state === 'down' && segment.to - segment.from === 1000),
    ).toBe(false)
    const marker = history.locator(`.lm-beat-segment[data-from="${confirmed!.from}"][data-state="down"]`)
    await expect(marker).toHaveAttribute('title', / · 60 秒$/)
    const markerBox = await marker.boundingBox()
    expect(markerBox!.width).toBeGreaterThanOrEqual(2)
    const empty = history.locator('i[data-state="none"]').first()
    await expect(empty).not.toHaveAttribute('data-uptime')
    expect(await empty.evaluate((element) => getComputedStyle(element).backgroundImage)).toBe('none')
  }
  await expect(page.locator('.lm-svc-title .lm-badge')).not.toHaveAttribute('data-state', 'down')
  const directory = process.env.LGMON_U411_SCREENSHOT_DIR
  if (directory) {
    await mkdir(directory, { recursive: true })
    await page.screenshot({
      path: join(directory, 'u4-11-history-desktop.png'),
      fullPage: true,
      animations: 'disabled',
    })
  }
  await page.setViewportSize({ width: 375, height: 812 })
  // The drawer slides away after a resize; measure once the layout has settled.
  await expect.poll(() => page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true)
  if (directory)
    await page.screenshot({
      path: join(directory, 'u4-11-history-mobile.png'),
      fullPage: true,
      animations: 'disabled',
    })
  await page.getByRole('button', { name: '打开导航' }).click()
  await page.getByRole('complementary', { name: '导航' }).getByRole('button', { name: '切换到深色' }).click()
  await page.locator('.lm-scrim').click({ position: { x: 365, y: 400 } })
  await expect(page.locator('html')).toHaveAttribute('data-theme', 'dark')
  await expect(page.getByRole('img', { name: '最近 24 小时状态' }).locator('i')).toHaveCount(96)
  const markerWidths = await page.evaluate(() =>
    Array.from(
      document.querySelectorAll<HTMLElement>(
        '[aria-label="最近 24 小时状态"] .lm-beat-segment[data-state="down"]',
      ),
    ).map((element) => element.getBoundingClientRect().width),
  )
  expect(markerWidths.length).toBeGreaterThan(0)
  expect(Math.min(...markerWidths)).toBeGreaterThanOrEqual(2)
  if (directory)
    await page.screenshot({
      path: join(directory, 'u4-11-history-mobile-dark.png'),
      fullPage: true,
      animations: 'disabled',
    })
  expect((await page.request.post('/__fixture/history-state?state=down')).status()).toBe(204)
})

test('a verified config credential can acknowledge and silence through real API handlers', async ({
  page,
}) => {
  await page.goto('/#/alerts')
  await expect(page.getByRole('button', { name: '确认' })).toBeDisabled()
  await useToken(page, 'fake-config', page.getByRole('button', { name: '怎么获得' }))
  await expect(page.getByRole('dialog')).toHaveCount(0)
  await expect(page.getByRole('button', { name: '确认' })).toBeEnabled()
  await page.getByRole('button', { name: '确认' }).click()
  await expect(page.getByText('已确认告警；本次故障的重复提醒已停止。')).toBeVisible()
  await expect(page.getByRole('button', { name: '已确认', exact: true })).toBeDisabled()
  await page.getByRole('button', { name: '静音', exact: true }).click()
  await page.getByLabel('原因（选填）').fill('虚构浏览器验收维护')
  await page.getByRole('button', { name: '设置静音', exact: true }).click()
  await expect(page.getByText('已设置静音。监控仍会记录状态，静音期间不发送匹配的通知。')).toBeVisible()
  const audit = await page.evaluate(async () => {
    const response = await fetch('/api/v1/query', {
      method: 'POST',
      headers: { Authorization: 'Bearer fake-config', 'Content-Type': 'application/json' },
      body: JSON.stringify({ sql: "SELECT reason FROM silences WHERE rule_id='down'" }),
    })
    return response.json()
  })
  expect(audit.rows).toContainEqual(['虚构浏览器验收维护'])
})

test('SSE reset reloads a snapshot and disconnected streams expose their state', async ({ page }) => {
  let snapshots = 0
  page.on('request', (request) => {
    if (new URL(request.url()).pathname === '/api/v1/collectors') snapshots++
  })
  await page.route('**/api/v1/stream', (route) =>
    route.fulfill({ status: 200, contentType: 'text/event-stream', body: 'event: reset\ndata: {}\n\n' }),
  )
  await page.goto('/')
  await expect(page.getByRole('heading', { name: '欢迎使用 lgmon' })).toBeVisible()
  await expect.poll(() => snapshots).toBeGreaterThan(1)
  await expect(page.getByRole('button', { name: '刷新数据' })).toContainText('重连中')
})

test('notification and dashboard entries show existing data and honest future capabilities', async ({
  page,
}) => {
  await page.goto('/#/notifications')
  await expect(page.getByText('飞书应用', { exact: true }).first()).toBeVisible()
  await expect(page.getByRole('cell', { name: 'fake-channel', exact: true })).toBeVisible()
  await expect(page.getByText('预设模板')).toHaveCount(4)
  const directory = process.env.LGMON_SCREENSHOT_DIR
  if (directory) await page.screenshot({ path: join(directory, 'm1-13-notifications.png'), fullPage: true })
  await page.getByRole('link', { name: '接入飞书', exact: true }).click()
  await expect(page.getByRole('region', { name: '交给 Agent 接入飞书' })).toBeVisible()
  await expect(page.getByRole('button', { name: '复制这段话' })).toBeVisible()
  if (directory) await page.screenshot({ path: join(directory, 'm1-13-setup.png'), fullPage: true })
  await page.getByRole('link', { name: 'lgmon 首页' }).click()
  await expect(page.getByRole('heading', { name: '欢迎使用 lgmon' })).toBeVisible()
  await expect(page.getByRole('region', { name: '让 Agent 帮你开始' })).toBeVisible()
  if (directory) await page.screenshot({ path: join(directory, 'm1-13-boards.png'), fullPage: true })
})

test('opening lgmon shows the small start page with the services and the agent card', async ({ page }) => {
  // The window lgmon opens is about this size.
  await page.setViewportSize({ width: 460, height: 800 })
  await page.goto('/#/start')
  await expect(page.getByRole('region', { name: '服务概况' })).toBeVisible()
  await expect(page.getByRole('navigation', { name: '面板', exact: true })).toHaveCount(0)
  const card = page.locator('.agent-card')
  await expect(card.getByRole('button', { name: '复制这段话' })).toBeVisible()
  await expect(card).not.toContainText('start_here')
  await card.getByRole('button', { name: '显示这段话' }).click()
  await expect(card).toContainText('start_here')
  await expect(card).not.toContainText('【')
  await expect(page.getByRole('link', { name: '打开看板' })).toHaveAttribute('target', '_blank')
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true)
  await page.setViewportSize({ width: 1440, height: 1000 })
})

test('agent prompts and board creation work on desktop and phone without leaving the dialog open', async ({
  page,
}) => {
  await page.goto('/')
  await useToken(page, 'fake-config')
  await expect(page.getByRole('dialog')).toHaveCount(0)
  for (const viewport of [
    { width: 1440, height: 1000 },
    { width: 375, height: 812 },
  ]) {
    await page.setViewportSize(viewport)
    await page.goto('/')
    await expect(page.getByRole('heading', { name: '欢迎使用 lgmon' })).toBeVisible()
    // The message for the agent is folded away until someone wants to read it.
    const start = page.getByRole('region', { name: '让 Agent 帮你开始' })
    await expect(start.getByText(/create_board/)).toHaveCount(0)
    await start.getByRole('button', { name: '显示这段话' }).click()
    await expect(start.getByText(/create_board/)).toBeVisible()
    await expect(start.getByText(/--elevate --json/)).toBeVisible()
    await start.getByRole('button', { name: '收起这段话' }).click()
    if (viewport.width < 600) await page.getByRole('button', { name: '打开导航' }).click()
    await page
      .getByRole('navigation', { name: '面板', exact: true })
      .getByRole('button', { name: '新建面板', exact: true })
      .click()
    const dialog = page.getByRole('dialog', { name: '新建面板' })
    const name = '虚构 U4-10 ' + (viewport.width < 600 ? '手机' : '桌面')
    await dialog.getByRole('textbox', { name: '名称', exact: true }).fill(name)
    const agent = dialog.getByRole('region', { name: '交给 Agent 创建' })
    await expect(agent.getByRole('button', { name: '复制这段话' })).toBeVisible()
    await agent.getByRole('button', { name: '显示这段话' }).click()
    await expect(agent).toContainText('name 使用 "' + name + '"')
    expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true)
    const box = await dialog.boundingBox()
    expect(box).not.toBeNull()
    expect(box!.y + box!.height).toBeLessThanOrEqual(viewport.height)
    const directory = process.env.LGMON_U410_SCREENSHOT_DIR
    const device = viewport.width < 600 ? 'mobile' : 'desktop'
    if (directory) {
      await mkdir(directory, { recursive: true })
      await page.screenshot({
        path: join(directory, 'u4-10-new-board-' + device + '.png'),
        animations: 'disabled',
      })
    }
    const created = page.waitForResponse(
      (response) =>
        response.request().method() === 'POST' && new URL(response.url()).pathname === '/api/v1/boards',
    )
    await dialog.getByRole('button', { name: '新建', exact: true }).click()
    const board = await (await created).json()
    try {
      await expect(dialog).toHaveCount(0)
      await expect(page.getByRole('heading', { name, exact: true })).toBeVisible()
      // The first board gathers existing services, so it may not be empty; either way adding starts from the agent.
      const empty = page.getByRole('region', { name: '空面板指引' })
      const list = page.getByRole('list', { name: '服务列表' })
      await expect(empty.or(list)).toBeVisible()
      if (await empty.count()) {
        await empty.getByRole('button', { name: '显示这段话' }).click()
        await expect(empty).toContainText('board: ' + board.id)
        await expect(empty.getByRole('button', { name: '手动创建' })).toBeVisible()
      } else {
        await page.getByRole('button', { name: '添加服务', exact: true }).click()
        const add = page.getByRole('dialog', { name: '添加服务' })
        await add.getByRole('button', { name: '显示这段话' }).click()
        await expect(add).toContainText('board: ' + board.id)
        await expect(add.getByRole('button', { name: '手动创建' })).toBeVisible()
        await add.getByRole('button', { name: '关闭' }).click()
      }
      if (directory)
        await page.screenshot({
          path: join(directory, 'u4-10-created-board-' + device + '.png'),
          animations: 'disabled',
        })
    } finally {
      const removed = await page.request.delete('/api/v1/boards/' + encodeURIComponent(board.id), {
        headers: { Authorization: 'Bearer fake-config' },
      })
      expect(removed.ok()).toBe(true)
    }
  }
})
