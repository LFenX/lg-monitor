import { expect, test } from '@playwright/test'
import { mkdir } from 'node:fs/promises'
import { join } from 'node:path'

test('read access cannot restart; administrator reviews exact host and service, confirms once, then reads the same result', async ({
  page,
}) => {
  await page.goto('/#/hosts/fake-host')
  const start = page.getByRole('button', { name: '准备重启 FakeService' })
  await expect(start).toBeVisible()
  await expect(start).toBeDisabled()
  await page.getByRole('button', { name: '只读', exact: true }).click()
  await page.getByLabel('访问令牌', { exact: true }).fill('fake-admin')
  await page.getByRole('button', { name: '连接监控器' }).click()
  await expect(start).toBeEnabled()
  await start.click()
  const modal = page.getByRole('dialog', { name: '确认重启服务' })
  await expect(modal.getByText('虚构主机', { exact: true })).toBeVisible()
  await expect(modal.getByText('FakeService', { exact: true })).toBeVisible()
  const execute = modal.getByRole('button', { name: '确认执行本次重启' })
  await expect(execute).toBeDisabled()
  await modal.getByLabel('服务重启原因').fill('本人确认的虚构浏览器重启')
  await expect(execute).toBeDisabled()
  await modal.getByRole('checkbox').check()
  const directory = process.env.LGMON_M207_SCREENSHOT_DIR
  if (directory) {
    await mkdir(directory, { recursive: true })
    await page.screenshot({ path: join(directory, 'm2-07-restart-desktop.png') })
  }
  await modal.getByRole('button', { name: '关闭' }).click()
  await page.setViewportSize({ width: 390, height: 844 })
  await page.getByRole('button', { name: '切换到深色' }).click()
  await start.click()
  await modal.getByLabel('服务重启原因').fill('本人确认的虚构浏览器重启')
  await modal.getByRole('checkbox').check()
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true)
  if (directory) await page.screenshot({ path: join(directory, 'm2-07-restart-mobile.png') })
  const response = page.waitForResponse(
    (value) =>
      value.url().endsWith('/collectors/fake-host/operations') && value.request().method() === 'POST',
  )
  await execute.click()
  expect((await response).ok()).toBe(true)
  await expect(modal).toHaveCount(0)
  await expect(page.getByText('已核对运行', { exact: true })).toBeVisible()
  const requestId = await page.getByLabel('服务操作请求 ID').inputValue()
  expect(requestId).toMatch(/^[a-f0-9]{32}$/)
  const query = page.waitForResponse(
    (value) => value.url().endsWith('/operations/' + requestId) && value.request().method() === 'GET',
  )
  await page.getByRole('button', { name: '只读查询操作结果' }).click()
  expect((await query).ok()).toBe(true)
  await page.getByRole('heading', { name: '受限服务操作', exact: true }).scrollIntoViewIfNeeded()
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true)
})
