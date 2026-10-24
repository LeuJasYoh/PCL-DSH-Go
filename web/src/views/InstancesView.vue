<script setup lang="ts">
import { computed, onMounted, ref, watch } from 'vue'

import { ApiError, instancesApi } from '@/api'
import { isRecord } from '@/api/normalize'
import type { InstanceInput, InstanceSummary } from '@/api/types'
import AppIcon from '@/components/AppIcon.vue'
import AppNotice from '@/components/AppNotice.vue'
import BaseModal from '@/components/BaseModal.vue'
import ConfirmDialog from '@/components/ConfirmDialog.vue'
import DirectAccessDialog from '@/components/DirectAccessDialog.vue'
import EmptyState from '@/components/EmptyState.vue'
import InstanceDialog from '@/components/InstanceDialog.vue'
import StatusBadge from '@/components/StatusBadge.vue'
import TaskProgressStrip from '@/components/TaskProgressStrip.vue'
import { useJobStore } from '@/stores/jobs'
import { useNotifyStore } from '@/stores/notify'
import { useAppStateStore } from '@/stores/state'
import { displayPort, isRunningStatus } from '@/utils/display'
import { describeApiError } from '@/utils/errors'
import { validateInstanceName } from '@/utils/validation'

const appState = useAppStateStore()
const jobs = useJobStore()
const notify = useNotifyStore()

const pendingId = ref<string | null>(null)
const saving = ref(false)
const dialogMode = ref<'create' | 'edit' | null>(null)
const editing = ref<InstanceSummary | null>(null)
const dialogError = ref('')
const deleteTarget = ref<InstanceSummary | null>(null)
const deleting = ref(false)
const readonlyVersionProfile = ref(false)

const directTarget = ref<InstanceSummary | null>(null)

const copyTarget = ref<InstanceSummary | null>(null)
const copyName = ref('')
const copyError = ref('')
const copying = ref(false)
let handledCopyJob: string | null = null

const instances = computed(() => appState.instances)
const busy = computed(
  () => jobs.active || saving.value || deleting.value || copying.value || pendingId.value !== null,
)
const installedVersions = computed(() =>
  appState.runtimes.filter((runtime) => runtime.installed).map((runtime) => runtime.version),
)

onMounted(() => {
  void appState.refresh()
})

function installed(instance: InstanceSummary): boolean {
  return appState.runtimes.some((runtime) => runtime.installed && runtime.version === instance.version)
}

/** "打开 Web" is only enabled after the backend confirmed the web service is ready. */
function webReady(instance: InstanceSummary): boolean {
  const status = (instance.status || '').toLowerCase()
  return status === 'running' || status === '运行中' || status === '已启动'
}

function openCreate(): void {
  if (busy.value) return
  dialogMode.value = 'create'
  editing.value = null
  dialogError.value = ''
}

function openEdit(instance: InstanceSummary): void {
  if (busy.value) return
  dialogMode.value = 'edit'
  editing.value = instance
  dialogError.value = ''
  readonlyVersionProfile.value = false
}

function closeDialog(): void {
  if (saving.value) return
  dialogMode.value = null
  editing.value = null
  dialogError.value = ''
  readonlyVersionProfile.value = false
}

// COPY_INSTANCE also backs "edit version/profile": the original instance is kept and
// a new instance is created; select it after the Job finishes.
watch(
  [() => jobs.state, () => jobs.result, () => jobs.type],
  () => {
    if (jobs.state !== 'SUCCEEDED' || jobs.type !== 'COPY_INSTANCE' || !jobs.jobId) return
    if (handledCopyJob === jobs.jobId) return
    const result = isRecord(jobs.result) ? jobs.result : null
    if (!result || typeof result.instanceId !== 'string') return
    handledCopyJob = jobs.jobId
    const instanceId = result.instanceId
    const name = typeof result.name === 'string' ? result.name : ''
    void finishCopy(instanceId, name)
  },
)

async function finishCopy(instanceId: string, name: string): Promise<void> {
  await appState.refresh()
  try {
    await instancesApi.select(instanceId)
    appState.setCurrentInstanceId(instanceId)
  } catch {
    // The new instance is still listed if selecting it failed.
  }
  await appState.refresh()
  notify.success('复制完成' + (name ? '：' + name : '') + '，已选择新实例。')
}

