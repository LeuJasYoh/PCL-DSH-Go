import { test, expect } from '@playwright/test'

import { deleteInstance, jobView, mockJobStream, seedInstance } from './helpers'

test('实例复制与修改版本走 Job，完成后刷新并选中新实例', async ({ page, request }) => {
  const instance = await seedInstance(request, 'E2E 复制源')
  const copyJob = 'j-e2e-copy-instance'
  const versionJob = 'j-e2e-copy-version'
  try {
    await page.route('**/api/instances/*/copy', (route) =>
      route.fulfill({
        status: 202,
        contentType: 'application/json',
        body: JSON.stringify(jobView(copyJob, 'COPY_INSTANCE')),
      }),
    )
    await mockJobStream(page, copyJob, [
      { event: 'state', data: { state: 'RUNNING', phase: '复制实例', message: '正在复制…' } },
      { event: 'log', data: { level: 'info', line: '[复制] E2E 复制日志' } },
      { event: 'result', data: { state: 'SUCCEEDED', result: { instanceId: instance.id, name: 'E2E 副本' } } },
    ])
    // PUT /api/instances/{id} with a changed version -> 202 COPY_INSTANCE Job.
    await page.route(/\/api\/instances\/[^/]+$/, async (route) => {
      if (route.request().method() !== 'PUT') {
        await route.fallback()
        return
      }
      await route.fulfill({
        status: 202,
        contentType: 'application/json',
        body: JSON.stringify(jobView(versionJob, 'COPY_INSTANCE')),
      })
    })
    await mockJobStream(page, versionJob, [
      { event: 'state', data: { state: 'RUNNING', phase: '复制到新版本', message: '正在复制…' } },
      { event: 'result', data: { state: 'SUCCEEDED', result: { instanceId: instance.id, name: 'E2E 复制源' } } },
    ])

    await page.goto('/instances')
    const card = page.locator('.instance-card').filter({ hasText: 'E2E 复制源' }).first()
    await expect(card).toBeVisible()

    // Copy via the dialog.
    await card.locator('[data-testid^="instance-copy-"]').click()
    await expect(page.getByTestId('copy-dialog')).toBeVisible()
    await page.getByTestId('copy-name').fill('E2E 副本')
    await page.getByTestId('copy-confirm').click()
    await expect(page.locator('.toast').filter({ hasText: '复制完成' })).toBeVisible({ timeout: 15_000 })

    // Change the Harness version through the edit dialog (mocked Job path).
    await card.locator('[data-testid^="instance-edit-"]').click()
    await expect(page.getByTestId('instance-dialog')).toBeVisible()
    await expect(page.getByTestId('instance-version')).not.toHaveAttribute('readonly', '')
    await page.getByTestId('instance-version').fill('0.2.0-rc.3')
    await page.getByTestId('instance-save').click()
    await expect(page.getByTestId('instance-dialog')).toHaveCount(0)
    await expect(page.locator('.toast').filter({ hasText: '任务完成：复制实例' })).toBeVisible({
      timeout: 15_000,
    })
    await expect(page.locator('.toast').filter({ hasText: '复制完成' })).toBeVisible({ timeout: 15_000 })
  } finally {
    await deleteInstance(request, instance.id)
  }
})

test('旧后端返回 409 CONFLICT_UNSUPPORTED_CHANGE 时版本/profile 回退只读', async ({ page, request }) => {
  const instance = await seedInstance(request, 'E2E 旧后端实例')
  try {
    await page.route(new RegExp('/api/instances/' + instance.id + '$'), async (route) => {
      if (route.request().method() !== 'PUT') {
        await route.fallback()
        return
      }
      await route.fulfill({
        status: 409,
        contentType: 'application/json',
        body: JSON.stringify({
          code: 'CONFLICT_UNSUPPORTED_CHANGE',
          message: '本增量暂不支持修改实例的版本或 profile，下个增量提供；请先删除并重新创建实例。',
          details: {},
          traceId: 'e2e',
        }),
      })
    })

    await page.goto('/instances')
    const card = page.locator('.instance-card').filter({ hasText: 'E2E 旧后端实例' }).first()
    await expect(card).toBeVisible()
    await card.locator('[data-testid^="instance-edit-"]').click()
    await expect(page.getByTestId('instance-dialog')).toBeVisible()
    await page.getByTestId('instance-version').fill('0.2.0-rc.4')
    await page.getByTestId('instance-save').click()

    await expect(page.getByTestId('instance-validation')).toContainText('暂不支持修改')
    await expect(page.getByTestId('instance-version')).toBeDisabled()
    await expect(page.getByTestId('instance-version-readonly')).toBeVisible()
    await expect(page.getByTestId('instance-dialog')).toBeVisible()

    await page.locator('[data-testid="instance-dialog"] .modal-footer .btn-secondary').click()
    await expect(page.getByTestId('instance-dialog')).toHaveCount(0)
  } finally {
    await deleteInstance(request, instance.id)
  }
})
