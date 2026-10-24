import { test, expect } from '@playwright/test'

// M5.1 A: instance creation version picker (dropdown) + manual custom version.
test('版本下拉：展开/选择更新字段/关闭列表，手输自定义版本仍可提交', async ({ page, request }) => {
  const versions = ['0.2.1-rc.3', '0.2.1-rc.2', '0.2.0-rc.2']
  await page.route('**/api/runtimes/available', (route) =>
    route.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify({ versions }) }),
  )
  await page.route('**/api/instances', (route) => {
    if (route.request().method() === 'GET') {
      return route.fulfill({ status: 200, contentType: 'application/json', body: '[]' })
    }
    return route.fallback()
  })
  let submitted: Record<string, unknown> | null = null
  await page.route('**/api/instances', (route) => {
    if (route.request().method() !== 'POST') return route.fallback()
    submitted = route.request().postDataJSON() as Record<string, unknown>
    return route.fulfill({
      status: 201,
      contentType: 'application/json',
      body: JSON.stringify({ id: 'i-m51-picker', name: 'M5.1 下拉实例', version: String(submitted?.version ?? ''), profile: 'web', port: 3080, status: '未启动', path: '/tmp/m51', profiles: ['web'] }),
    })
  })

  await page.goto('/instances')
  await expect(page.getByTestId('page-instances')).toBeVisible()
  await page.getByTestId('instance-create').click()
  await expect(page.getByTestId('instance-dialog')).toBeVisible()
  await page.getByTestId('instance-name').fill('M5.1 下拉实例')

  // Open the dropdown; options become visible and selectable.
  await page.getByTestId('instance-version-picker').click()
  await expect(page.getByTestId('instance-version-options')).toBeVisible()
  const options = page.getByTestId('instance-version-option')
  await expect(options.first()).toBeVisible()
  await expect(options.first()).toContainText('0.2.1-rc.3')
  await page.screenshot({ path: '../docs/evidence/m5/m5.1-version-dropdown.png' })

  await options.first().click()
  await expect(page.getByTestId('instance-version')).toHaveValue('0.2.1-rc.3')
  await expect(page.getByTestId('instance-version-options')).toHaveCount(0)
  // Free typing is still accepted.
  await page.getByTestId('instance-version').fill('0.2.1-rc.99-custom')
  await page.getByTestId('instance-save').click()
  await expect(page.getByTestId('instance-dialog')).toHaveCount(0)
  expect(submitted?.version).toBe('0.2.1-rc.99-custom')
  void request
})
