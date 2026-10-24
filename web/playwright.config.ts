import { defineConfig, devices } from '@playwright/test'

/**
 * M4 Playwright E2E: Windows Edge (channel: 'msedge') against the production-like
 * stack — Go manager (WSL) serving the built web/dist via --server.static-dir.
 *
 * e2e/global-setup.ts starts the stack through scripts/e2e-stack.ps1 (or reuses an
 * external one when E2E_EXTERNAL_STACK=1), performs the first-login password
 * change via the real API and writes .e2e-data/auth.json for dependent tests.
 */
const authState = '.e2e-data/auth.json'
const baseURL = process.env.E2E_BASE_URL ?? 'http://127.0.0.1:18080'
const edge = { ...devices['Desktop Chrome'], channel: 'msedge' as const }

export default defineConfig({
  testDir: './e2e',
  timeout: 90_000,
  expect: { timeout: 10_000 },
  fullyParallel: false,
  workers: 1,
  retries: process.env.CI ? 1 : 0,
  forbidOnly: !!process.env.CI,
  reporter: [['list'], ['html', { open: 'never' }]],
  outputDir: 'test-results',
  globalSetup: './e2e/global-setup.ts',
  globalTeardown: './e2e/global-teardown.ts',
  use: {
    baseURL,
    channel: 'msedge',
    locale: 'zh-CN',
    viewport: { width: 1280, height: 800 },
    trace: 'retain-on-failure',
    screenshot: 'only-on-failure',
    video: 'off',
  },
  projects: [
    {
      name: 'auth',
      testMatch: /01-auth\.spec\.ts/,
      use: edge,
    },
    {
      name: 'msedge',
      testIgnore: /01-auth\.spec\.ts/,
      dependencies: ['auth'],
      use: { ...edge, storageState: authState },
    },
  ],
})
