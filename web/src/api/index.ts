import { request, uploadForm } from './client'
import type { UploadOptions } from './client'
import {
  normalizeArtifactRef,
  normalizeAvailableVersions,
  normalizeDeleteResult,
  normalizeDirectAccess,
  normalizeExportPackJobResult,
  normalizeImportPackJobResult,
  normalizeInstallRuntimeJobResult,
  normalizeInstance,
  normalizeInstances,
  normalizeJobAccepted,
  normalizeJobList,
  normalizeLaunchJobResult,
  normalizeLogTail,
  normalizePackPreview,
  normalizePluginMutateJobResult,
  normalizePlugins,
  normalizeProbeRuntimeJobResult,
  normalizeRuntimes,
  normalizeSettings,
  normalizeState,
  normalizeWebUrl,
} from './normalize'
import type {
  AppSettings,
  ArtifactRef,
  AuthMe,
  AvailableRuntimes,
  CreateInstanceInput,
  DeleteInstanceResult,
  DirectAccess,
  ExportPackInput,
  ExportPackJobResult,
  ImportPackInput,
  ImportPackJobResult,
  InstallRuntimeJobResult,
  InstanceSummary,
  JobView,
  LaunchJobResult,
  LogTail,
  PackPreviewDto,
  PluginMutateJobResult,
  ProbeRuntimeJobResult,
  RuntimeSummary,
  StateResponse,
  UpdateInstanceInput,
  WebUrl,
} from './types'

export { ApiError } from './client'
export type {
  ApiErrorBody,
  AppSettings,
  ArtifactRef,
  AuthMe,
  AvailableRuntimes,
  CreateInstanceInput,
  DeleteInstanceResult,
  DirectAccess,
  ExportPackInput,
  ExportPackJobResult,
  ImportPackInput,
  ImportPackJobResult,
  InstallRuntimeJobResult,
  InstalledPluginDto,
  InstanceSummary,
  JobAccepted,
  JobError,
  JobErrorEvent,
  JobLogEntry,
  JobResult,
  JobResultEvent,
  JobState,
  JobStateEvent,
  JobView,
  LaunchJobResult,
  LogTail,
  PackExportMode,
  PackPreview,
  PackPreviewDto,
  PluginEntry,
  PluginMutateJobResult,
  ProbeRuntimeJobResult,
  RuntimeSummary,
  StateResponse,
  StatusSnapshot,
  UpdateInstanceInput,
  WebUrl,
} from './types'

function instancePath(id: string, suffix = ''): string {
  return '/api/instances/' + encodeURIComponent(id) + suffix
}

function profileQuery(profile?: string): string {
  return profile ? '?profile=' + encodeURIComponent(profile) : ''
}

/** Thrown by helpers when the backend answers 202 without a usable job id. */
export class MissingJobError extends Error {
  constructor() {
    super('后端已接受任务，但没有返回任务 ID。请稍后在任务页面查看。')
    this.name = 'MissingJobError'
  }
}

export const authApi = {
  me: () => request<AuthMe>('GET', '/api/auth/me'),
  login: (password: string) => request<void>('POST', '/api/auth/login', { password }),
  logout: () => request<void>('POST', '/api/auth/logout'),
  changePassword: (oldPassword: string, newPassword: string) =>
    request<void>('PUT', '/api/auth/password', { oldPassword, newPassword }),
}

export const stateApi = {
  get: async (): Promise<StateResponse> => normalizeState(await request<unknown>('GET', '/api/state')),
}

export const instancesApi = {
  list: async (): Promise<InstanceSummary[]> =>
    normalizeInstances(await request<unknown>('GET', '/api/instances')),
  get: async (id: string): Promise<InstanceSummary | null> =>
    normalizeInstance(await request<unknown>('GET', instancePath(id))),
  /** POST /api/instances {name,version,profile,port} → 201 InstanceDto. */
  create: async (input: CreateInstanceInput): Promise<InstanceSummary | null> =>
    normalizeInstance(await request<unknown>('POST', '/api/instances', input)),
  /** PUT /api/instances/{id} {name?,port?} → 200 InstanceDto (version/profile are immutable). */
  update: async (id: string, input: UpdateInstanceInput): Promise<InstanceSummary | null> =>
    normalizeInstance(await request<unknown>('PUT', instancePath(id), input)),
  remove: async (id: string): Promise<DeleteInstanceResult> =>
    normalizeDeleteResult(await request<unknown>('DELETE', instancePath(id))),
  select: (id: string): Promise<unknown> => request<unknown>('PUT', instancePath(id, '/selection')),
  launch: async (id: string): Promise<JobView> => {
    const job = normalizeJobAccepted(await request<unknown>('POST', instancePath(id, '/launch')))
    if (!job) throw new MissingJobError()
    return job
  },
  stop: (id: string): Promise<unknown> => request<unknown>('POST', instancePath(id, '/stop')),
  webUrl: async (id: string): Promise<WebUrl> =>
    normalizeWebUrl(await request<unknown>('GET', instancePath(id, '/web-url'))),
  /**
   * Loopback direct-access info (URL with a one-time token plus SSH tunnel
   * command). The upstream DSH web login only accepts loopback addresses.
   */
  directAccess: async (id: string): Promise<DirectAccess> =>
    normalizeDirectAccess(await request<unknown>('GET', instancePath(id, '/direct-access'))),
}

