#!/usr/bin/env node
'use strict'

/**
 * Live DSH workbench probe (NOT part of CI, default does not run).
 *
 * Requires a real, already-running backend (BASE_URL, default http://127.0.0.1)
 * that has the /workbench reverse proxy, a built/working DSH runtime so an
 * instance can actually launch, and Playwright Chromium (npx playwright install chromium).
 *
 * Flow: login -> pick instance -> launch (if needed) + poll Job -> open
 * /workbench/{id}/ -> wait for the DSH shell -> collect WebSocket/console/pageerror
 * -> screenshot -> stop instance.
 *
 * Environment:
 *   BASE_URL            backend origin (default http://127.0.0.1)
 *   ADMIN_PASSWORD      required admin password (unless SESSION_STATE is used)
 *   NEW_PASSWORD        optional: only needed when the initial password must be changed
 *   SESSION_STATE       optional Playwright storageState JSON from a previous login
 *   INSTANCE_ID         optional instance id (default: current instance or first)
 *   SCREENSHOT_PATH     output PNG (default ./live-workbench.png)
 *   KEEP_RUNNING=1      do not stop the instance at the end
 *   HEADED=1            show the browser window
 *   BROWSER_CHANNEL     optional Playwright channel, e.g. msedge
 *   BROWSER_EXECUTABLE  optional explicit Chromium executable path
 *   WORKBENCH_TIMEOUT_MS default 60000
 *   READY_TIMEOUT_MS     default 120000 (wait for 运行中 after launch)
 *   LAUNCH_TIMEOUT_MS    default 1200000 (installing a runtime can take long)
 */

const fs = require('fs')
const path = require('path')

const BASE_URL = (process.env.BASE_URL || 'http://127.0.0.1').replace(/\/+$/, '')
const ADMIN_PASSWORD = process.env.ADMIN_PASSWORD || ''
const NEW_PASSWORD = process.env.NEW_PASSWORD || ''
const SESSION_STATE = process.env.SESSION_STATE || ''
const INSTANCE_ID = process.env.INSTANCE_ID || ''
const SCREENSHOT_PATH = process.env.SCREENSHOT_PATH || path.join(process.cwd(), 'live-workbench.png')
const KEEP_RUNNING = process.env.KEEP_RUNNING === '1'
const HEADED = process.env.HEADED === '1'
const BROWSER_CHANNEL = process.env.BROWSER_CHANNEL || ''
const BROWSER_EXECUTABLE = process.env.BROWSER_EXECUTABLE || ''
const WORKBENCH_TIMEOUT_MS = Number(process.env.WORKBENCH_TIMEOUT_MS || 60_000)
const READY_TIMEOUT_MS = Number(process.env.READY_TIMEOUT_MS || 120_000)
const LAUNCH_TIMEOUT_MS = Number(process.env.LAUNCH_TIMEOUT_MS || 1_200_000)

function log() {
  const args = Array.prototype.slice.call(arguments)
  console.log.apply(console, ['[live-workbench]'].concat(args))
}

function fail(message) {
  const error = new Error(message)
  error.isProbeFailure = true
  throw error
}

function sleep(ms) {
  return new Promise((resolve) => setTimeout(resolve, ms))
}

function isRunning(status) {
  const value = String(status || '').toLowerCase()
  return [
    'running',
    '运行中',
    '已启动',
    'starting',
    '启动中',
    '进程运行中，服务未就绪',
    '子进程仍在运行',
  ].indexOf(value) >= 0
}

async function api(context, method, apiPath, body) {
  const cookies = await context.cookies(BASE_URL)
  const csrf = cookies.find((cookie) => cookie.name === 'XSRF-TOKEN')
  const headers = { Accept: 'application/json' }
  if (csrf) headers['X-XSRF-TOKEN'] = csrf.value
  if (body !== undefined) headers['Content-Type'] = 'application/json'
  const response = await context.request.fetch(BASE_URL + apiPath, {
    method: method,
    headers: headers,
    data: body === undefined ? undefined : JSON.stringify(body),
  })
  const text = await response.text()
  let payload = null
  try {
    payload = text ? JSON.parse(text) : null
  } catch {
    payload = text
  }
  if (!response.ok()) {
    const message = payload && payload.message ? payload.message : String(text || '').slice(0, 300)
    throw new Error(method + ' ' + apiPath + ' -> HTTP ' + response.status() + (message ? '：' + message : ''))
  }
  return payload
}

