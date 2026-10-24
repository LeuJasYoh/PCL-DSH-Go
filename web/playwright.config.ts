import { defineConfig, devices } from '@playwright/test'

/**
 * Playwright E2E for the PCL-DSH-Linux web UI (plan §5.4 core behaviours).
 *
 * The stack (temporary-data backend jar + Vite dev server) is started and stopped
 * by e2e/global-setup.ts / e2e/global-teardown.ts via scripts/e2e-stack.ps1.
 * Set E2E_EXTERNAL_STACK=1 to use an already running stack (e.g. one started
 * manually with: pwsh -File scripts/e2e-stack.ps1 start).
 */
const authState = '.e2e-data/auth.json'

export default defineConfig({
  testDir: './e2e',
  timeout: 60_000,
  expect: { timeout: 10_000 },
  fullyParallel: false,
  workers: 1,
  retries: 0,
  forbidOnly: !!process.env.CI,
  reporter: [['list'], ['html', { open: 'never' }]],
  outputDir: 'test-results',
  globalSetup: './e2e/global-setup.ts',
  globalTeardown: './e2e/global-teardown.ts',
  use: {
    baseURL: 'http://127.0.0.1:5173',
    locale: 'zh-CN',
    viewport: { width: 1280, height: 800 },
    trace: 'retain-on-failure',
    screenshot: 'only-on-failure',
    video: 'off',
  },
  projects: [
    // 1) Authentication flows run first (no storage state) and write
    //    .e2e-data/auth.json for the dependent project.
    {
      name: 'auth',
      testMatch: /01-auth\.spec\.ts/,
      use: { ...devices['Desktop Chrome'] },
    },
    // 2) Everything else reuses the authenticated session.
    {
      name: 'chromium',
      testIgnore: /01-auth\.spec\.ts/,
      dependencies: ['auth'],
      use: {
        ...devices['Desktop Chrome'],
        storageState: authState,
      },
    },
  ],
})
