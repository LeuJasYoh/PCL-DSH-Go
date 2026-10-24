import { test, expect } from '@playwright/test'

test('关于页可查看 GPL 全文与 HMCL 附加条款', async ({ page }) => {
  await page.goto('/settings')
  await expect(page.getByTestId('about-card')).toBeVisible()
  await expect(page.getByTestId('about-card')).toContainText('版权所有 © 2013-2026 huangyuhui')

  await page.getByTestId('license-gpl').click()
  await expect(page.getByTestId('license-dialog')).toBeVisible()
  await expect(page.getByTestId('license-text')).toContainText('GNU GENERAL PUBLIC LICENSE')

  await page.getByTestId('license-tab-hmcl').click()
  await expect(page.getByTestId('license-text')).toContainText('HMCL 附加条款')

  await page.locator('[data-testid="license-dialog"] .modal-footer .btn-primary').click()
  await expect(page.getByTestId('license-dialog')).toHaveCount(0)
})
