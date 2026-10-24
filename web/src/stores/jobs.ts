import { defineStore } from 'pinia'
import { computed, ref } from 'vue'

import { jobsApi } from '@/api'
import { ApiError } from '@/api/client'
import { normalizeJob } from '@/api/normalize'
import type { JobLogEntry, JobState, JobView } from '@/api/types'
import { jobStateLabel, jobTypeLabel } from '@/utils/jobs'

const STORAGE_KEY = 'pcl-dsh-linux.currentJobId'
const MAX_LOG_LINES = 4000
/** After a terminal "state" event wait this long for the matching result/error event. */
const TERMINAL_GRACE_MS = 3000
const MAX_RECONNECT_ATTEMPTS = 10

/** UI-only context that cannot be derived from the Job View itself. */
export interface JobContext {
  instanceId?: string
  version?: string
}

function readStoredJobId(): string | null {
  try {
    return window.localStorage.getItem(STORAGE_KEY)
  } catch {
    return null
  }
}

function storeJobId(id: string | null): void {
  try {
    if (id) window.localStorage.setItem(STORAGE_KEY, id)
    else window.localStorage.removeItem(STORAGE_KEY)
  } catch {
    // Private mode / storage disabled: recovery after a refresh is best effort.
  }
}

function isTerminal(state: JobState): boolean {
  return state === 'SUCCEEDED' || state === 'FAILED' || state === 'CANCELLED'
}

/**
 * Global "current background task" store (plan 4.3 / 5.3).
 *
 * - One mutation task at a time; the backend returns a Job View (202) which is
 *   passed to {@link begin}. The store then follows /api/jobs/{id}/events.
 * - SSE uses the browser EventSource: its built-in reconnect sends the
 *   Last-Event-ID header, so a transient disconnect resumes after the last seq
 *   and the store de-duplicates by seq as a safety net.
 * - The job id is persisted so a page refresh can restore the subscription.
 */
