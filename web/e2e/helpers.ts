import fs from 'node:fs'
import path from 'node:path'

import type { APIRequestContext, Page } from '@playwright/test'

/** Playwright runs with cwd = pcl-dsh-web (config directory). */
export const webRoot = process.cwd()
export const e2eDataDir = path.join(webRoot, '.e2e-data')
export const authStatePath = path.join(e2eDataDir, 'auth.json')
export const initialPasswordCopy = path.join(e2eDataDir, 'e2e-initial-password.txt')

/** Initial admin password created by the fresh disposable backend. */
export function readInitialPassword(): string {
  const fromEnv = process.env.E2E_INITIAL_PASSWORD
  if (fromEnv) return fromEnv.trim()
  if (!fs.existsSync(initialPasswordCopy)) {
    throw new Error(
      'Initial password file not found: ' + initialPasswordCopy +
      '. Start the E2E stack with scripts/e2e-stack.ps1 start first.',
    )
  }
  return fs.readFileSync(initialPasswordCopy, 'utf8').trim()
}

/** CSRF double-submit header for APIRequestContext writes. */
export async function csrfHeaders(request: APIRequestContext): Promise<Record<string, string>> {
  const state = await request.storageState()
  const token = state.cookies.find((cookie) => cookie.name === 'XSRF-TOKEN')?.value
  return token ? { 'X-XSRF-TOKEN': token } : {}
}

export interface SeededInstance {
  id: string
  name: string
}

/** Create an instance through the real backend API and select it. */
export async function seedInstance(
  request: APIRequestContext,
  name: string,
  overrides: Record<string, unknown> = {},
): Promise<SeededInstance> {
  const headers = await csrfHeaders(request)
  const response = await request.post('/api/instances', {
    data: { name, version: '0.2.0-rc.2', profile: 'web', ...overrides },
    headers,
  })
  if (!response.ok()) {
    throw new Error('seedInstance failed: HTTP ' + response.status() + ' ' + (await response.text()))
  }
  const body = (await response.json()) as { id: string; name: string }
  await request.put('/api/instances/' + encodeURIComponent(body.id) + '/selection', { headers })
  return { id: body.id, name: body.name }
}

export async function deleteInstance(request: APIRequestContext, id: string): Promise<void> {
  const headers = await csrfHeaders(request)
  await request.delete('/api/instances/' + encodeURIComponent(id), { headers }).catch(() => undefined)
}

/** A JobManager.View body used by mocked 202 responses / GET /api/jobs/{id}. */
export function jobView(
  id: string,
  type: string,
  overrides: Record<string, unknown> = {},
): Record<string, unknown> {
  const now = Date.now()
  return {
    id,
    type,
    state: 'RUNNING',
    phase: '开始',
    message: '任务已提交。',
    createdAt: now,
    startedAt: now,
    finishedAt: null,
    lastSeq: 1,
    result: null,
    error: null,
    ...overrides,
  }
}

export interface SseEvent {
  event: string
  data: Record<string, unknown>
}

export function sseBody(jobId: string, events: SseEvent[]): string {
  return events
    .map((entry, index) => {
      const seq = index + 1
      const data = { jobId, seq, ts: Date.now(), ...entry.data }
      return 'id: ' + seq + '\nevent: ' + entry.event + '\ndata: ' + JSON.stringify(data) + '\n\n'
    })
    .join('')
}

/** Mock the per-job SSE stream; fulfils all events at once and then closes. */
export async function mockJobStream(page: Page, jobId: string, events: SseEvent[]): Promise<void> {
  const body = sseBody(jobId, events)
  await page.route('**/api/jobs/' + jobId + '/events', (route) =>
    route.fulfill({
      status: 200,
      headers: { 'Content-Type': 'text/event-stream; charset=utf-8', 'Cache-Control': 'no-cache' },
      body,
    }),
  )
}

/** Fulfil matching write requests with a 202 Job View (path/pattern based mocks). */
export async function mockAcceptedJob(
  page: Page,
  pattern: string | RegExp,
  job: Record<string, unknown>,
): Promise<void> {
  await page.route(pattern, async (route) => {
    const method = route.request().method()
    if (method !== 'POST' && method !== 'PUT' && method !== 'PATCH') {
      await route.fallback()
      return
    }
    await route.fulfill({
      status: 202,
      contentType: 'application/json',
      body: JSON.stringify(job),
    })
  })
}

export async function mockJson(
  page: Page,
  pattern: string | RegExp,
  body: unknown,
  status = 200,
): Promise<void> {
  await page.route(pattern, (route) =>
    route.fulfill({ status, contentType: 'application/json', body: JSON.stringify(body) }),
  )
}
