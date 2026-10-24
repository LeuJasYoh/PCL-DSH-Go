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
