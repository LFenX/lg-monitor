import { expect } from '@playwright/test'
import type { Locator, Page } from '@playwright/test'

/*
 * Switches the page to another token through the same dialog a blocked action opens. Pass the control that
 * opened it when the test is about that path; otherwise the dialog is requested directly.
 */
export async function useToken(page: Page, token: string, via?: Locator) {
  if (via) await via.click()
  else
    await page.evaluate(() =>
      window.dispatchEvent(new CustomEvent('lgmon-access', { detail: { need: 'config', what: '' } })),
    )
  const dialog = page.getByRole('dialog', { name: /^需要(修改|管理员)权限$/ })
  await dialog.getByLabel('访问令牌', { exact: true }).fill(token)
  await dialog.getByRole('button', { name: '使用这个令牌' }).click()
  await expect(dialog).toHaveCount(0)
}
