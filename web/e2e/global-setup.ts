import { execFileSync } from 'node:child_process'
import fs from 'node:fs'
import path from 'node:path'

/**
 * Starts a disposable stack: backend jar with a temporary data directory
 * (pcl-dsh-web/.e2e-data) plus the Vite dev server. Playwright's globalTeardown
 * stops it again. Set E2E_EXTERNAL_STACK=1 to reuse an externally started stack
 * (pwsh -File scripts/e2e-stack.ps1 start).
 */
const webRoot = process.cwd()
const script = path.join(webRoot, 'scripts', 'e2e-stack.ps1')
const dataDir = path.join(webRoot, '.e2e-data')
const initialPasswordCopy = path.join(dataDir, 'e2e-initial-password.txt')

function runPowerShell(args: string[]): void {
  const candidates = ['pwsh', 'powershell']
  let lastError: unknown = null
  for (const executable of candidates) {
    try {
      execFileSync(executable, ['-NoProfile', '-ExecutionPolicy', 'Bypass', '-File', script, ...args], {
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

async function waitForHttp(url: string, timeoutMs: number): Promise<void> {
  const deadline = Date.now() + timeoutMs
  let lastError = ''
  while (Date.now() < deadline) {
    try {
      const response = await fetch(url)
      if (response.status < 500) return
      lastError = 'HTTP ' + response.status
    } catch (error) {
      lastError = error instanceof Error ? error.message : String(error)
    }
    await new Promise((resolve) => setTimeout(resolve, 500))
  }
  throw new Error('Timed out waiting for ' + url + (lastError ? ' (' + lastError + ')' : ''))
}

export default async function globalSetup(): Promise<void> {
  if (!fs.existsSync(script)) {
    throw new Error('Missing E2E stack script: ' + script)
  }
  if (process.env.E2E_EXTERNAL_STACK === '1') {
    console.log('[e2e] E2E_EXTERNAL_STACK=1: using the already running stack')
    await waitForHttp('http://127.0.0.1:8080/api/health', 15_000)
    await waitForHttp('http://127.0.0.1:5173/', 15_000)
  } else {
    console.log('[e2e] starting disposable stack (backend jar + Vite)...')
    runPowerShell(['start'])
  }
  if (!fs.existsSync(initialPasswordCopy)) {
    throw new Error(
      'Initial password file not found: ' + initialPasswordCopy +
      '. Start the stack with scripts/e2e-stack.ps1 start (fresh .e2e-data) or set E2E_INITIAL_PASSWORD.',
    )
  }
}
