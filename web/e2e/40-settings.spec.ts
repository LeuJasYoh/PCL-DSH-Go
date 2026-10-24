import { test, expect } from '@playwright/test'

import { csrfHeaders } from './helpers'

test('设置保存代理/背景，且 1.5s 轮询不覆盖未保存编辑', async ({ page, request }) => {
  await page.goto('/settings')
  await expect(page.getByTestId('settings-proxy')).toBeVisible()

  await page.getByTestId('settings-proxy').fill('http://127.0.0.1:7890')
  await page.getByTestId('background-whale-03').click()
  await expect(page.getByTestId('settings-dirty')).toBeVisible()

  await page.getByTestId('settings-save').click()
  await expect(page.locator('.toast').filter({ hasText: '设置已保存' })).toBeVisible()

  // The backend really stored the values.
  const headers = await csrfHeaders(request)
  const response = await request.get('/api/settings', { headers })
  expect(response.ok()).toBeTruthy()
  const saved = (await response.json()) as { proxy: string; background: string }
  expect(saved.proxy).toBe('http://127.0.0.1:7890')
  expect(saved.background).toBe('builtin:whale-03')

  // Unsaved edits must survive the 1.5s /api/state polling (desktop acceptance).
  await page.getByTestId('settings-proxy').fill('http://127.0.0.1:7891')
  await page.waitForTimeout(2600)
  await expect(page.getByTestId('settings-proxy')).toHaveValue('http://127.0.0.1:7891')
  await expect(page.getByTestId('settings-dirty')).toBeVisible()
})
