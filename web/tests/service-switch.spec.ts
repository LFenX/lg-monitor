import { expect, test } from '@playwright/test'
import { mkdir } from 'node:fs/promises'
import { join } from 'node:path'

test('previews three target types with read, then explicitly saves selected targets with config', async ({
  page,
}) => {
  await page.clock.setFixedTime(new Date(Number(process.env.LGMON_BROWSER_NOW)))
  await page.goto('/')
  const card = page.locator('.host-card').filter({ hasText: 'fake-host' }).first()
  await card.getByRole('button', { name: '从服务开关导入' }).click()
  let dialog = page.getByRole('dialog', { name: '从服务开关导入 · 虚构主机' })
  await expect(dialog.getByText('虚构博客', { exact: true })).toBeVisible()
  await expect(dialog.getByText('虚构博客 Tunnel', { exact: true })).toBeVisible()
  await expect(dialog.getByText('虚构数据库', { exact: true })).toBeVisible()
  await expect(dialog.getByRole('button', { name: '确认并保存所选目标' })).toBeDisabled()
  await expect(dialog.getByLabel(/一并删除所有/)).not.toBeChecked()
  await dialog.getByRole('button', { name: '关闭' }).click()
  await page.getByRole('button', { name: '只读', exact: true }).click()
  await page.getByLabel('访问令牌', { exact: true }).fill('fake-config')
  await page.getByRole('button', { name: '连接监控器' }).click()
  await expect(page.getByRole('dialog')).toHaveCount(0)
  // Opening with the new token still shares the server-side two-second collector limit.
  await page.waitForTimeout(2100)
  await card.getByRole('button', { name: '从服务开关导入' }).click()
  dialog = page.getByRole('dialog', { name: '从服务开关导入 · 虚构主机' })
  await expect(dialog.getByLabel('导入 虚构博客', { exact: true })).toBeChecked()
  await dialog.getByText('查看将保存的完整目标').first().click()
  await expect(dialog.locator('pre').first()).toContainText('https://fake.example.test/health')
  await expect(dialog.locator('pre').first()).not.toContainText('fake-private-source')
  const directory = process.env.LGMON_M202_SCREENSHOT_DIR
  if (directory) {
    await mkdir(directory, { recursive: true })
    await page.screenshot({ path: join(directory, 'm2-02-import-desktop.png') })
  }
  await page.setViewportSize({ width: 390, height: 844 })
  expect(await page.evaluate(() => document.documentElement.scrollWidth)).toBeLessThanOrEqual(390)
  expect(
    await dialog
      .locator('pre')
      .first()
      .evaluate((element) => element.clientWidth),
  ).toBeGreaterThan(250)
  expect(
    await dialog.locator('.table-scroll').evaluate((element) => element.scrollWidth <= element.clientWidth),
  ).toBe(true)
  if (directory) await page.screenshot({ path: join(directory, 'm2-02-import-mobile.png') })
  await dialog.getByRole('button', { name: '确认并保存所选目标' }).click()
  await expect(dialog.getByText(/已创建 3 个、更新 0 个、删除 0 个目标/)).toBeVisible()
  const targets = await page.request.get('/api/v1/config/targets', {
    headers: { Authorization: 'Bearer fake-read' },
  })
  const saved = await targets.json()
  expect(saved.targets.map((target: { id: string }) => target.id)).toEqual([
    'web',
    'fake-gone',
    'fake-import',
    'fake-import-tunnel',
    'postgresql',
  ])
})
