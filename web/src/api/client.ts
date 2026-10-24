import type { ApiErrorBody } from './types'

const CSRF_COOKIE = 'XSRF-TOKEN'
const CSRF_HEADER = 'X-XSRF-TOKEN'

export type HttpMethod = 'GET' | 'POST' | 'PUT' | 'PATCH' | 'DELETE'

export class ApiError extends Error {
  readonly status: number
  readonly code: string
  readonly details: unknown
  readonly traceId: string | null

  constructor(status: number, code: string, message: string, details: unknown = null, traceId: string | null = null) {
    super(message)
    this.name = 'ApiError'
    this.status = status
    this.code = code
    this.details = details
    this.traceId = traceId
  }

  /** True when the failure most likely means "backend not implemented yet". */
  get unavailable(): boolean {
    return this.status === 0 || this.status === 404 || this.status === 501 || this.status === 502 || this.status === 503
  }
}

export interface RequestOptions {
  signal?: AbortSignal
  /** Multipart body (background upload); mutually exclusive with the JSON body. */
  formData?: FormData
}

let unauthorizedHandler: (() => void) | null = null

/** Registered from main.ts so an expired session sends the SPA back to /login. */
export function setUnauthorizedHandler(handler: () => void): void {
  unauthorizedHandler = handler
}

export function readCookie(name: string): string | null {
  const prefix = name + '='
  for (const part of document.cookie.split(';')) {
    const item = part.trim()
    if (item.startsWith(prefix)) return decodeURIComponent(item.slice(prefix.length))
  }
  return null
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === 'object' && value !== null
}

function codeForStatus(status: number): string {
  switch (status) {
    case 400: return 'BAD_REQUEST'
    case 401: return 'UNAUTHORIZED'
    case 403: return 'FORBIDDEN'
    case 404: return 'NOT_FOUND'
    case 405: return 'METHOD_NOT_ALLOWED'
    case 409: return 'CONFLICT'
    case 422: return 'UNPROCESSABLE'
    case 423: return 'LOCKED'
    case 429: return 'TOO_MANY_REQUESTS'
    case 500: return 'INTERNAL'
    case 502: return 'BAD_GATEWAY'
    case 503: return 'UNAVAILABLE'
    default: return 'HTTP_' + String(status)
  }
}

function defaultMessage(status: number, code: string): string {
  switch (status) {
    case 400: return '请求参数不正确。'
    case 401: return '未登录或会话已过期。'
    case 403: return code === 'CSRF_INVALID' ? 'CSRF 校验失败，请刷新页面后重试。' : '没有权限执行此操作。'
    case 404: return '接口尚未实现，或请求的资源不存在。'
    case 409: return '当前有任务正在执行，请等待完成或取消。'
    case 422: return '当前版本或运行时不满足要求。'
    case 423: return '尝试次数过多，账户已被暂时锁定，请稍后再试。'
    case 429: return '请求过于频繁，请稍后再试。'
    case 500: return '后端服务内部错误，请查看服务日志。'
    case 502: case 503: return '无法连接后端服务，请确认 PCL-DSH-Linux 服务已启动。'
    default: return '请求失败（HTTP ' + String(status) + '）。'
  }
}

export interface DetailedResponse<T> {
  status: number
  payload: T
}

export interface UploadOptions {
  signal?: AbortSignal
  /** Upload progress in percent, or null when the browser cannot compute it. */
  onProgress?: (percent: number | null) => void
}

function buildApiError(status: number, payload: unknown, path: string): ApiError {
  const errorBody = isRecord(payload) ? (payload as ApiErrorBody) : null
  // A plain-text 5xx usually comes from the reverse proxy / Vite dev proxy
  // because the Spring service is not reachable; report it as unreachable.
  const gatewayFailure = status >= 500 && !isRecord(payload)
  const effectiveStatus = gatewayFailure ? 503 : status
  const code = gatewayFailure
    ? 'BAD_GATEWAY'
    : typeof errorBody?.code === 'string' && errorBody.code
      ? errorBody.code
      : codeForStatus(status)
  const message = gatewayFailure
    ? '无法连接后端服务，请确认 PCL-DSH-Linux 服务已在本机 8080 端口启动。'
    : typeof errorBody?.message === 'string' && errorBody.message.trim()
      ? errorBody.message
      : defaultMessage(effectiveStatus, code)
  const traceId = typeof errorBody?.traceId === 'string' ? errorBody.traceId : null
  const error = new ApiError(effectiveStatus, code, message, errorBody?.details ?? null, traceId)
  if (effectiveStatus === 401 && path !== '/api/auth/login') unauthorizedHandler?.()
  return error
}

