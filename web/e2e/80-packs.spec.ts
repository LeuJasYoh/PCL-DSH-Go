import { test, expect } from '@playwright/test'

import { jobView, mockJobStream } from './helpers'

const JOB_ID = 'j-e2e-import-pack'
const state = {
  version: '0.3.0-m4',
  currentInstanceId: 'i-e2e-fixture',
  instances: [{ id: 'i-e2e-fixture', name: 'E2E 实例', version: '0.2.1-rc.3', profile: 'web', port: 3080, status: '未启动', path: '/tmp/e2e', profiles: ['web'] }],
  runtimes: [{ version: '0.2.1-rc.3', installed: true, compatibility: 'verified' }],
  status: { code: 'READY', message: '就绪' },
  settings: { proxy: '', registry: 'https://registry.npmjs.org', directAccessSshUser: 'root' },
  availableVersions: [],
  warnings: [],
}

test('整合包：上传预览与导入任务（202 + SSE mock）', async ({ page }) => {
  await page.route('**/api/state', (route) => route.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify(state) }))
  await page.route('**/api/packs/preview', (route) =>
    route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify({ previewId: 'abc123def456', name: 'E2E 整合包', format: 'dspack', version: '1.0.0', dshVersion: '0.2.1-rc.3', profiles: ['web'], unpackedBytes: 4096, warnings: [] }),
    }),
  )
  await page.route('**/api/packs/import', (route) =>
    route.fulfill({ status: 202, contentType: 'application/json', body: JSON.stringify(jobView(JOB_ID, 'IMPORT_PACK')) }),
  )
  await mockJobStream(page, JOB_ID, [
    { event: 'state', data: { state: 'RUNNING', phase: '导入整合包', message: '正在导入…' } },
    { event: 'log', data: { level: 'info', line: '[导入] E2E 模拟导入日志' } },
    { event: 'result', data: { state: 'SUCCEEDED', result: { instanceId: 'i-e2e-imported', name: 'E2E 整合包' } } },
  ])

  await page.goto('/packs')
  await expect(page.getByTestId('page-packs')).toBeVisible()
  await page.locator('input[type="file"]').first().setInputFiles('../testdata/pack/valid/dspack-v3-profile.dspack')
  await expect(page.getByText('E2E 整合包').first()).toBeVisible()
  await page.getByRole('button', { name: /导入/ }).first().click()
  await expect(page.locator('.toast').filter({ hasText: '任务' }).first()).toBeVisible({ timeout: 15_000 })
})

test('整合包预览错误显示 400 中文消息', async ({ page }) => {
  await page.route('**/api/state', (route) => route.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify(state) }))
  await page.route('**/api/packs/preview', (route) =>
    route.fulfill({
      status: 400,
      contentType: 'application/json',
      body: JSON.stringify({ code: 'VALIDATION', message: '整合包安全校验失败：归档路径包含 . 或 .. 段。', details: {}, traceId: 'e2e' }),
    }),
  )
  await page.goto('/packs')
  await page.locator('input[type="file"]').first().setInputFiles('../testdata/pack/malicious/14_zip_slip_parent.dspack')
  await expect(page.getByText(/安全校验失败/).first()).toBeVisible()
})


test('整合包：导出实例为 artifact（202 + SSE + 下载提示）', async ({ page }) => {
  const exportJob = 'j-e2e-export-pack'
  await page.route('**/api/state', (route) => route.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify(state) }))
  await page.route('**/api/packs/export', (route) =>
    route.fulfill({ status: 202, contentType: 'application/json', body: JSON.stringify(jobView(exportJob, 'EXPORT_PACK')) }),
  )
  await page.route('**/api/jobs/' + exportJob, (route) =>
    route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify(jobView(exportJob, 'EXPORT_PACK', { state: 'SUCCEEDED', result: { artifactId: 'abcdef123456', filename: 'export.dspack', size: 1024 } })),
    }),
  )
  await mockJobStream(page, exportJob, [
    { event: 'state', data: { state: 'RUNNING', phase: '导出实例', message: '正在导出…' } },
    { event: 'result', data: { state: 'SUCCEEDED', result: { artifactId: 'abcdef123456', filename: 'export.dspack', size: 1024 } } },
  ])
  await page.goto('/packs')
  await expect(page.getByTestId('page-packs')).toBeVisible()
  await page.getByRole('button', { name: /导出/ }).first().click()
  await expect(page.locator('.toast').filter({ hasText: '导出完成' }).first()).toBeVisible({ timeout: 15_000 })
})
