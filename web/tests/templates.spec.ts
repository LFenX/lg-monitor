import { expect, test } from '@playwright/test'
import { mkdir } from 'node:fs/promises'
import { join } from 'node:path'

test.beforeEach(async ({ page }) => {
  await page.clock.setFixedTime(new Date(Number(process.env.LGMON_BROWSER_NOW)))
})
async function login(page: import('@playwright/test').Page) {
  await page.goto('/#/notifications')
  await page.getByRole('button', { name: '只读', exact: true }).click()
  await page.getByRole('textbox', { name: '访问令牌', exact: true }).fill('fake-config')
  await page.getByRole('button', { name: '连接监控器' }).click()
  await expect(page.getByRole('button', { name: '可配置', exact: true })).toBeVisible()
}

test('template library shows both states, and a basic edit preserves an explicit empty field list', async ({
  page,
}) => {
  await login(page)
  const standard = page
    .locator('.template-card')
    .filter({ has: page.getByRole('heading', { name: '标准', exact: true }) })
  await expect(standard.getByRole('group', { name: '标准 · 触发预览' })).toBeVisible()
  await expect(standard.getByRole('group', { name: '标准 · 恢复预览' })).toBeVisible()
  let requests = 0
  page.on('request', (request) => {
    if (request.method() === 'POST' && request.url().endsWith('/preview')) requests++
  })
  await page.getByRole('button', { name: '刷新模板', exact: true }).click()
  await expect.poll(() => requests).toBeGreaterThan(0)
  await standard.getByRole('link', { name: '基于此模板自定义' }).click()
  await page.getByRole('textbox', { name: '模板代号', exact: true }).fill('fake-browser-template')
  await page.getByRole('textbox', { name: '模板名称', exact: true }).fill('虚构编辑模板')
  await page.getByRole('textbox', { name: '标题', exact: true }).fill('虚构网页自定义标题')
  const preview = page.getByRole('group', { name: '当前模板预览' })
  await expect(preview.getByText('虚构网页自定义标题', { exact: true })).toBeVisible()
  await page
    .getByRole('group', { name: '标题颜色' })
    .getByRole('combobox', { name: '严重', exact: true })
    .selectOption('green')
  await expect(preview.locator('.card-header')).toHaveClass(/card-color-green/)
  await page.getByRole('button', { name: '上移目标', exact: true }).click()
  await expect(preview.locator('.card-column strong').first()).toHaveText('目标')
  for (const field of ['主机', '目标', '开始时间', '持续时长', '当前值'])
    await page.getByRole('checkbox', { name: field, exact: true }).uncheck()
  await expect(preview.locator('.card-columns')).toHaveCount(0)
  await page.getByRole('button', { name: '添加附加字段', exact: true }).click()
  await page.getByRole('textbox', { name: '字段名称', exact: true }).fill('环境')
  await page.getByRole('textbox', { name: '字段内容', exact: true }).fill('虚构测试环境')
  await page
    .getByRole('group', { name: '提醒谁' })
    .getByRole('combobox', { name: '严重时', exact: true })
    .selectOption('all')
  await page.getByRole('button', { name: '添加按钮', exact: true }).click()
  await page.getByRole('textbox', { name: '按钮文字', exact: true }).fill('虚构操作手册')
  await page.getByRole('textbox', { name: '跳转地址', exact: true }).fill('https://example.com/fake-runbook')
  await page.getByRole('textbox', { name: '底部备注', exact: true }).fill('虚构底部备注')
  const extra = preview.locator('.card-column').filter({ hasText: '环境' })
  await expect(extra).toBeVisible()
  await expect(extra).toContainText('虚构测试环境')
  await expect(preview.getByText('@所有人', { exact: true })).toBeVisible()
  await expect(preview.getByText('虚构底部备注', { exact: true })).toBeVisible()
  await expect(preview.getByRole('button', { name: '虚构操作手册' })).toBeDisabled()
  await expect(page.getByRole('button', { name: '校验并保存模板' })).toBeEnabled()
  await page.getByRole('button', { name: '校验并保存模板' }).click()
  await expect(page.getByText('模板已校验并保存。', { exact: true })).toBeVisible()
  const response = await page.request.get('/api/v1/templates/fake-browser-template', {
    headers: { Authorization: 'Bearer fake-read' },
  })
  expect(response.ok()).toBe(true)
  const definition = await response.json()
  expect(definition.fields).toEqual([])
  expect(definition.title).toBe('虚构网页自定义标题')
  expect(definition.extraFields).toEqual([{ label: '环境', value: '虚构测试环境' }])
  expect(definition.mention).toEqual({ critical: 'all' })
  expect(definition.buttons).toEqual([{ text: '虚构操作手册', url: 'https://example.com/fake-runbook' }])
  expect(definition.note).toBe('虚构底部备注')
  const directory = process.env.LGMON_M114_SCREENSHOT_DIR
  if (directory) {
    await mkdir(directory, { recursive: true })
    await page.evaluate(() => window.scrollTo(0, 0))
    await expect.poll(() => page.evaluate(() => window.scrollY)).toBe(0)
    await page.screenshot({ path: join(directory, 'm1-14-template-desktop.png'), fullPage: true })
  }
  await page.setViewportSize({ width: 390, height: 844 })
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(true)
  await page.evaluate(() => window.scrollTo(0, 0))
  if (directory) await page.screenshot({ path: join(directory, 'm1-14-template-mobile.png') })
  await preview.scrollIntoViewIfNeeded()
  if (directory) await page.screenshot({ path: join(directory, 'm1-14-template-mobile-preview.png') })
})