function networkError(): ApiError {
  return new ApiError(0, 'NETWORK_ERROR', '无法连接后端服务，请确认 PCL-DSH-Linux 服务已在本机 8080 端口启动。')
}

/**
 * Typed fetch wrapper shared by every API call.
 * - credentials: include, so the JSESSIONID cookie follows the SPA.
 * - Non-GET requests echo the XSRF-TOKEN cookie in X-XSRF-TOKEN (plan 4.4).
 * - A 401 (except from the login request itself) triggers the global handler.
 * - {@link requestDetailed} additionally exposes the HTTP status so callers can tell
 *   200 (synchronous resource) from 202 (background Job) on the same endpoint.
 */
export async function requestDetailed<T>(
  method: HttpMethod,
  path: string,
  body?: unknown,
  options: RequestOptions = {},
): Promise<DetailedResponse<T>> {
  const headers: Record<string, string> = { Accept: 'application/json' }
  const formData = options.formData
  // fetch sets the multipart boundary itself; never set Content-Type for FormData.
  if (body !== undefined && formData === undefined) headers['Content-Type'] = 'application/json'
  if (method !== 'GET') {
    const token = readCookie(CSRF_COOKIE)
    if (token) headers[CSRF_HEADER] = token
  }

  let response: Response
  try {
    response = await fetch(path, {
      method,
      headers,
      credentials: 'include',
      body: formData ?? (body !== undefined ? JSON.stringify(body) : undefined),
      signal: options.signal,
    })
  } catch (cause) {
    if (cause instanceof DOMException && cause.name === 'AbortError') throw cause
    throw networkError()
  }

  if (response.status === 204 || response.status === 205) {
    return { status: response.status, payload: undefined as T }
  }

  const contentType = response.headers.get('content-type') ?? ''
  let payload: unknown = null
  if (contentType.includes('application/json')) {
    try {
      payload = await response.json()
    } catch {
      payload = null
    }
  } else {
    const text = await response.text().catch(() => '')
    payload = text || null
  }

  if (!response.ok) throw buildApiError(response.status, payload, path)
  return { status: response.status, payload: payload as T }
}

export async function request<T>(
  method: HttpMethod,
  path: string,
  body?: unknown,
  options: RequestOptions = {},
): Promise<T> {
  return (await requestDetailed<T>(method, path, body, options)).payload
}

/**
 * Multipart upload through XMLHttpRequest so large pack previews get a real
 * progress percentage (fetch cannot report upload progress). Shares the same
 * CSRF header, credentials, error mapping and 401 handling as request().
 */
export function uploadForm<T>(path: string, file: File, options: UploadOptions = {}): Promise<T> {
  return new Promise<T>((resolve, reject) => {
    const xhr = new XMLHttpRequest()
    xhr.open('POST', path, true)
    xhr.withCredentials = true
    xhr.responseType = 'text'
    xhr.setRequestHeader('Accept', 'application/json')
    const token = readCookie(CSRF_COOKIE)
    if (token) xhr.setRequestHeader(CSRF_HEADER, token)

    let aborted = false
    const onAbort = (): void => {
      aborted = true
      xhr.abort()
    }
    if (options.signal) {
      if (options.signal.aborted) {
        reject(new DOMException('Aborted', 'AbortError'))
        return
      }
      options.signal.addEventListener('abort', onAbort, { once: true })
    }

    xhr.upload.onprogress = (event) => {
      if (!options.onProgress) return
      options.onProgress(event.lengthComputable ? Math.round((event.loaded / event.total) * 100) : null)
    }
    xhr.onerror = () => {
      options.signal?.removeEventListener('abort', onAbort)
      reject(networkError())
    }
    xhr.onabort = () => {
      options.signal?.removeEventListener('abort', onAbort)
      reject(new DOMException('Aborted', 'AbortError'))
    }
    xhr.onload = () => {
      options.signal?.removeEventListener('abort', onAbort)
      const contentType = xhr.getResponseHeader('content-type') ?? ''
      let payload: unknown = null
      if (xhr.responseText) {
        if (contentType.includes('application/json')) {
          try {
            payload = JSON.parse(xhr.responseText)
          } catch {
            payload = null
          }
        } else {
          payload = xhr.responseText
        }
      }
      if (xhr.status === 204 || xhr.status === 205) {
        resolve(undefined as T)
        return
      }
      if (xhr.status < 200 || xhr.status >= 300) {
        reject(buildApiError(xhr.status, payload, path))
        return
      }
      resolve(payload as T)
    }

    const formData = new FormData()
    formData.append('file', file)
    if (aborted) return
    xhr.send(formData)
  })
}
