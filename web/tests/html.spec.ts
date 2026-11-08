import { expect, test } from '@playwright/test'
import { mkdir } from 'node:fs/promises'
import { join } from 'node:path'

test('runs custom HTML with read-only queries, shared SSE, isolation and responsive themes', async ({
  page,
  context,
}) => {
  const outside: string[] = []
  await page.route('**/lgmon-frame.html', async (route) => {
    await new Promise((resolve) => setTimeout(resolve, 120))
    await route.continue()
  })
  await page.addInitScript(() => {
    if (window.parent !== window) return
    let previous: Element | null = null
    const spoof = () => {
      const frame = document.querySelector('.html-dashboard-frame')
      if (frame && frame !== previous) {
        previous = frame
        window.postMessage({ type: 'lgmon-ready', version: 1 }, '*')
      }
    }
    new MutationObserver(spoof).observe(document, { subtree: true, childList: true })
  })
  await context.route('https://blocked.invalid/**', async (route) => {
    outside.push(route.request().url())
    await route.abort()
  })
  await page.clock.setFixedTime(new Date(Number(process.env.LGMON_BROWSER_NOW)))
  await page.goto('/#/boards/overview')
  await page.getByRole('button', { name: '只读', exact: true }).click()
  await page.getByLabel('访问令牌', { exact: true }).fill('fake-config')
  await page.getByRole('button', { name: '连接监控器' }).click()
  await expect(page.getByRole('dialog')).toHaveCount(0)
  const frame = page.frameLocator('.html-dashboard-frame')
  await expect(frame.locator('#value')).toHaveText('up')
  await expect(frame.locator('#connection')).toHaveText('connected')
  const headers = { Authorization: 'Bearer fake-test-control' }
  const streams = await (await page.request.get('/__test/html/stats', { headers })).json()
  const iframe = page.locator('.html-dashboard-frame')
  expect(await iframe.getAttribute('sandbox')).toBe('allow-scripts')
  const isolated = await frame.locator('body').evaluate(async () => {
    const sdk = (
      window as unknown as { lgmon: { query: (sql: string) => Promise<unknown>; context: unknown } }
    ).lgmon
    let parentBlocked = false,
      storageBlocked = false,
      writeBlocked = false,
      directBlocked = false
    try {
      void window.parent.document.body
    } catch {
      parentBlocked = true
    }
    try {
      void window.localStorage.length
    } catch {
      storageBlocked = true
    }
    try {
      await sdk.query('DELETE FROM collectors')
    } catch {
      writeBlocked = true
    }
    try {
      await fetch('/api/v1/config/targets', { method: 'PUT', body: '{"targets":[]}' })
    } catch {
      directBlocked = true
    }
    return {
      parentBlocked,
      storageBlocked,
      writeBlocked,
      directBlocked,
      origin: window.origin,
      context: JSON.stringify(sdk.context),
    }
  })
  expect(isolated).toMatchObject({
    parentBlocked: true,
    storageBlocked: true,
    writeBlocked: true,
    directBlocked: true,
    origin: 'null',
  })
  expect(isolated.context).not.toContain('fake-config')
  await page.getByLabel('主机', { exact: true }).selectOption('fake-host')
  const parameters = await frame.locator('body').evaluate(async () => {
    const sdk = (
      window as unknown as {
        lgmon: {
          query: (
            sql: string,
            params?: unknown,
            options?: { signal: AbortSignal },
          ) => Promise<{ params: { from: number; to: number; collector: string | null } }>
        }
      }
    ).lgmon
    const inherited = await sdk.query('SELECT :collector AS host')
    const explicit = await sdk.query('SELECT :collector AS host', { collector: null, range: '6h' })
    const controller = new AbortController()
    const waiting = sdk.query(
      'WITH RECURSIVE x(n) AS (SELECT 1 UNION ALL SELECT n+1 FROM x) SELECT sum(n) FROM x',
      undefined,
      { signal: controller.signal },
    )
    controller.abort()
    let cancelled = false
    try {
      await waiting
    } catch (error) {
      cancelled = error instanceof DOMException && error.name === 'AbortError'
    }
    return {
      collector: inherited.params.collector,
      all: explicit.params.collector,
      span: explicit.params.to - explicit.params.from,
      cancelled,
    }
  })
  expect(parameters).toEqual({ collector: 'fake-host', all: null, span: 21600000, cancelled: true })
  expect((await page.request.post('/__test/html/target', { headers })).status()).toBe(204)
  await expect(frame.locator('#event')).toHaveText('实时：down')
  await expect(frame.locator('#value')).toHaveText('down')
  expect((await (await page.request.get('/__test/html/stats', { headers })).json()).streams).toBe(
    streams.streams,
  )
  expect((await page.request.post('/__test/html/reconnect', { headers })).status()).toBe(204)
  await expect
    .poll(async () => (await (await page.request.get('/__test/html/stats', { headers })).json()).streams)
    .toBe(streams.streams + 1)
  await expect(frame.locator('#connection')).toHaveText('connected')
  await expect(frame.locator('#value')).toHaveText('down')
  const directory = process.env.LGMON_M203_SCREENSHOT_DIR
  if (directory) {
    await mkdir(directory, { recursive: true })
    await page.screenshot({ path: join(directory, 'm2-03-html-desktop.png'), fullPage: true })
  }
  await page.getByRole('button', { name: '切换到深色' }).click()
  await expect(frame.locator('html')).toHaveAttribute('data-theme', 'dark')
  await page.setViewportSize({ width: 390, height: 844 })
  expect(await page.evaluate(() => document.documentElement.scrollWidth)).toBeLessThanOrEqual(390)
  expect(
    await frame.locator('body').evaluate(() => document.documentElement.scrollWidth <= window.innerWidth),
  ).toBe(true)
  if (directory) await page.screenshot({ path: join(directory, 'm2-03-html-mobile.png'), fullPage: true })
  expect((await page.request.post('/__test/html/reload', { headers })).status()).toBe(204)
  await expect(frame.getByRole('heading', { name: 'HTML 虚构大屏 · 更新' })).toBeVisible()
  await expect(frame.locator('#value')).toHaveText('down')
  await frame.locator('body').evaluate(() => {
    const script = document.createElement('script')
    // Playwright's clock reports timer exceptions to the console instead of dispatching window errors.
    script.textContent = 'throw new Error("<b>虚构脚本错误</b>")'
    document.body.append(script)
  })
  await expect(page.getByRole('alert').filter({ hasText: '虚构脚本错误' })).toBeVisible()
  expect(await page.locator('.html-dashboard .notice b').count()).toBe(0)
  await expect(frame.locator('#value')).toHaveText('down')
  expect((await page.request.post('/__test/html/bad', { headers })).status()).toBe(204)
  await expect(
    page.getByRole('alert').filter({ hasText: '文件有错误，当前保留上一次成功的版本' }),
  ).toBeVisible()
  await expect(frame.getByRole('heading', { name: 'HTML 虚构大屏 · 更新' })).toBeVisible()
  await frame.locator('body').evaluate(() => {
    const image = new Image()
    image.src = 'https://blocked.invalid/pixel'
    document.body.append(image)
    const script = document.createElement('script')
    script.src = 'https://blocked.invalid/script.js'
    document.body.append(script)
    window.open('https://blocked.invalid/popup')
    const form = document.createElement('form')
    form.action = 'https://blocked.invalid/form'
    form.method = 'POST'
    document.body.append(form)
    form.submit()
  })
  await page.waitForTimeout(200)
  expect(outside).toEqual([])
  const standalone = await context.newPage()
  await standalone.goto('/lgmon-frame.html')
  await expect(standalone.locator('#lgmon-content')).toBeEmpty()
  expect(
    await standalone.evaluate(async () => {
      try {
        await (window as unknown as { lgmon: { query: (sql: string) => Promise<unknown> } }).lgmon.query(
          'SELECT 1',
        )
        return false
      } catch {
        return true
      }
    }),
  ).toBe(true)
  await standalone.close()
  await frame.locator('body').evaluate(() => {
    window.location.href = 'https://blocked.invalid/navigation'
  })
  await page.waitForTimeout(200)
  expect(outside).toEqual([])
})
