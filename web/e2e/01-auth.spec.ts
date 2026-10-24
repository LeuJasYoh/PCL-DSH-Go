import { test, expect } from '@playwright/test'

import { E2E_NEW_PASSWORD } from './helpers'

// Runs in the "auth" project (no storage state). global-setup.ts already changed
// the initial password through the real API and saved .e2e-data/auth.json.
test.describe.configure({ mode: 'serial' })

test('未登录访问首页会跳转到登录页且不轮询 /api/state', async ({ page }) => {
  const stateRequests: string[] = []
  page.on('request', (request) => {
    try {
      if (new URL(request.url()).pathname === '/api/state') stateRequests.push(request.url())
    } catch {
      // Ignore non-URL requests.
    }
  })

  await page.goto('/')
  await expect(page).toHaveURL(/\/login/)
  await expect(page.getByTestId('page-login')).toBeVisible()
  await expect(page.locator('[data-testid="page-header"]')).toHaveCount(0)

  await page.waitForTimeout(2200)
  expect(stateRequests, 'login page must not poll /api/state').toEqual([])
})

test('错误密码显示中文提示', async ({ page }) => {
  await page.goto('/login')
  await page.getByTestId('login-password').fill('definitely-not-the-password')
  await page.getByTestId('login-submit').click()
  await expect(page.getByTestId('login-error')).toContainText('密码错误')
})

test('修改后的密码可登录并进入首页', async ({ page }) => {
  await page.goto('/login')
  await page.getByTestId('login-password').fill(E2E_NEW_PASSWORD)
  await page.getByTestId('login-submit').click()
  await expect(page.getByTestId('page-home')).toBeVisible()
  await expect(page.getByTestId('page-header')).toBeVisible()
})
