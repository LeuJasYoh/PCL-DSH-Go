import { request, requestDetailed, uploadForm } from './client'
import type { UploadOptions } from './client'
import {
  normalizeAccount,
  normalizeAccounts,
  normalizeAvailableVersions,
  normalizeBackgroundAsset,
  normalizeCatalogPage,
  normalizeDeleteResult,
  normalizeDirectAccess,
  normalizeInstance,
  normalizeInstances,
  normalizeJobAccepted,
  normalizeJobList,
  normalizeArtifactRef,
  normalizeLogTail,
  normalizePackPreview,
  normalizePluginDetails,
  normalizePlugins,
  normalizeProviders,
  normalizeRuntimes,
  normalizeSettings,
  normalizeState,
  normalizeWebUrl,
} from './normalize'
import type {
  AccountInput,
  AccountSummary,
  AppSettings,
  AuthMe,
  ArtifactRef,
  AvailableRuntimes,
  BackgroundAsset,
  CatalogPage,
  DeleteInstanceResult,
  DirectAccess,
  ExportPackInput,
  ImportPackInput,
  InstanceInput,
  InstanceSaveResult,
  InstanceSummary,
  JobView,
  LogTail,
  PackPreview,
  PluginDetails,
  PluginEntry,
  ProviderPreset,
  RuntimeSummary,
  StateResponse,
  WebUrl,
} from './types'

export { ApiError } from './client'
export type {
  AccountInput,
  AccountSummary,
  AppSettings,
  AuthMe,
  AvailableRuntimes,
  BackgroundAsset,
  CatalogItem,
  CatalogPage,
  DeleteInstanceResult,
  DirectAccess,
  ExportPackInput,
  ImportPackInput,
  InstallTarget,
  InstanceInput,
  InstanceSaveResult,
  InstanceSummary,
  JobState,
  JobView,
  LogTail,
  PackExportMode,
  PackPreview,
  ArtifactRef,
  PluginDetails,
  PluginEntry,
  ProviderPreset,
  RuntimeSummary,
  StateResponse,
  WebUrl,
} from './types'

function instancePath(id: string, suffix = ''): string {
  return '/api/instances/' + encodeURIComponent(id) + suffix
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
  create: async (input: InstanceInput): Promise<InstanceSummary | null> =>
    normalizeInstance(await request<unknown>('POST', '/api/instances', input)),
  update: async (id: string, input: InstanceInput): Promise<InstanceSummary | null> =>
    normalizeInstance(await request<unknown>('PUT', instancePath(id), input)),
  /**
   * PUT /api/instances/{id}: ordinary field edits return 200 InstanceDto, while a
   * changed Harness version/profile returns 202 + a COPY_INSTANCE Job (contract
   * with the backend). The HTTP status is the reliable discriminator.
   */
  save: async (id: string, input: InstanceInput): Promise<InstanceSaveResult> => {
    const response = await requestDetailed<unknown>('PUT', instancePath(id), input)
    if (response.status === 202) {
      const job = normalizeJobAccepted(response.payload)
      if (!job) throw new MissingJobError()
      return { instance: null, job }
    }
    return { instance: normalizeInstance(response.payload), job: null }
  },
  /** POST /api/instances/{id}/copy → 202 Job (type=COPY_INSTANCE, result {instanceId,name}). */
  copy: async (id: string, name: string): Promise<JobView> => {
    const job = normalizeJobAccepted(
      await request<unknown>('POST', instancePath(id, '/copy'), { name }),
    )
    if (!job) throw new MissingJobError()
    return job
  },
  remove: async (id: string): Promise<DeleteInstanceResult> =>
    normalizeDeleteResult(await request<unknown>('DELETE', instancePath(id))),
  select: (id: string): Promise<unknown> => request<unknown>('PUT', instancePath(id, '/selection')),
  bindAccount: (id: string, accountId: string): Promise<unknown> =>
    request<unknown>('PUT', instancePath(id, '/account'), { accountId }),
  launch: async (id: string): Promise<JobView> => {
    const job = normalizeJobAccepted(await request<unknown>('POST', instancePath(id, '/launch')))
    if (!job) throw new MissingJobError()
    return job
  },
  stop: (id: string): Promise<unknown> => request<unknown>('POST', instancePath(id, '/stop')),
  webUrl: async (id: string): Promise<WebUrl> =>
    normalizeWebUrl(await request<unknown>('GET', instancePath(id, '/web-url'))),
  /**
   * 远程账号网页登录辅助：返回回环直连地址（含 token）与 SSH 隧道命令。
   * 上游 DSH 的 OAuth 回调只接受回环地址，远程访问需按提示建立隧道完成一次登录。
   */
  directAccess: async (id: string): Promise<DirectAccess> =>
    normalizeDirectAccess(await request<unknown>('GET', instancePath(id, '/direct-access'))),
}

