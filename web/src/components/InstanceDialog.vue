<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, onMounted, ref, watch } from 'vue'

import { runtimesApi } from '@/api'
import type { CreateInstanceInput, InstanceSummary, UpdateInstanceInput } from '@/api/types'
import { parsePort, validateInstanceName, validatePort, validateProfile, validateVersion } from '@/utils/validation'

import AppIcon from './AppIcon.vue'
import BaseModal from './BaseModal.vue'

const props = withDefaults(
  defineProps<{
    mode: 'create' | 'edit'
    instance?: InstanceSummary | null
    /** Installed Harness versions offered as suggestions (create only). */
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

/* ------------------------------------------------------------------ */
/* Release-order helpers (the API returns registry order, but installed */
/* versions and mixed sources still need a deterministic order).        */
/* ------------------------------------------------------------------ */

/** Compare two SemVer strings; returns >0 when a is newer than b. */
function compareSemver(a: string, b: string): number {
  const [coreA, preA = ''] = a.split('-', 2)
  const [coreB, preB = ''] = b.split('-', 2)
  const partsA = coreA.split('.').map((part) => Number(part) || 0)
  const partsB = coreB.split('.').map((part) => Number(part) || 0)
  for (let index = 0; index < 3; index += 1) {
    const diff = (partsA[index] ?? 0) - (partsB[index] ?? 0)
    if (diff !== 0) return diff
  }
  // A release ranks above any of its pre-releases.
  if (!preA && preB) return 1
  if (preA && !preB) return -1
  if (preA === preB) return 0
  return preA.localeCompare(preB)
}

/** Installed first (newest first), then available releases (newest first), deduped. */
const versionChoices = computed(() => {
  const seen = new Set<string>()
  const choices: string[] = []
  const push = (item: string): void => {
    const value = item.trim()
    if (!value || seen.has(value)) return
    seen.add(value)
    choices.push(value)
  }
  const installed = [...props.installedVersions].filter(Boolean).sort((a, b) => compareSemver(b, a))
  const available = [...availableVersions.value].filter(Boolean).sort((a, b) => compareSemver(b, a))
  for (const item of installed) push(item)
  for (const item of available) push(item)
  return choices
})

/* ------------------------------------------------------------------ */
/* Explicit version dropdown (native datalist does not open on click in */
/* Edge/Chromium; users could not pick a loaded release).              */
/* ------------------------------------------------------------------ */

const pickerRoot = ref<HTMLElement | null>(null)
const versionInput = ref<HTMLInputElement | null>(null)
const dropdown = ref<HTMLElement | null>(null)
const dropdownOpen = ref(false)
const activeIndex = ref(0)
const dropdownBox = ref({ left: 0, top: 0, width: 260 })

function positionDropdown(): void {
  const input = versionInput.value
  if (!input) return
  const rect = input.getBoundingClientRect()
  const width = Math.max(rect.width, 240)
  const left = Math.min(rect.left, Math.max(8, window.innerWidth - width - 8))
  dropdownBox.value = { left, top: rect.bottom + 4, width }
}

async function openDropdown(): Promise<void> {
  if (!create.value || dropdownOpen.value) return
  positionDropdown()
  const current = version.value.trim()
  const found = versionChoices.value.indexOf(current)
  activeIndex.value = found >= 0 ? found : 0
  dropdownOpen.value = true
  await nextTick()
  positionDropdown()
  scrollActiveIntoView()
}

function closeDropdown(): void {
  dropdownOpen.value = false
}

function toggleDropdown(): void {
  if (dropdownOpen.value) closeDropdown()
  else void openDropdown()
}

function chooseVersion(item: string): void {
  version.value = item
  versionEdited.value = true
  closeDropdown()
  void nextTick(() => versionInput.value?.focus())
}

function scrollActiveIntoView(): void {
  const list = dropdown.value
  if (!list) return
  const active = list.querySelector<HTMLElement>('.version-dropdown__item.is-active')
  active?.scrollIntoView({ block: 'nearest' })
}

function moveActive(delta: number): void {
  const count = versionChoices.value.length
  if (count === 0) return
  activeIndex.value = (activeIndex.value + delta + count) % count
  void nextTick(scrollActiveIntoView)
}

function onPickerKeydown(event: KeyboardEvent): void {
  if (!create.value) return
  const key = event.key
  if (key === 'ArrowDown') {
    event.preventDefault()
    if (!dropdownOpen.value) {
      void openDropdown()
    } else {
      moveActive(1)
    }
    return
  }
  if (key === 'ArrowUp') {
    event.preventDefault()
    if (!dropdownOpen.value) {
      void openDropdown()
    } else {
      moveActive(-1)
    }
    return
  }
  if (key === 'Enter' && dropdownOpen.value) {
    event.preventDefault()
    const item = versionChoices.value[activeIndex.value]
    if (item) chooseVersion(item)
    else closeDropdown()
    return
  }
  if (key === 'Tab' && dropdownOpen.value) closeDropdown()
}

function onVersionInput(event: Event): void {
  versionEdited.value = true
  version.value = (event.target as HTMLInputElement).value
}

function onDocumentMouseDown(event: MouseEvent): void {
  if (!dropdownOpen.value) return
  const target = event.target
  if (!(target instanceof Node)) return
  if (pickerRoot.value?.contains(target) || dropdown.value?.contains(target)) return
  closeDropdown()
}

function onDocumentKeydownCapture(event: KeyboardEvent): void {
  // Close the dropdown first: BaseModal's own capture handler would otherwise
  // close the whole dialog on Escape.
  if (!dropdownOpen.value || event.key !== 'Escape') return
  event.preventDefault()
  event.stopImmediatePropagation()
  closeDropdown()
  versionInput.value?.focus()
}

function onViewportChange(): void {
  if (dropdownOpen.value) positionDropdown()
}

// Registered during setup (before BaseModal mounts) so the Escape handler runs
// before the modal's document-level capture handler.
document.addEventListener('mousedown', onDocumentMouseDown, true)
document.addEventListener('keydown', onDocumentKeydownCapture, true)
window.addEventListener('resize', onViewportChange)
document.addEventListener('scroll', onViewportChange, true)

onBeforeUnmount(() => {
  document.removeEventListener('mousedown', onDocumentMouseDown, true)
  document.removeEventListener('keydown', onDocumentKeydownCapture, true)
  window.removeEventListener('resize', onViewportChange)
  document.removeEventListener('scroll', onViewportChange, true)
})

watch(
  () => props.submitting,
  (submitting) => {
    if (submitting) closeDropdown()
  },
)

/* ------------------------------------------------------------------ */
/* Version query + submit                                              */
/* ------------------------------------------------------------------ */

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
    versionStatus.value = '已获取 ' + String(result.versions.length) + ' 个发布版本；可点「选择版本」或直接输入。'
  } catch {
    versionStatus.value = '获取失败，可重试或手动输入完整版本号；已保留现有内容。'
  } finally {
    querying.value = false
  }
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
      <div v-if="create" ref="pickerRoot" class="version-picker" @keydown="onPickerKeydown">
        <div class="inline-field">
          <input
            id="instance-version"
            ref="versionInput"
            class="input"
            :value="version"
            :disabled="submitting"
            data-testid="instance-version"
            placeholder="完整版本号，例如 0.2.1-rc.3"
            autocomplete="off"
            role="combobox"
            aria-haspopup="listbox"
            aria-controls="instance-version-listbox"
            :aria-expanded="dropdownOpen"
            @input="onVersionInput"
          />
          <button
            class="btn btn-secondary version-toggle"
            type="button"
            data-testid="instance-version-picker"
            :disabled="submitting"
            :aria-expanded="dropdownOpen"
            aria-haspopup="listbox"
            @click="toggleDropdown"
          >
            选择版本<span class="version-toggle__arrow" :class="{ 'is-open': dropdownOpen }" aria-hidden="true"></span>
          </button>
          <button
            class="btn btn-secondary"
            type="button"
            data-testid="instance-refresh-versions"
            :disabled="querying || submitting"
            title="从 registry 重新获取版本列表"
            @click="queryVersions(true)"
          >
            <AppIcon name="refresh" :size="15" />刷新
          </button>
        </div>
        <p class="form-hint" data-testid="instance-version-status">{{ versionStatus }}</p>
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
      <p v-if="!create" class="form-hint" data-testid="instance-version-status">{{ versionStatus }}</p>
    </div>

    <Teleport to="body">
      <div
        v-if="create && dropdownOpen"
        id="instance-version-listbox"
        ref="dropdown"
        class="version-dropdown"
        role="listbox"
        aria-label="Harness 版本列表"
        data-testid="instance-version-options"
        :style="{
          left: dropdownBox.left + 'px',
          top: dropdownBox.top + 'px',
          width: dropdownBox.width + 'px',
        }"
      >
        <p v-if="querying && versionChoices.length === 0" class="version-dropdown__empty">
          正在获取版本列表…
        </p>
        <p v-else-if="versionChoices.length === 0" class="version-dropdown__empty" data-testid="instance-version-empty">
          暂无已知版本，可手动输入完整版本号
        </p>
        <button
          v-for="(item, index) in versionChoices"
          :key="item"
          class="version-dropdown__item"
          :class="{
            'is-active': index === activeIndex,
            'is-selected': item === version.trim(),
          }"
          type="button"
          role="option"
          :aria-selected="item === version.trim()"
          :data-version="item"
          data-testid="instance-version-option"
          @mousedown.prevent
          @click="chooseVersion(item)"
        >
          <span class="version-dropdown__label">{{ item }}</span>
          <span v-if="item === version.trim()" class="version-dropdown__check" aria-hidden="true">✓</span>
        </button>
      </div>
    </Teleport>

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

