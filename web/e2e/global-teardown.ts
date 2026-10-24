import { execFileSync } from 'node:child_process'
import path from 'node:path'

/** Stop the disposable backend + Vite stack started by global-setup.ts. */
export default async function globalTeardown(): Promise<void> {
  if (process.env.E2E_EXTERNAL_STACK === '1') {
    console.log('[e2e] E2E_EXTERNAL_STACK=1: leaving the external stack running')
    return
  }
  const script = path.join(process.cwd(), 'scripts', 'e2e-stack.ps1')
  const candidates = ['pwsh', 'powershell']
  let lastError: unknown = null
  for (const executable of candidates) {
    try {
      execFileSync(executable, ['-NoProfile', '-ExecutionPolicy', 'Bypass', '-File', script, 'stop'], {
        cwd: process.cwd(),
        stdio: 'inherit',
        windowsHide: true,
        timeout: 120_000,
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
