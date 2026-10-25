import type {
  AppSettings,
  ArtifactRef,
  AvailableRuntimes,
  DeleteInstanceResult,
  DirectAccess,
  ExportPackJobResult,
  ImportPackJobResult,
  InstallRuntimeJobResult,
  InstanceSummary,
  JobError,
  JobLogEntry,
  JobState,
  JobView,
  LaunchJobResult,
  LogTail,
  PackPreviewDto,
  PluginMutateJobResult,
  ProbeRuntimeJobResult,
  RuntimeSummary,
  StateResponse,
  StatusSnapshot,
  WebUrl,
} from './types'

export function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === 'object' && value !== null
}

function asString(value: unknown, fallback = ''): string {
  if (typeof value === 'string') return value
  if (typeof value === 'number' || typeof value === 'boolean') return String(value)
  return fallback
}

function asNullableString(value: unknown): string | null {
  const text = asString(value)
  return text ? text : null
}

function asBoolean(value: unknown, fallback = false): boolean {
  return typeof value === 'boolean' ? value : fallback
}

function asNumberOrNull(value: unknown): number | null {
  if (typeof value === 'number' && Number.isFinite(value)) return value
  if (typeof value === 'string' && value.trim() !== '') {
    const parsed = Number(value)
    return Number.isFinite(parsed) ? parsed : null
  }
  return null
}

function asArray(value: unknown): unknown[] {
  return Array.isArray(value) ? value : []
}

function unwrapRecord(raw: unknown, key: string): Record<string, unknown> {
  if (!isRecord(raw)) return {}
  const nested = raw[key]
  return isRecord(nested) ? nested : raw
}

/** GET/PUT /api/settings: {proxy,registry,directAccessSshUser}. */
export function normalizeSettings(raw: unknown): AppSettings {
  const record = isRecord(raw) ? (isRecord(raw.settings) ? raw.settings : raw) : {}
  return {
    proxy: asString(record.proxy),
    registry: asString(record.registry),
    directAccessSshUser: asString(record.directAccessSshUser) || 'root',
  }
}

/** GET /api/instances/{id} and the entries of GET /api/state (InstanceDto). */
export function normalizeInstance(raw: unknown): InstanceSummary | null {
  if (!isRecord(raw)) return null
  const item = isRecord(raw.instance) ? raw.instance : raw
  const id = asString(item.id)
  if (!id) return null
  const profile = asString(item.profile) || 'web'
  const profiles = asArray(item.profiles)
    .map((value) => asString(value))
    .filter(Boolean)
  return {
    id,
    name: asString(item.name) || id,
    version: asString(item.version) || asString(item.runtimeVersion),
    profile,
    port: asNumberOrNull(item.port) ?? 0,
    status: asString(item.status),
    path: asNullableString(item.path),
    profiles: profiles.length ? profiles : [profile],
  }
}

export function normalizeInstances(raw: unknown): InstanceSummary[] {
  const record = isRecord(raw) ? raw : {}
  const list = Array.isArray(raw) ? raw : asArray(record.instances ?? record.items)
  return list
    .map((item) => normalizeInstance(item))
    .filter((item): item is InstanceSummary => item !== null)
}

export function normalizeRuntimes(raw: unknown): RuntimeSummary[] {
  const record = isRecord(raw) ? raw : {}
  const list = Array.isArray(raw) ? raw : asArray(record.runtimes ?? record.items)
  return list
    .filter(isRecord)
    .map((item) => ({
      version: asString(item.version),
      installed: asBoolean(item.installed, true),
      compatibility: asString(item.compatibility ?? item.compatibilityMessage),
    }))
    .filter((item) => item.version !== '')
}

function normalizeStatus(raw: unknown): StatusSnapshot {
  if (isRecord(raw)) {
    return {
      code: asString(raw.code) || 'READY',
      message: asString(raw.message) || '就绪',
      phase: asString(raw.phase),
    }
  }
  if (typeof raw === 'string' && raw) return { code: 'READY', message: raw, phase: '' }
  return { code: 'READY', message: '就绪', phase: '' }
}

/** GET /api/state: {version,currentInstanceId,instances,runtimes,status,settings,availableVersions,warnings}. */
export function normalizeState(raw: unknown): StateResponse {
  const record = unwrapRecord(raw, 'state')
  return {
    version: asString(record.version),
    currentInstanceId: asNullableString(record.currentInstanceId),
    instances: normalizeInstances(record.instances),
    runtimes: normalizeRuntimes(record.runtimes),
    status: normalizeStatus(record.status),
    settings: normalizeSettings(record.settings),
    availableVersions: asArray(record.availableVersions)
      .map((value) => asString(value))
      .filter(Boolean),
    warnings: asArray(record.warnings)
      .map((value) => asString(value))
      .filter(Boolean),
  }
}

