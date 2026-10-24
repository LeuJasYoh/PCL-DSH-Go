<script setup lang="ts">
import { computed, onMounted, ref, watch } from 'vue'

import { ApiError, pluginsApi } from '@/api'
import type { InstalledPluginDto } from '@/api/types'
import AppIcon from '@/components/AppIcon.vue'
import AppNotice from '@/components/AppNotice.vue'
import ConfirmDialog from '@/components/ConfirmDialog.vue'
import EmptyState from '@/components/EmptyState.vue'
import TaskProgressStrip from '@/components/TaskProgressStrip.vue'
import { useJobStore } from '@/stores/jobs'
import { useNotifyStore } from '@/stores/notify'
import { useAppStateStore } from '@/stores/state'
import { describeApiError } from '@/utils/errors'

/**
 * Installed plugins of one instance/profile (M3 API): list, add spec, update
 * in place and remove. Mutations are 202 PLUGIN_MUTATE Jobs; synchronous 400
 * VALIDATION (bad spec/profile) and 409 (instance running) are shown inline,
 * Job-level failures are shown in the task strip/log page and as a toast.
 */
const appState = useAppStateStore()
const jobs = useJobStore()
const notify = useNotifyStore()

const selectedId = ref<string | null>(null)
const selectedProfile = ref('')
const plugins = ref<InstalledPluginDto[]>([])
const loading = ref(false)
const loadedOnce = ref(false)
const loadError = ref('')
const loadErrorStatus = ref<number | null>(null)

const spec = ref('')
const validationError = ref('')
const submitting = ref(false)

const removeTarget = ref<InstalledPluginDto | null>(null)
const mutating = ref(false)

const instances = computed(() => appState.instances)
const selected = computed(() => instances.value.find((item) => item.id === selectedId.value) ?? null)
const profileOptions = computed(() => {
  const instance = selected.value
  if (!instance) return []
  const options = instance.profiles?.length ? instance.profiles : [instance.profile]
  return Array.from(new Set(options.filter((profile) => profile)))
})
const busy = computed(() => jobs.active || loading.value || submitting.value || mutating.value)
const selectedProfileLabel = computed(() => {
  const instance = selected.value
  if (!instance) return ''
  return selectedProfile.value || instance.profile
})

let lastLoadedKey = ''

onMounted(() => {
  void appState.refresh()
  selectedId.value = appState.currentInstanceId ?? appState.instances[0]?.id ?? null
  const instance = selected.value
  if (instance) selectedProfile.value = instance.profile
  if (selectedId.value) void loadPlugins()
})

watch(
  [() => appState.instances, () => appState.currentInstanceId],
  () => {
    if (selectedId.value && instances.value.some((item) => item.id === selectedId.value)) return
    selectedId.value = appState.currentInstanceId ?? instances.value[0]?.id ?? null
  },
)

watch(selectedId, () => {
  const instance = selected.value
  selectedProfile.value = instance?.profile ?? ''
  void loadPlugins()
})

watch(selectedProfile, () => {
  void loadPlugins()
})

// Reload after any plugin mutation job settles (the backend has finished by then).
watch(
  () => jobs.state,
  (next) => {
    if (jobs.type !== 'PLUGIN_MUTATE') return
    if (next === 'SUCCEEDED' || next === 'FAILED' || next === 'CANCELLED') {
      void loadPlugins()
    }
  },
)

// Job-level failures (e.g. pnpm/CLI VALIDATION) surface here as a toast too.
watch(
  () => jobs.state,
  (next) => {
    if (jobs.type !== 'PLUGIN_MUTATE') return
    if (next !== 'FAILED' && next !== 'CANCELLED') return
    const message = jobs.error?.message || (next === 'CANCELLED' ? '任务已取消。' : '插件操作未完成，请查看任务与日志页。')
    notify.error(message)
  },
)

async function loadPlugins(): Promise<void> {
  const instance = selected.value
  if (!instance) {
    plugins.value = []
    loadedOnce.value = true
    return
  }
  const profile = selectedProfile.value || instance.profile
  const key = instance.id + '|' + profile
  if (loading.value && key === lastLoadedKey) return
  lastLoadedKey = key
  loading.value = true
  loadError.value = ''
  loadErrorStatus.value = null
  try {
    plugins.value = await pluginsApi.list(instance.id, profile)
    loadedOnce.value = true
  } catch (caught) {
    loadError.value = describeApiError(caught, '读取插件列表')
    loadErrorStatus.value = caught instanceof ApiError ? caught.status : null
    plugins.value = []
    loadedOnce.value = true
  } finally {
    loading.value = false
  }
}

