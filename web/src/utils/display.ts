import type { InstanceSummary, StatusSnapshot } from '@/api/types'

export const APP_VERSION = '0.3.2'

const TASK_PREFIX = '进行中：'

export type InstanceStatusKind = 'running' | 'starting' | 'ready' | 'error' | 'unknown'

export function statusToText(status: StatusSnapshot): string {
  if (typeof status === 'string') return status
  if (status && typeof status === 'object') return status.message ?? status.code ?? ''
  return ''
}

export function shortStatus(value: string | null | undefined, max = 160): string {
  if (!value) return ''
  const first = value.replace(/[\r\n]+/g, ' ').trim()
  return first.length > max ? first.slice(0, max - 3) + '…' : first
}

export function isTaskStatus(status: StatusSnapshot): boolean {
  return statusToText(status).startsWith(TASK_PREFIX)
}

export function taskPhase(status: StatusSnapshot): string {
  const text = statusToText(status)
  if (!text.startsWith(TASK_PREFIX)) return ''
  return text.slice(TASK_PREFIX.length).trim()
}

export function isRunningStatus(status: string): boolean {
  const value = (status || '').toLowerCase()
  return [
    'running',
    'starting',
    '运行中',
    '已启动',
    '启动中',
    '进程运行中，服务未就绪',
    '子进程仍在运行',
  ].includes(value)
}

export function instanceStatusKind(status: string): InstanceStatusKind {
  const value = (status || '').toLowerCase()
  if (value === 'running' || value === '运行中' || value === '已启动') return 'running'
  if (value === 'starting' || value === '启动中' || value === '进程运行中，服务未就绪' || value === '子进程仍在运行') return 'starting'
  if (value === 'error' || value === 'failed') return 'error'
  if (value === '' || value === 'stopped' || value === 'ready' || value === 'idle' || value === '就绪') return 'ready'
  return 'unknown'
}

export function displayInstanceStatus(status: string): string {
  const kind = instanceStatusKind(status)
  if (kind === 'running') return '运行中'
  if (kind === 'starting') return '启动中'
  if (kind === 'ready') return '就绪'
  if (kind === 'error') return '需要检查'
  return status || '就绪'
}

export function displayPort(instance: InstanceSummary): string {
  return instance.port === null ? '自动分配' : String(instance.port)
}

export function elapsedText(startedAt: number | null, now: number): string {
  const seconds = startedAt === null ? 0 : Math.max(0, Math.floor((now - startedAt) / 1000))
  const hours = Math.floor(seconds / 3600)
  const minutes = Math.floor((seconds % 3600) / 60)
  const secs = seconds % 60
  const pad = (value: number) => String(value).padStart(2, '0')
  const body = hours > 0 ? pad(hours) + ':' + pad(minutes) + ':' + pad(secs) : pad(minutes) + ':' + pad(secs)
  return '已用时 ' + body
}

/** Human-readable byte size, e.g. "12.3 MiB" (binary units, as in the desktop pack preview). */
export function formatBytes(bytes: number): string {
  const value = Number.isFinite(bytes) ? Math.max(0, bytes) : 0
  if (value < 1024) return String(Math.round(value)) + ' B'
  const kib = value / 1024
  if (kib < 1024) return kib.toFixed(1) + ' KiB'
  const mib = kib / 1024
  if (mib < 1024) return mib.toFixed(1) + ' MiB'
  return (mib / 1024).toFixed(2) + ' GiB'
}

/** Only allow same-site absolute paths as post-login redirect targets. */
export function safeRedirect(value: unknown): string | null {
  if (typeof value !== 'string') return null
  if (!value.startsWith('/') || value.startsWith('//')) return null
  return value
}
