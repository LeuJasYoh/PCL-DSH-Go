import { test, expect } from '@playwright/test'

import { jobView, mockJobStream } from './helpers'

const JOB_ID = 'j-e2e-plugin-add'
const instance = { id: 'i-e2e-fixture', name: 'E2E 实例', version: '0.2.1-rc.3', profile: 'web', port: 3080, status: '未启动', path: '/tmp/e2e', profiles: ['web'] }
const state = {
  version: '0.3.0-m4', currentInstanceId: instance.id, instances: [instance],
  runtimes: [{ version: '0.2.1-rc.3', installed: true, compatibility: 'verified' }],
  status: { code: 'READY', message: '就绪' },
  settings: { proxy: '', registry: 'https://registry.npmjs.org', directAccessSshUser: 'root' },
  availableVersions: [], warnings: [],
}

test('插件：列表与安装任务（202 + SSE mock）', async ({ page }) => {
  let plugins = [{ name: '@deepseek-ai/dsh-base', version: '0.2.1-rc.3', description: '内置核心包', source: '内置' }]
  await page.route('**/api/state', (route) => route.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify(state) }))
  await page.route(/\/api\/instances\/[^/]+\/plugins(\?.*)?$/, (route) => {
    const method = route.request().method()
    if (method === 'GET') {
      return route.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify(plugins) })
    }
    if (method === 'POST') {
      plugins = plugins.concat([{ name: '@pcl-dsh/m3-test-plugin', version: '0.1.0', description: '本地测试插件', source: 'file:./local-test-plugin' }])
      return route.fulfill({ status: 202, contentType: 'application/json', body: JSON.stringify(jobView(JOB_ID, 'PLUGIN_MUTATE')) })
    }
    return route.fallback()
  })
  await mockJobStream(page, JOB_ID, [
    { event: 'state', data: { state: 'RUNNING', phase: '安装插件', message: '正在安装…' } },
    { event: 'result', data: { state: 'SUCCEEDED', result: { instanceId: instance.id, profile: 'web', action: 'add', name: '@pcl-dsh/m3-test-plugin' } } },
  ])

  await page.goto('/plugins')
  await expect(page.getByTestId('page-plugins')).toBeVisible()
  await expect(page.getByText('@deepseek-ai/dsh-base').first()).toBeVisible()
  const specInput = page.getByTestId('plugin-spec')
  if (await specInput.count()) await specInput.fill('file:./local-test-plugin')
  await page.getByRole('button', { name: /安装|添加/ }).first().click()
  await expect(page.locator('.toast').filter({ hasText: '任务' }).first()).toBeVisible({ timeout: 15_000 })
})


test('插件错误：忙 409 / 校验 400 / 任务 VALIDATION 提示', async ({ page }) => {
  await page.route('**/api/state', (route) => route.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify(state) }))
  let mode: 'busy' | 'validation' | 'job-error' = 'busy'
  await page.route(/\/api\/instances\/[^/]+\/plugins(\?.*)?$/, (route) => {
    const method = route.request().method()
    if (method === 'GET') {
      return route.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify([]) })
    }
    if (mode === 'busy') {
      mode = 'validation'
      return route.fulfill({ status: 409, contentType: 'application/json', body: JSON.stringify({ code: 'CONFLICT_BUSY', message: '已有任务正在执行，请稍后重试。', details: {}, traceId: 'e2e' }) })
    }
    if (mode === 'validation') {
      mode = 'job-error'
      return route.fulfill({ status: 400, contentType: 'application/json', body: JSON.stringify({ code: 'VALIDATION', message: '插件 spec 非法：不能以 - 开头。', details: {}, traceId: 'e2e' }) })
    }
    return route.fallback()
  })

  await page.goto('/plugins')
  await expect(page.getByTestId('page-plugins')).toBeVisible()
  const specInput = page.getByTestId('plugin-spec')
  const submit = page.getByRole('button', { name: /安装|添加/ }).first()

  await specInput.fill('lodash@4.17.21')
  await submit.click()
  await expect(page.getByText(/已有任务正在执行/).first()).toBeVisible({ timeout: 10_000 })

  await specInput.fill('-evil')
  await submit.click()
  await expect(page.getByText(/不能以连字符开头|非法/).first()).toBeVisible({ timeout: 10_000 })

})
