import { test, expect } from '@playwright/test'

test('新壳层：56px 命令栏 / 64px 导航 / 232px 上下文栏 / 3 个入口 + 活动面板 / 960x650 无横向溢出', async ({ page }) => {
  await page.setViewportSize({ width: 960, height: 650 })
  await page.goto('/')
  await expect(page.getByTestId('page-home')).toBeVisible()

  const headerElement = page.getByTestId('page-header')
  const header = await headerElement.boundingBox()
  expect(header).not.toBeNull()
  expect(Math.round(header!.height)).toBe(56)
  await expect(headerElement.getByTestId('window-home')).toContainText('PCL-DSH-Go')
  await expect(headerElement.getByTestId('task-status')).toBeVisible()

  const sidebar = await page.locator('[data-testid="sidebar"]').boundingBox()
  expect(sidebar).not.toBeNull()
  expect(Math.round(sidebar!.width)).toBe(232)

  // 3 rail destinations + the activity drawer's日志 entry.
  await expect(page.locator('[data-testid^="nav-"]')).toHaveCount(4)
  await expect(page.getByTestId('nav-instances')).toBeVisible()
  await expect(page.getByTestId('current-instance-item')).toBeVisible()

  const launch = await page.getByTestId('primary-launch').boundingBox()
  expect(launch).not.toBeNull()
  expect(launch!.width).toBeGreaterThan(80)

  const overflow = await page.evaluate(() => ({
    scrollWidth: document.documentElement.scrollWidth,
    clientWidth: document.documentElement.clientWidth,
  }))
  expect(overflow.scrollWidth, 'no horizontal overflow at 960px').toBeLessThanOrEqual(overflow.clientWidth + 1)
})
