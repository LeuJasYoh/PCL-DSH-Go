import { test, expect } from '@playwright/test'

import { mockJson } from './helpers'

const instance = { id: 'i-e2e-fixture', name: 'E2E 实列', version: '0.2.1-rc.3', profile: 'web', port: 3080, status: '运行中', path: '/tmp/e2e', profiles: ['web'] }
const state = {
  version: '0.3.0-m4', currentInstanceId: instance.id, instances: [instance],
  runtimes: [{ version: '0.2.1-rc.3', installed: true, compatibility: 'verified' }],
  status: { code: 'READY', message: '就绪' },
  settings: { proxy: '', registry: 'https://registry.npmjs.org', directAccessSshUser: 'root' },
  availableVersions: [], warnings: [],
}

test('直连对话框展示 tunnelCommand/directUrl/sshUser 与未就绪提示', async ({ page }) => {
  await mockJson(page, '**/api/state', state)
  await mockJson(page, '**/api/instances/*/direct-access', {
    ready: true,
    port: 3080,
    directUrl: 'http://127.0.0.1:3080/?token=e2e-token',
    tunnelCommand: 'ssh -L 3080:127.0.0.1:3080 root@127.0.0.1',
    sshHost: '127.0.0.1',
    sshUser: 'root',
    note: '通过 SSH 隧道访问工作台。',
  })
  await page.goto('/instances')
  await expect(page.getByTestId('page-instances')).toBeVisible()
  await page.locator('[data-testid^="instance-direct-"]').first().click()
  await expect(page.getByTestId('direct-access-tunnel')).toContainText('ssh -L 3080:127.0.0.1:3080')
  await expect(page.getByTestId('direct-access-url')).toContainText('127.0.0.1:3080')
})

test('直连未就绪时展示提示', async ({ page }) => {
  await mockJson(page, '**/api/state', state)
  await mockJson(page, '**/api/instances/*/direct-access', {
    ready: false, port: 0, directUrl: null, tunnelCommand: '',
    sshHost: '127.0.0.1', sshUser: 'root', note: '实例未运行，请先启动实例。',
  })
  await page.goto('/instances')
  await page.locator('[data-testid^="instance-direct-"]').first().click()
  await expect(page.getByTestId('direct-access-note').or(page.getByText('实例未运行'))).toBeVisible()
})

test('Clipboard API 不可用时使用兼容复制', async ({ page }) => {
  await page.addInitScript(() => {
    Object.defineProperty(navigator, 'clipboard', { configurable: true, value: undefined })
    document.execCommand = (command) => {
      if (command !== 'copy') return false
      const active = document.activeElement
      if (active instanceof HTMLTextAreaElement) {
        document.documentElement.dataset.copiedText = active.value
      }
      return true
    }
  })
  await mockJson(page, '**/api/state', state)
  await mockJson(page, '**/api/instances/*/direct-access', {
    ready: true,
    port: 3080,
    directUrl: 'http://127.0.0.1:3080/?token=e2e-token',
    tunnelCommand: 'ssh -L 3080:127.0.0.1:3080 root@127.0.0.1',
    sshHost: '127.0.0.1',
    sshUser: 'root',
    note: '通过 SSH 隧道访问工作台。',
  })
  await page.goto('/instances')
  await page.locator('[data-testid^="instance-direct-"]').first().click()
  await page.getByTestId('direct-access-copy-tunnel').click()
  await expect(page.getByText('已复制到剪贴板。')).toBeVisible()
  await expect(page.getByTestId('direct-access-manual-copy')).toHaveCount(0)
  const copied = await page.evaluate(() => document.documentElement.dataset.copiedText)
  expect(copied).toBe('ssh -L 3080:127.0.0.1:3080 root@127.0.0.1')
})

test('无法自动复制时滚动到手动复制框并选中文本', async ({ page }) => {
  await page.addInitScript(() => {
    Object.defineProperty(navigator, 'clipboard', { configurable: true, value: undefined })
    document.execCommand = () => false
  })
  await mockJson(page, '**/api/state', state)
  await mockJson(page, '**/api/instances/*/direct-access', {
    ready: true,
    port: 3080,
    directUrl: 'http://127.0.0.1:3080/?token=e2e-token',
    tunnelCommand: 'ssh -L 3080:127.0.0.1:3080 root@127.0.0.1',
    sshHost: '127.0.0.1',
    sshUser: 'root',
    note: '通过 SSH 隧道访问工作台。',
  })
  await page.goto('/instances')
  await page.locator('[data-testid^="instance-direct-"]').first().click()
  await page.getByTestId('direct-access-copy-url').click()
  const input = page.getByTestId('direct-access-manual-copy')
  await expect(input).toBeVisible()
  await expect(input).toBeInViewport()
  await expect(input).toHaveValue('http://127.0.0.1:3080/?token=e2e-token')
  const selection = await input.evaluate((element) => {
    const textarea = element as HTMLTextAreaElement
    return {
      focused: document.activeElement === textarea,
      start: textarea.selectionStart,
      end: textarea.selectionEnd,
    }
  })
  expect(selection.focused).toBe(true)
  expect(selection.start).toBe(0)
  expect(selection.end).toBe('http://127.0.0.1:3080/?token=e2e-token'.length)
})