interface SourceBadge {
  label: string
  className: string
}

function sourceBadge(plugin: InstalledPluginDto): SourceBadge {
  const source = (plugin.source || '').trim()
  if (source === '官方') return { label: '官方', className: 'badge--green' }
  if (source === '内置') return { label: '内置', className: 'badge--blue' }
  if (source === '本地') return { label: '本地', className: 'badge--gray' }
  if (source === '社区') return { label: '社区', className: 'badge--gray' }
  if (!source) return { label: '未知来源', className: 'badge--gray' }
  return { label: source, className: 'badge--gray' }
}

function needsRestore(plugin: InstalledPluginDto): boolean {
  return (plugin.version || '').trim() === '待恢复依赖'
}

async function installPlugin(): Promise<void> {
  const instance = selected.value
  if (!instance) {
    notify.info('请先选择一个实例，插件安装在实例中。')
    return
  }
  if (jobs.active) {
    notify.info('当前有任务正在执行，请等待完成或取消。')
    return
  }
  const value = spec.value.trim()
  if (!value) {
    validationError.value = '请输入插件名称或 npm 包规格。'
    return
  }
  if (value.startsWith('-') || /[\u0000-\u001f]/.test(value)) {
    validationError.value = '插件规格不能以连字符开头，也不能包含控制字符。'
    return
  }
  validationError.value = ''
  submitting.value = true
  try {
    const job = await pluginsApi.add(instance.id, value, selectedProfileLabel.value)
    jobs.begin(job, { instanceId: instance.id })
    spec.value = ''
    notify.success('已提交任务：' + jobs.typeLabel + '（' + job.id + '）。')
  } catch (caught) {
    validationError.value = describeApiError(caught, '安装插件')
  } finally {
    submitting.value = false
  }
}

async function updatePlugin(plugin: InstalledPluginDto): Promise<void> {
  const instance = selected.value
  if (!instance || jobs.active || mutating.value) return
  mutating.value = true
  try {
    const job = await pluginsApi.update(instance.id, plugin.name, selectedProfileLabel.value)
    jobs.begin(job, { instanceId: instance.id })
    notify.success('已提交任务：更新插件 ' + plugin.name + '（' + job.id + '）。')
  } catch (caught) {
    notify.error(describeApiError(caught, '更新插件'))
  } finally {
    mutating.value = false
  }
}

async function confirmRemove(): Promise<void> {
  const instance = selected.value
  const plugin = removeTarget.value
  if (!instance || !plugin || jobs.active || mutating.value) return
  mutating.value = true
  try {
    const job = await pluginsApi.remove(instance.id, plugin.name, selectedProfileLabel.value)
    jobs.begin(job, { instanceId: instance.id })
    removeTarget.value = null
    notify.success('已提交任务：移除插件 ' + plugin.name + '（' + job.id + '）。')
  } catch (caught) {
    removeTarget.value = null
    notify.error(describeApiError(caught, '移除插件'))
  } finally {
    mutating.value = false
  }
}
</script>