async function login(page, context) {
  log('打开', BASE_URL)
  let response
  try {
    response = await page.goto(BASE_URL + '/', { waitUntil: 'domcontentloaded', timeout: 60_000 })
  } catch (error) {
    fail('无法打开 ' + BASE_URL + '：' + error.message + '。请确认后端与前端服务已启动，且 BASE_URL 正确。')
  }
  if (!response) fail('无法打开 ' + BASE_URL)
  if (response.status() >= 500) fail('后端返回 HTTP ' + response.status() + '，请确认服务已启动。')
  await page
    .waitForFunction(
      function () {
        return (
          document.querySelector('[data-testid="page-login"]') !== null ||
          document.querySelector('[data-testid="page-home"]') !== null
        )
      },
      null,
      { timeout: 20_000 },
    )
    .catch(function () {
      fail('页面未渲染登录页或首页：请确认 ' + BASE_URL + ' 是 PCL-DSH-Linux Web 前端，且后端健康。')
    })

  const loginPage = page.locator('[data-testid="page-login"]')
  if (!(await loginPage.count())) {
    log('已有登录会话，跳过登录')
    await page.waitForSelector('[data-testid="page-home"]', { timeout: 20_000 })
    return
  }
  if (!ADMIN_PASSWORD) {
    fail('需要 ADMIN_PASSWORD 环境变量（真实管理员密码）。示例：$env:ADMIN_PASSWORD="..."; node scripts/live-workbench.cjs')
  }
  log('登录页：使用 ADMIN_PASSWORD 登录')
  await page.locator('[data-testid="login-password"]').fill(ADMIN_PASSWORD)
  await page.locator('[data-testid="login-submit"]').click()

  // The form resolves to: home (success), initial-password change, or an error.
  await page
    .waitForFunction(
      function () {
        return (
          document.querySelector('[data-testid="page-home"]') !== null ||
          document.querySelector('[data-testid="login-new-password"]') !== null ||
          document.querySelector('[data-testid="login-error"]') !== null
        )
      },
      null,
      { timeout: 25_000 },
    )
    .catch(function () {
      fail('登录请求超时：请检查后端 /api/auth/login 与网络。')
    })

  const error = page.locator('[data-testid="login-error"]')
  if ((await error.count()) && (await error.isVisible().catch(() => false))) {
    fail('登录失败：' + (await error.innerText()).trim())
  }

  const changeForm = page.locator('[data-testid="login-new-password"]')
  if ((await changeForm.count()) && (await changeForm.isVisible().catch(() => false))) {
    if (!NEW_PASSWORD) {
      fail('首次登录需要修改初始密码；请设置 NEW_PASSWORD，或先在 Web 界面完成改密后重试。')
    }
    log('检测到初始密码待修改：使用 NEW_PASSWORD 完成改密')
    await changeForm.fill(NEW_PASSWORD)
    await page.locator('[data-testid="login-confirm-password"]').fill(NEW_PASSWORD)
    await page.locator('[data-testid="login-change-submit"]').click()
  }

  await page.waitForSelector('[data-testid="page-home"]', { timeout: 30_000 })
  log('登录成功，已进入首页')
}

async function pickInstance(context) {
  const state = await api(context, 'GET', '/api/state')
  const instances = Array.isArray(state.instances) ? state.instances : []
  if (instances.length === 0) {
    fail('当前没有实例：请先在 Web 界面创建实例并安装可用的 DSH 运行时。')
  }
  const selected =
    (INSTANCE_ID && instances.find((item) => item.id === INSTANCE_ID)) ||
    instances.find((item) => item.id === state.currentInstanceId) ||
    instances[0]
  if (INSTANCE_ID && selected.id !== INSTANCE_ID) {
    fail('找不到 INSTANCE_ID=' + INSTANCE_ID + ' 对应的实例。')
  }
  log('目标实例：' + selected.name + '（' + selected.id + '），版本 ' + selected.version + '，状态 ' + selected.status)
  return selected
}

