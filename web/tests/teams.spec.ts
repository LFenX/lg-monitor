import { expect, test } from '@playwright/test'
import { mkdir } from 'node:fs/promises'
import { join } from 'node:path'

test('joins a device, shares selected cards and one host, then updates and revokes access', async ({
  page,
  context,
}) => {
  test.setTimeout(90000)
  const ownerURL = process.env.LGMON_BROWSER_OWNER_URL
  if (!ownerURL || new URL(ownerURL).hostname !== '127.0.0.1')
    throw new Error('负责人测试地址必须是临时回环监听')
  const memberURL = process.env.LGMON_BROWSER_URL!
  await page.clock.setFixedTime(new Date(Number(process.env.LGMON_BROWSER_NOW)))
  await context.addInitScript(() => {
    Object.defineProperty(navigator, 'clipboard', {
      configurable: true,
      value: { writeText: async (text: string) => sessionStorage.setItem('fake-team-copy', text) },
    })
  })
  await page.goto('/#/boards')
  await expect(page.getByRole('heading', { name: '团队共享', exact: true })).toHaveCount(0)
  const owner = await context.newPage()
  await owner.goto(ownerURL + '/#/boards')
  await owner.getByRole('button', { name: '只读', exact: true }).click()
  await owner.getByLabel('访问令牌', { exact: true }).fill('fake-admin')
  await owner.getByRole('button', { name: '连接监控器' }).click()
  await owner.getByRole('link', { name: '团队与分享', exact: true }).click()
  await owner.getByLabel('成员设备显示名', { exact: true }).fill('虚构成员设备')
  await owner.getByRole('button', { name: '生成一次性邀请' }).click()
  const invite = await owner.getByRole('textbox', { name: '完整邀请文字', exact: true }).inputValue()
  expect(invite).toMatch(/^lgmon-team-v1:/)
  await owner.getByRole('button', { name: '复制邀请', exact: true }).click()
  await expect.poll(() => owner.evaluate(() => sessionStorage.getItem('fake-team-copy'))).toBe(invite)
  await page.goto(memberURL + '/#/teams/join')
  await page.getByRole('button', { name: '只读', exact: true }).click()
  await page.getByLabel('访问令牌', { exact: true }).fill('fake-admin')
  await page.getByRole('button', { name: '连接监控器' }).click()
  await page.getByLabel('负责人提供的完整邀请文字', { exact: true }).fill(invite)
  await page.getByRole('button', { name: '检查地址与指纹', exact: true }).click()
  await expect(page.getByRole('button', { name: '确认指纹并加入', exact: true })).toBeDisabled()
  await page.getByLabel('已通过可信渠道与负责人核对以上 16 位指纹', { exact: true }).check()
  await page.getByRole('button', { name: '确认指纹并加入', exact: true }).click()
  await expect(page.getByRole('heading', { name: '团队与分享', exact: true })).toBeVisible()
  await owner.reload()
  await expect(owner.locator('.team-entry').filter({ hasText: '虚构成员设备' }).first()).toBeVisible()
  await owner.getByRole('button', { name: '新建分享', exact: true }).click()
  await owner.getByLabel('声明式面板', { exact: true }).selectOption('overview')
  await owner.getByLabel('全部卡片', { exact: true }).uncheck()
  await owner.getByLabel('已分享主机', { exact: true }).check()
  await owner.getByLabel('已分享目标数', { exact: true }).check()
  await owner.getByLabel('全部主机', { exact: true }).uncheck()
  await owner.getByRole('group', { name: '主机', exact: true }).locator('input[type=checkbox]').nth(1).check()
  await owner.getByLabel('全部目标', { exact: true }).uncheck()
  await owner.getByRole('group', { name: '目标', exact: true }).locator('input[type=checkbox]').nth(1).check()
  await owner.getByLabel('全部成员', { exact: true }).uncheck()
  await owner.getByLabel('虚构成员设备', { exact: true }).check()
  await owner.getByRole('button', { name: '保存分享范围', exact: true }).click()
  await expect(owner.getByRole('button', { name: '修改分享', exact: true })).toBeVisible()
  await page.getByRole('button', { name: '管理员', exact: true }).click()
  await page.getByLabel('访问令牌', { exact: true }).fill('fake-read')
  await page.getByRole('button', { name: '连接监控器' }).click()
  await page.goto(memberURL + '/#/boards')
  await expect(page.getByRole('heading', { name: '团队共享', exact: true })).toBeVisible()
  await page.getByRole('link', { name: /虚构共享面板.*2 张卡片/ }).click()
  await expect(page.getByRole('heading', { name: '虚构共享面板', exact: true })).toBeVisible()
  await expect(page.locator('.dashboard-grid')).toContainText('fake-host')
  await expect(page.locator('.dashboard-grid')).not.toContainText('outside-host')
  await expect(page.locator('.dashboard-grid')).not.toContainText('未分享的卡片')
  await expect(page.getByRole('button', { name: /保存分享|修改分享|复制.*面板/ })).toHaveCount(0)
  const definition = await page.request.get('/api/v1/team-joins', {
    headers: { Authorization: 'Bearer fake-read' },
  })
  const joined = await definition.json()
  expect(JSON.stringify(joined)).not.toMatch(/certificate|keyRef|privateKey|"ca"/)
  const directory = process.env.LGMON_M205_SCREENSHOT_DIR
  if (directory) {
    await mkdir(directory, { recursive: true })
    await page.screenshot({ path: join(directory, 'm2-05-team-shared-desktop.png'), fullPage: true })
  }
  await page.getByRole('button', { name: '切换到深色' }).click()
  await page.setViewportSize({ width: 390, height: 844 })
  expect(await page.evaluate(() => document.documentElement.scrollWidth)).toBeLessThanOrEqual(390)
  if (directory) await page.screenshot({ path: join(directory, 'm2-05-team-shared-mobile.png') })
  await owner.getByRole('button', { name: '修改分享', exact: true }).click()
  await owner.getByLabel('已分享目标数', { exact: true }).uncheck()
  await owner.getByRole('button', { name: '保存分享范围', exact: true }).click()
  expect(
    (
      await page.request.post('/__test/teams/advance', {
        headers: { Authorization: 'Bearer fake-test-control' },
      })
    ).status(),
  ).toBe(204)
  await page.getByRole('button', { name: '刷新共享面板', exact: true }).click()
  await expect(page.locator('.dashboard-grid')).toContainText('fake-host')
  await expect(page.locator('.dashboard-grid')).not.toContainText('已分享目标数')
  await owner.getByRole('button', { name: '撤销分享', exact: true }).click()
  await page.getByRole('button', { name: '刷新共享面板', exact: true }).click()
  await expect(page.getByText('已清空上次结果。', { exact: false })).toBeVisible()
  await expect(page.locator('.dashboard-grid')).toHaveCount(0)
  await owner.getByRole('button', { name: '移除 虚构成员设备', exact: true }).click()
  await expect(owner.locator('.team-entry').filter({ hasText: '虚构成员设备' }).first()).toContainText(
    '已移除',
  )
  await page.goto(memberURL + '/#/teams')
  await expect(page.getByRole('alert')).toContainText('负责人暂不可连接')
  await owner.close()
})