test('advanced JSON errors are visible at the field and read-only users cannot save', async ({ page }) => {
  await page.goto('/#/notifications/templates/new')
  await expect(page.getByRole('button', { name: '校验并保存模板' })).toBeDisabled()
  await page.getByRole('button', { name: '高级模式', exact: true }).click()
  const input = page.getByRole('textbox', { name: '卡片 JSON', exact: true })
  await input.fill('{')
  await expect(page.getByText(/JSON 语法错误：/)).toBeVisible()
  await expect(input).toHaveAttribute('aria-invalid', 'true')
  await input.fill(
    JSON.stringify({ schema: '2.0', body: { elements: [{ tag: 'markdown', content: '{{.Missing}}' }] } }),
  )
  await expect(page.getByRole('alert')).toContainText('Missing')
  await expect(input).toHaveAttribute('aria-invalid', 'true')
  await expect(page.getByRole('button', { name: '校验并保存模板' })).toBeDisabled()
})

test('rule template selection changes only that field in the current rules document', async ({ page }) => {
  const headers = { Authorization: 'Bearer fake-config' }
  const before = await (await page.request.get('/api/v1/config/rules', { headers })).json()
  await login(page)
  await page.getByRole('combobox', { name: 'down的卡片模板', exact: true }).selectOption('compact')
  await expect(page.getByText('已更新该规则的卡片模板。', { exact: true })).toBeVisible()
  const after = await (await page.request.get('/api/v1/config/rules', { headers })).json()
  expect(after.rules).toEqual(
    before.rules.map((rule: Record<string, unknown>) =>
      rule.id === 'down' ? { ...rule, template: 'compact' } : rule,
    ),
  )
})

