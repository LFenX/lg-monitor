import { expect, test } from '@playwright/test'
import { useToken } from './access'
import { mkdir } from 'node:fs/promises'
import { join } from 'node:path'

test('read access sees delegation; administrator enables and revokes the real API on desktop and phone', async ({
  page,
}) => {
  await page.goto('/#/notifications')
  await expect(page.getByRole('heading', { name: '飞书卡片操作', exact: true })).toBeVisible()
  await expect(page.getByRole('button', { name: '启用卡片操作' })).toBeDisabled()
  await useToken(page, 'fake-admin', page.getByRole('button', { name: '怎么获得' }).first())
  const enable = page.getByRole('button', { name: '启用卡片操作' })
  await expect(enable).toBeEnabled()
  const saved = page.waitForResponse(
    (response) =>
      response.url().endsWith('/notifiers/fake-channel/card-actions') &&
      response.request().method() === 'PUT',
  )
  await enable.click()
  expect((await saved).ok()).toBe(true)
  await expect(page.getByRole('button', { name: '撤销卡片操作' })).toBeEnabled()
  await expect(page.getByText('已授权 · 等待监控器加载')).toBeVisible()
  const directory = process.env.LGMON_M206_SCREENSHOT_DIR
  if (directory) {
    await mkdir(directory, { recursive: true })
    await page
      .locator('.panel')
      .filter({ has: page.getByRole('heading', { name: '飞书卡片操作', exact: true }) })
      .screenshot({ path: join(directory, 'm2-06-card-actions-desktop.png') })
  }
  await page.getByRole('button', { name: '切换到深色' }).click()
  await page.setViewportSize({ width: 390, height: 844 })
  await page.getByRole('heading', { name: '飞书卡片操作', exact: true }).scrollIntoViewIfNeeded()
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true)
  if (directory) await page.screenshot({ path: join(directory, 'm2-06-card-actions-mobile.png') })
  await page.getByRole('button', { name: '撤销卡片操作' }).click()
  await expect(page.getByRole('button', { name: '启用卡片操作' })).toBeEnabled()
  await expect(page.getByText('未授权', { exact: true })).toBeVisible()
})
