/**
 * Typed DTOs for the REST contract in docs/IMPLEMENTATION_PLAN.md 4.2 / 4.3 / 4.9.
 *
 * The backend is implemented in parallel, so every field is read defensively
 * (see api/normalize.ts). Screens must degrade instead of showing made-up data.
 */

/** /api/state mixes a desktop-era status string with the planned {code,message}. */
export type StatusSnapshot = string | { code?: string; message?: string } | null

export interface InstanceSummary {
  id: string
  name: string
  version: string
  profile: string
  port: number | null
  profiles: string[]
  accountId: string | null
  path: string | null
  status: string
}

export interface RuntimeSummary {
  version: string
  installed: boolean
  compatibility: string
}

export interface AccountSummary {
  id: string
  name: string
  provider: string
  baseUrl?: string
  model?: string
  hasKey?: boolean
  status?: string
}

export interface AppSettings {
  proxy: string
  registry: string
  background: string
}

export interface StateResponse {
  currentInstanceId: string | null
  settings: AppSettings
  instances: InstanceSummary[]
  runtimes: RuntimeSummary[]
  accounts: AccountSummary[]
  availableVersions: string[]
  status: StatusSnapshot
}

export interface AuthMe {
  authenticated: boolean
  csrfToken: string | null
  version: string
  initialPasswordPending: boolean
}

export interface WebUrl {
  url: string
}

/**
 * GET /api/instances/{id}/direct-access：远程完成 DeepSeek 账号网页登录所需的直连信息。
 *
 * 上游 DSH 的账号 OAuth 回调只接受回环地址（127.0.0.1/localhost），远程浏览器无法直接完成
 * 登录；需要先用 SSH 隧道把实例端口映射到本机，再打开 directUrl 完成一次登录。
 */
export interface DirectAccess {
  ready: boolean
  port: number
  directUrl: string | null
  tunnelCommand: string
  sshHost: string
  sshUser: string
  note: string
}

/** POST/PUT instance payload (plan 4.2). port === null means "auto assign". */
export interface InstanceInput {
  name: string
  version?: string
  profile?: string
  port?: number | null
  accountId?: string
}

export interface DeleteInstanceResult {
  trashedPath: string
}

/** GET /api/runtimes/available (plan 4.2). */
export interface AvailableRuntimes {
  versions: string[]
}

export interface LogTail {
  lines: string[]
}

/** Job state machine from plan 4.3. */
export type JobState = 'RUNNING' | 'SUCCEEDED' | 'FAILED' | 'CANCELLED'

export interface JobError {
  code: string
  message: string
  details?: unknown
}

export interface JobView {
  id: string
  type: string
  state: JobState
  phase: string
  message: string
  createdAt: number | null
  startedAt: number | null
  finishedAt: number | null
  lastSeq: number
  result: unknown
  error: JobError | null
}

export interface JobLogEntry {
  jobId: string
  seq: number
  ts: number
  level: string
  line: string
}

export interface JobStateEvent {
  jobId: string
  seq: number
  ts: number
  state: JobState
  phase: string
  message: string
}

export interface JobResultEvent {
  jobId: string
  state: JobState
  result: unknown
}

export interface JobErrorEvent {
  jobId: string
  state: JobState
  code: string
  message: string
  details?: unknown
}

/** A 202 response body: either a Job View, or a compact {jobId} envelope. */
export interface JobAccepted {
  jobId?: string
  id?: string
  state?: string
  phase?: string
  message?: string
  type?: string
}

/** Unified error body from plan 4.10. */
export interface ApiErrorBody {
  code?: string
  message?: string
  details?: unknown
  traceId?: string
}

/* ------------------------------------------------------------------ */
/* Third increment (M2 F-7/F-13/F-15): accounts, plugins, catalog, assets */
/* ------------------------------------------------------------------ */

/** GET /api/accounts/providers: official provider presets. */
export interface ProviderPreset {
  id: string
  name: string
  baseUrl: string
  model: string
}

/** POST/PUT /api/accounts body; a blank apiKey on edit keeps the stored secret. */
export interface AccountInput {
  name: string
  provider: string
  baseUrl: string
  model: string
  apiKey?: string
}

/** GET /api/instances/{id}/plugins?profile= item. */
export interface PluginEntry {
  name: string
  version: string
  description: string
  source: string
}

/** GET /api/plugin-catalog?q=&page=&refresh= item. */
export interface CatalogItem {
  repo: string
  name: string
  description: string
  stars: number
  url: string
  spec: string
  status: string
}

export interface CatalogPage {
  query: string
  page: number
  total: number
  hasNext: boolean
  fromCache: boolean
  warning: string
  items: CatalogItem[]
}

/** GET /api/plugin-catalog/{owner}/{repo}: resolved install targets. */
export interface InstallTarget {
  name: string
  version: string
  spec: string
  packagePath: string
  evidenceUrl: string
}

export interface PluginDetails {
  repo: string
  status: string
  message: string
  fromCache: boolean
  targets: InstallTarget[]
}

/** POST /api/settings/background response. */
export interface BackgroundAsset {
  background: string
}

/* ------------------------------------------------------------------ */
/* Fourth increment (M3): pack preview / import / export / artifacts   */
/* ------------------------------------------------------------------ */

/** POST /api/packs/preview response (multipart field "file", 200, previewId TTL 30min). */
export interface PackPreview {
  previewId: string
  name: string
  format: string
  version: string
  /** Harness version declared by the pack; empty when not declared. */
  dshVersion: string
  profiles: string[]
  unpackedBytes: number
  warnings: string[]
}

/** POST /api/packs/import body → 202 Job (type=IMPORT_PACK, result {instanceId,name}). */
export interface ImportPackInput {
  previewId: string
  name?: string
  /** Exact Harness version; empty means "use the pack/installed resolution order". */
  version?: string
}

export type PackExportMode = 'profile' | 'home'

/** POST /api/packs/export body → 202 Job (type=EXPORT_PACK, result {artifactId,filename,size}). */
export interface ExportPackInput {
  instanceId: string
  mode: PackExportMode
}

export interface ArtifactRef {
  artifactId: string
  filename: string
  size: number
}

/** PUT /api/instances/{id}: 200 returns the updated Instance, 202 returns a Job. */
export interface InstanceSaveResult {
  instance: InstanceSummary | null
  job: JobView | null
}
