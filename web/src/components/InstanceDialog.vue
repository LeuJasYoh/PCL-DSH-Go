<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'

import { runtimesApi } from '@/api'
import type { CreateInstanceInput, InstanceSummary, UpdateInstanceInput } from '@/api/types'
import { parsePort, validateInstanceName, validatePort, validateProfile, validateVersion } from '@/utils/validation'

import AppIcon from './AppIcon.vue'
import BaseModal from './BaseModal.vue'

const props = withDefaults(
  defineProps<{
    mode: 'create' | 'edit'
    instance?: InstanceSummary | null
    /** Installed Harness versions offered as datalist suggestions (create only). */
    installedVersions: string[]
    submitting?: boolean
    submitError?: string
  }>(),
  { instance: null, submitting: false, submitError: '' },
)

const emit = defineEmits<{
  create: [input: CreateInstanceInput]
  save: [input: UpdateInstanceInput]
  close: []
}>()

const create = computed(() => props.mode === 'create')

const name = ref(props.instance?.name ?? '')
const version = ref(props.instance?.version || props.installedVersions[0] || '')
const profile = ref(props.instance?.profile || 'web')
const port = ref(props.instance ? String(props.instance.port) : '')
const validationError = ref('')

const availableVersions = ref<string[]>([])
const querying = ref(false)
const versionEdited = ref(false)
let queried = false

const versionStatus = ref(
  create.value
    ? '可选择已知版本，或直接输入完整版本号。'
    : 'Harness 版本创建后不可修改。',
)

const versionChoices = computed(() => {
  const set = new Set<string>()
  for (const item of props.installedVersions) if (item) set.add(item)
  for (const item of availableVersions.value) if (item) set.add(item)
  return Array.from(set)
})

async function queryVersions(manual: boolean): Promise<void> {
  if (querying.value || !create.value) return
  querying.value = true
  if (manual) versionStatus.value = '正在获取版本列表，可继续输入或创建实例…'
  try {
    const result = await runtimesApi.available()
    availableVersions.value = result.versions
    if (!version.value && !versionEdited.value && result.versions.length) {
      // First response only pre-fills an untouched field; a manual or late edit wins.
      version.value = result.versions[0]
    }
    versionStatus.value = '已获取 ' + String(result.versions.length) + ' 个发布版本；也可手动输入完整版本号。'
  } catch {
    versionStatus.value = '获取失败，可重试或手动输入完整版本号；已保留现有内容。'
  } finally {
    querying.value = false
  }
}

function onVersionInput(event: Event): void {
  versionEdited.value = true
  version.value = (event.target as HTMLInputElement).value
}

onMounted(() => {
  if (create.value && !queried) {
    queried = true
    // Exactly one query on open; saving is never blocked by it.
    void queryVersions(false)
  }
})

function submit(): void {
  validationError.value = ''
  const nameError = validateInstanceName(name.value)
  if (nameError) {
    validationError.value = nameError
    return
  }
  const portError = validatePort(port.value)
  if (portError) {
    validationError.value = portError
    return
  }
  if (create.value) {
    const versionError = validateVersion(version.value)
    if (versionError) {
      validationError.value = versionError
      return
    }
    const profileError = validateProfile(profile.value)
    if (profileError) {
      validationError.value = profileError
      return
    }
    emit('create', {
      name: name.value.trim(),
      version: version.value.trim(),
      profile: profile.value.trim(),
      port: parsePort(port.value),
    })
    return
  }
  // Edit: version/profile are immutable, so only name/port are sent.
  emit('save', { name: name.value.trim(), port: parsePort(port.value) })
}
</script>

<template>
  <BaseModal
    :title="create ? '创建实例' : '编辑实例'"
    testid="instance-dialog"
    wide
    :closable="!submitting"
    @close="emit('close')"
  >
    <div class="form-field">
      <label class="field-label" for="instance-name">实例名称</label>
      <input
        id="instance-name"
        v-model="name"
        class="input"
        data-testid="instance-name"
        placeholder="例如 日常工作"
        :disabled="submitting"
        autofocus
      />
    </div>

    <div class="form-field">
      <label class="field-label" for="instance-version">Harness 版本</label>
      <div v-if="create" class="inline-field">
        <input
          id="instance-version"
          class="input"
          :value="version"
          :disabled="submitting"
          data-testid="instance-version"
          placeholder="完整版本号，例如 0.2.1-rc.3"
          list="instance-version-options"
          @input="onVersionInput"
        />
        <button
          class="btn btn-secondary"
          type="button"
          data-testid="instance-refresh-versions"
          :disabled="querying || submitting"
          @click="queryVersions(true)"
        >
          <AppIcon name="refresh" :size="15" />刷新版本
        </button>
        <datalist id="instance-version-options">
          <option v-for="item in versionChoices" :key="item" :value="item"></option>
        </datalist>
      </div>
      <input
        v-else
        id="instance-version"
        class="input"
        :value="version"
        readonly
        disabled
        data-testid="instance-version"
      />
      <p class="form-hint" data-testid="instance-version-status">{{ versionStatus }}</p>
    </div>

    <div class="form-field">
      <label class="field-label" for="instance-profile">{{ create ? '初始 Profile' : 'Profile' }}</label>
      <input
        v-if="create"
        id="instance-profile"
        v-model="profile"
        class="input"
        data-testid="instance-profile"
        :disabled="submitting"
      />
      <input
        v-else
        id="instance-profile"
        class="input"
        :value="profile"
        readonly
        disabled
        data-testid="instance-profile"
      />
      <p class="form-hint">
        {{ create
          ? '新实例默认使用 web；自定义 profile 需先通过整合包导入，或在 Harness 中创建。'
          : '版本与 profile 创建后不可修改；仅可编辑名称与端口。' }}
      </p>
    </div>

    <div class="form-field">
      <label class="field-label" for="instance-port">本地 Web 端口（可选）</label>
      <input
        id="instance-port"
        v-model="port"
        class="input"
        data-testid="instance-port"
        inputmode="numeric"
        placeholder="留空自动分配未使用的实例端口"
        :disabled="submitting"
      />
    </div>

    <p v-if="validationError || submitError" class="validation-error" data-testid="instance-validation" role="alert">
      {{ validationError || submitError }}
    </p>

    <template #footer>
      <button class="btn btn-secondary" type="button" :disabled="submitting" @click="emit('close')">
        取消
      </button>
      <button
        class="btn btn-primary"
        type="button"
        data-testid="instance-save"
        :disabled="submitting"
        @click="submit"
      >
        {{ submitting ? '处理中…' : create ? '创建实例' : '保存' }}
      </button>
    </template>
  </BaseModal>
</template>
