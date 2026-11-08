import { expect, test } from '@playwright/test'
import { mkdir } from 'node:fs/promises'
import { join } from 'node:path'

test('reads full incident event windows on desktop and phone and refreshes late events', async ({ page }) => {
  await page.clock.setFixedTime(new Date(Number(process.env.LGMON_BROWSER_NOW)))
  await page.goto('/#/alerts')
  const row = page.getByRole('row').filter({ hasText: '虚构事件诊断' })
  await row.getByRole('button', { name: '事件详情' }).click()
  const detail = page.getByRole('dialog', { name: '告警事件详情' })
  await expect(detail).toBeVisible()
  await expect(detail.getByText('200 条已加载')).toBeVisible()
  await expect(detail.getByText(/记录 9007199254740993/)).toBeVisible()
  await expect(detail.locator('.eventlog-entry')).toHaveCount(200)
  await detail.getByRole('button', { name: '加载更多事件' }).click()
  await expect(detail.getByText('205 条已加载')).toBeVisible()
  await expect(detail.getByRole('button', { name: '加载更多事件' })).toHaveCount(0)
  const directory = process.env.LGMON_M201_SCREENSHOT_DIR
  if (directory) {
    await mkdir(directory, { recursive: true })
    await detail.evaluate((element) => {
      element.scrollTop = 0
    })
    await page.screenshot({ path: join(directory, 'm2-01-events-desktop.png') })
  }
  await page.setViewportSize({ width: 390, height: 844 })
  await detail.evaluate((element) => {
    element.scrollTop = 0
  })
  expect(await page.evaluate(() => document.documentElement.scrollWidth)).toBeLessThanOrEqual(390)
  if (directory) await page.screenshot({ path: join(directory, 'm2-01-events-mobile.png') })
  expect((await page.request.post('/__fixture/eventlog-late')).status()).toBe(204)
  await detail.getByRole('button', { name: '刷新事件' }).click()
  await expect(detail.getByText('刷新后到达的虚构事件')).toBeVisible()
  await expect(detail.getByText('200 条已加载')).toBeVisible()
  await detail.getByRole('button', { name: '加载更多事件' }).click()
  await expect(detail.getByText('206 条已加载')).toBeVisible()
})