<template>
  <div class="page page--solid" data-testid="page-plugins">
    <div class="card-list">
      <div class="toolbar">
        <span class="section-title">已装插件</span>
        <span v-if="selected" class="small-muted">当前 profile · {{ selectedProfileLabel }}</span>
        <span class="toolbar__spacer"></span>
        <select
          v-model="selectedId"
          class="input plugin-instance-select"
          data-testid="plugin-instance-select"
          aria-label="选择实例"
          :disabled="busy"
        >
          <option :value="null" disabled>选择实例</option>
          <option v-for="instance in instances" :key="instance.id" :value="instance.id">
            {{ instance.name }} · {{ instance.profile }}
          </option>
        </select>
        <select
          v-if="selected && profileOptions.length > 1"
          v-model="selectedProfile"
          class="input"
          data-testid="plugin-profile-select"
          aria-label="选择 profile"
          :disabled="busy"
        >
          <option v-for="profile in profileOptions" :key="profile" :value="profile">{{ profile }}</option>
        </select>
        <button
          class="btn btn-secondary"
          type="button"
          data-testid="plugin-refresh"
          :disabled="busy || !selected"
          @click="loadPlugins"
        >
          <AppIcon name="refresh" :size="15" />刷新
        </button>
      </div>

      <AppNotice title="插件与 profile" tone="info">
        <p class="paragraph">
          插件由当前实例的 Harness 管理，安装到所选 profile。支持 npm 包名@版本、GitHub 仓库或本地 file: 路径；
          本地 file: 路径会锚定到实例 workspace，请填写其中的相对路径。插件操作前需要实例已停止。
        </p>
      </AppNotice>

      <TaskProgressStrip />

      <EmptyState
        v-if="instances.length === 0"
        title="先创建一个实例"
        description="插件安装在实例中。创建实例后即可管理它的插件。"
        icon="puzzle"
      />

      <template v-else>
        <section class="card content-card">
          <p class="card-title">安装插件</p>
          <p class="paragraph">
            插件名称或 npm 包规格，例如 @scope/plugin@1.2.3；也可填写 GitHub 仓库或本地 file: 相对路径（相对实例 workspace）。
          </p>
          <div class="inline-field">
            <input
              v-model="spec"
              class="input"
              data-testid="plugin-spec"
              placeholder="插件名称或 npm 包规格"
              :disabled="busy || !selected"
              @keyup.enter="installPlugin"
            />
            <button
              class="btn btn-primary"
              type="button"
              data-testid="plugin-add"
              :disabled="busy || !selected"
              @click="installPlugin"
            >
              <AppIcon name="plus" :size="15" />安装插件
            </button>
          </div>
          <p v-if="validationError" class="validation-error" data-testid="plugin-validation" role="alert">
            {{ validationError }}
          </p>
        </section>

        <div v-if="loading && !loadedOnce" class="card card--flat">
          <div class="loading-row"><span class="spinner"></span>正在读取插件列表…</div>
        </div>

        <AppNotice v-else-if="loadError" title="插件列表读取失败" tone="warn">
          <p class="paragraph">{{ loadError }}</p>
          <div class="toolbar">
            <button class="btn btn-secondary btn--small" type="button" :disabled="busy" @click="loadPlugins">重试</button>
          </div>
        </AppNotice>

        <EmptyState
          v-else-if="plugins.length === 0"
          title="暂无插件记录"
          description="点击刷新读取当前实例 profile，或安装一个插件。"
          icon="puzzle"
        />

        <div v-else class="card-list" data-testid="plugin-list">
          <div v-for="plugin in plugins" :key="plugin.name" class="card plugin-card" :data-testid="'plugin-' + plugin.name">
            <div class="plugin-card__info">
              <div class="plugin-card__heading">
                <span class="card-title">{{ plugin.name }}</span>
                <span class="badge" :class="sourceBadge(plugin).className">{{ sourceBadge(plugin).label }}</span>
                <span
                  v-if="plugin.version"
                  class="badge"
                  :class="needsRestore(plugin) ? 'badge--red' : 'badge--blue'"
                  :data-testid="'plugin-version-' + plugin.name"
                >
                  {{ plugin.version }}
                </span>
              </div>
              <p v-if="plugin.description" class="paragraph">{{ plugin.description }}</p>
              <p v-if="needsRestore(plugin)" class="form-hint" :data-testid="'plugin-restore-' + plugin.name">
                该依赖尚未安装到 profile；按需在启动时恢复，或使用“更新”重新安装。
              </p>
              <p class="small-muted">{{ plugin.version || '版本未知' }} · {{ plugin.source || '来源未知' }}</p>
            </div>
            <div class="instance-card__actions">
              <button
                class="btn btn-secondary"
                type="button"
                :disabled="busy"
                :data-testid="'plugin-update-' + plugin.name"
                @click="updatePlugin(plugin)"
              >
                <AppIcon name="refresh" :size="15" />更新
              </button>
              <button
                class="btn btn-subtle btn--danger"
                type="button"
                :disabled="busy"
                :data-testid="'plugin-remove-' + plugin.name"
                @click="removeTarget = plugin"
              >
                <AppIcon name="trash" :size="15" />移除
              </button>
            </div>
          </div>
        </div>
      </template>
    </div>

    <ConfirmDialog
      v-if="removeTarget"
      title="移除插件"
      :question="'从「' + (selected?.name ?? '当前实例') + '」移除 ' + removeTarget.name + '？'"
      detail="这会改变当前 profile 的插件配置；操作作为后台任务执行，进度会显示在任务与日志页。内置 bundles 与非独立依赖不允许移除。"
      confirm-text="确认移除"
      :busy="mutating"
      @confirm="confirmRemove"
      @cancel="removeTarget = null"
    />
  </div>
</template>