export const runtimesApi = {
  installed: async (): Promise<RuntimeSummary[]> =>
    normalizeRuntimes(await request<unknown>('GET', '/api/runtimes')),
  available: async (): Promise<AvailableRuntimes> =>
    normalizeAvailableVersions(await request<unknown>('GET', '/api/runtimes/available')),
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

export const accountsApi = {
  providers: async (): Promise<ProviderPreset[]> =>
    normalizeProviders(await request<unknown>('GET', '/api/accounts/providers')),
  list: async (): Promise<AccountSummary[]> =>
    normalizeAccounts(await request<unknown>('GET', '/api/accounts')),
  create: async (input: AccountInput): Promise<AccountSummary | null> =>
    normalizeAccount(await request<unknown>('POST', '/api/accounts', input)),
  update: async (id: string, input: AccountInput): Promise<AccountSummary | null> =>
    normalizeAccount(await request<unknown>('PUT', '/api/accounts/' + encodeURIComponent(id), input)),
  remove: (id: string): Promise<unknown> =>
    request<unknown>('DELETE', '/api/accounts/' + encodeURIComponent(id)),
}

/**
 * Scoped npm names must reach the backend as the literal "@scope/name" path.
 * The servlet container rejects percent-encoded slashes (%2F), so only unsafe
 * characters are escaped per segment and the scope separator stays a real "/".
 */
function pluginNamePath(name: string): string {
  return name
    .split('/')
    .map((segment) => segment.replace(/[^A-Za-z0-9@._~-]/g, (char) => encodeURIComponent(char)))
    .join('/')
}

export const pluginsApi = {
  list: async (instanceId: string, profile?: string): Promise<PluginEntry[]> => {
    const query = profile ? '?profile=' + encodeURIComponent(profile) : ''
    return normalizePlugins(
      await request<unknown>('GET', instancePath(instanceId, '/plugins') + query),
    )
  },
  add: async (instanceId: string, spec: string): Promise<JobView> => {
    const job = normalizeJobAccepted(
      await request<unknown>('POST', instancePath(instanceId, '/plugins'), { spec }),
    )
    if (!job) throw new MissingJobError()
    return job
  },
  update: async (instanceId: string, name: string): Promise<JobView> => {
    const job = normalizeJobAccepted(
      await request<unknown>('PATCH', instancePath(instanceId, '/plugins/' + pluginNamePath(name))),
    )
    if (!job) throw new MissingJobError()
    return job
  },
  remove: async (instanceId: string, name: string): Promise<JobView> => {
    const job = normalizeJobAccepted(
      await request<unknown>('DELETE', instancePath(instanceId, '/plugins/' + pluginNamePath(name))),
    )
    if (!job) throw new MissingJobError()
    return job
  },
}

export const catalogApi = {
  search: async (query: string, page = 1, refresh = false): Promise<CatalogPage> => {
    const params = new URLSearchParams()
    if (query.trim()) params.set('q', query.trim())
    params.set('page', String(Math.max(1, page)))
    if (refresh) params.set('refresh', 'true')
    return normalizeCatalogPage(
      await request<unknown>('GET', '/api/plugin-catalog?' + params.toString()),
    )
  },
  resolve: async (repo: string, refresh = false): Promise<PluginDetails> => {
    const segments = repo.split('/').map((part) => encodeURIComponent(part))
    const query = refresh ? '?refresh=true' : ''
    return normalizePluginDetails(
      await request<unknown>('GET', '/api/plugin-catalog/' + segments.join('/') + query),
    )
  },
}

export const artifactsApi = {
  /** Downloads stream through the session cookie; artifacts appear in M3 pack flows first. */
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
  /**
   * POST /api/settings/background (multipart, field "file") returns the stored asset URL.
   * Callers treat 404/405/501 as "not implemented yet" and say so in the UI.
   */
  uploadBackground: async (file: File): Promise<BackgroundAsset> => {
    const formData = new FormData()
    formData.append('file', file)
    return normalizeBackgroundAsset(
      await request<unknown>('POST', '/api/settings/background', undefined, { formData }),
    )
  },
}

export const packsApi = {
  /**
   * POST /api/packs/preview (multipart field "file", 200, previewId TTL 30min).
   * Uses XHR so large packs report upload progress; abortable via options.signal.
   */
  preview: async (file: File, options: UploadOptions = {}): Promise<PackPreview> => {
    const preview = normalizePackPreview(await uploadForm<unknown>('/api/packs/preview', file, options))
    if (!preview) throw new Error('后端返回的整合包预览数据不完整，请重试或更换整合包。')
    return preview
  },
  /** POST /api/packs/import → 202 Job (type=IMPORT_PACK, result {instanceId,name}). */
  importPack: async (input: ImportPackInput): Promise<JobView> => {
    const job = normalizeJobAccepted(await request<unknown>('POST', '/api/packs/import', input))
    if (!job) throw new MissingJobError()
    return job
  },
  /** POST /api/packs/export → 202 Job (type=EXPORT_PACK, result {artifactId,filename,size}). */
  exportPack: async (input: ExportPackInput): Promise<JobView> => {
    const job = normalizeJobAccepted(await request<unknown>('POST', '/api/packs/export', input))
    if (!job) throw new MissingJobError()
    return job
  },
  /** Parse an EXPORT_PACK Job result into an artifact reference (null when incomplete). */
  artifactFromResult: (result: unknown): ArtifactRef | null => normalizeArtifactRef(result),
}