test('missing permissions show the official action, and a new group is discovered automatically', async ({
  page,
}) => {
  await login(page)
  await page.goto('/#/notifications/setup')
  const action = page.getByRole('link', { name: /一键开通权限/ })
  await expect(action).toBeVisible()
  await expect(action).toHaveAttribute('target', '_blank')
  await expect(action).toHaveAttribute('href', /^https:\/\/open\.feishu\.cn\//)
  await expect(page.getByText('虚构机器人', { exact: true })).toBeVisible()
  await expect(page.getByText('尚未发现机器人所在的群', { exact: true })).toBeVisible()
  const group = page.locator('.discovered-chats li').filter({ hasText: '虚构测试群' })
  await expect(group).toBeVisible({ timeout: 10000 })
  await expect(group.getByText('新发现', { exact: true })).toBeVisible()
  await expect(page.locator('.permission-list')).toContainText('已开通')
  await expect(page.locator('.inspection-details')).not.toHaveAttribute('open')
  await expect(page.locator('.inspection-details pre')).toBeHidden()
  const directory = process.env.LGMON_M114_SCREENSHOT_DIR
  if (directory) await page.screenshot({ path: join(directory, 'm1-14-inspection.png'), fullPage: true })
})

test('advanced templates preserve both states, insert variables at the caret and reopen without conversion', async ({
  page,
}) => {
  await login(page)
  await page.goto('/#/notifications/templates/new')
  await page.getByRole('textbox', { name: '模板代号', exact: true }).fill('fake-browser-raw')
  await page.getByRole('textbox', { name: '模板名称', exact: true }).fill('虚构高级模板')
  await page.getByRole('button', { name: '高级模式', exact: true }).click()
  const definition = {
    firing: {
      schema: '2.0',
      header: { template: 'carmine', title: { tag: 'plain_text', content: '虚构高级触发' } },
      body: { elements: [{ tag: 'markdown', content: '指定值 ' }] },
    },
    resolved: {
      schema: '2.0',
      header: { template: 'green', title: { tag: 'plain_text', content: '虚构高级恢复' } },
      body: { elements: [{ tag: 'markdown', content: '虚构恢复文案' }] },
    },
  }
  const source = JSON.stringify(definition, null, 2)
  const input = page.getByRole('textbox', { name: '卡片 JSON', exact: true })
  await input.fill(source)
  const preview = page.getByRole('group', { name: '当前模板预览' })
  await expect(preview.getByText('虚构高级触发', { exact: true })).toBeVisible()
  const state = page.getByRole('group', { name: '预览状态' })
  await state.getByRole('button', { name: '恢复', exact: true }).click()
  await expect(preview.getByText('虚构高级恢复', { exact: true })).toBeVisible()
  await expect(preview.getByText('虚构恢复文案', { exact: true })).toBeVisible()
  await state.getByRole('button', { name: '触发', exact: true }).click()
  const position = source.indexOf('指定值 ') + '指定值 '.length
  await input.evaluate((element, caret) => {
    const textarea = element as HTMLTextAreaElement
    textarea.focus()
    textarea.setSelectionRange(caret, caret)
  }, position)
  await page.getByText('模板变量与用法', { exact: true }).click()
  await page.getByRole('button', { name: '{{.Host.Name}}', exact: true }).click()
  definition.firing.body.elements[0].content = '指定值 {{.Host.Name}}'
  await expect(input).toHaveValue(JSON.stringify(definition, null, 2))
  await expect(preview).toContainText('指定值 演示主机')
  await expect(page.getByRole('button', { name: '校验并保存模板' })).toBeEnabled()
  await page.getByRole('button', { name: '校验并保存模板' }).click()
  await expect(page.getByText('模板已校验并保存。', { exact: true })).toBeVisible()
  const response = await page.request.get('/api/v1/templates/fake-browser-raw', {
    headers: { Authorization: 'Bearer fake-read' },
  })
  expect(response.ok()).toBe(true)
  const saved = await response.json()
  expect(saved.raw).toEqual(definition)
  expect(saved.extends).toBeUndefined()
  await page.goto('/#/notifications/templates/fake-browser-raw')
  await expect(input).toBeVisible()
  await expect.poll(async () => JSON.parse(await input.inputValue())).toEqual(definition)
  const reopened = await input.inputValue()
  await page.getByRole('button', { name: '基础模式', exact: true }).click()
  await page.getByRole('button', { name: '高级模式', exact: true }).click()
  await expect(input).toHaveValue(reopened)
  await page.evaluate(() => window.scrollTo(0, 0))
  await page.getByRole('button', { name: '切换到深色', exact: true }).click()
  await expect(page.locator('html')).toHaveAttribute('data-theme', 'dark')
  await page.setViewportSize({ width: 390, height: 844 })
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(true)
  const directory = process.env.LGMON_M114_SCREENSHOT_DIR
  if (directory) await page.screenshot({ path: join(directory, 'm1-14-raw-mobile-dark.png') })
})
