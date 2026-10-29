import { computed, ref } from 'vue'
import { useRouter } from 'vue-router'

import { instancesApi } from '@/api'
import type { InstanceSummary } from '@/api/types'
import { useJobStore } from '@/stores/jobs'
import { useNotifyStore } from '@/stores/notify'
import { useAppStateStore } from '@/stores/state'
import { displayPort, isRunningStatus } from '@/utils/display'
import { describeApiError } from '@/utils/errors'

/** Shared header/palette actions for the current instance. */
export function useInstanceActions() {
  const router = useRouter()
  const appState = useAppStateStore()
  const jobs = useJobStore()
  const notify = useNotifyStore()
  const pending = ref(false)

  const current = computed<InstanceSummary | null>(() => appState.currentInstance)
  const running = computed(() => current.value !== null && isRunningStatus(current.value.status))
  const runtimeInstalled = computed(() => {
    const instance = current.value
    if (!instance) return false
    return appState.runtimes.some((runtime) => runtime.installed && runtime.version === instance.version)
  })
  const webReady = computed(() => {
    const status = (current.value?.status ?? '').toLowerCase()
    return status === 'running' || status === '运行中' || status === '已启动'
  })
  const busy = computed(() => pending.value || jobs.active || appState.taskActive)

  const primaryLabel = computed(() => {
    if (jobs.active) return '任务进行中…'
    if (!current.value) return '创建实例'
    if (running.value) return '打开 Web 界面'
    return runtimeInstalled.value ? '启动实例' : '下载并启动'
  })

  const primaryHint = computed(() => {
    if (jobs.active) return jobs.progressText || '正在执行任务'
    if (!current.value) return '先创建一个实例'
    return current.value.name + ' · ' + displayPort(current.value)
  })

  async function selectInstance(id: string): Promise<void> {
    if (pending.value || id === appState.currentInstanceId) return
    pending.value = true
    try {
      await instancesApi.select(id)
      appState.setCurrentInstanceId(id)
      await appState.refresh()
      notify.success('已切换当前实例。')
    } catch (caught) {
      notify.error(describeApiError(caught, '切换实例'))
    } finally {
      pending.value = false
    }
  }

  async function launchCurrent(): Promise<void> {
    if (busy.value) return
    if (!current.value) {
      await router.push({ name: 'instances' })
      return
    }
    if (running.value) {
      await openWeb()
      return
    }
    pending.value = true
    try {
      const job = await instancesApi.launch(current.value.id)
      jobs.begin(job, { instanceId: current.value.id, version: current.value.version })
      notify.success('已提交任务：' + jobs.typeLabel + '（' + job.id + '）。')
      await appState.refresh()
    } catch (caught) {
      notify.error(describeApiError(caught, '启动实例'))
    } finally {
      pending.value = false
    }
  }

  async function stopCurrent(): Promise<void> {
    if (busy.value || !current.value) return
    pending.value = true
    try {
      await instancesApi.stop(current.value.id)
      notify.success('已停止实例。')
      await appState.refresh()
    } catch (caught) {
      notify.error(describeApiError(caught, '停止实例'))
    } finally {
      pending.value = false
    }
  }

  async function openWeb(): Promise<void> {
    if (busy.value || !current.value) return
    if (!webReady.value) {
      notify.info('网页服务尚未确认就绪，请等待启动完成或查看任务日志。')
      return
    }
    pending.value = true
    try {
      const result = await instancesApi.webUrl(current.value.id)
      if (!result.url) {
        notify.error('后端没有返回 Web 界面地址。')
        return
      }
      const opened = window.open(result.url, '_blank', 'noopener')
      if (!opened) notify.info('浏览器阻止了新窗口，请允许本页弹出窗口后重试。')
    } catch (caught) {
      notify.error(describeApiError(caught, '打开 Web 界面'))
    } finally {
      pending.value = false
    }
  }

  async function primary(): Promise<void> {
    if (jobs.active) {
      await router.push({ name: 'logs' })
      return
    }
    if (!current.value) {
      await router.push({ name: 'instances' })
      return
    }
    if (running.value) {
      await openWeb()
      return
    }
    await launchCurrent()
  }

  return {
    current,
    running,
    runtimeInstalled,
    webReady,
    busy,
    pending,
    primaryLabel,
    primaryHint,
    selectInstance,
    launchCurrent,
    stopCurrent,
    openWeb,
    primary,
  }
}
