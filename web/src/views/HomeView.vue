<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { useRouter } from 'vue-router'

import { instancesApi } from '@/api'
import type { InstanceInput } from '@/api/types'
import HomeLaunchPane from '@/components/HomeLaunchPane.vue'
import HomeTaskCard from '@/components/HomeTaskCard.vue'
import InstanceDialog from '@/components/InstanceDialog.vue'
import StatusBadge from '@/components/StatusBadge.vue'
import { useJobStore } from '@/stores/jobs'
import { useNotifyStore } from '@/stores/notify'
import { useAppStateStore } from '@/stores/state'
import { backgroundPosition, resolveBackground } from '@/utils/backgrounds'
import { displayPort, isRunningStatus } from '@/utils/display'
import { describeApiError } from '@/utils/errors'

const router = useRouter()
const appState = useAppStateStore()
const jobs = useJobStore()
const notify = useNotifyStore()

const pending = ref(false)
const dialogOpen = ref(false)
const saving = ref(false)
const dialogError = ref('')
const homeMessage = ref('')

const backgroundStyle = computed(() => {
  const background = appState.settings.background
  return {
    backgroundImage: 'url("' + resolveBackground(background) + '")',
    backgroundPosition: backgroundPosition(background),
  }
})
const current = computed(() => appState.currentInstance)
const running = computed(() => current.value !== null && isRunningStatus(current.value.status))
const webReady = computed(() => {
  const status = (current.value?.status ?? '').toLowerCase()
  return status === 'running' || status === '运行中' || status === '已启动'
})
const runtimeInstalled = computed(
  () =>
    current.value !== null &&
    appState.runtimes.some((item) => item.installed && item.version === current.value?.version),
)
const firstLaunchHint = computed(() => current.value !== null && !running.value && !runtimeInstalled.value)
const busy = computed(() => pending.value || jobs.active || saving.value)

const taskVisible = computed(() => jobs.active)
const taskPhase = computed(() => jobs.progressText)

const launchLabel = computed(() => {
  if (!current.value) return '创建实例'
  if (jobs.active && jobs.type === 'LAUNCH_INSTANCE') return '启动中…'
  if (running.value) return '打开 Web 界面'
  return runtimeInstalled.value ? '启动实例' : '下载并启动'
})
const launchSub = computed(() => {
  if (!current.value) return ''
  if (jobs.active && jobs.type === 'LAUNCH_INSTANCE') return jobs.progressText
  return current.value.name
})

const installedVersions = computed(() =>
  appState.runtimes.filter((runtime) => runtime.installed).map((runtime) => runtime.version),
)

onMounted(() => {
  void appState.refresh()
})

function goLogs(): void {
  void router.push({ name: 'logs' })
}

function openCreate(): void {
  if (busy.value) return
  dialogOpen.value = true
  dialogError.value = ''
}

async function submitCreate(input: InstanceInput): Promise<void> {
  if (saving.value) return
  saving.value = true
  dialogError.value = ''
  const hadCurrent = appState.currentInstanceId !== null
  try {
    const created = await instancesApi.create(input)
    if (!hadCurrent && created) {
      // First instance: select it so the home launch button becomes usable immediately.
      try {
        await instancesApi.select(created.id)
      } catch {
        // Selection is a convenience; the instance list offers it explicitly.
      }
    }
    notify.success('实例已创建。')
    dialogOpen.value = false
    await appState.refresh()
  } catch (caught) {
    dialogError.value = describeApiError(caught, '创建实例')
  } finally {
    saving.value = false
  }
}

async function handleLaunch(): Promise<void> {
  if (busy.value) return
  homeMessage.value = ''
  if (!current.value) {
    openCreate()
    return
  }
  if (running.value) {
    await openWeb()
    return
  }
  pending.value = true
  try {
    const job = await instancesApi.launch(current.value.id)
    jobs.begin(job, { instanceId: current.value.id })
    notify.success('已提交任务：' + jobs.typeLabel + '（' + job.id + '）。')
    await appState.refresh()
  } catch (caught) {
    homeMessage.value = describeApiError(caught, '启动实例')
    notify.error(homeMessage.value)
  } finally {
    pending.value = false
  }
}