async function launchInstance(context, instance) {
  log('启动实例：POST /api/instances/' + instance.id + '/launch')
  const job = await api(context, 'POST', '/api/instances/' + encodeURIComponent(instance.id) + '/launch')
  if (!job || !job.id) fail('启动接口没有返回任务 ID。')
  log('启动任务已提交：' + job.id + '（' + (job.type || 'LAUNCH_INSTANCE') + '）')

  const deadline = Date.now() + LAUNCH_TIMEOUT_MS
  let lastPhase = ''
  while (Date.now() < deadline) {
    await sleep(2000)
    const view = await api(context, 'GET', '/api/jobs/' + encodeURIComponent(job.id))
    if (view.phase && view.phase !== lastPhase) {
      lastPhase = view.phase
      log('任务阶段：' + view.phase)
    }
    if (view.state === 'SUCCEEDED') {
      log('启动任务完成')
      return
    }
    if (view.state === 'FAILED' || view.state === 'CANCELLED' || view.state === 'TIMEOUT') {
      const detail = view.error && view.error.message ? '：' + view.error.message : ''
      fail('启动任务未成功（' + view.state + detail + '）。可打开任务与日志页查看完整日志。')
    }
  }
  fail('启动任务等待超时（' + LAUNCH_TIMEOUT_MS + 'ms，最后阶段 ' + (lastPhase || '未知') + '）。')
}

async function waitWebReady(context, instanceId) {
  log('等待实例网页服务就绪…')
  const deadline = Date.now() + READY_TIMEOUT_MS
  while (Date.now() < deadline) {
    const list = await api(context, 'GET', '/api/instances')
    const instance = list.find((item) => item.id === instanceId)
    const status = instance ? instance.status : ''
    if (status === '运行中' || status === 'running') {
      log('实例已就绪（' + status + '）')
      return
    }
    if (/已退出|退出|failed/i.test(String(status))) {
      fail('实例进程异常退出：' + status + '。请查看任务与日志页。')
    }
    await sleep(1500)
  }
  fail('等待网页服务就绪超时（' + READY_TIMEOUT_MS + 'ms）。')
}

async function waitForDshShell(page) {
  try {
    await page.waitForFunction(
      function () {
        const title = document.title || ''
        const body = document.body ? document.body.innerText || '' : ''
        const hasLayout =
          document.querySelector('[class*="sidebar"],[data-dsh],nav,aside,[role="navigation"]') !== null
        const titleLooksDsh = /deepseek|harness|dsh/i.test(title)
        const textLooksDsh = /DeepSeek|Harness|会话|工作区|模型/.test(body.slice(0, 6000))
        if (body.trim().length < 40 || !hasLayout) return false
        return titleLooksDsh || textLooksDsh
      },
      null,
      { timeout: WORKBENCH_TIMEOUT_MS },
    )
  } catch {
    const title = await page.title().catch(() => '')
    const body = await page
      .evaluate(() => (document.body ? document.body.innerText.slice(0, 300) : ''))
      .catch(() => '')
    fail(
      '工作台页面未在 ' + WORKBENCH_TIMEOUT_MS + 'ms 内加载 DSH 界面（title="' + title +
        '"，body 前缀="' + String(body).replace(/\s+/g, ' ').slice(0, 160) +
        '"）。请确认实例真正就绪、后端 /workbench 反代正常、DSH 版本兼容。',
    )
  }
}

async function openWorkbench(context, instance) {
  const web = await api(context, 'GET', '/api/instances/' + encodeURIComponent(instance.id) + '/web-url')
  if (!web || !web.url) fail('web-url 接口没有返回地址。')
  const url = /^https?:/i.test(web.url) ? web.url : BASE_URL + web.url
  log('打开工作台：' + url)

  const page = await context.newPage()
  page.setDefaultTimeout(30_000)
  const consoleMessages = []
  const pageErrors = []
  const sockets = []
  page.on('console', (message) => {
    if (message.type() === 'error' || message.type() === 'warning') {
      consoleMessages.push(message.type() + ': ' + message.text().slice(0, 300))
    }
  })
  page.on('pageerror', (error) => pageErrors.push(String((error && error.stack) || error).slice(0, 500)))
  page.on('websocket', (socket) => {
    const stat = { url: socket.url().slice(0, 200), sent: 0, received: 0 }
    sockets.push(stat)
    socket.on('framesent', () => {
      stat.sent += 1
    })
    socket.on('framereceived', () => {
      stat.received += 1
    })
  })

  const response = await page.goto(url, { waitUntil: 'domcontentloaded', timeout: 60_000 })
  log('工作台 HTTP 状态：' + (response ? response.status() : 'n/a'))
  await waitForDshShell(page)
  await page.waitForTimeout(2500)
  const title = await page.title()
  log('DSH 页面标题：' + title)
  log('WebSocket：' + sockets.length + ' 个连接，累计收到帧 ' + sockets.reduce((n, item) => n + item.received, 0))

  await fs.promises.mkdir(path.dirname(SCREENSHOT_PATH), { recursive: true })
  await page.screenshot({ path: SCREENSHOT_PATH })
  log('截图已保存：' + SCREENSHOT_PATH)

  if (pageErrors.length > 0) {
    const failureShot = SCREENSHOT_PATH.replace(/\.png$/i, '') + '-failure.png'
    await page.screenshot({ path: failureShot }).catch(() => {})
    fail('DSH 页面出现 pageerror：' + pageErrors[0] + '（失败截图：' + failureShot + '）')
  }
  if (sockets.length === 0) {
    log('警告：未观察到 WebSocket 连接（DSH 可能走 SSE 或仍在校验会话）。')
  }
  if (consoleMessages.length > 0) {
    log('控制台警告/错误（前 5 条）：')
    consoleMessages.slice(0, 5).forEach((line) => log('  ' + line))
  }
  return { page: page, title: title, url: url, sockets: sockets, consoleMessages: consoleMessages, pageErrors: pageErrors }
}

