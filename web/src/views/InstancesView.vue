<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'

import { instancesApi } from '@/api'
import type { CreateInstanceInput, InstanceSummary, UpdateInstanceInput } from '@/api/types'
import AppIcon from '@/components/AppIcon.vue'
import AppNotice from '@/components/AppNotice.vue'
import ConfirmDialog from '@/components/ConfirmDialog.vue'
import DirectAccessDialog from '@/components/DirectAccessDialog.vue'
import EmptyState from '@/components/EmptyState.vue'
import InstanceDialog from '@/components/InstanceDialog.vue'
import StatusBadge from '@/components/StatusBadge.vue'
import { useJobStore } from '@/stores/jobs'
import { useNotifyStore } from '@/stores/notify'
import { useAppStateStore } from '@/stores/state'
import { displayPort, isRunningStatus } from '@/utils/display'
import { describeApiError } from '@/utils/errors'

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
const directTarget = ref<InstanceSummary | null>(null)

const instances = computed(() => appState.instances)
const busy = computed(() => jobs.active || saving.value || deleting.value || pendingId.value !== null)
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
}

function closeDialog(): void {
  if (saving.value) return
  dialogMode.value = null
  editing.value = null
  dialogError.value = ''
}

async function createInstance(input: CreateInstanceInput): Promise<void> {
  if (saving.value) return
  saving.value = true
  dialogError.value = ''
  try {
    await instancesApi.create(input)
    notify.success('实例已创建。')
    dialogMode.value = null
    editing.value = null
    await appState.refresh()
  } catch (caught) {
    dialogError.value = describeApiError(caught, '创建实例')
  } finally {
    saving.value = false
  }
}

async function saveInstance(input: UpdateInstanceInput): Promise<void> {
  const target = editing.value
  if (saving.value || !target) return
  saving.value = true
  dialogError.value = ''
  try {
    // version/profile are immutable: only name/port are sent (a 409 would mean
    // the UI tried to change them, which the dialog prevents).
    await instancesApi.update(target.id, input)
    notify.success('实例设置已保存。')
    dialogMode.value = null
    editing.value = null
    await appState.refresh()
  } catch (caught) {
    dialogError.value = describeApiError(caught, '保存实例')
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
                title="远程完成 DeepSeek 网页登录（SSH 隧道 + 回环直连地址）"
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
      :installed-versions="installedVersions"
      :submitting="saving"
      :submit-error="dialogError"
      @create="createInstance"
      @save="saveInstance"
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
  </div>
</template>
