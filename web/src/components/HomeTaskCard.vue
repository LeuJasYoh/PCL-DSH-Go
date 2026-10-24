<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'

import { elapsedText } from '@/utils/display'

const props = defineProps<{
  phase: string
  startedAt: number | null
  typeLabel?: string
  jobId?: string | null
  logsTail?: string[]
}>()

const emit = defineEmits<{
  view: []
}>()

const now = ref(Date.now())
let timer: number | null = null

const elapsed = computed(() => elapsedText(props.startedAt, now.value))
const tail = computed(() => (props.logsTail ?? []).filter((line) => line.trim() !== '').slice(-3))

onMounted(() => {
  timer = window.setInterval(() => {
    now.value = Date.now()
  }, 1000)
})

onBeforeUnmount(() => {
  if (timer !== null) window.clearInterval(timer)
})
</script>

<template>
  <div class="card home-task-card" data-testid="home-task-panel">
    <div class="task-heading">
      <span class="task-heading__title">
        任务进行中
        <span v-if="typeLabel" class="badge badge--blue" data-testid="home-task-type">{{ typeLabel }}</span>
      </span>
      <span class="task-elapsed" data-testid="home-task-elapsed">{{ elapsed }}</span>
    </div>
    <div class="task-body">
      <span class="task-spinner" aria-hidden="true"></span>
      <span class="task-phase" data-testid="home-task-phase">{{ phase || '正在准备任务…' }}</span>
      <button class="btn btn-secondary btn--small" type="button" data-testid="home-view-task" @click="emit('view')">
        查看任务
      </button>
    </div>
    <pre v-if="tail.length" class="task-log-tail" data-testid="home-task-log">{{ tail.join('\n') }}</pre>
    <p v-if="jobId" class="task-job-id">任务 {{ jobId }}</p>
  </div>
</template>