async function stopInstance(context, instanceId) {
  if (!instanceId || KEEP_RUNNING) {
    log('KEEP_RUNNING=1 或没有实例：跳过停止')
    return
  }
  log('停止实例：POST /api/instances/' + instanceId + '/stop')
  try {
    await api(context, 'POST', '/api/instances/' + encodeURIComponent(instanceId) + '/stop')
    log('已停止实例')
  } catch (error) {
    log('停止实例失败（请手动停止）：' + error.message)
  }
}

async function main() {
  let chromium
  try {
    chromium = require('playwright').chromium
  } catch (error) {
    fail('未找到 playwright 依赖：请先在 pcl-dsh-web 下执行 npm install（含 @playwright/test）。')
  }
  if (!ADMIN_PASSWORD && !SESSION_STATE) {
    fail('缺少 ADMIN_PASSWORD 环境变量。示例：$env:ADMIN_PASSWORD="你的密码"; $env:BASE_URL="http://127.0.0.1"; node scripts/live-workbench.cjs')
  }

  let browser
  try {
    browser = await chromium.launch({
      headless: !HEADED,
      channel: BROWSER_CHANNEL || undefined,
      executablePath: BROWSER_EXECUTABLE || undefined,
    })
  } catch (error) {
    fail('无法启动浏览器：' + error.message + '。请先执行 npx playwright install chromium（或设置 BROWSER_CHANNEL=msedge）。')
  }

  const contextOptions = { viewport: { width: 1440, height: 900 }, ignoreHTTPSErrors: true }
  if (SESSION_STATE && fs.existsSync(SESSION_STATE)) contextOptions.storageState = SESSION_STATE
  const context = await browser.newContext(contextOptions)
  const page = await context.newPage()
  let instance = null
  try {
    await login(page, context)
    instance = await pickInstance(context)
    if (!isRunning(instance.status)) {
      await launchInstance(context, instance)
    } else {
      log('实例已在运行，跳过启动')
    }
    await waitWebReady(context, instance.id)
    const result = await openWorkbench(context, instance)
    log('SUCCESS：工作台已加载，title="' + result.title + '"，WebSocket 连接 ' + result.sockets.length + ' 个。')
    process.exitCode = 0
  } catch (error) {
    const reason = error && error.message ? error.message : String(error)
    console.error('[live-workbench] FAILED: ' + reason)
    const failureShot = SCREENSHOT_PATH.replace(/\.png$/i, '') + '-failure.png'
    try {
      if (page && !page.isClosed()) {
        await fs.promises.mkdir(path.dirname(failureShot), { recursive: true })
        await page.screenshot({ path: failureShot })
        console.error('[live-workbench] 失败截图：' + failureShot)
      }
    } catch {
      // Screenshot is best effort.
    }
    process.exitCode = 1
  } finally {
    if (instance) {
      await stopInstance(context, instance.id).catch((error) => {
        console.error('[live-workbench] 停止实例异常：' + error.message)
      })
    }
    await context.close().catch(() => {})
    await browser.close().catch(() => {})
  }
}

main().catch((error) => {
  console.error('[live-workbench] FAILED: ' + (error && error.message ? error.message : String(error)))
  process.exitCode = 1
})