export function normalizeAvailableVersions(raw: unknown): AvailableRuntimes {
  const record = isRecord(raw) ? raw : {}
  const list = Array.isArray(raw)
    ? raw
    : asArray(record.versions ?? record.availableVersions ?? record.items)
  const versions = list
    .map((value) => (typeof value === 'string' ? value : isRecord(value) ? asString(value.version) : ''))
    .filter((value) => value !== '')
  return { versions }
}

/** GET /api/instances/{id}/web-url returns {url}. */
export function normalizeWebUrl(raw: unknown): WebUrl {
  if (typeof raw === 'string') return { url: raw }
  const record = isRecord(raw) ? raw : {}
  return { url: asString(record.url ?? record.webUrl) }
}

/**
 * GET /api/instances/{id}/direct-access: missing fields degrade to "not ready"
 * so the UI never shows an unusable direct URL.
 */
export function normalizeDirectAccess(raw: unknown): DirectAccess {
  const record = isRecord(raw) ? raw : {}
  const directUrl = asString(record.directUrl)
  const portValue = typeof record.port === 'number' ? record.port : Number(asString(record.port))
  return {
    ready: record.ready === true && directUrl !== '',
    port: Number.isFinite(portValue) ? portValue : 0,
    directUrl: directUrl === '' ? null : directUrl,
    tunnelCommand: asString(record.tunnelCommand),
    sshHost: asString(record.sshHost),
    sshUser: asString(record.sshUser) || 'root',
    note: asString(record.note),
  }
}

export function normalizeDeleteResult(raw: unknown): DeleteInstanceResult {
  const record = isRecord(raw) ? raw : {}
  return { trashedPath: asString(record.trashedPath ?? record.path) }
}

/** GET /api/logs/tail?lines=N: {lines:string[]}. */
export function normalizeLogTail(raw: unknown): LogTail {
  const record = isRecord(raw) ? raw : {}
  const list = Array.isArray(raw) ? raw : asArray(record.lines ?? record.entries)
  const lines = list
    .map((entry) => {
      if (typeof entry === 'string') return entry
      if (isRecord(entry)) return asString(entry.line ?? entry.text ?? entry.message)
      return ''
    })
    .filter((line) => line !== '')
  return { lines }
}

function normalizeJobState(raw: unknown): JobState {
  const text = asString(raw).toUpperCase()
  if (text === 'SUCCEEDED' || text === 'FAILED' || text === 'CANCELLED' || text === 'RUNNING') return text
  // Unknown/newer states stay visible rather than silently becoming RUNNING.
  return text === '' ? 'RUNNING' : (text as JobState)
}

function normalizeJobError(raw: unknown): JobError | null {
  if (!isRecord(raw)) return null
  const code = asString(raw.code)
  const message = asString(raw.message)
  if (!code && !message) return null
  return { code: code || 'INTERNAL', message: message || '任务未完成。', details: raw.details }
}

/** Normalize a JobManager.View (body of a 202 and of GET /api/jobs/{id}). */
export function normalizeJob(raw: unknown): JobView | null {
  if (!isRecord(raw)) return null
  const id = asString(raw.id ?? raw.jobId)
  if (!id) return null
  return {
    id,
    type: asString(raw.type),
    state: normalizeJobState(raw.state),
    phase: asString(raw.phase),
    message: asString(raw.message),
    createdAt: asNumberOrNull(raw.createdAt),
    startedAt: asNumberOrNull(raw.startedAt),
    finishedAt: asNumberOrNull(raw.finishedAt),
    lastSeq: asNumberOrNull(raw.lastSeq) ?? 0,
    result: raw.result ?? null,
    error: normalizeJobError(raw.error),
  }
}

/** 202 responses are a Job View; keep tolerating a compact {jobId} envelope. */
export function normalizeJobAccepted(raw: unknown): JobView | null {
  const job = normalizeJob(raw)
  if (job) return job
  if (!isRecord(raw)) return null
  const id = asString(raw.jobId ?? raw.id)
  if (!id) return null
  return {
    id,
    type: asString(raw.type),
    state: normalizeJobState(raw.state),
    phase: asString(raw.phase),
    message: asString(raw.message),
    createdAt: null,
    startedAt: null,
    finishedAt: null,
    lastSeq: 0,
    result: null,
    error: null,
  }
}

export function normalizeJobList(raw: unknown): JobView[] {
  const list = Array.isArray(raw) ? raw : isRecord(raw) ? asArray(raw.jobs ?? raw.items) : []
  return list.map((item) => normalizeJob(item)).filter((item): item is JobView => item !== null)
}

