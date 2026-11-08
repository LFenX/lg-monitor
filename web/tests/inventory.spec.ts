import { expect, test } from '@playwright/test'
import { mkdir } from 'node:fs/promises'
import { join } from 'node:path'

test('shows task results, startup changes and coverage and refreshes over the existing SSE', async ({
  page,
}) => {
  test.setTimeout(60000)
  await page.clock.setFixedTime(new Date(Number(process.env.LGMON_BROWSER_NOW)))
  await page.goto('/#/hosts/fake-host')
  await expect(page.getByRole('heading', { name: '启动项与计划任务', exact: true })).toBeVisible()
  const task = page.getByRole('row').filter({ hasText: '虚构备份任务' })
  await expect(task).toContainText('0x80070005')
  await expect(task).toContainText('就绪')
  await expect(page.getByRole('row').filter({ hasText: '虚构未运行任务' })).toContainText('从未运行')
  await expect(page.getByRole('row').filter({ hasText: '虚构未运行任务' })).toContainText('已禁用')
  await expect(page.getByRole('heading', { name: '最近启动项变化' })).toBeVisible()
  await expect(
    page.getByRole('row').filter({ hasText: '虚构启动项' }).filter({ hasText: '修改' }),
  ).toBeVisible()
  await expect(page.getByRole('status').filter({ hasText: '来源读取失败' })).toBeVisible()
  await page.getByText('采集覆盖（3 个来源）', { exact: true }).click()
  await expect(page.getByRole('row').filter({ hasText: 'user/fake/Run' })).toContainText('来源暂不可读')
  const directory = process.env.LGMON_M204_SCREENSHOT_DIR
  await page.getByRole('heading', { name: '启动项与计划任务', exact: true }).scrollIntoViewIfNeeded()
  if (directory) {
    await mkdir(directory, { recursive: true })
    await page
      .locator('.inventory-section')
      .screenshot({ path: join(directory, 'm2-04-inventory-desktop.png') })
  }
  expect(
    (
      await page.request.post('/__test/inventory/update', {
        headers: { Authorization: 'Bearer fake-test-control' },
      })
    ).status(),
  ).toBe(204)
  await expect(task).toContainText('成功 · 0x00000000')
  await expect(page.getByRole('status').filter({ hasText: '来源读取失败' })).toHaveCount(0)
  await expect(page.getByRole('row').filter({ hasText: 'user/fake/Run' })).toContainText('最近一次读取成功')
  await page.getByRole('button', { name: '切换到深色' }).click()
  await page.setViewportSize({ width: 390, height: 844 })
  await page.getByText('采集覆盖（3 个来源）', { exact: true }).click()
  await page.getByRole('heading', { name: '启动项与计划任务', exact: true }).scrollIntoViewIfNeeded()
  expect(await page.evaluate(() => document.documentElement.scrollWidth)).toBeLessThanOrEqual(390)
  await expect(page.locator('.inventory-task-cards')).toContainText('成功 · 0x00000000')
  await expect(page.locator('.inventory-task-cards')).toContainText('从未运行')
  await page.locator('.inventory-section').evaluate((element) => {
    window.scrollBy(0, element.getBoundingClientRect().top - 80)
  })
  if (directory) await page.screenshot({ path: join(directory, 'm2-04-inventory-mobile.png') })
})
