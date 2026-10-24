import { test, expect } from '@playwright/test'

import { deleteInstance, seedInstance } from './helpers'

test.beforeEach(async ({ page }) => {
  // Keep version discovery deterministic and offline.
  await page.route('**/api/runtimes/available', (route) =>
    route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify({ versions: ['0.2.0-rc.2', '0.2.0-rc.1'] }),
    }),
  )
})

test('创建实例校验、创建成功、选择当前实例与删除确认', async ({ page, request }) => {
  const seeded = await seedInstance(request, 'E2E 第二实例')
  try {
    await page.goto('/instances')
    await expect(page.getByTestId('page-instances')).toBeVisible()

    await page.getByTestId('instance-create').click()
    await expect(page.getByTestId('instance-dialog')).toBeVisible()

    // 名称必填
    await page.getByTestId('instance-save').click()
    await expect(page.getByTestId('instance-validation')).toContainText('请输入实例名称。')
    await page.getByTestId('instance-name').fill('E2E 新实例')

    // 版本必填
    await page.getByTestId('instance-version').fill('')
    await page.getByTestId('instance-save').click()
    await expect(page.getByTestId('instance-validation')).toContainText('请输入 Harness 版本。')
    await page.getByTestId('instance-version').fill('0.2.0-rc.2')

    // 端口范围
    await page.getByTestId('instance-port').fill('80')
    await page.getByTestId('instance-save').click()
    await expect(page.getByTestId('instance-validation')).toContainText('端口必须在 1024–65535 之间。')
    await page.getByTestId('instance-port').fill('')

    // 创建成功并出现在列表（后端会把新实例设为当前实例）
    await page.getByTestId('instance-save').click()
    await expect(page.getByTestId('instance-dialog')).toHaveCount(0)
    const card = page.locator('.instance-card').filter({ hasText: 'E2E 新实例' }).first()
    await expect(card).toBeVisible()
    await expect(card.locator('.badge', { hasText: '当前' })).toBeVisible()

    // 选择当前实例（切换到之前通过 API 创建的实例）
    const seededCard = page.locator('.instance-card').filter({ hasText: 'E2E 第二实例' }).first()
    await expect(seededCard).toBeVisible()
    await expect(seededCard.locator('[data-testid^="instance-select-"]')).toBeVisible({ timeout: 10_000 })
    await seededCard.locator('[data-testid^="instance-select-"]').click()
    await expect(seededCard.locator('.badge', { hasText: '当前' })).toBeVisible()
    await expect(card.locator('.badge', { hasText: '当前' })).toHaveCount(0)

    // 删除确认（移入回收目录）
    await card.locator('[data-testid^="instance-delete-"]').click()
    await expect(page.getByTestId('confirm-dialog')).toBeVisible()
    await expect(page.getByTestId('confirm-dialog')).toContainText('移入回收目录')
    await page.getByTestId('confirm-ok').click()
    await expect(page.locator('.instance-card').filter({ hasText: 'E2E 新实例' })).toHaveCount(0)
    await expect(page.locator('.instance-card').filter({ hasText: 'E2E 第二实例' })).toBeVisible()
  } finally {
    await deleteInstance(request, seeded.id)
  }
})
