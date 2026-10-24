import { defineStore } from 'pinia'
import { computed, ref } from 'vue'

import { stateApi } from '@/api'
import { ApiError } from '@/api/client'
import { useAuthStore } from '@/stores/auth'
import type {
  AccountSummary,
  AppSettings,
  InstanceSummary,
  RuntimeSummary,
  StateResponse,
  StatusSnapshot,
} from '@/api/types'

const DEFAULT_SETTINGS: AppSettings = {
  proxy: '',
  registry: '',
  background: 'builtin:whale-01',
}

const POLL_IDLE_MS = 1500
const POLL_BUSY_MS = 3000
const POLL_RETRY_MS = 5000

export { POLL_IDLE_MS, POLL_BUSY_MS }

/**
 * Aggregated workspace snapshot from GET /api/state (plan 5.3):
 * page-first frame, then 1.5s polling that pauses while the tab is hidden.
 * Failures are surfaced truthfully; polling backs off to 5s and recovers.
 */
export const useAppStateStore = defineStore('appState', () => {
  const currentInstanceId = ref<string | null>(null)
  const settings = ref<AppSettings>({ ...DEFAULT_SETTINGS })
  const instances = ref<InstanceSummary[]>([])
  const runtimes = ref<RuntimeSummary[]>([])
  const accounts = ref<AccountSummary[]>([])
  const availableVersions = ref<string[]>([])
  const status = ref<StatusSnapshot>(null)
  const lastUpdatedAt = ref<number | null>(null)
  const loading = ref(false)
  const pollIntervalMs = ref(POLL_IDLE_MS)
  const error = ref<string | null>(null)
  const errorStatus = ref<number | null>(null)

  let timer: number | null = null
  let running = false
  let inFlight = false

  const currentInstance = computed<InstanceSummary | null>(() => {
    if (currentInstanceId.value === null) return null
    return instances.value.find((item) => item.id === currentInstanceId.value) ?? null
  })

  const statusText = computed(() => {
    const snapshot = status.value
    if (typeof snapshot === 'string' && snapshot) return snapshot
    if (snapshot && typeof snapshot === 'object') return snapshot.message ?? snapshot.code ?? ''
    return error.value ? '读取工作空间失败' : '就绪'
  })

  const taskActive = computed(() => statusText.value.startsWith('进行中：'))
  const taskPhase = computed(() => {
    const text = statusText.value
    return taskActive.value ? text.slice('进行中：'.length).trim() : ''
  })

  const backendState = computed<'unknown' | 'ok' | 'not-implemented' | 'error'>(() => {
    const code = errorStatus.value
    if (code === 404 || code === 501 || code === 502 || code === 503) return 'not-implemented'
    if (error.value) return 'error'
    if (lastUpdatedAt.value !== null) return 'ok'
    return 'unknown'
  })

  function apply(snapshot: StateResponse): void {
    currentInstanceId.value = snapshot.currentInstanceId
    settings.value = snapshot.settings
    instances.value = snapshot.instances
    runtimes.value = snapshot.runtimes
    accounts.value = snapshot.accounts
    availableVersions.value = snapshot.availableVersions
    status.value = snapshot.status
  }

  async function refresh(): Promise<void> {
    // Never poll protected endpoints while unauthenticated: the login page and the
    // post-401 redirect must not produce /api/state 401 noise.
    if (!useAuthStore().authenticated) return
    if (inFlight) return
    inFlight = true
    if (lastUpdatedAt.value === null) loading.value = true
    try {
      const snapshot = await stateApi.get()
      apply(snapshot)
      lastUpdatedAt.value = Date.now()
      error.value = null
      errorStatus.value = null
    } catch (caught) {
      error.value = caught instanceof ApiError ? caught.message : '读取工作空间失败。'
      errorStatus.value = caught instanceof ApiError ? caught.status : null
    } finally {
      loading.value = false
      inFlight = false
    }
  }

  function scheduleNext(delay: number): void {
    if (!running || document.hidden) return
    if (!useAuthStore().authenticated) {
      stopPolling()
      return
    }
    timer = window.setTimeout(async () => {
      timer = null
      await refresh()
      scheduleNext(error.value ? POLL_RETRY_MS : pollIntervalMs.value)
    }, delay)
  }

  function onVisibilityChange(): void {
    if (document.hidden) {
      if (timer !== null) {
        window.clearTimeout(timer)
        timer = null
      }
      return
    }
    if (!running || timer !== null) return
    void refresh().finally(() => scheduleNext(POLL_IDLE_MS))
  }

  function startPolling(): void {
    if (running) return
    if (!useAuthStore().authenticated) return
    running = true
    document.addEventListener('visibilitychange', onVisibilityChange)
    void refresh().finally(() => scheduleNext(POLL_IDLE_MS))
  }

  function stopPolling(): void {
    running = false
    document.removeEventListener('visibilitychange', onVisibilityChange)
    if (timer !== null) {
      window.clearTimeout(timer)
      timer = null
    }
  }

  /** Plan 5.3: 1.5s while idle, 3s while a background task is running. */
  function setPollInterval(ms: number): void {
    pollIntervalMs.value = Math.max(500, ms)
  }

  function setCurrentInstanceId(id: string | null): void {
    currentInstanceId.value = id
  }

  function replaceSettings(next: AppSettings): void {
    settings.value = next
  }

  /** Optimistic list replacement after account mutations (server refresh still follows). */
  function replaceAccounts(next: AccountSummary[]): void {
    accounts.value = next
  }

  return {
    currentInstanceId,
    settings,
    instances,
    runtimes,
    accounts,
    availableVersions,
    status,
    lastUpdatedAt,
    loading,
    pollIntervalMs,
    error,
    errorStatus,
    currentInstance,
    statusText,
    taskActive,
    taskPhase,
    backendState,
    apply,
    refresh,
    startPolling,
    stopPolling,
    setPollInterval,
    setCurrentInstanceId,
    replaceSettings,
    replaceAccounts,
  }
})
