import { expect, test } from '@playwright/test'
import { mkdir } from 'node:fs/promises'
import { join } from 'node:path'

test('shows configuration upgrade status on desktop and phone, and refreshes idle acknowledgements', async ({
  page,
}) => {
  await page.clock.setFixedTime(new Date(Number(process.env.LGMON_BROWSER_NOW)))
  let applied = false,
    reads = 0
  await page.route('**/api/v1/collectors', async (route) => {
    const response = await route.fetch()
    const collectors = await response.json()
    if (applied)
      collectors[0].config = {
        state: 'applied',
        version: 'fake-confirmed-version',
        message: '采集器已应用配置',
      }
    reads++
    await route.fulfill({ response, json: collectors })
  })
  await page.goto('/')
  await expect(page.getByRole('heading', { name: '运行概览' })).toBeVisible()
  const warning = page.getByRole('status').filter({ hasText: '配置未更新：目标 blog 使用 match.workDir' })
  await expect(warning).toBeVisible()
  await expect(page.getByText(/配置：采集器需要升级/)).toBeVisible()
  await expect.poll(() => reads).toBeGreaterThanOrEqual(2)
  const directory = process.env.LGMON_M120_SCREENSHOT_DIR
  if (directory) {
    await mkdir(directory, { recursive: true })
    await page.screenshot({ path: join(directory, 'm1-20-config-desktop.png'), fullPage: true })
  }
  await page.setViewportSize({ width: 390, height: 844 })
  await expect(warning).toBeVisible()
  expect(await page.evaluate(() => document.documentElement.scrollWidth)).toBeLessThanOrEqual(390)
  if (directory) await page.screenshot({ path: join(directory, 'm1-20-config-mobile.png') })
  const before = reads
  applied = true
  await expect(warning).not.toBeVisible({ timeout: 20000 })
  expect(reads).toBeGreaterThan(before)
  await expect(page.getByText('配置：已应用')).toBeVisible()
})
