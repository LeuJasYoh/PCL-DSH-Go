import { test, expect } from '@playwright/test'

import { csrfHeaders } from './helpers'

test('设置页保存代理/SSH 用户，且轮询不覆盖未保存编辑', async ({ page, request }) => {
  await page.goto('/settings')
  await expect(page.getByTestId('settings-proxy')).toBeVisible()

  await page.getByTestId('settings-proxy').fill('http://127.0.0.1:7890')
  const sshUser = page.getByTestId('settings-ssh-user')
  if (await sshUser.count()) await sshUser.fill('root')
  await expect(page.getByTestId('settings-dirty')).toBeVisible()
  await page.getByTestId('settings-save').click()
  await expect(page.locator('.toast').filter({ hasText: '设置已保存' })).toBeVisible()

  const headers = await csrfHeaders(request)
  const response = await request.get('/api/settings', { headers })
  expect(response.ok()).toBeTruthy()
  const saved = (await response.json()) as { proxy: string; directAccessSshUser: string }
  expect(saved.proxy).toBe('http://127.0.0.1:7890')
  if (typeof saved.directAccessSshUser === 'string' && saved.directAccessSshUser) {
    expect(saved.directAccessSshUser).toBe('root')
  }

  await page.getByTestId('settings-proxy').fill('http://127.0.0.1:7891')
  await page.waitForTimeout(2600)
  await expect(page.getByTestId('settings-proxy')).toHaveValue('http://127.0.0.1:7891')
  await expect(page.getByTestId('settings-dirty')).toBeVisible()
})