async function submitDialog(input: InstanceInput): Promise<void> {
  if (saving.value || dialogMode.value === null) return
  saving.value = true
  dialogError.value = ''
  const creating = dialogMode.value === 'create'
  const target = editing.value
  try {
    if (creating) {
      await instancesApi.create(input)
      notify.success('实例已创建。')
    } else if (target) {
      const versionChanged = input.version !== target.version
      const profileChanged = input.profile !== target.profile
      if (versionChanged || profileChanged) {
        const result = await instancesApi.save(target.id, input)
        if (result.job) {
          handledCopyJob = null
          jobs.begin(result.job, { instanceId: target.id, version: input.version })
          notify.success('已提交任务：' + jobs.typeLabel + '（保留原实例，完成后自动选择新实例）。')
          dialogMode.value = null
          editing.value = null
          await appState.refresh()
          return
        }
      } else {
        await instancesApi.update(target.id, input)
      }
      notify.success('实例设置已保存。')
    }
    dialogMode.value = null
    editing.value = null
    await appState.refresh()
  } catch (caught) {
    if (
      caught instanceof ApiError &&
      caught.status === 409 &&
      (caught.code === 'CONFLICT_UNSUPPORTED_CHANGE' || caught.code.includes('UNSUPPORTED'))
    ) {
      // Older backend: fall back to read-only version/profile instead of a generic error.
      readonlyVersionProfile.value = true
      dialogError.value =
        '当前后端暂不支持修改 Harness 版本或 profile；已切换为只读，可继续修改名称、端口与账户。'
    } else {
      dialogError.value = describeApiError(caught, creating ? '创建实例' : '保存实例')
    }
  } finally {
    saving.value = false
  }
}

async function selectInstance(instance: InstanceSummary): Promise<void> {
  if (busy.value || instance.id === appState.currentInstanceId) return
  pendingId.value = instance.id
  try {
    await instancesApi.select(instance.id)
    appState.setCurrentInstanceId(instance.id)
    await appState.refresh()
    notify.success('已切换当前实例。')
  } catch (caught) {
    notify.error(describeApiError(caught, '切换实例'))
  } finally {
    pendingId.value = null
  }
}

async function launchInstance(instance: InstanceSummary): Promise<void> {
  if (busy.value) return
  pendingId.value = instance.id
  try {
    const job = await instancesApi.launch(instance.id)
    jobs.begin(job, { instanceId: instance.id })
    notify.success('已提交任务：' + jobs.typeLabel + '（' + job.id + '）。')
    await appState.refresh()
  } catch (caught) {
    notify.error(describeApiError(caught, '启动实例'))
  } finally {
    pendingId.value = null
  }
}

async function stopInstance(instance: InstanceSummary): Promise<void> {
  if (busy.value) return
  pendingId.value = instance.id
  try {
    await instancesApi.stop(instance.id)
    notify.success('已停止实例。')
    await appState.refresh()
  } catch (caught) {
    notify.error(describeApiError(caught, '停止实例'))
  } finally {
    pendingId.value = null
  }
}

async function openWeb(instance: InstanceSummary): Promise<void> {
  if (busy.value) return
  if (!webReady(instance)) {
    notify.info('网页服务尚未确认就绪，请等待启动完成或查看任务日志。')
    return
  }
  pendingId.value = instance.id
  try {
    const result = await instancesApi.webUrl(instance.id)
    if (!result.url) {
      notify.error('后端没有返回 Web 界面地址。')
      return
    }
    const opened = window.open(result.url, '_blank', 'noopener')
    if (!opened) notify.info('浏览器阻止了新窗口，请允许本页弹出窗口后重试。')
  } catch (caught) {
    notify.error(describeApiError(caught, '打开 Web 界面'))
  } finally {
    pendingId.value = null
  }
}

function openDirectAccess(instance: InstanceSummary): void {
  if (busy.value) return
  directTarget.value = instance
}

function askDelete(instance: InstanceSummary): void {
  if (busy.value) return
  deleteTarget.value = instance
}

