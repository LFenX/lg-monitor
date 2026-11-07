import { expect, test } from '@playwright/test'
import { mkdir } from 'node:fs/promises'
import { join } from 'node:path'

test.beforeEach(async ({ page }) => {
  await page.clock.setFixedTime(new Date(Number(process.env.LGMON_BROWSER_NOW)))
})

test('desktop and phone show real API snapshots, and local access stays read-only', async ({ page }) => {
  const failures: string[] = []
  page.on('pageerror', (error) => failures.push(error.message))
  await page.goto('/')
  await expect(page.getByRole('heading', { name: '运行概览' })).toBeVisible()
  await expect(page.getByText('实时连接', { exact: true })).toBeVisible()
  await expect(page.getByText('42.5%')).toBeVisible()
  await expect(page.getByRole('heading', { name: '虚构主机' })).toBeVisible()
  const directory = process.env.LGMON_SCREENSHOT_DIR
  if (directory) {
    await mkdir(directory, { recursive: true })
    await page.screenshot({ path: join(directory, 'm1-13-desktop.png'), fullPage: true })
  }
  await page.setViewportSize({ width: 390, height: 844 })
  await expect(page.getByRole('navigation', { name: '手机导航' })).toBeVisible()
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(true)
  if (directory) await page.screenshot({ path: join(directory, 'm1-13-mobile.png') })
  await page.getByRole('heading', { name: '无采样主机' }).scrollIntoViewIfNeeded()
  await expect(page.getByRole('heading', { name: '无采样主机' })).toBeVisible()
  await page.getByRole('navigation', { name: '手机导航' }).getByRole('link', { name: '告警' }).click()
  await expect(page.getByRole('button', { name: '确认' })).toBeDisabled()
  await page.getByLabel('严重程度').selectOption('warning')
  await expect(page.getByText('没有符合条件的告警', { exact: true })).toBeVisible()
  await page.getByLabel('严重程度').selectOption('')
  await page.getByRole('combobox', { name: '主机', exact: true }).selectOption('fake-pending')
  await expect(page.getByText('没有符合条件的告警', { exact: true })).toBeVisible()
  await page.getByRole('combobox', { name: '主机', exact: true }).selectOption('')
  await page.getByRole('searchbox', { name: '搜索告警' }).fill('没有这个目标')
  await expect(page.getByText('没有符合条件的告警', { exact: true })).toBeVisible()
  await page.getByRole('searchbox', { name: '搜索告警' }).clear()
  await expect(page.getByRole('button', { name: '确认' })).toBeDisabled()
  await page.getByRole('button', { name: '切换到深色' }).click()
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

test('a verified config credential can acknowledge and silence through real API handlers', async ({
  page,
}) => {
  await page.goto('/#/alerts')
  await expect(page.getByRole('button', { name: '确认' })).toBeDisabled()
  await page.getByRole('button', { name: '只读', exact: true }).click()
  await page.getByLabel('访问令牌').fill('fake-config')
  await page.getByRole('button', { name: '连接监控器' }).click()
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
  await expect(page.getByRole('heading', { name: '运行概览' })).toBeVisible()
  await expect.poll(() => snapshots).toBeGreaterThan(1)
  await expect(page.getByText('正在重连', { exact: true })).toBeVisible()
})

test('notification and dashboard entries show existing data and honest future capabilities', async ({
  page,
}) => {
  await page.goto('/#/notifications')
  await expect(page.getByText('飞书应用', { exact: true }).first()).toBeVisible()
  await expect(page.getByText('fake-channel', { exact: true })).toBeVisible()
  await expect(page.getByText('预设模板')).toHaveCount(4)
  const directory = process.env.LGMON_SCREENSHOT_DIR
  if (directory) await page.screenshot({ path: join(directory, 'm1-13-notifications.png'), fullPage: true })
  await page.getByRole('link', { name: '接入飞书', exact: true }).click()
  await expect(page.getByText('1. 填写应用凭据', { exact: true })).toBeVisible()
  if (directory) await page.screenshot({ path: join(directory, 'm1-13-setup.png'), fullPage: true })
  await page
    .getByRole('navigation', { name: '主导航' })
    .getByRole('link', { name: '面板', exact: true })
    .click()
  await expect(page.getByText('声明式面板尚未开放', { exact: true })).toBeVisible()
  if (directory) await page.screenshot({ path: join(directory, 'm1-13-boards.png'), fullPage: true })
})
