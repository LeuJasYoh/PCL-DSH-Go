import { test, expect } from '@playwright/test'

import { jobView, mockJobStream } from './helpers'

const JOB_ID = 'j-e2e-install-runtime'

test('运行时版本刷新与安装任务卡/日志（202 + SSE mock）', async ({ page }) => {
  await page.route('**/api/runtimes/available', (route) =>
    route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify({ versions: ['0.2.0-rc.3', '0.2.0-rc.2'] }),
    }),
  )
  await page.route('**/api/runtimes', async (route) => {
    if (route.request().method() === 'POST') {
      await route.fulfill({
        status: 202,
        contentType: 'application/json',
        body: JSON.stringify(jobView(JOB_ID, 'INSTALL_RUNTIME')),
      })
      return
    }
    await route.fallback()
  })
  await mockJobStream(page, JOB_ID, [
    { event: 'state', data: { state: 'RUNNING', phase: '下载与安装依赖', message: '正在安装…' } },
    { event: 'log', data: { level: 'info', line: '[安装] E2E 模拟安装日志' } },
    {
      event: 'result',
      data: { state: 'SUCCEEDED', result: { version: '0.2.0-rc.3', installed: true, compatibility: 'E2E' } },
    },
  ])

  await page.goto('/runtimes')
  await expect(page.getByTestId('page-runtimes')).toBeVisible()
  await page.getByTestId('runtimes-refresh').click()
  await expect(page.getByTestId('runtimes-available')).toContainText('0.2.0-rc.3')
  await expect(page.getByTestId('runtimes-available-status')).toContainText('已获取')

  const row = page.locator('.version-row', { hasText: '0.2.0-rc.3' }).first()
  await row.locator('button').click()
  await expect(page.locator('.toast').filter({ hasText: '已提交任务' })).toBeVisible()
  await expect(page.locator('.toast').filter({ hasText: '任务完成：安装运行时' })).toBeVisible({
    timeout: 15_000,
  })

  await page.getByTestId('nav-logs').click()
  await expect(page.getByTestId('page-logs')).toBeVisible()
  await expect(page.getByTestId('log-area')).toContainText('[安装] E2E 模拟安装日志')
})
