<script setup lang="ts">
import { onBeforeUnmount, onMounted, ref, watch } from 'vue'

import type { InstanceSummary } from '@/api/types'

const props = defineProps<{
  instances: InstanceSummary[]
  currentId: string | null
  launchLabel: string
  launchSub: string
  busy: boolean
}>()

const emit = defineEmits<{
  launch: []
  select: [id: string]
}>()

const root = ref<HTMLElement | null>(null)
const open = ref(false)

function toggle(): void {
  if (props.busy) return
  open.value = !open.value
}

function choose(id: string): void {
  open.value = false
  emit('select', id)
}

function onDocumentPointerDown(event: MouseEvent): void {
  if (!open.value) return
  const target = event.target
  if (root.value && target instanceof Node && !root.value.contains(target)) open.value = false
}

function onDocumentKeyDown(event: KeyboardEvent): void {
  if (event.key === 'Escape' && open.value) open.value = false
}

watch(
  () => props.busy,
  (busy) => {
    if (busy) open.value = false
  },
)

onMounted(() => {
  document.addEventListener('mousedown', onDocumentPointerDown)
  document.addEventListener('keydown', onDocumentKeyDown)
})

onBeforeUnmount(() => {
  document.removeEventListener('mousedown', onDocumentPointerDown)
  document.removeEventListener('keydown', onDocumentKeyDown)
})
</script>

<template>
  <div ref="root" class="launch-pane" data-testid="launch-pane">
    <button
      id="primary-launch"
      class="launch-button"
      type="button"
      data-testid="primary-launch"
      :disabled="busy"
      :title="launchLabel"
      @click="emit('launch')"
    >
      <span class="launch-button__main">{{ launchLabel }}</span>
      <span v-if="launchSub" class="launch-button__sub">{{ launchSub }}</span>
    </button>
    <button
      id="instance-switcher"
      class="launch-menu"
      type="button"
      data-testid="instance-switcher"
      :disabled="busy"
      title="切换实例"
      aria-label="切换实例"
      :aria-expanded="open"
      @click="toggle"
    >
      <svg
        class="launch-arrow"
        :class="{ 'is-open': open }"
        width="16"
        height="18"
        viewBox="0 0 24 24"
        aria-hidden="true"
      >
        <path d="M 7 14 L 12 9 L 17 14 Z" fill="currentColor" />
      </svg>
    </button>
    <div v-if="open" class="instance-popup" role="menu" aria-label="切换实例">
      <button
        v-for="item in instances"
        :key="item.id"
        class="instance-popup__item"
        :class="{ 'is-selected': item.id === currentId }"
        type="button"
        role="menuitemradio"
        :aria-checked="item.id === currentId"
        @click="choose(item.id)"
      >
        <span class="instance-popup__radio" aria-hidden="true"></span>
        <span>{{ item.name }} · {{ item.version }}</span>
      </button>
      <p v-if="instances.length === 0" class="instance-popup__empty">尚未创建实例</p>
    </div>
  </div>
</template>
