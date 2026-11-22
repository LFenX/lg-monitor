import { expect, test } from '@playwright/test'
import { useToken } from './access'

test('local owner controls full access for a LAN visitor', async ({ page, browser }) => {
  const admin = await browser.newPage()
  try {
    await admin.goto(process.env.LGMON_BROWSER_OWNER_URL + '/#/lan')
    const settings = admin.getByRole('region', { name: '完全访问密钥' })
    await useToken(admin, 'fake-admin', settings.getByRole('button', { name: '生成密钥' }))
    await settings.getByRole('button', { name: '生成密钥' }).click()
    await admin.getByRole('dialog', { name: '生成密钥' }).getByRole('button', { name: '生成密钥' }).click()
    const reveal = admin.getByRole('dialog', { name: '完全访问密钥' })
    const key = await reveal.locator('code').innerText()
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
    await settings.getByRole('button', { name: '撤销密钥' }).click()
    await admin.getByRole('dialog', { name: '撤销密钥' }).getByRole('button', { name: '撤销密钥' }).click()
    await expect(settings).toContainText('未设置')
    await page.reload()
    await expect(page.getByRole('button', { name: '解锁完全访问' })).toBeVisible()
  } finally {
    await admin.close()
  }
})
