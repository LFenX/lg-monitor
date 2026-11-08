import { expect, test } from '@playwright/test'
import { mkdir, readFile } from 'node:fs/promises'
import { join } from 'node:path'

test('hands off the same scoped metadata through copy and download, retries errors and fits mobile', async ({
  page,
}, testInfo) => {
  test.setTimeout(60000)
  await page.clock.setFixedTime(new Date(Number(process.env.LGMON_BROWSER_NOW)))
  // Keep the user's operating-system clipboard untouched.
  await page.addInitScript(() => {
    Object.defineProperty(navigator, 'clipboard', {
      configurable: true,
      value: {
        writeText: async (text: string) => {
          sessionStorage.setItem('fake-diagnostic-copy', text)
        },
      },
    })
  })
  const mutations: string[] = []
  page.on('request', (request) => {
    if (
      request.url().includes('/api/v1/') &&
      request.method() !== 'GET' &&
      !(request.method() === 'POST' && new URL(request.url()).pathname === '/api/v1/query')
    )
      mutations.push(request.method() + ' ' + request.url())
  })
  await page.goto('/#/alerts')
  const row = page.getByRole('row').filter({ hasText: 'fake-private-alert' })
  await row.getByRole('link', { name: '诊断包', exact: true }).click()
  await expect(page.getByRole('heading', { name: '告警诊断包', exact: true })).toBeVisible()
  await expect(page.getByRole('heading', { name: '故障前最近匹配进程', exact: false })).toBeVisible()
  await expect(page.locator('.diagnostic-page')).toContainText('PID 42')
  await expect(page.locator('.diagnostic-page')).toContainText('PID 43')
  await expect(page.locator('.diagnostic-page')).toContainText('9007199254740993')
  await expect(page.locator('.diagnostic-page')).toContainText('虚构启动项')
  await expect(page.locator('.diagnostic-page')).toContainText('不能证明故障不存在')
  await expect(page.locator('.diagnostic-page')).not.toContainText('fake-private')
  await expect(page.locator('.diagnostic-page')).not.toContainText('fake-other-host')
  await page.getByRole('button', { name: '复制给 Agent', exact: true }).click()
  await expect(page.getByRole('status').filter({ hasText: '已复制交接文本和 JSON' })).toBeVisible()
  const copied = await page.evaluate(() => sessionStorage.getItem('fake-diagnostic-copy') || '')
  expect(copied).toContain('先核对 schemaVersion')
  expect(copied).not.toContain('fake-private')
  const copiedValue = JSON.parse(copied.slice(copied.indexOf('\n\n') + 2))
  expect(copiedValue.schemaVersion).toBe(1)
  expect(copiedValue.events[0].recordId).toBe('9007199254740993')
  const [download] = await Promise.all([
    page.waitForEvent('download'),
    page.getByRole('button', { name: '下载 JSON', exact: true }).click(),
  ])
  expect(download.suggestedFilename()).toBe('lgmon-diagnostic-fake-diagnostic.json')
  const output = testInfo.outputPath('fake-diagnostic.json')
  await download.saveAs(output)
  expect(JSON.parse(await readFile(output, 'utf8'))).toEqual(copiedValue)
  await page.getByText('交接文本与完整 JSON', { exact: true }).click()
  await expect(page.getByRole('textbox', { name: '诊断交接文本' })).toHaveValue(copied)
  await page.getByText('交接文本与完整 JSON', { exact: true }).click()
  const directory = process.env.LGMON_M208_SCREENSHOT_DIR
  if (directory) {
    await mkdir(directory, { recursive: true })
    await page.screenshot({ path: join(directory, 'm2-08-diagnostic-desktop.png'), fullPage: true })
  }
  await page.getByRole('button', { name: '切换到深色' }).click()
  await page.setViewportSize({ width: 390, height: 844 })
  expect(await page.evaluate(() => document.documentElement.scrollWidth)).toBeLessThanOrEqual(390)
  if (directory) await page.screenshot({ path: join(directory, 'm2-08-diagnostic-mobile.png') })
  await page.route('**/api/v1/alerts/fake-diagnostic/diagnostics', (route) =>
    route.fulfill({
      status: 503,
      contentType: 'application/json',
      body: JSON.stringify({ error: { code: 'unavailable', message: '虚构临时错误' } }),
    }),
  )
  await page.getByRole('button', { name: '刷新诊断' }).click()
  await expect(page.getByRole('alert')).toContainText('虚构临时错误')
  await page.unroute('**/api/v1/alerts/fake-diagnostic/diagnostics')
  await page.getByRole('button', { name: '刷新诊断' }).click()
  await expect(page.getByRole('alert')).toHaveCount(0)
  await page.evaluate(() => {
    Object.defineProperty(navigator, 'clipboard', { configurable: true, value: undefined })
  })
  await page.getByRole('button', { name: '复制给 Agent', exact: true }).click()
  await expect(page.getByRole('status').filter({ hasText: '复制不可用' })).toBeVisible()
  expect(mutations).toEqual([])
})
