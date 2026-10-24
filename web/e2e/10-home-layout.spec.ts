import { test, expect } from '@playwright/test'

test('首页布局：40px 页头 / 200px 侧栏 / 7 个导航 / 200x55 启动按钮 / 960x650 无横向溢出', async ({ page }) => {
  await page.setViewportSize({ width: 960, height: 650 })
  await page.goto('/')
  await expect(page.getByTestId('page-home')).toBeVisible()

  const header = await page.locator('[data-testid="page-header"]').boundingBox()
  expect(header).not.toBeNull()
  expect(Math.round(header!.height)).toBe(40)

  const sidebar = await page.locator('[data-testid="sidebar"]').boundingBox()
  expect(sidebar).not.toBeNull()
  expect(Math.round(sidebar!.width)).toBe(200)

  // Plan 5.2: three groups, 7 navigation entries plus the current-instance item.
  await expect(page.locator('[data-testid^="nav-"]')).toHaveCount(7)
  await expect(page.getByTestId('current-instance-item')).toBeVisible()

  const launch = await page.getByTestId('primary-launch').boundingBox()
  expect(launch).not.toBeNull()
  expect(Math.round(launch!.width)).toBe(200)
  expect(Math.round(launch!.height)).toBe(55)

  const overflow = await page.evaluate(() => ({
    scrollWidth: document.documentElement.scrollWidth,
    clientWidth: document.documentElement.clientWidth,
  }))
  expect(overflow.scrollWidth, 'no horizontal overflow at 960px').toBeLessThanOrEqual(overflow.clientWidth + 1)
})
