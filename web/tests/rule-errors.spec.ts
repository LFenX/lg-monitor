import { expect, test } from '@playwright/test'
import { mkdir } from 'node:fs/promises'
import { join } from 'node:path'

test('shows persisted rule errors on desktop and phone, then clears on SSE recovery', async ({ page }) => {
  await page.clock.setFixedTime(new Date(Number(process.env.LGMON_BROWSER_NOW)))
  await page.goto('/')
  await expect(page.getByRole('heading', { name: '运行概览' })).toBeVisible()
  const warning = page.getByRole('status', { name: '规则错误' })
  await expect(warning).toBeVisible()
  await expect(warning.getByRole('listitem')).toHaveCount(3)
  await expect(warning).toContainText('虚构证书检查')
  await expect(warning).toContainText('采集器 fake-legacy 版本过旧')
  await expect(warning).toContainText('虚构 SQL 查询失败 1')
  await expect(warning).not.toContainText('虚构 SQL 查询失败 3')
  await expect(warning).toContainText('等 4 条规则错误')
  const directory = process.env.LGMON_R502_SCREENSHOT_DIR
  if (directory) {
    await mkdir(directory, { recursive: true })
    await page.screenshot({ path: join(directory, 'r5-02-rule-errors-desktop.png'), fullPage: true })
  }
  await page.setViewportSize({ width: 390, height: 844 })
  await expect(warning).toBeVisible()
  expect(await page.evaluate(() => document.documentElement.scrollWidth)).toBeLessThanOrEqual(390)
  if (directory) await page.screenshot({ path: join(directory, 'r5-02-rule-errors-mobile.png') })
  const result = await page.request.post('/__fixture/recover-rule-errors')
  expect(result.status()).toBe(204)
  await expect(warning).not.toBeVisible({ timeout: 5000 })
  const rows = await page.request.get('/api/v1/rule-errors', {
    headers: { Authorization: 'Bearer fake-read' },
  })
  expect((await rows.json()).every((row: { active: boolean }) => !row.active)).toBe(true)
})
