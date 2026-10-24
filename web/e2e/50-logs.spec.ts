import { test, expect } from '@playwright/test'

import { deleteInstance, jobView, mockJobStream, seedInstance } from './helpers'

const JOB_ID = 'j-e2e-log-viewer'

test('日志页 tail、SSE 实时日志、复制回退与取消任务', async ({ page, request }) => {
  const instance = await seedInstance(request, 'E2E 日志实例')
  try {
    // Force the manual-copy fallback even though the page runs on localhost.
    await page.addInitScript(() => {
      Object.defineProperty(navigator, 'clipboard', {
        configurable: true,
        value: { writeText: () => Promise.reject(new Error('clipboard blocked in E2E')) },
      })
    })
    await page.route('**/api/logs/tail?*', (route) =>
      route.fulfill({
        status: 200,
        contentType: 'application/json',
        body: JSON.stringify({ lines: ['12:00:01 E2E tail 行'] }),
      }),
    )
    await page.route('**/api/instances/*/launch', (route) =>
      route.fulfill({
        status: 202,
        contentType: 'application/json',
        body: JSON.stringify(jobView(JOB_ID, 'LAUNCH_INSTANCE')),
      }),
    )
    await mockJobStream(page, JOB_ID, [
      { event: 'state', data: { state: 'RUNNING', phase: '启动中', message: '正在启动…' } },
      { event: 'log', data: { level: 'info', line: '[启动] E2E 模拟启动日志' } },
    ])
    await page.route('**/api/jobs/' + JOB_ID + '/cancel', (route) =>
      route.fulfill({
        status: 202,
        contentType: 'application/json',
        body: JSON.stringify(jobView(JOB_ID, 'LAUNCH_INSTANCE', { phase: '取消中' })),
      }),
    )

    await page.goto('/instances')
    const card = page.locator('.instance-card').filter({ hasText: 'E2E 日志实例' }).first()
    await expect(card).toBeVisible()
    await card.locator('[data-testid^="instance-launch-"]').click()
    await expect(page.getByTestId('task-strip')).toBeVisible()

    await page.getByTestId('nav-logs').click()
    await expect(page.getByTestId('page-logs')).toBeVisible()
    await expect(page.getByTestId('log-tail')).toContainText('E2E tail 行')
    await expect(page.getByTestId('log-area')).toContainText('[启动] E2E 模拟启动日志')

    // Clipboard unavailable -> read-only textarea fallback.
    await page.getByTestId('copy-logs').click()
    await expect(page.getByTestId('copy-fallback')).toBeVisible()
    await expect(page.getByTestId('copy-fallback-text')).toHaveValue(/E2E 模拟启动日志/)
    await page.locator('[data-testid="copy-fallback"] .modal-footer .btn-primary').click()

    // Cancel the (mock) running task.
    await expect(page.getByTestId('cancel-task')).toBeEnabled()
    await page.getByTestId('cancel-task').click()
    await expect(page.locator('.toast').filter({ hasText: '已请求取消' })).toBeVisible()
  } finally {
    await deleteInstance(request, instance.id)
  }
})
