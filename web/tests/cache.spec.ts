import { expect, test } from '@playwright/test'
import { mkdir } from 'node:fs/promises'
import { join } from 'node:path'

test('shows cache capacity on desktop and phone, and clears a short-capacity warning', async ({ page }) => {
  await page.clock.setFixedTime(new Date(Number(process.env.LGMON_BROWSER_NOW)))
  await page.goto('/')
  await expect(page.getByRole('heading', { name: '运行概览' })).toBeVisible()
  await expect(page.getByText('缓存用量：25.6 / 500.0 MB')).toBeVisible()
  await expect(page.getByText('写入速度：50.0 MB/小时')).toBeVisible()
  await expect(page.getByText('预计可缓存：9.8 小时')).toBeVisible()
  const warning = page.getByRole('note').filter({ hasText: '按当前数据量，断网时只能缓存约 9.8 小时' })
  await expect(warning).toBeVisible()
  const directory = process.env.LGMON_M122_SCREENSHOT_DIR
  if (directory) {
    await mkdir(directory, { recursive: true })
    await page.screenshot({ path: join(directory, 'm1-22-cache-desktop.png'), fullPage: true })
  }
  await page.setViewportSize({ width: 390, height: 844 })
  await page.getByText('缓存用量：25.6 / 500.0 MB').scrollIntoViewIfNeeded()
  await warning.evaluate((element) => element.scrollIntoView({ block: 'center' }))
  await expect(warning).toBeVisible()
  const warningBox = await warning.boundingBox()
  expect(warningBox && warningBox.y + warningBox.height).toBeLessThan(744)
  expect(await page.evaluate(() => document.documentElement.scrollWidth)).toBeLessThanOrEqual(390)
  if (directory) await page.screenshot({ path: join(directory, 'm1-22-cache-mobile.png') })
  const result = await page.request.post('/__fixture/cache-normal')
  expect(result.status()).toBe(204)
  await page.getByRole('button', { name: '刷新数据' }).click()
  await expect(warning).not.toBeVisible()
  await expect(page.getByText('预计可缓存：49.2 小时')).toBeVisible()
})
