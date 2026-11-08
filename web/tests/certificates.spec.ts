import { expect, test } from '@playwright/test'
import { mkdir } from 'node:fs/promises'
import { join } from 'node:path'

test('shows persisted certificate expiry on desktop and phone', async ({ page }) => {
  await page.clock.setFixedTime(new Date(Number(process.env.LGMON_BROWSER_NOW)))
  await page.goto('/#/hosts/fake-host/targets/web')
  await expect(page.getByRole('heading', { name: 'HTTPS 证书' })).toBeVisible()
  await expect(page.getByText(/还剩 3 天/)).toBeVisible()
  await expect(page.getByText(/证书到期：2026\/11\/11 16:30:00/)).toBeVisible()
  await expect(page.getByText(/已过期/)).toBeVisible()
  await expect(page.getByText(/fake intermediate/)).toBeVisible()
  const directory = process.env.LGMON_M121_SCREENSHOT_DIR
  if (directory) {
    await mkdir(directory, { recursive: true })
    await page.screenshot({ path: join(directory, 'm1-21-certificates-desktop.png'), fullPage: true })
  }
  await page.setViewportSize({ width: 390, height: 844 })
  expect(await page.evaluate(() => document.documentElement.scrollWidth)).toBeLessThanOrEqual(390)
  await expect(page.getByText(/还剩 3 天/)).toBeVisible()
  if (directory) await page.screenshot({ path: join(directory, 'm1-21-certificates-mobile.png') })
  await page.getByLabel('时间范围').selectOption('24')
  await expect(page.getByText(/还剩 3 天/)).toBeVisible()
  await expect(page.getByRole('alert')).toHaveCount(0)
})
