import type { JobState } from '@/api/types'

const TYPE_LABELS: Record<string, string> = {
  LAUNCH_INSTANCE: '启动实例',
  INSTALL_RUNTIME: '安装运行时',
  PROBE_RUNTIME: '检测运行时能力',
  COPY_INSTANCE: '复制实例',
  IMPORT_PACK: '导入整合包',
  EXPORT_PACK: '导出整合包',
  PLUGIN_MUTATE: '插件操作',
  PREVIEW_PACK: '检查整合包',
  GENERIC: '后台任务',
}

export function jobTypeLabel(type: string | null | undefined): string {
  if (!type) return '后台任务'
  return TYPE_LABELS[type] ?? type
}

export function jobStateLabel(state: JobState | '' | null | undefined): string {
  switch (state) {
    case 'RUNNING':
      return '进行中'
    case 'SUCCEEDED':
      return '已完成'
    case 'FAILED':
      return '未完成'
    case 'CANCELLED':
      return '已取消'
    default:
      return '无任务'
  }
}

export function emptyJobHint(): string {
  return '这里会显示安装、兼容性检测和实例运行日志。'
}