async function openWeb(): Promise<void> {
  if (!current.value || busy.value) return
  if (!webReady.value) {
    homeMessage.value = '网页服务尚未确认就绪，请等待启动完成或查看任务日志。'
    notify.info(homeMessage.value)
    return
  }
  pending.value = true
  homeMessage.value = ''
  try {
    const result = await instancesApi.webUrl(current.value.id)
    if (!result.url) {
      homeMessage.value = '后端没有返回 Web 界面地址。'
      notify.error(homeMessage.value)
      return
    }
    const opened = window.open(result.url, '_blank', 'noopener')
    if (!opened) notify.info('浏览器阻止了新窗口，请允许本页弹出窗口后重试。')
  } catch (caught) {
    homeMessage.value = describeApiError(caught, '打开 Web 界面')
    notify.error(homeMessage.value)
  } finally {
    pending.value = false
  }
}

async function stopInstance(): Promise<void> {
  if (!current.value || busy.value) return
  pending.value = true
  homeMessage.value = ''
  try {
    await instancesApi.stop(current.value.id)
    notify.success('已停止实例。')
    await appState.refresh()
  } catch (caught) {
    homeMessage.value = describeApiError(caught, '停止实例')
    notify.error(homeMessage.value)
  } finally {
    pending.value = false
  }
}

async function selectInstance(id: string): Promise<void> {
  if (id === appState.currentInstanceId || busy.value) return
  try {
    await instancesApi.select(id)
    appState.setCurrentInstanceId(id)
    await appState.refresh()
    notify.success('已切换当前实例。')
  } catch (caught) {
    notify.error(describeApiError(caught, '切换实例'))
  }
}
</script>

<template>
  <div class="home-page" data-testid="page-home" :style="backgroundStyle">
    <div class="home-page__shade" aria-hidden="true"></div>

    <section
      v-if="current && !taskVisible"
      class="card content-card home-instance-card"
      data-testid="home-current-instance"
    >
      <div class="home-instance-card__heading">
        <span class="section-title">当前实例</span>
        <StatusBadge :status="current.status" />
      </div>
      <p class="home-instance-card__name" data-testid="home-instance-name">{{ current.name }}</p>
      <p class="home-instance-card__meta" data-testid="home-instance-meta">
        Harness {{ current.version || '—' }} · {{ current.profile }} · 端口 {{ displayPort(current) }}
      </p>
    </section>

    <div v-if="taskVisible" class="home-task-slot">
      <HomeTaskCard
        :phase="taskPhase"
        :started-at="jobs.elapsedBase"
        :type-label="jobs.typeLabel"
        :job-id="jobs.jobId"
        :logs-tail="jobs.logTail"
        @view="goLogs"
      />
    </div>

    <div v-if="firstLaunchHint" class="first-launch-note card content-card" data-testid="first-launch-hint">
      <p>首次启动将下载 Harness 和依赖。</p>
      <p>下载阶段和已用时会显示在这里，可查看任务或取消。</p>
    </div>

    <button
      v-if="running"
      class="btn btn-secondary home-stop"
      type="button"
      data-testid="home-stop"
      :disabled="busy || appState.taskActive"
      @click="stopInstance"
    >
      停止
    </button>

    <div class="home-launch">
      <HomeLaunchPane
        :instances="appState.instances"
        :current-id="appState.currentInstanceId"
        :launch-label="launchLabel"
        :launch-sub="launchSub"
        :busy="busy || appState.taskActive"
        @launch="handleLaunch"
        @select="selectInstance"
      />
    </div>

    <div v-if="appState.error" class="home-message card content-card" role="alert">
      <p class="paragraph">{{ appState.error }}</p>
      <div class="toolbar">
        <button class="btn btn-secondary btn--small" type="button" @click="appState.refresh()">
          重试读取工作空间
        </button>
      </div>
    </div>
    <div v-else-if="homeMessage" class="home-message card content-card" role="alert">
      <p class="paragraph">{{ homeMessage }}</p>
    </div>

    <p v-if="!current && !appState.loading" class="first-launch-note">
      当前工作空间还没有实例。点击右下角“创建实例”开始。
    </p>

    <InstanceDialog
      v-if="dialogOpen"
      mode="create"
      :instance="null"
      :accounts="appState.accounts"
      :installed-versions="installedVersions"
      :submitting="saving"
      :submit-error="dialogError"
      @submit="submitCreate"
      @close="dialogOpen = false"
    />
  </div>
</template>