<style scoped>
.version-picker {
  position: relative;
  display: flex;
  flex-direction: column;
  gap: 6px;
  min-width: 0;
}

.version-toggle {
  flex: 0 0 auto;
  white-space: nowrap;
}

.version-toggle__arrow {
  display: inline-block;
  width: 0;
  height: 0;
  margin-left: 6px;
  vertical-align: middle;
  border-right: 4px solid transparent;
  border-left: 4px solid transparent;
  border-top: 5px solid currentColor;
  transition: transform 160ms ease;
}

.version-toggle__arrow.is-open {
  transform: rotate(180deg);
}

/* Teleported outside the modal panel: fixed positioning avoids the modal
   body's overflow clipping and keeps the list above the dialog layer. */
.version-dropdown {
  position: fixed;
  z-index: 950;
  max-height: 240px;
  padding: 4px;
  overflow-y: auto;
  background: var(--monet-surface);
  border: 1px solid var(--color-border);
  border-radius: var(--radius-sm);
  box-shadow: var(--dialog-shadow);
}

.version-dropdown__empty {
  padding: 8px 10px;
  font-size: 12px;
  color: var(--color-text-muted);
}

.version-dropdown__item {
  display: flex;
  gap: 8px;
  align-items: center;
  justify-content: space-between;
  width: 100%;
  padding: 6px 10px;
  font-size: 12px;
  color: var(--color-text);
  text-align: left;
  cursor: pointer;
  background: transparent;
  border: 0;
  border-radius: var(--radius-sm);
}

.version-dropdown__item:hover,
.version-dropdown__item.is-active {
  background: var(--color-selected);
}

.version-dropdown__item.is-selected {
  font-weight: 700;
}

.version-dropdown__label {
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.version-dropdown__check {
  flex: 0 0 auto;
  color: var(--monet-primary);
}
</style>