export const useJobStore = defineStore('jobs', () => {
  const jobId = ref<string | null>(null)
  const type = ref('')
  const state = ref<JobState | ''>('')
  const phase = ref('')
  const message = ref('')
  const createdAt = ref<number | null>(null)
  const startedAt = ref<number | null>(null)
  const finishedAt = ref<number | null>(null)
  const result = ref<unknown>(null)
  const error = ref<{ code: string; message: string } | null>(null)
  const logs = ref<JobLogEntry[]>([])
  const connected = ref(false)
  const loading = ref(false)
  const connectionHint = ref('')
  const context = ref<JobContext>({})

  let source: EventSource | null = null
  let maxSeq = 0
  let terminalReceived = false
  let reconnectAttempts = 0
  let terminalTimer: number | null = null

  const running = computed(() => state.value === 'RUNNING')
  const active = computed(() => running.value)
  const finished = computed(() => isTerminal(state.value as JobState))
  const typeLabel = computed(() => jobTypeLabel(type.value))
  const stateLabel = computed(() => jobStateLabel(state.value))
  const elapsedBase = computed<number | null>(() => startedAt.value ?? createdAt.value)
  const progressText = computed(() => {
    if (phase.value) return phase.value
    if (message.value) return message.value
    return running.value ? '正在准备任务…' : ''
  })
  const logText = computed(() => logs.value.map((entry) => entry.line).join('\n'))
  const logTail = computed(() => logs.value.slice(-4).map((entry) => entry.line))
  const resultWebUrl = computed<string>(() => {
    const value = result.value
    if (value && typeof value === 'object' && 'webUrl' in value) {
      const url = (value as { webUrl?: unknown }).webUrl
      return typeof url === 'string' ? url : ''
    }
    return ''
  })

  function clearTerminalTimer(): void {
    if (terminalTimer !== null) {
      window.clearTimeout(terminalTimer)
      terminalTimer = null
    }
  }

  function closeSource(): void {
    clearTerminalTimer()
    if (source) {
      source.close()
      source = null
    }
    connected.value = false
  }

  function resetFields(): void {
    jobId.value = null
    type.value = ''
    state.value = ''
    phase.value = ''
    message.value = ''
    createdAt.value = null
    startedAt.value = null
    finishedAt.value = null
    result.value = null
    error.value = null
    logs.value = []
    connectionHint.value = ''
    context.value = {}
    maxSeq = 0
    terminalReceived = false
    loading.value = false
  }

  function applySnapshot(view: JobView): void {
    jobId.value = view.id
    type.value = view.type
    state.value = view.state
    if (view.phase) phase.value = view.phase
    if (view.message) message.value = view.message
    if (view.createdAt !== null) createdAt.value = view.createdAt
    if (view.startedAt !== null) startedAt.value = view.startedAt
    if (view.finishedAt !== null) finishedAt.value = view.finishedAt
    result.value = view.result
    error.value = view.error ? { code: view.error.code, message: view.error.message } : null
  }

  function appendLog(entry: JobLogEntry): void {
    if (entry.seq > 0) {
      if (entry.seq <= maxSeq) return
      maxSeq = entry.seq
    }
    logs.value.push(entry)
    if (logs.value.length > MAX_LOG_LINES) {
      logs.value.splice(0, logs.value.length - MAX_LOG_LINES)
    }
  }

  function finishFromTerminal(finalState: JobState): void {
    terminalReceived = true
    finishedAt.value = finishedAt.value ?? Date.now()
    state.value = finalState
    closeSource()
    storeJobId(null)
  }

  function scheduleTerminalClose(): void {
    if (terminalTimer !== null) return
    terminalTimer = window.setTimeout(() => {
      terminalTimer = null
      // Some terminal states (CANCELLED) only emit a state event; never hang.
      if (state.value && isTerminal(state.value as JobState)) {
        finishFromTerminal(state.value as JobState)
      }
    }, TERMINAL_GRACE_MS)
  }

  function parseEvent<T>(event: MessageEvent): T | null {
    try {
      return JSON.parse(event.data) as T
    } catch {
      return null
    }
  }

  function handleStateEvent(event: MessageEvent): void {
    const payload = parseEvent<{ state?: string; phase?: string; message?: string; ts?: number }>(event)
    if (!payload) return
    if (typeof payload.phase === 'string') phase.value = payload.phase
    if (typeof payload.message === 'string') message.value = payload.message
    const next = typeof payload.state === 'string' ? (payload.state.toUpperCase() as JobState) : null
    if (!next) return
    state.value = next
    if (startedAt.value === null && next === 'RUNNING') startedAt.value = Date.now()
    if (next === 'CANCELLED') {
      finishFromTerminal('CANCELLED')
      error.value = null
    } else if (next === 'FAILED') {
      scheduleTerminalClose()
    } else if (next === 'SUCCEEDED') {
      scheduleTerminalClose()
    }
  }

  function handleLogEvent(event: MessageEvent): void {
    const payload = parseEvent<{ jobId?: string; seq?: number; ts?: number; level?: string; line?: string }>(event)
    if (!payload || typeof payload.line !== 'string') return
    appendLog({
      jobId: typeof payload.jobId === 'string' ? payload.jobId : (jobId.value ?? ''),
      seq: typeof payload.seq === 'number' ? payload.seq : 0,
      ts: typeof payload.ts === 'number' ? payload.ts : Date.now(),
      level: typeof payload.level === 'string' ? payload.level : 'info',
      line: payload.line,
    })
  }

  function handleResultEvent(event: MessageEvent): void {
    const payload = parseEvent<{ state?: string; result?: unknown }>(event)
    if (payload) {
      result.value = payload.result ?? null
      const next = typeof payload.state === 'string' ? (payload.state.toUpperCase() as JobState) : 'SUCCEEDED'
      phase.value = '完成'
      finishFromTerminal(next)
    } else {
      finishFromTerminal('SUCCEEDED')
    }
  }

  function handleServerErrorEvent(event: MessageEvent): void {
    const payload = parseEvent<{ state?: string; code?: string; message?: string }>(event)
    if (payload) {
      error.value = {
        code: typeof payload.code === 'string' && payload.code ? payload.code : 'INTERNAL',
        message: typeof payload.message === 'string' && payload.message ? payload.message : '任务未完成。',
      }
      phase.value = '未完成'
      message.value = error.value.message
      finishFromTerminal((payload.state?.toUpperCase() as JobState) || 'FAILED')
    } else {
      finishFromTerminal('FAILED')
    }
  }

  function onConnectionError(): void {
    connected.value = false
    loading.value = false
    if (terminalReceived) {
      closeSource()
      return
    }
    // The backend closes the stream after a terminal event. Until the terminal
    // event arrives, rely on EventSource auto-reconnect (with Last-Event-ID).
    if (state.value && isTerminal(state.value as JobState)) {
      scheduleTerminalClose()
      return
    }
    reconnectAttempts += 1
    if (reconnectAttempts > MAX_RECONNECT_ATTEMPTS) {
      closeSource()
      connectionHint.value = '任务连接已断开；可点击“重新连接”，或稍后在任务页查看结果。'
      loading.value = false
      return
    }
    connectionHint.value = '连接中断，正在自动重连…'
  }

  function subscribe(id: string): void {
    closeSource()
    terminalReceived = false
    reconnectAttempts = 0
    connectionHint.value = ''
    loading.value = true
    let next: EventSource
    try {
      next = new EventSource(jobsApi.eventsPath(id))
    } catch {
      connectionHint.value = '浏览器无法建立任务事件连接。'
      loading.value = false
      return
    }
    source = next
    next.onopen = () => {
      connected.value = true
      loading.value = false
      reconnectAttempts = 0
      connectionHint.value = ''
    }
    next.addEventListener('state', (event) => handleStateEvent(event as MessageEvent))
    next.addEventListener('log', (event) => handleLogEvent(event as MessageEvent))
    next.addEventListener('result', (event) => handleResultEvent(event as MessageEvent))
    next.addEventListener('error', (event) => {
      const message = event as MessageEvent
      // Server-sent "event: error" carries JSON data; a transport error does not.
      if (typeof message.data === 'string' && message.data !== '') handleServerErrorEvent(message)
      else onConnectionError()
    })
  }

  /** Start following a job returned by a 202 response. */
  function begin(view: JobView, jobContext: JobContext = {}): void {
    resetFields()
    context.value = jobContext
    applySnapshot(view)
    storeJobId(view.id)
    subscribe(view.id)
  }

  /** Restore the subscription after a page refresh (best effort). */
  async function restore(): Promise<void> {
    const stored = readStoredJobId()
    if (!stored || jobId.value) return
    loading.value = true
    try {
      const view = await jobsApi.get(stored)
      resetFields()
      applySnapshot(view)
      if (view.state === 'RUNNING') storeJobId(view.id)
      // A finished job is replayed once so the log survives the refresh, then closed.
      subscribe(view.id)
    } catch (caught) {
      if (caught instanceof ApiError && caught.status === 404) storeJobId(null)
      else connectionHint.value = '无法恢复上次任务；可稍后在任务页重新查看。'
      loading.value = false
    }
  }

  async function refresh(): Promise<JobView | null> {
    if (!jobId.value) return null
    const raw = await jobsApi.get(jobId.value)
    if (!raw) return null
    if (!logs.value.length) applySnapshot(raw)
    return raw
  }

  async function cancel(): Promise<void> {
    if (!jobId.value || !running.value) return
    await jobsApi.cancel(jobId.value)
    phase.value = '取消中'
    message.value = '已请求取消当前任务，正在收尾。'
  }

  /** Manual reconnect after a persistent transport failure. */
  async function reconnect(): Promise<void> {
    if (!jobId.value) return
    closeSource()
    try {
      const view = await jobsApi.get(jobId.value)
      applySnapshot(normalizeJob(view) ?? view)
    } catch {
      // Keep the known snapshot; EventSource replay may still recover.
    }
    subscribe(jobId.value)
  }

  function reset(): void {
    closeSource()
    const previous = jobId.value
    resetFields()
    if (previous) storeJobId(null)
  }

  return {
    jobId,
    type,
    state,
    phase,
    message,
    createdAt,
    startedAt,
    finishedAt,
    result,
    error,
    logs,
    connected,
    loading,
    connectionHint,
    context,
    running,
    active,
    finished,
    typeLabel,
    stateLabel,
    elapsedBase,
    progressText,
    logText,
    logTail,
    resultWebUrl,
    begin,
    restore,
    refresh,
    cancel,
    reconnect,
    reset,
  }
})
