import { expect, test } from '@playwright/test'
import { mkdir } from 'node:fs/promises'
import { join } from 'node:path'

test.beforeEach(async ({ page }) => {
  await page.clock.setFixedTime(new Date(Number(process.env.LGMON_BROWSER_NOW)))
})

test('pause SSE updates desktop and mobile, read sessions cannot resume, config can resume', async ({
  page,
  request,
}) => {
  await page.goto('/')
  await expect(page.getByRole('heading', { name: '运行概览' })).toBeVisible()
  const response = await request.put('/api/v1/notifications/pause', {
    headers: { Authorization: 'Bearer fake-config' },
    data: { for: '1h', reason: '虚构会议' },
  })
  expect(response.status()).toBe(200)
  const banner = page.locator('[data-control="notification-pause"]')
  await expect(banner).toContainText('通知已暂停到')
  await expect(banner.getByRole('button', { name: '恢复通知' })).toBeDisabled()
  const directory = process.env.LGMON_M117_SCREENSHOT_DIR
  if (directory) {
    await mkdir(directory, { recursive: true })
    await page.screenshot({ path: join(directory, 'm1-17-pause-desktop.png'), fullPage: true })
  }
  await page.setViewportSize({ width: 390, height: 844 })
  await expect(banner).toBeVisible()
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(true)
  if (directory) await page.screenshot({ path: join(directory, 'm1-17-pause-mobile.png') })
  const changed = await request.put('/api/v1/notifications/pause', {
    headers: { Authorization: 'Bearer fake-config' },
    data: { for: '30m', reason: '缩短虚构会议' },
  })
  expect(changed.status()).toBe(200)
  const until = (await changed.json()).until as number
  const expected = await page.evaluate(
    (value) =>
      new Date(value).toLocaleTimeString('zh-CN', { hour: '2-digit', minute: '2-digit', hour12: false }),
    until,
  )
  await expect(banner).toContainText('通知已暂停到 ' + expected)
  await page.getByRole('button', { name: '只读' }).click()
  await page.getByLabel('访问令牌').fill('fake-config')
  await page.getByRole('button', { name: '连接监控器' }).click()
  await expect(banner.getByRole('button', { name: '恢复通知' })).toBeEnabled()
  await banner.getByRole('button', { name: '恢复通知' }).click()
  await expect(banner).toHaveCount(0)
  const resumed = await request.get('/api/v1/notifications/pause', {
    headers: { Authorization: 'Bearer fake-read' },
  })
  expect(await resumed.json()).toEqual({
    paused: false,
    startedAt: null,
    until: null,
    reason: null,
    by: null,
    suppressed: null,
  })
})