export const runtimesApi = {
  installed: async (): Promise<RuntimeSummary[]> =>
    normalizeRuntimes(await request<unknown>('GET', '/api/runtimes')),
  available: async (): Promise<AvailableRuntimes> =>
    normalizeAvailableVersions(await request<unknown>('GET', '/api/runtimes/available')),
  /** POST /api/runtimes {version} → 202 INSTALL_RUNTIME job. */
  install: async (version: string): Promise<JobView> => {
    const job = normalizeJobAccepted(await request<unknown>('POST', '/api/runtimes', { version }))
    if (!job) throw new MissingJobError()
    return job
  },
  probe: async (version: string): Promise<JobView> => {
    const job = normalizeJobAccepted(
      await request<unknown>('POST', '/api/runtimes/' + encodeURIComponent(version) + '/probe'),
    )
    if (!job) throw new MissingJobError()
    return job
  },
  remove: async (version: string): Promise<DeleteInstanceResult> =>
    normalizeDeleteResult(await request<unknown>('DELETE', '/api/runtimes/' + encodeURIComponent(version))),
}

/**
 * Scoped npm names must reach the backend as the literal "@scope/name" path.
 * Segments are escaped individually so the scope separator stays a real "/".
 */
function pluginNamePath(name: string): string {
  return name
    .split('/')
    .map((segment) => segment.replace(/[^A-Za-z0-9@._~-]/g, (char) => encodeURIComponent(char)))
    .join('/')
}

export const pluginsApi = {
  list: async (instanceId: string, profile?: string): Promise<import('./types').InstalledPluginDto[]> =>
    normalizePlugins(
      await request<unknown>('GET', instancePath(instanceId, '/plugins') + profileQuery(profile)),
    ),
  add: async (instanceId: string, spec: string, profile?: string): Promise<JobView> => {
    const job = normalizeJobAccepted(
      await request<unknown>('POST', instancePath(instanceId, '/plugins') + profileQuery(profile), { spec }),
    )
    if (!job) throw new MissingJobError()
    return job
  },
  update: async (instanceId: string, name: string, profile?: string): Promise<JobView> => {
    const job = normalizeJobAccepted(
      await request<unknown>(
        'PATCH',
        instancePath(instanceId, '/plugins/' + pluginNamePath(name)) + profileQuery(profile),
      ),
    )
    if (!job) throw new MissingJobError()
    return job
  },
  remove: async (instanceId: string, name: string, profile?: string): Promise<JobView> => {
    const job = normalizeJobAccepted(
      await request<unknown>(
        'DELETE',
        instancePath(instanceId, '/plugins/' + pluginNamePath(name)) + profileQuery(profile),
      ),
    )
    if (!job) throw new MissingJobError()
    return job
  },
}

export const artifactsApi = {
  /** Download URL; the session cookie authenticates the streaming response. */
  url: (id: string): string => '/api/artifacts/' + encodeURIComponent(id),
}

export const jobsApi = {
  list: async (): Promise<JobView[]> => normalizeJobList(await request<unknown>('GET', '/api/jobs')),
  get: async (id: string): Promise<JobView> => {
    const job = normalizeJobAccepted(await request<unknown>('GET', '/api/jobs/' + encodeURIComponent(id)))
    if (!job) throw new MissingJobError()
    return job
  },
  cancel: (id: string): Promise<unknown> =>
    request<unknown>('POST', '/api/jobs/' + encodeURIComponent(id) + '/cancel'),
  eventsPath: (id: string): string => '/api/jobs/' + encodeURIComponent(id) + '/events',
}

export const logsApi = {
  tail: async (lines = 800): Promise<LogTail> =>
    normalizeLogTail(await request<unknown>('GET', '/api/logs/tail?lines=' + String(lines))),
}

export const settingsApi = {
  get: async (): Promise<AppSettings> => normalizeSettings(await request<unknown>('GET', '/api/settings')),
  update: async (payload: AppSettings): Promise<AppSettings> =>
    normalizeSettings(await request<unknown>('PUT', '/api/settings', payload)),
}

export const packsApi = {
  /**
   * POST /api/packs/preview (multipart field "file", 200, previewId TTL 30min).
   * Uses XHR so large packs report upload progress; abortable via options.signal.
   */
  preview: async (file: File, options: UploadOptions = {}): Promise<PackPreviewDto> => {
    const preview = normalizePackPreview(await uploadForm<unknown>('/api/packs/preview', file, options))
    if (!preview) throw new Error('后端返回的整合包预览数据不完整，请重试或更换整合包。')
    return preview
  },
  /** POST /api/packs/import → 202 IMPORT_PACK Job (result {instanceId,name}). */
  importPack: async (input: ImportPackInput): Promise<JobView> => {
    const job = normalizeJobAccepted(await request<unknown>('POST', '/api/packs/import', input))
    if (!job) throw new MissingJobError()
    return job
  },
  /** POST /api/packs/export → 202 EXPORT_PACK Job (result {artifactId,filename,size}). */
  exportPack: async (input: ExportPackInput): Promise<JobView> => {
    const job = normalizeJobAccepted(await request<unknown>('POST', '/api/packs/export', input))
    if (!job) throw new MissingJobError()
    return job
  },
  /** Parse an EXPORT_PACK Job result into an artifact reference (null when incomplete). */
  artifactFromResult: (result: unknown): ArtifactRef | null => normalizeArtifactRef(result),
}

/* Typed Job result helpers (docs/M3_INTERFACES.md `7). */
export const jobResults = {
  launch: (result: unknown): LaunchJobResult | null => normalizeLaunchJobResult(result),
  installRuntime: (result: unknown): InstallRuntimeJobResult | null =>
    normalizeInstallRuntimeJobResult(result),
  probeRuntime: (result: unknown): ProbeRuntimeJobResult | null => normalizeProbeRuntimeJobResult(result),
  importPack: (result: unknown): ImportPackJobResult | null => normalizeImportPackJobResult(result),
  exportPack: (result: unknown): ExportPackJobResult | null => normalizeExportPackJobResult(result),
  pluginMutate: (result: unknown): PluginMutateJobResult | null => normalizePluginMutateJobResult(result),
}
