import type {
  AccountSummary,
  AppSettings,
  ArtifactRef,
  AvailableRuntimes,
  BackgroundAsset,
  CatalogItem,
  CatalogPage,
  DeleteInstanceResult,
  DirectAccess,
  InstallTarget,
  InstanceSummary,
  JobError,
  JobLogEntry,
  JobState,
  JobView,
  LogTail,
  PackPreview,
  PluginDetails,
  PluginEntry,
  ProviderPreset,
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

function asStatusText(value: unknown): string {
  if (typeof value === 'string') return value
  if (isRecord(value)) {
    const message = asString(value.message)
    if (message) return message
    return asString(value.code)
  }
  return ''
}

function unwrapRecord(raw: unknown, key: string): Record<string, unknown> {
  if (!isRecord(raw)) return {}
  const nested = raw[key]
  return isRecord(nested) ? nested : raw
}

export function normalizeSettings(raw: unknown): AppSettings {
  const record = isRecord(raw) ? (isRecord(raw.settings) ? raw.settings : raw) : {}
  return {
    proxy: asString(record.proxy),
    registry: asString(record.registry),
    // The desktop default is builtin:whale-01; an absent value falls back to it.
    background: asString(record.background) || 'builtin:whale-01',
  }
}

export function normalizeInstance(raw: unknown): InstanceSummary | null {
  if (!isRecord(raw)) return null
  const item = isRecord(raw.instance) ? raw.instance : raw
  const id = asString(item.id)
  if (!id) return null
  return {
    id,
    name: asString(item.name) || id,
    version: asString(item.version) || asString(item.runtimeVersion),
    profile: asString(item.profile) || 'web',
    port: asNumberOrNull(item.port),
    profiles: asArray(item.profiles).map((value) => asString(value)).filter(Boolean),
    accountId: asNullableString(item.accountId),
    path: asNullableString(item.path),
    status: asStatusText(item.status),
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
  return list.filter(isRecord).map((item) => ({
    version: asString(item.version),
    installed: asBoolean(item.installed, true),
    compatibility: asString(item.compatibility ?? item.compatibilityMessage),
  })).filter((item) => item.version !== '')
}

export function normalizeAccounts(raw: unknown): AccountSummary[] {
  const record = isRecord(raw) ? raw : {}
  const list = Array.isArray(raw) ? raw : asArray(record.accounts ?? record.items)
  return list.filter(isRecord).map((item, index) => ({
    id: asString(item.id) || 'account-' + String(index + 1),
    name: asString(item.name),
    provider: asString(item.provider),
    baseUrl: asString(item.baseUrl) || undefined,
    model: asString(item.model) || undefined,
    hasKey: typeof item.hasKey === 'boolean' ? item.hasKey : undefined,
    status: asString(item.status) || undefined,
  }))
}

function normalizeStatus(raw: unknown): StatusSnapshot {
  if (typeof raw === 'string') return raw
  if (isRecord(raw)) {
    return { code: asString(raw.code) || undefined, message: asString(raw.message) || undefined }
  }
  return null
}

export function normalizeState(raw: unknown): StateResponse {
  const record = unwrapRecord(raw, 'state')
  return {
    currentInstanceId: asNullableString(record.currentInstanceId),
    settings: normalizeSettings(record.settings),
    instances: normalizeInstances(record.instances),
    runtimes: normalizeRuntimes(record.runtimes),
    accounts: normalizeAccounts(record.accounts),
    availableVersions: asArray(record.availableVersions).map((value) => asString(value)).filter(Boolean),
    status: normalizeStatus(record.status),
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

/** GET /api/instances/{id}/web-url returns {url}; a bare string is accepted as a fallback. */
export function normalizeWebUrl(raw: unknown): WebUrl {
  if (typeof raw === 'string') return { url: raw }
  const record = isRecord(raw) ? raw : {}
  return { url: asString(record.url ?? record.webUrl) }
}

/**
 * GET /api/instances/{id}/direct-access：缺失字段按"未就绪"处理，
 * 避免界面展示无法使用的直连地址。
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

/** GET /api/logs/tail lines may be plain strings or {line|text, ts, level} records. */
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

/** Normalize a JobManager.View (the body of a 202 and of GET /api/jobs/{id}). */
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

/** 202 responses are a Job View today; keep tolerating the compact {jobId} envelope. */
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
/* Third increment: accounts / plugins / catalog / assets             */
/* ------------------------------------------------------------------ */

export function normalizeProvider(raw: unknown): ProviderPreset | null {
  if (!isRecord(raw)) return null
  const id = asString(raw.id)
  if (!id) return null
  return {
    id,
    name: asString(raw.name) || id,
    baseUrl: asString(raw.baseUrl ?? raw.baseURL),
    model: asString(raw.model),
  }
}

export function normalizeProviders(raw: unknown): ProviderPreset[] {
  const record = isRecord(raw) ? raw : {}
  const list = Array.isArray(raw) ? raw : asArray(record.providers ?? record.items)
  return list.map((item) => normalizeProvider(item)).filter((item): item is ProviderPreset => item !== null)
}

/** A single account from POST/PUT /api/accounts; lists use normalizeAccounts. */
export function normalizeAccount(raw: unknown): AccountSummary | null {
  if (!isRecord(raw)) return null
  const item = isRecord(raw.account) ? raw.account : raw
  const id = asString(item.id)
  if (!id) return null
  return {
    id,
    name: asString(item.name) || id,
    provider: asString(item.provider),
    baseUrl: asString(item.baseUrl) || undefined,
    model: asString(item.model) || undefined,
    hasKey: typeof item.hasKey === 'boolean' ? item.hasKey : undefined,
    status: asString(item.status) || undefined,
  }
}

export function normalizePlugins(raw: unknown): PluginEntry[] {
  const record = isRecord(raw) ? raw : {}
  const list = Array.isArray(raw) ? raw : asArray(record.plugins ?? record.items)
  return list
    .filter(isRecord)
    .map((item) => ({
      // The desktop UiState.PluginItem calls it "name"; tolerate an id-only fallback.
      name: asString(item.name ?? item.id ?? item.spec),
      version: asString(item.version),
      description: asString(item.description ?? item.detail),
      source: asString(item.source),
    }))
    .filter((item) => item.name !== '')
}

export function normalizeCatalogItem(raw: unknown): CatalogItem | null {
  if (!isRecord(raw)) return null
  const repo = asString(raw.repo ?? raw.fullName)
  if (!repo) return null
  return {
    repo,
    name: asString(raw.name) || repo,
    description: asString(raw.description),
    stars: asNumberOrNull(raw.stars) ?? 0,
    url: asString(raw.url) || 'https://github.com/' + repo,
    spec: asString(raw.spec),
    status: asString(raw.status),
  }
}

export function normalizeCatalogPage(raw: unknown): CatalogPage {
  const record = unwrapRecord(raw, 'catalog')
  const items = asArray(record.items ?? record.entries)
    .map((item) => normalizeCatalogItem(item))
    .filter((item): item is CatalogItem => item !== null)
  const page = asNumberOrNull(record.page) ?? 1
  return {
    query: asString(record.query),
    page: page > 0 ? page : 1,
    total: asNumberOrNull(record.total) ?? items.length,
    hasNext: asBoolean(record.hasNext ?? record.has_next),
    fromCache: asBoolean(record.fromCache ?? record.from_cache ?? record.cached),
    warning: asString(record.warning),
    items,
  }
}

function normalizeInstallTarget(raw: unknown): InstallTarget | null {
  if (!isRecord(raw)) return null
  const spec = asString(raw.spec)
  if (!spec) return null
  return {
    name: asString(raw.name),
    version: asString(raw.version),
    spec,
    packagePath: asString(raw.packagePath ?? raw.path),
    evidenceUrl: asString(raw.evidenceUrl ?? raw.evidence),
  }
}

export function normalizePluginDetails(raw: unknown): PluginDetails {
  const record = isRecord(raw) ? raw : {}
  return {
    repo: asString(record.repo),
    status: asString(record.status).toUpperCase(),
    message: asString(record.message ?? record.warning),
    fromCache: asBoolean(record.fromCache ?? record.from_cache ?? record.cached),
    targets: asArray(record.targets)
      .map((item) => normalizeInstallTarget(item))
      .filter((item): item is InstallTarget => item !== null),
  }
}

export function normalizeBackgroundAsset(raw: unknown): BackgroundAsset {
  if (typeof raw === 'string') return { background: raw }
  const record = isRecord(raw) ? (isRecord(raw.settings) ? raw.settings : raw) : {}
  return { background: asString(record.background ?? record.url) }
}

export function normalizePackPreview(raw: unknown): PackPreview | null {
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
    profiles: asArray(item.profiles).map((value) => asString(value)).filter(Boolean),
    unpackedBytes: asNumberOrNull(item.unpackedBytes ?? item.size) ?? 0,
    warnings: asArray(item.warnings).map((value) => asString(value)).filter(Boolean),
  }
}

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


