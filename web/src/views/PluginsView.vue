<script setup lang="ts">
import { computed, onMounted, ref, watch } from 'vue'

import { pluginsApi } from '@/api'
import type { PluginEntry } from '@/api/types'
import AppIcon from '@/components/AppIcon.vue'
import AppNotice from '@/components/AppNotice.vue'
import ConfirmDialog from '@/components/ConfirmDialog.vue'
import EmptyState from '@/components/EmptyState.vue'
import TaskProgressStrip from '@/components/TaskProgressStrip.vue'
import { useJobStore } from '@/stores/jobs'
import { useNotifyStore } from '@/stores/notify'
import { useAppStateStore } from '@/stores/state'
import { describeApiError } from '@/utils/errors'

const appState = useAppStateStore()
const jobs = useJobStore()
const notify = useNotifyStore()

const selectedId = ref<string | null>(null)
const plugins = ref<PluginEntry[]>([])
const loading = ref(false)
const loadedOnce = ref(false)
const loadError = ref('')
const loadErrorStatus = ref<number | null>(null)

const spec = ref('')
const validationError = ref('')
const submitting = ref(false)

const updateTarget = ref<PluginEntry | null>(null)
const removeTarget = ref<PluginEntry | null>(null)
const mutating = ref(false)

const instances = computed(() => appState.instances)
const selected = computed(() => instances.value.find((item) => item.id === selectedId.value) ?? null)
const busy = computed(() => jobs.active || loading.value || submitting.value || mutating.value)

let lastLoadedKey = ''

onMounted(() => {
  void appState.refresh()
  selectedId.value = appState.currentInstanceId ?? appState.instances[0]?.id ?? null
  if (selectedId.value) void loadPlugins()
})

watch(
  () => appState.currentInstanceId,
  (currentId) => {
    if (selectedId.value === null && currentId) {
      selectedId.value = currentId
    }
  },
)

watch(selectedId, () => {
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

async function loadPlugins(): Promise<void> {
  const instance = selected.value
  if (!instance) {
    plugins.value = []
    loadedOnce.value = true
    return
  }
  const key = instance.id + '|' + instance.profile
  if (loading.value && key === lastLoadedKey) return
  lastLoadedKey = key
  loading.value = true
  loadError.value = ''
  loadErrorStatus.value = null
  try {
    plugins.value = await pluginsApi.list(instance.id, instance.profile)
    loadedOnce.value = true
  } catch (caught) {
    loadError.value = describeApiError(caught, '读取插件列表')
    loadErrorStatus.value = caught && typeof caught === 'object' && 'status' in caught
      ? Number((caught as { status?: unknown }).status)
      : null
    plugins.value = []
    loadedOnce.value = true
  } finally {
    loading.value = false
  }
}

function sourceBadge(plugin: PluginEntry): { label: string; className: string } {
  const source = (plugin.source || '').trim()
  if (source === '官方' || source.toLowerCase().includes('official')) {
    return { label: '官方', className: 'badge--green' }
  }
  return { label: source || '社区 / 本地', className: 'badge--gray' }
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
  validationError.value = ''
  submitting.value = true
  try {
    const job = await pluginsApi.add(instance.id, value)
    jobs.begin(job, { instanceId: instance.id })
    spec.value = ''
    notify.success('已提交任务：' + jobs.typeLabel + '（' + job.id + '）。')
  } catch (caught) {
    validationError.value = describeApiError(caught, '安装插件')
  } finally {
    submitting.value = false
  }
}

async function updatePlugin(plugin: PluginEntry): Promise<void> {
  const instance = selected.value
  if (!instance || jobs.active || mutating.value) return
  updateTarget.value = plugin
  mutating.value = true
  try {
    const job = await pluginsApi.update(instance.id, plugin.name)
    jobs.begin(job, { instanceId: instance.id })
    notify.success('已提交任务：更新插件 ' + plugin.name + '（' + job.id + '）。')
  } catch (caught) {
    notify.error(describeApiError(caught, '更新插件'))
  } finally {
    updateTarget.value = null
    mutating.value = false
  }
}

async function confirmRemove(): Promise<void> {
  const instance = selected.value
  const plugin = removeTarget.value
  if (!instance || !plugin || jobs.active || mutating.value) return
  mutating.value = true
  try {
    const job = await pluginsApi.remove(instance.id, plugin.name)
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
        <span v-if="selected" class="small-muted">当前 profile · {{ selected.profile }}</span>
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
          插件由当前实例的 Harness 管理，安装到该实例当前 profile。支持 npm 包名@版本、GitHub 仓库或本地 file: 路径，
          安装前请确认来源。插件操作前需要实例已停止。
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
          <p class="paragraph">插件名称或 npm 包规格，例如 @scope/plugin@1.2.3；也可填写 GitHub 仓库或本地 file: 路径。</p>
          <div class="inline-field">
            <input
              v-model="spec"
              class="input"
              data-testid="plugin-add-spec"
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
            <span class="small-muted">
              GET /api/instances/{{ selected?.id }}/plugins{{ loadErrorStatus === 404 ? ' 尚未实现' : '' }}
            </span>
          </div>
        </AppNotice>

        <EmptyState
          v-else-if="plugins.length === 0"
          title="暂无插件记录"
          description="点击刷新读取当前实例，或安装一个插件。"
          icon="puzzle"
        />

        <div v-else class="card-list" data-testid="plugin-list">
          <div v-for="plugin in plugins" :key="plugin.name" class="card plugin-card" :data-testid="'plugin-' + plugin.name">
            <div class="plugin-card__info">
              <div class="plugin-card__heading">
                <span class="card-title">{{ plugin.name }}</span>
                <span class="badge" :class="sourceBadge(plugin).className">{{ sourceBadge(plugin).label }}</span>
                <span v-if="plugin.version" class="badge badge--blue">{{ plugin.version }}</span>
              </div>
              <p v-if="plugin.description" class="paragraph">{{ plugin.description }}</p>
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
      detail="这会改变当前 profile 的插件配置；操作作为后台任务执行，进度会显示在任务与日志页。"
      confirm-text="确认移除"
      :busy="mutating"
      @confirm="confirmRemove"
      @cancel="removeTarget = null"
    />
  </div>
</template>