async function confirmDelete(): Promise<void> {
  const target = deleteTarget.value
  if (!target || deleting.value) return
  deleting.value = true
  try {
    const result = await instancesApi.remove(target.id)
    notify.success(
      result.trashedPath
        ? '实例已移入回收目录：' + result.trashedPath
        : '实例已移入回收目录，可从数据目录下的 trash 恢复。',
    )
    deleteTarget.value = null
    await appState.refresh()
  } catch (caught) {
    // Close the confirmation on failure so the page is not blocked by a stale dialog.
    deleteTarget.value = null
    notify.error(describeApiError(caught, '删除实例'))
  } finally {
    deleting.value = false
  }
}

function openCopy(instance: InstanceSummary): void {
  if (busy.value) return
  copyTarget.value = instance
  copyName.value = instance.name + ' 副本'
  copyError.value = ''
}

function closeCopy(): void {
  if (copying.value) return
  copyTarget.value = null
  copyError.value = ''
}

async function confirmCopy(): Promise<void> {
  const target = copyTarget.value
  if (!target || copying.value) return
  copyError.value = ''
  const name = copyName.value.trim()
  const nameError = validateInstanceName(name)
  if (nameError) {
    copyError.value = nameError
    return
  }
  if (jobs.active) {
    copyError.value = '当前有任务正在执行，请等待完成或取消。'
    return
  }
  copying.value = true
  try {
    const job = await instancesApi.copy(target.id, name)
    handledCopyJob = null
    jobs.begin(job, { instanceId: target.id })
    notify.success('已提交任务：' + jobs.typeLabel + '（' + job.id + '）。')
    copyTarget.value = null
  } catch (caught) {
    copyError.value = describeApiError(caught, '复制实例')
  } finally {
    copying.value = false
  }
}
</script>

