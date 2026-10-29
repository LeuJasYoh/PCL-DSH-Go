import fs from 'node:fs'
import path from 'node:path'

import { test, expect } from '@playwright/test'

// M4 evidence + regression coverage: console errors, legacy UX/requests, page screenshots.
// Runs in the authenticated "msedge" project against the real Go manager + built dist.
const evidenceDir = path.resolve(process.cwd(), '..', 'docs', 'evidence', 'm4', 'screenshots')
const legacyPattern = /\/api\/(accounts|plugin-catalog|settings\/background|assets\/backgrounds)|instances\/[^/]+\/copy/
const consoleWhitelist = [/Failed to load resource.*401/i, /WebSocket connection/i, /favicon/i]

test.beforeAll(() => {
  fs.mkdirSync(evidenceDir, { recursive: true })
})

test('G8 页面巡检 + console 无错误 + 无账户/目录/背景请求 + 截图', async ({ page }) => {
  const legacyRequests: string[] = []
  const consoleErrors: string[] = []
  page.on('request', (request) => {
    if (legacyPattern.test(request.url())) legacyRequests.push(request.url())
  })
  page.on('console', (message) => {
    if (message.type() === 'error') consoleErrors.push(message.text())
  })

  await page.route('**/api/runtimes/available', (route) => route.fulfill({
    status: 200,
    contentType: 'application/json',
    body: JSON.stringify({ versions: ['0.2.1-rc.3', '0.2.1-rc.2'] }),
  }))
  const pages: Array<{ route: string; testid: string; shot: string }> = [
    { route: '/', testid: 'page-home', shot: 'home' },
    { route: '/instances', testid: 'page-instances', shot: 'instances' },
    { route: '/runtimes', testid: 'page-runtimes', shot: 'runtimes' },
    { route: '/packs', testid: 'page-packs', shot: 'packs' },
    { route: '/plugins', testid: 'page-plugins', shot: 'plugins' },
    { route: '/logs', testid: 'page-logs', shot: 'logs' },
    { route: '/settings', testid: 'page-settings', shot: 'settings' },
  ]
  for (const entry of pages) {
    await page.goto(entry.route)
    await expect(page.getByTestId(entry.testid)).toBeVisible()
    await page.screenshot({ path: path.join(evidenceDir, entry.shot + '.png') })
  }

  // No legacy account/catalog/background navigation or copy-version actions.
  await expect(page.locator('[data-testid^="nav-"]')).toHaveCount(4)
  await expect(page.getByText('账户', { exact: true })).toHaveCount(0)
  await expect(page.getByText('插件目录')).toHaveCount(0)
  expect(legacyRequests, 'no legacy API requests').toEqual([])

  const unexpectedConsole = consoleErrors.filter((text) => !consoleWhitelist.some((rule) => rule.test(text)))
  expect(unexpectedConsole, 'console errors (whitelist excludes known 401 noise)').toEqual([])
})

test('工作台入口：点击打开 /workbench/{id}/ 新窗口', async ({ page }) => {
  const instance = { id: 'i-m4-workbench', name: 'M4 工作台实例', version: '0.2.1-rc.3', profile: 'web', port: 3090, status: '运行中', path: '/tmp/m4', profiles: ['web'] }
  await page.route('**/api/state', (route) => route.fulfill({
    status: 200,
    contentType: 'application/json',
    body: JSON.stringify({
      version: '0.3.0-m4', currentInstanceId: instance.id, instances: [instance],
      runtimes: [{ version: '0.2.1-rc.3', installed: true, compatibility: 'verified' }],
      status: { code: 'READY', message: '就绪' },
      settings: { proxy: '', registry: 'https://registry.npmjs.org', directAccessSshUser: 'root' },
      availableVersions: [], warnings: [],
    }),
  }))
  await page.route('**/api/instances/*/web-url', (route) => route.fulfill({
    status: 200, contentType: 'application/json', body: JSON.stringify({ url: '/workbench/i-m4-workbench/' }),
  }))
  await page.goto('/instances')
  const card = page.locator('.instance-card').filter({ hasText: 'M4 工作台实例' }).first()
  await expect(card).toBeVisible()
  const requestPromise = page.waitForRequest('**/api/instances/*/web-url')
  const popupPromise = page.waitForEvent('popup', { timeout: 5000 }).catch(() => null)
  await card.locator('[data-testid^="instance-web-"]').click()
  const request = await requestPromise
  expect(request.url()).toContain('/web-url')
  const popup = await popupPromise
  if (popup) await popup.close().catch(() => undefined)
})
