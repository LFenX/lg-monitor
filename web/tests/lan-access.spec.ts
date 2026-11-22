import { expect, test } from '@playwright/test'
import { useToken } from './access'

test('local owner controls full access for a LAN visitor', async ({ page, browser }) => {
  test.setTimeout(90_000)
  const admin = await browser.newPage()
  try {
    await admin.goto(process.env.LGMON_BROWSER_OWNER_URL + '/#/lan')
    const settings = admin.getByRole('region', { name: '完全访问密钥' })
    await useToken(admin, 'fake-admin', settings.getByRole('button', { name: '生成密钥' }))
    const share = admin.locator('details.lm-service-scope').filter({ hasText: '发布面板' })
    await share.locator('summary').click()
    const database = share.getByRole('checkbox', { name: '示例数据库' })
    await expect(database).toBeChecked()
    await database.uncheck()
    await expect(database).not.toBeChecked()
    await expect(share.getByRole('checkbox', { name: '示例站点' })).toBeChecked()
    await settings.getByRole('button', { name: '生成密钥' }).click()
    const generate = admin.getByRole('dialog', { name: '生成密钥' })
    await generate.getByRole('combobox').selectOption('custom')
    for (const width of [390, 1280]) {
      await admin.setViewportSize({ width, height: 800 })
      const dialogBox = await generate.boundingBox()
      const dateBox = await generate.locator('input[type=datetime-local]').boundingBox()
      const submitBox = await generate.getByRole('button', { name: '生成密钥' }).boundingBox()
      expect(
        dialogBox &&
          dateBox &&
          submitBox &&
          dialogBox.x >= 0 &&
          dialogBox.x + dialogBox.width <= width + 1 &&
          dateBox.x >= dialogBox.x &&
          dateBox.x + dateBox.width <= dialogBox.x + dialogBox.width + 1 &&
          dateBox.y + dateBox.height <= submitBox.y + 1,
      ).toBeTruthy()
    }
    await generate.getByRole('combobox').selectOption('86400')
    await generate.getByRole('button', { name: '生成密钥' }).click()
    const reveal = admin.getByRole('dialog', { name: '完全访问密钥' })
    const key = await reveal.locator('code').innerText()
    await reveal.getByRole('button', { name: '关闭' }).click()
    await admin.reload()
    await settings.getByRole('button', { name: '查看并复制密钥' }).click()
    await expect(reveal.locator('code')).toHaveText(key)
    await reveal.getByRole('button', { name: '复制', exact: true }).click()
    await expect(reveal.getByRole('button', { name: '已复制', exact: true })).toBeVisible()
    await reveal.getByRole('button', { name: '关闭' }).click()
    await settings.getByRole('button', { name: '修改有效期' }).click()
    const expiry = admin.getByRole('dialog', { name: '修改有效期' })
    await expiry.getByRole('combobox').selectOption('0')
    await expiry.getByRole('button', { name: '保存' }).click()
    await expect(settings).toContainText('长期有效')
    await page.setViewportSize({ width: 390, height: 844 })
    await page.goto('/')
    await page.getByRole('button', { name: '解锁完全访问' }).click()
    const unlock = page.getByRole('dialog', { name: '解锁完全访问' })
    await unlock.getByLabel('完全访问密钥').fill(key)
    await unlock.getByRole('button', { name: '解锁完全访问' }).click()
    await expect(page.getByRole('button', { name: '返回只读模式' })).toBeVisible()
    await page.reload()
    await expect(page.getByRole('button', { name: '返回只读模式' })).toBeVisible()
    const errors: string[] = []
    page.on('pageerror', (err) => errors.push(err.message))
    await page.getByRole('button', { name: '返回只读模式' }).click()
    for (let visit = 0; visit < 2; visit++) {
      await expect(page.getByRole('heading', { name: '发布面板' })).toBeVisible()
      await expect(page.getByRole('button', { name: '解锁完全访问' })).toBeVisible()
      if (visit === 0) await page.reload()
    }
    await page.getByRole('button', { name: '关系', exact: true }).click()
    const graph = page.getByRole('figure', { name: '发布面板 的依赖关系' })
    await expect(graph.getByRole('link', { name: /示例站点/ })).toBeVisible()
    const locked = graph.locator('[aria-disabled="true"]').filter({ hasText: '示例数据库' })
    await expect(locked).toBeVisible()
    await expect(locked).not.toHaveAttribute('href', /./)
    await page.goto('/#/monitors/db')
    await expect(page.locator('.lm-note').filter({ hasText: '此内容未向内网访客开放' })).toBeVisible()
    await share.locator('summary').click()
    await Promise.all([
      admin.waitForResponse(
        (res) => res.url().endsWith('/api/v1/lan') && res.request().method() === 'PUT' && res.ok(),
      ),
      share.getByRole('checkbox', { name: '示例站点' }).uncheck(),
    ])
    await page.goto('/')
    await expect(graph.locator('[aria-disabled="true"]')).toHaveCount(2)
    await expect(graph.getByRole('link')).toHaveCount(0)
    expect(errors).toEqual([])
    await page.getByRole('button', { name: '解锁完全访问' }).click()
    await unlock.getByLabel('完全访问密钥').fill(key)
    await unlock.getByRole('button', { name: '解锁完全访问' }).click()
    await expect(page.getByRole('button', { name: '返回只读模式' })).toBeVisible()
    await settings.getByRole('button', { name: '撤销密钥' }).click()
    await admin.getByRole('dialog', { name: '撤销密钥' }).getByRole('button', { name: '撤销密钥' }).click()
    await expect(settings).toContainText('未设置')
    await page.reload()
    await expect(page.getByRole('button', { name: '解锁完全访问' })).toBeVisible()
  } finally {
    await admin.close()
  }
})