<template>
  <div class="page page--solid" data-testid="page-instances">
    <div class="card-list">
      <div class="toolbar">
        <span class="section-title">{{ instances.length }} 个实例</span>
        <span class="toolbar__spacer"></span>
        <button
          class="btn btn-primary"
          type="button"
          data-testid="instance-create"
          :disabled="busy"
          @click="openCreate"
        >
          <AppIcon name="plus" :size="15" />创建实例
        </button>
      </div>

      <TaskProgressStrip />

      <div v-if="appState.loading && appState.lastUpdatedAt === null" class="card card--flat">
        <div class="loading-row"><span class="spinner"></span>正在读取实例列表…</div>
      </div>

      <AppNotice v-else-if="appState.error" title="加载失败" tone="warn">
        <p class="paragraph">{{ appState.error }}</p>
        <div class="toolbar">
          <button class="btn btn-secondary btn--small" type="button" @click="appState.refresh()">
            重试读取工作空间
          </button>
          <span class="small-muted">
            当前实例列表来自 GET /api/state{{ appState.backendState === 'not-implemented' ? '（后端尚未就绪）' : '' }}
          </span>
        </div>
      </AppNotice>

      <template v-else>
        <EmptyState
          v-if="instances.length === 0"
          title="还没有实例"
          description="创建独立的配置空间，为它选择 Harness 版本与 profile。"
          icon="layers"
        >
          <button class="btn btn-primary" type="button" data-testid="instance-create-empty" :disabled="busy" @click="openCreate">
            <AppIcon name="plus" :size="15" />创建实例
          </button>
        </EmptyState>

        <div v-else class="card-list" data-testid="instance-list">
          <div
            v-for="instance in instances"
            :key="instance.id"
            class="card instance-card"
            :class="{ 'is-selected': instance.id === appState.currentInstanceId }"
            :data-testid="'instance-' + instance.id"
          >
            <div class="instance-card__info">
              <div class="instance-card__heading">
                <span class="instance-card__name" :title="instance.name">{{ instance.name }}</span>
                <StatusBadge :status="instance.status" />
                <span v-if="instance.id === appState.currentInstanceId" class="badge badge--blue">当前</span>
              </div>
              <p class="instance-card__meta">
                Harness {{ instance.version || '—' }} · {{ instance.profile }} · 端口 {{ displayPort(instance) }}
              </p>
              <p v-if="instance.path" class="path-label" :title="instance.path">{{ instance.path }}</p>
            </div>
            <div class="instance-card__actions">
              <button
                v-if="instance.id !== appState.currentInstanceId"
                class="btn btn-secondary"
                type="button"
                :disabled="busy"
                :data-testid="'instance-select-' + instance.id"
                @click="selectInstance(instance)"
              >
                <AppIcon name="check" :size="15" />选择
              </button>
              <button
                v-if="isRunningStatus(instance.status)"
                class="btn btn-primary"
                type="button"
                :disabled="busy"
                :data-testid="'instance-stop-' + instance.id"
                @click="stopInstance(instance)"
              >
                <AppIcon name="stop" :size="15" />停止
              </button>
              <button
                v-else
                class="btn btn-primary"
                type="button"
                :disabled="busy"
                :data-testid="'instance-launch-' + instance.id"
                @click="launchInstance(instance)"
              >
                <AppIcon name="play" :size="15" />{{ installed(instance) ? '启动' : '下载并启动' }}
              </button>
              <button
                class="btn btn-secondary"
                type="button"
                :disabled="busy || !webReady(instance)"
                :title="webReady(instance) ? '在新标签页打开 Web 界面' : '网页服务尚未确认就绪'"
                :data-testid="'instance-web-' + instance.id"
                @click="openWeb(instance)"
              >
                <AppIcon name="external" :size="15" />打开 Web
              </button>
              <button
                class="btn btn-secondary"
                type="button"
                :disabled="busy"
                title="远程完成 DeepSeek 账号网页登录（SSH 隧道 + 回环直连地址）"
                :data-testid="'instance-direct-' + instance.id"
                @click="openDirectAccess(instance)"
              >
                <AppIcon name="terminal" :size="15" />直连登录
              </button>
              <button
                class="btn btn-secondary"
                type="button"
                :disabled="busy"
                :data-testid="'instance-edit-' + instance.id"
                @click="openEdit(instance)"
              >
                <AppIcon name="settings" :size="15" />编辑
              </button>
              <button
                class="btn btn-subtle"
                type="button"
                :disabled="busy"
                title="复制实例（保留原实例，新实例自动分配端口）"
                :data-testid="'instance-copy-' + instance.id"
                @click="openCopy(instance)"
              >
                <AppIcon name="copy" :size="15" />复制
              </button>
              <button
                class="btn btn-subtle btn--danger"
                type="button"
                :disabled="busy"
                :data-testid="'instance-delete-' + instance.id"
                @click="askDelete(instance)"
              >
                <AppIcon name="trash" :size="15" />删除
              </button>
            </div>
          </div>
        </div>
      </template>
    </div>

    <DirectAccessDialog v-if="directTarget" :instance="directTarget" @close="directTarget = null" />

    <InstanceDialog
      v-if="dialogMode"
      :mode="dialogMode"
      :instance="editing"
      :accounts="appState.accounts"
      :installed-versions="installedVersions"
      :submitting="saving"
      :submit-error="dialogError"
      :readonly-version-profile="readonlyVersionProfile"
      @submit="submitDialog"
      @close="closeDialog"
    />

    <ConfirmDialog
      v-if="deleteTarget"
      title="删除实例"
      :question="'将「' + deleteTarget.name + '」移入回收目录？'"
      :detail="'实例配置、插件和日志会一同移入数据目录下的 trash，可从该目录恢复。\n' + (deleteTarget.path ?? '')"
      confirm-text="确认删除"
      :busy="deleting"
      @confirm="confirmDelete"
      @cancel="deleteTarget = null"
    />

    <BaseModal
      v-if="copyTarget"
      title="复制实例"
      testid="copy-dialog"
      :closable="!copying"
      @close="closeCopy"
    >
      <p class="paragraph">
        复制「{{ copyTarget.name }}」当前 profile（{{ copyTarget.profile }}）的可分享配置与插件；
        凭据、会话和可能含密钥的 patch 配置留在原实例。新实例会自动分配端口并成为当前实例。
      </p>
      <div class="form-field">
        <label class="field-label" for="copy-name">新实例名称</label>
        <input
          id="copy-name"
          v-model="copyName"
          class="input"
          data-testid="copy-name"
          :disabled="copying"
          placeholder="新实例名称"
          autofocus
        />
      </div>
      <p v-if="copyError" class="validation-error" data-testid="copy-error" role="alert">{{ copyError }}</p>
      <template #footer>
        <button class="btn btn-secondary" type="button" :disabled="copying" @click="closeCopy">取消</button>
        <button class="btn btn-primary" type="button" data-testid="copy-confirm" :disabled="copying" @click="confirmCopy">
          {{ copying ? '正在提交…' : '复制实例' }}
        </button>
      </template>
    </BaseModal>
  </div>
</template>
