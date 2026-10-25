/**
 * Shared DTOs for the PCL-DSH-Go manager API v1.
 *
 * Contract: docs/IMPLEMENTATION_SPEC.md `4.2/`4.3, docs/M3_INTERFACES.md `8,
 * docs/M4_INTERFACES.md `4. Every field is read defensively in
 * api/normalize.ts so screens degrade instead of showing made-up data.
 */

/** GET /api/state status: READY when idle, RUNNING while a job is active. */
export interface StatusSnapshot {
  code: string
  message: string
  phase: string
}

/** One configured instance (docs/IMPLEMENTATION_SPEC.md `4.3 InstanceDto). */
export interface InstanceSummary {
  id: string
  name: string
  version: string
  profile: string
  port: number
  status: string
  path: string | null
  profiles: string[]
}

export interface RuntimeSummary {
  version: string
  installed: boolean
  compatibility: string
}

/** GET/PUT /api/settings and the settings block of /api/state. */
export interface AppSettings {
  proxy: string
  registry: string
  directAccessSshUser: string
}

export interface StateResponse {
  version: string
  currentInstanceId: string | null
  instances: InstanceSummary[]
  runtimes: RuntimeSummary[]
  status: StatusSnapshot
  settings: AppSettings
  availableVersions: string[]
  warnings: string[]
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
 * GET /api/instances/{id}/direct-access: loopback URL plus SSH tunnel command.
 * The upstream DSH web login only accepts loopback addresses, so a remote
 * operator has to open a tunnel once.
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

/** POST /api/instances body (docs/IMPLEMENTATION_SPEC.md `4.2). */
export interface CreateInstanceInput {
  name: string
  version: string
  profile: string
  /** null/omitted means "let the manager pick a free port". */
  port?: number | null
}

/** PUT /api/instances/{id} body: version/profile are immutable after creation. */
export interface UpdateInstanceInput {
  name?: string
  port?: number | null
}

export interface DeleteInstanceResult {
  trashedPath: string
}

/** GET /api/runtimes/available. */
export interface AvailableRuntimes {
  versions: string[]
}

export interface LogTail {
  lines: string[]
}

/** Job state machine (docs/IMPLEMENTATION_SPEC.md `4.5). */
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

/** Unified error body (docs/IMPLEMENTATION_SPEC.md `4.4). */
export interface ApiErrorBody {
  code?: string
  message?: string
  details?: unknown
  traceId?: string
}

/* ------------------------------------------------------------------ */
/* M3 DTOs: installed plugins, packs, artifacts                        */
/* ------------------------------------------------------------------ */

/** GET /api/instances/{id}/plugins?profile= item (InstalledPluginDto). */
export interface InstalledPluginDto {
  name: string
  version: string
  description: string
  source: string
}

/** Reference name kept by the views. */
export type PluginEntry = InstalledPluginDto

/** POST /api/packs/preview response (multipart field "file", previewId TTL 30min). */
export interface PackPreviewDto {
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

/** Reference name kept by the views. */
export type PackPreview = PackPreviewDto

/** POST /api/packs/import body → 202 Job(IMPORT_PACK). */
export interface ImportPackInput {
  previewId: string
  name?: string
  /** Exact Harness version; empty means "use the pack/installed resolution order". */
  version?: string
}

export type PackExportMode = 'profile' | 'home'

/** POST /api/packs/export body → 202 Job(EXPORT_PACK). */
export interface ExportPackInput {
  instanceId: string
  mode: PackExportMode
}

/** EXPORT_PACK artifact reference parsed from a Job result. */
export interface ArtifactRef {
  artifactId: string
  filename: string
  size: number
}

/* ------------------------------------------------------------------ */
/* Job result payloads (docs/M3_INTERFACES.md `7)                      */
/* ------------------------------------------------------------------ */

export interface LaunchJobResult {
  instanceId: string
  /** Console path only: never a token URL. */
  webUrl: string
}

export interface InstallRuntimeJobResult {
  version: string
  installed: boolean
  compatibility: string
}

export interface ProbeRuntimeJobResult {
  version: string
  compatibility: string
}

export interface ImportPackJobResult {
  instanceId: string
  name: string
}

export interface ExportPackJobResult {
  artifactId: string
  filename: string
  size: number
}

export interface PluginMutateJobResult {
  instanceId: string
  profile: string
  action: string
  name: string
}

/** Union of every typed Job result payload. */
export type JobResult =
  | LaunchJobResult
  | InstallRuntimeJobResult
  | ProbeRuntimeJobResult
  | ImportPackJobResult
  | ExportPackJobResult
  | PluginMutateJobResult
