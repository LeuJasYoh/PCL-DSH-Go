import { execFileSync } from 'node:child_process'
import fs from 'node:fs'
import path from 'node:path'

import { E2E_NEW_PASSWORD } from './helpers'

/**
 * Start (or reuse) the Go manager + built web/dist E2E stack, then perform the
 * first-login password change through the real API and persist the authenticated
 * storage state for the dependent Playwright project.
 *
 * Stack: scripts/e2e-stack.ps1 starts the WSL Go manager with a fresh data root and
 * --server.static-dir=<repo>/web/dist, and copies the initial password to
 * web/.e2e-data/e2e-initial-password.txt. E2E_EXTERNAL_STACK=1 reuses a stack that
 * is already running (the initial password must already have been changed, or
 * E2E_INITIAL_PASSWORD must be set for a first run).
 */
const webRoot = process.cwd()
const e2eDataDir = path.join(webRoot, '.e2e-data')
const stackScript = path.join(webRoot, 'scripts', 'e2e-stack.ps1')
const initialPasswordCopy = path.join(e2eDataDir, 'e2e-initial-password.txt')
const authStatePath = path.join(e2eDataDir, 'auth.json')
const baseURL = process.env.E2E_BASE_URL ?? 'http://127.0.0.1:18080'

function runPowerShell(args: string[]): void {
  const candidates = ['pwsh', 'powershell']
  let lastError: unknown = null
  for (const executable of candidates) {
    try {
      execFileSync(executable, ['-NoProfile', '-ExecutionPolicy', 'Bypass', '-File', stackScript, ...args], {
        cwd: webRoot,
        stdio: 'inherit',
        windowsHide: true,
        timeout: 240_000,
      })
      return
    } catch (error) {
      lastError = error
      const code = (error as { code?: string }).code
      if (code !== 'ENOENT') throw error
    }
  }
  throw lastError
}

async function waitForHttp(url: string, timeoutMs: number, accept: (status: number) => boolean): Promise<void> {
  const deadline = Date.now() + timeoutMs
  let lastError = ''
  while (Date.now() < deadline) {
    try {
      const response = await fetch(url)
      if (accept(response.status)) return
      lastError = 'HTTP ' + response.status
    } catch (error) {
      lastError = error instanceof Error ? error.message : String(error)
    }
    await new Promise((resolve) => setTimeout(resolve, 500))
  }
  throw new Error('Timed out waiting for ' + url + (lastError ? ' (' + lastError + ')' : ''))
}

/** Minimal cookie jar for the global-setup API login/change-password flow. */
class CookieJar {
  private cookies = new Map<string, string>()

  absorb(response: Response): void {
    const header = response.headers as Headers & { getSetCookie?: () => string[] }
    const raw = header.getSetCookie ? header.getSetCookie() : []
    const values = raw.length > 0 ? raw : (response.headers.get('set-cookie') ?? '').split(/,(?=[^;]+=)/)
    for (const value of values) {
      const pair = value.split(';')[0]
      const index = pair.indexOf('=')
      if (index > 0) this.cookies.set(pair.slice(0, index).trim(), pair.slice(index + 1).trim())
    }
  }

  header(): string {
    return Array.from(this.cookies.entries()).map(([name, value]) => name + '=' + value).join('; ')
  }

  storageCookies(): unknown[] {
    const httpOnly = new Set(['JSESSIONID'])
    return Array.from(this.cookies.entries()).map(([name, value]) => ({
      name,
      value,
      domain: '127.0.0.1',
      path: '/',
      expires: -1,
      httpOnly: httpOnly.has(name),
      secure: false,
      sameSite: 'Lax' as const,
    }))
  }
}

async function apiCall(jar: CookieJar, method: string, urlPath: string, body?: unknown, csrf?: string): Promise<Response> {
  const headers: Record<string, string> = { Accept: 'application/json' }
  if (body !== undefined) headers['Content-Type'] = 'application/json'
  if (jar.header()) headers.Cookie = jar.header()
  if (csrf) headers['X-XSRF-TOKEN'] = csrf
  const response = await fetch(baseURL + urlPath, {
    method,
    headers,
    body: body === undefined ? undefined : JSON.stringify(body),
    redirect: 'manual',
  })
  jar.absorb(response)
  return response
}

async function loginAndChangePassword(initialPassword: string): Promise<CookieJar> {
  const jar = new CookieJar()
  const login = await apiCall(jar, 'POST', '/api/auth/login', { password: initialPassword })
  if (login.status !== 204) {
    throw new Error('E2E login failed: HTTP ' + login.status + ' ' + (await login.text()).slice(0, 200))
  }
  const me = await apiCall(jar, 'GET', '/api/auth/me')
  const meBody = (await me.json()) as { csrfToken?: string; initialPasswordPending?: boolean }
  if (!meBody.csrfToken) throw new Error('E2E me response has no csrfToken')
  const changed = await apiCall(jar, 'PUT', '/api/auth/password', {
    oldPassword: initialPassword,
    newPassword: E2E_NEW_PASSWORD,
  }, meBody.csrfToken)
  if (changed.status !== 204) {
    throw new Error('E2E change password failed: HTTP ' + changed.status + ' ' + (await changed.text()).slice(0, 200))
  }
  const after = await apiCall(jar, 'GET', '/api/auth/me')
  const afterBody = (await after.json()) as { authenticated?: boolean; initialPasswordPending?: boolean }
  if (!afterBody.authenticated || afterBody.initialPasswordPending) {
    throw new Error('E2E session is not ready after password change: ' + JSON.stringify(afterBody))
  }
  return jar
}

async function loginWithNewPassword(): Promise<CookieJar> {
  const jar = new CookieJar()
  const login = await apiCall(jar, 'POST', '/api/auth/login', { password: E2E_NEW_PASSWORD })
  if (login.status !== 204) {
    throw new Error('E2E external-stack login failed with E2E_NEW_PASSWORD: HTTP ' + login.status)
  }
  return jar
}

function readInitialPassword(): string | null {
  const fromEnv = process.env.E2E_INITIAL_PASSWORD
  if (fromEnv) return fromEnv.trim()
  if (fs.existsSync(initialPasswordCopy)) return fs.readFileSync(initialPasswordCopy, 'utf8').trim()
  return null
}

export default async function globalSetup(): Promise<void> {
  fs.mkdirSync(e2eDataDir, { recursive: true })
  if (process.env.E2E_EXTERNAL_STACK === '1') {
    console.log('[e2e] E2E_EXTERNAL_STACK=1: reusing the external Go manager at ' + baseURL)
    await waitForHttp(baseURL + '/api/health', 30_000, (status) => status === 200)
  } else {
    if (!fs.existsSync(stackScript)) throw new Error('Missing E2E stack script: ' + stackScript)
    console.log('[e2e] starting disposable Go manager + built web/dist stack...')
    runPowerShell(['start'])
    await waitForHttp(baseURL + '/api/health', 120_000, (status) => status === 200)
    await waitForHttp(baseURL + '/', 60_000, (status) => status === 200)
  }

  const initialPassword = readInitialPassword()
  let jar: CookieJar
  if (initialPassword) {
    jar = await loginAndChangePassword(initialPassword)
  } else {
    console.log('[e2e] no initial password available; assuming the password was already changed to E2E_NEW_PASSWORD')
    jar = await loginWithNewPassword()
  }
  fs.writeFileSync(authStatePath, JSON.stringify({ cookies: jar.storageCookies(), origins: [] }, null, 2))
  console.log('[e2e] authenticated storage state written: ' + authStatePath)
}
