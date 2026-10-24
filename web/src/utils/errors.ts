import { ApiError } from '@/api/client'

/** Chinese, action-specific error text; never invents server data. */
export function describeApiError(error: unknown, action: string): string {
  if (error instanceof ApiError) {
    if (error.status === 0 || error.code === 'NETWORK_ERROR') {
      return '无法连接后端服务，请确认 PCL-DSH-Linux 服务已在 127.0.0.1:8080 启动。'
    }
    if (error.status === 401) return '登录状态已失效，请重新登录。'
    if (error.status === 403) {
      if (error.code === 'PASSWORD_CHANGE_REQUIRED') return '首次登录需要先修改初始密码。'
      if (error.code === 'CSRF_INVALID') return 'CSRF 校验失败，请刷新页面后重试。'
      return error.message || '没有权限执行此操作。'
    }
    if (error.status === 404) return action + '接口尚未实现（后端开发中）。'
    if (error.status === 409) return error.message || '当前有任务正在执行，请等待完成或取消。'
    if (error.status === 422) return error.message || '当前版本或运行时不满足要求。'
    if (error.status === 423) return error.message || '尝试次数过多，请稍后再试。'
    if (error.status >= 500) return error.message || '后端服务异常（HTTP ' + String(error.status) + '），请查看服务日志。'
    return error.message || action + '失败（HTTP ' + String(error.status) + '）。'
  }
  if (error instanceof Error && error.message) return error.message
  return action + '失败，请稍后重试。'
}

export function backendLabel(errorStatus: number | null, hasData: boolean): string {
  if (errorStatus === 404 || errorStatus === 501) return '接口未就绪'
  if (errorStatus === 502 || errorStatus === 503) return '后端不可用'
  if (errorStatus !== null) return '后端异常'
  return hasData ? '后端已连接' : '正在连接'
}
