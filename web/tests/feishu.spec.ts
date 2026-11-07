import { expect, test } from '@playwright/test'
import type { Page } from '@playwright/test'
import { mkdir } from 'node:fs/promises'
import { join } from 'node:path'

test.describe.configure({ mode: 'serial' })
test.beforeEach(async ({ page }) => {
  await page.clock.setFixedTime(new Date(Number(process.env.LGMON_BROWSER_NOW)))
})
async function login(page: Page, role: string) {
  await page.getByRole('button', { name: '只读', exact: true }).click()
  await page.getByLabel('访问令牌', { exact: true }).fill('fake-' + role)
  await page.getByRole('button', { name: '连接监控器' }).click()
  await expect(page.getByRole('dialog')).toHaveCount(0)
}
async function config(page: Page) {
  return page.evaluate(async () => {
    const headers = { Authorization: 'Bearer fake-config' }
    const [notifiers, rules, templates] = await Promise.all(
      ['/config/notifiers', '/config/rules', '/templates'].map((path) =>
        fetch('/api/v1' + path, { headers }).then((response) => response.json()),
      ),
    )
    return { notifiers, rules, templates }
  })
}
async function screenshot(page: Page, name: string, fullPage = true) {
  const directory = process.env.LGMON_M114_SCREENSHOT_DIR
  if (!directory) return
  await mkdir(directory, { recursive: true })
  await page.evaluate(() => window.scrollTo(0, 0))
  await expect.poll(() => page.evaluate(() => window.scrollY)).toBe(0)
  await page.screenshot({ path: join(directory, name), fullPage })
}

test('five-step setup starts without an app and requires receipt before saving each selected group', async ({
  page,
}) => {
  await page.goto('/#/notifications/setup')
  await expect(page.getByText('需要管理员令牌', { exact: true })).toBeVisible()
  await expect(page.getByText('lgmon token create --scope admin', { exact: true })).toBeVisible()
  await expect(page.getByText('lgmon feishu setup', { exact: true })).toBeVisible()
  await login(page, 'admin')
  await page.getByLabel('App ID', { exact: true }).fill('cli_fake_browser_wizard')
  await page.getByLabel('App Secret', { exact: true }).fill('FAKE_BROWSER_SECRET')
  await page.getByRole('button', { name: '验证并保存应用' }).click()
  await expect(page.getByText('应用凭据已加密保存，接下来检查机器人、权限和群。')).toBeVisible()
  await expect(page.getByText('虚构机器人', { exact: true })).toBeVisible()
  await page.getByRole('checkbox', { name: '虚构值班群 人数未知' }).check()
  await page.getByRole('checkbox', { name: '虚构通知群 人数未知' }).check()
  const duty = page.getByRole('region', { name: '虚构值班群的通知设置' })
  const notify = page.getByRole('region', { name: '虚构通知群的通知设置' })
  await expect(duty.getByRole('checkbox', { name: '网站宕机' })).toBeChecked()
  await duty.getByLabel('告警范围').selectOption('critical')
  await notify.getByLabel('默认卡片模板').selectOption('compact')
  await notify.getByLabel('告警范围').selectOption('warning')
  await expect(duty.getByRole('button', { name: '收到了，保存' })).toHaveCount(0)
  await duty.getByRole('button', { name: '发送测试卡片' }).click()
  await expect(duty.getByText('群里收到了吗？')).toBeVisible()
  await duty.getByRole('button', { name: '没收到', exact: true }).click()
  await expect(duty.getByText('尚未保存这个群。请依次检查：')).toBeVisible()
  expect((await config(page)).notifiers.notifiers).toHaveLength(1)
  await expect(duty.getByRole('button', { name: /秒后可重发/ })).toBeDisabled()
  // Wait for the actual destination limit rather than advancing only the browser's clock.
  await expect(duty.getByRole('button', { name: '发送测试卡片', exact: true })).toBeEnabled({
    timeout: 12000,
  })
  await duty.getByRole('button', { name: '发送测试卡片', exact: true }).click()
  await duty.getByRole('button', { name: '收到了，保存' }).click()
  await expect(duty.getByText('群通知渠道已保存', { exact: true })).toBeVisible()
  await notify.getByRole('button', { name: '发送测试卡片' }).click()
  await notify.getByRole('button', { name: '收到了，保存' }).click()
  await expect(notify.getByText('群通知渠道已保存', { exact: true })).toBeVisible()
  const saved = await config(page)
  const channels = saved.notifiers.notifiers
  expect(channels).toHaveLength(3)
  expect(channels.find((channel: { chatName: string }) => channel.chatName === '虚构值班群').filter).toEqual({
    severity: ['critical'],
  })
  expect(channels.find((channel: { chatName: string }) => channel.chatName === '虚构通知群').template).toBe(
    'compact',
  )
  expect(saved.rules.rules[0].notify).toHaveLength(3)
  await screenshot(page, 'm1-14-wizard-desktop.png')
  await page.setViewportSize({ width: 390, height: 844 })
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true)
  await screenshot(page, 'm1-14-wizard-mobile.png')
  await page.goto('/#/notifications')
  await expect(page.getByRole('heading', { name: '通知', exact: true })).toBeVisible()
  await expect(page.getByText('虚构值班群', { exact: true })).toBeVisible()
  await expect(page.getByText('虚构通知群', { exact: true })).toBeVisible()
})

test('editor sends the current unsaved draft to a dedicated group and keeps configuration unchanged', async ({
  page,
}) => {
  await page.goto('/#/notifications/templates/new')
  await login(page, 'config')
  const before = await config(page)
  await page.getByRole('textbox', { name: '标题', exact: true }).fill('虚构尚未保存的试发标题')
  await page
    .getByRole('group', { name: '预览状态' })
    .getByRole('button', { name: '恢复', exact: true })
    .click()
  const send = page.getByRole('button', { name: '发送到测试群', exact: true })
  await expect(send).toBeEnabled()
  await send.click()
  const modal = page.getByRole('dialog', { name: '发送到测试群' })
  await expect(modal.getByLabel('测试群', { exact: true })).toHaveValue('oc_fake_duty')
  await modal.getByLabel('测试群', { exact: true }).selectOption('oc_fake_draft')
  await modal.getByRole('button', { name: '发送当前草稿' }).click()
  await expect(modal.getByText('测试卡片已发送，请在飞书群中确认实际收到', { exact: true })).toBeVisible()
  const after = await config(page)
  expect(after).toEqual(before)
  await screenshot(page, 'm1-14-draft-test.png', false)
  await modal.getByRole('button', { name: '关闭', exact: true }).last().click()
  await expect(page.getByRole('textbox', { name: '模板代号', exact: true })).toHaveValue('')
  await page.goto('/#/notifications/rules')
  await expect(page.getByRole('heading', { name: '规则通知设置', exact: true })).toBeVisible()
  await page.getByRole('checkbox', { name: 'desktop', exact: true }).uncheck()
  await page.getByRole('button', { name: '保存通知渠道', exact: true }).click()
  await expect(page.getByText('规则的通知渠道已更新。', { exact: true })).toBeVisible()
  const edited = await config(page)
  expect(edited.rules.rules).toEqual(
    before.rules.rules.map((rule: { notify: string[] }) => ({
      ...rule,
      notify: rule.notify.filter((id) => id !== 'desktop'),
    })),
  )
})
