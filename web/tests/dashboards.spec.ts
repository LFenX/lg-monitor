import { expect, test } from '@playwright/test'
import { mkdir } from 'node:fs/promises'
import { join } from 'node:path'
import type { Page } from '@playwright/test'

test.describe.configure({ mode: 'serial' })
async function change(page: Page, action: string) {
  expect(
    await page.evaluate(
      async (action) =>
        (
          await fetch('/__test/dashboards/' + action, {
            method: 'POST',
            headers: { Authorization: 'Bearer fake-test-control' },
          })
        ).status,
      action,
    ),
  ).toBe(204)
}
async function shot(page: Page, name: string) {
  const directory = process.env.LGMON_M115_SCREENSHOT_DIR
  if (!directory) return
  await mkdir(directory, { recursive: true })
  await page.evaluate(() => window.scrollTo(0, 0))
  await expect.poll(() => page.evaluate(() => window.scrollY)).toBe(0)
  await page.screenshot({ path: join(directory, name), fullPage: true })
}
test('all three example dashboards render real queries, both charts, states, table, alerts and log', async ({
  page,
}) => {
  await page.clock.setFixedTime(new Date(Number(process.env.LGMON_BROWSER_NOW)))
  await page.goto('/#/boards/overview')
  await expect(page.getByRole('heading', { name: '运行概览', exact: true })).toBeVisible()
  await expect(page.locator('.dashboard-stat').first()).toHaveText('1')
  await expect(page.getByRole('img', { name: '探测耗时（毫秒）' }).locator('svg')).toBeVisible()
  await expect(page.locator('.dashboard-status').getByText('fake-host / web', { exact: true })).toBeVisible()
  await expect(page.getByText('虚构面板告警', { exact: true })).toBeVisible()
  const refreshed = page.waitForRequest((request) =>
    new URL(request.url()).pathname.endsWith('/panels/healthy-targets/data'),
  )
  await page.getByRole('button', { name: '刷新面板', exact: true }).click()
  await refreshed
  await shot(page, 'm1-15-overview-desktop.png')
  await page.getByLabel('选择面板').selectOption('hosts')
  await expect(page.getByRole('combobox', { name: '时间范围', exact: true })).toHaveValue('6h')
  await expect(page.getByRole('img', { name: '各主机已观测目标数量' }).locator('svg')).toBeVisible()
  await expect(page.getByRole('img', { name: 'CPU 使用率（百分比）' }).locator('svg')).toBeVisible()
  await expect(page.getByRole('cell', { name: '虚构主机', exact: true })).toBeVisible()
  await page.getByLabel('选择面板').selectOption('diagnostics')
  await expect(page.getByText('虚构日志内容', { exact: true })).toBeVisible()
  await expect(page.getByRole('table')).toBeVisible()
  await page.setViewportSize({ width: 390, height: 844 })
  await page.getByRole('button', { name: '切换到深色' }).click()
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true)
  await shot(page, 'm1-15-diagnostics-mobile-dark.png')
})
test('relative ranges use server time despite a skewed browser, collector and fixed ranges are submitted correctly', async ({
  page,
}) => {
  await page.clock.setFixedTime(new Date('2099-01-01T00:00:00Z'))
  const requests: URL[] = []
  page.on('request', (request) => {
    const url = new URL(request.url())
    if (url.pathname.includes('/panels/') && url.pathname.endsWith('/data')) requests.push(url)
  })
  await page.goto('/#/boards/overview')
  await expect(page.locator('.dashboard-stat').first()).toHaveText('1')
  await page.getByRole('combobox', { name: '时间范围', exact: true }).selectOption('6h')
  await expect.poll(() => requests.some((url) => url.searchParams.get('range') === '6h')).toBe(true)
  expect(requests.every((url) => !url.searchParams.has('from') && !url.searchParams.has('to'))).toBe(true)
  await page.getByRole('combobox', { name: '主机', exact: true }).selectOption('fake-host')
  await expect
    .poll(() => requests.some((url) => url.searchParams.get('collector') === 'fake-host'))
    .toBe(true)
  const result = await page.evaluate(async () =>
    (
      await fetch('/api/v1/dashboards/overview/panels/probe-latency/data?range=6h&collector=fake-host', {
        headers: { Authorization: 'Bearer fake-read' },
      })
    ).json(),
  )
  expect(result.params.to).toBe(Number(process.env.LGMON_BROWSER_NOW))
  expect(result.params.to - result.params.from).toBe(6 * 3600000)
  await page.getByRole('combobox', { name: '时间方式', exact: true }).selectOption('fixed')
  await page.getByLabel('开始时间', { exact: true }).fill('2026-11-07T16:00')
  await page.getByLabel('结束时间', { exact: true }).fill('2026-11-07T16:30')
  await expect
    .poll(() =>
      requests.some(
        (url) =>
          url.searchParams.get('from') === '1794038400000' && url.searchParams.get('to') === '1794040200000',
      ),
    )
    .toBe(true)
})
test('SSE reloads definitions and versions, while malformed files and wrong columns stay isolated', async ({
  page,
}) => {
  await page.goto('/#/boards/overview')
  await expect(page.locator('.dashboard-stat').first()).toHaveText('1')
  await expect(page.getByText('实时连接', { exact: true })).toBeVisible()
  await change(page, 'reload')
  await expect(page.getByRole('heading', { name: '运行概览热更新', exact: true })).toBeVisible()
  await expect(page.locator('.dashboard-stat').first()).toHaveText('2')
  await change(page, 'bad-columns')
  await expect(page.getByRole('alert').filter({ hasText: 'wrong_value' })).toBeVisible()
  await expect(page.locator('.dashboard-stat').first()).toHaveText('1')
  await shot(page, 'm1-15-panel-error.png')
  await change(page, 'bad-file')
  await expect(page.getByRole('alert').filter({ hasText: '当前保留上一次成功的版本' })).toBeVisible()
  await expect(page.locator('.dashboard-stat').first()).toHaveText('1')
  await change(page, 'restore')
  await expect(page.getByRole('alert')).toHaveCount(0)
  await change(page, 'delete')
  await expect(page.getByRole('alert').filter({ hasText: '面板页不存在' })).toBeVisible()
  await page.getByLabel('选择面板').selectOption('hosts')
  await expect(page.getByRole('img', { name: '各主机已观测目标数量' })).toBeVisible()
})