export function normalizeJobLogEntry(raw: unknown): JobLogEntry | null {
  if (!isRecord(raw)) return null
  const line = asString(raw.line ?? raw.message)
  if (line === '') return null
  return {
    jobId: asString(raw.jobId),
    seq: asNumberOrNull(raw.seq) ?? 0,
    ts: asNumberOrNull(raw.ts) ?? Date.now(),
    level: asString(raw.level) || 'info',
    line,
  }
}

/* ------------------------------------------------------------------ */
/* M3: installed plugins, packs, artifacts, typed job results          */
/* ------------------------------------------------------------------ */

/** GET /api/instances/{id}/plugins?profile= item (InstalledPluginDto). */
export function normalizePlugins(raw: unknown): import('./types').InstalledPluginDto[] {
  const record = isRecord(raw) ? raw : {}
  const list = Array.isArray(raw) ? raw : asArray(record.plugins ?? record.items)
  return list
    .filter(isRecord)
    .map((item) => ({
      name: asString(item.name ?? item.id ?? item.spec),
      version: asString(item.version),
      description: asString(item.description ?? item.detail),
      source: asString(item.source),
    }))
    .filter((item) => item.name !== '')
}

/** POST /api/packs/preview response (PackPreviewDto). */
export function normalizePackPreview(raw: unknown): PackPreviewDto | null {
  if (!isRecord(raw)) return null
  const item = isRecord(raw.preview) ? raw.preview : raw
  const previewId = asString(item.previewId ?? item.id)
  if (!previewId) return null
  return {
    previewId,
    name: asString(item.name),
    format: asString(item.format),
    version: asString(item.version),
    dshVersion: asString(item.dshVersion),
    profiles: asArray(item.profiles)
      .map((value) => asString(value))
      .filter(Boolean),
    unpackedBytes: asNumberOrNull(item.unpackedBytes ?? item.size) ?? 0,
    warnings: asArray(item.warnings)
      .map((value) => asString(value))
      .filter(Boolean),
  }
}

/** EXPORT_PACK Job result → artifact reference (null when incomplete). */
export function normalizeArtifactRef(raw: unknown): ArtifactRef | null {
  if (!isRecord(raw)) return null
  const artifactId = asString(raw.artifactId ?? raw.id)
  if (!artifactId) return null
  return {
    artifactId,
    filename: asString(raw.filename ?? raw.name),
    size: asNumberOrNull(raw.size) ?? 0,
  }
}

/** LAUNCH_INSTANCE Job result {instanceId,webUrl}. */
export function normalizeLaunchJobResult(raw: unknown): LaunchJobResult | null {
  if (!isRecord(raw)) return null
  const instanceId = asString(raw.instanceId ?? raw.id)
  if (!instanceId) return null
  return { instanceId, webUrl: asString(raw.webUrl) }
}

/** INSTALL_RUNTIME Job result {version,installed,compatibility}. */
export function normalizeInstallRuntimeJobResult(raw: unknown): InstallRuntimeJobResult | null {
  if (!isRecord(raw)) return null
  const version = asString(raw.version)
  if (!version) return null
  return {
    version,
    installed: asBoolean(raw.installed, true),
    compatibility: asString(raw.compatibility),
  }
}

/** PROBE_RUNTIME Job result {version,compatibility}. */
export function normalizeProbeRuntimeJobResult(raw: unknown): ProbeRuntimeJobResult | null {
  if (!isRecord(raw)) return null
  const version = asString(raw.version)
  if (!version) return null
  return { version, compatibility: asString(raw.compatibility) }
}

/** IMPORT_PACK Job result {instanceId,name}. */
export function normalizeImportPackJobResult(raw: unknown): ImportPackJobResult | null {
  if (!isRecord(raw)) return null
  const instanceId = asString(raw.instanceId)
  if (!instanceId) return null
  return { instanceId, name: asString(raw.name) }
}

/** EXPORT_PACK Job result {artifactId,filename,size}. */
export function normalizeExportPackJobResult(raw: unknown): ExportPackJobResult | null {
  const artifact = normalizeArtifactRef(raw)
  if (!artifact) return null
  return artifact
}

/** PLUGIN_MUTATE Job result {instanceId,profile,action,name}. */
export function normalizePluginMutateJobResult(raw: unknown): PluginMutateJobResult | null {
  if (!isRecord(raw)) return null
  const instanceId = asString(raw.instanceId)
  if (!instanceId) return null
  return {
    instanceId,
    profile: asString(raw.profile),
    action: asString(raw.action),
    name: asString(raw.name),
  }
}
