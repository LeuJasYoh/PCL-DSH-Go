<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'
import { useRouter } from 'vue-router'

import { useJobStore } from '@/stores/jobs'
import { useNotifyStore } from '@/stores/notify'
import { elapsedText } from '@/utils/display'
import { describeApiError } from '@/utils/errors'

/** Compact "a background task is running" banner for pages that submit jobs. */
const jobs = useJobStore()
const notify = useNotifyStore()
const router = useRouter()

const now = ref(Date.now())
let timer: number | null = null
const elapsed = computed(() => elapsedText(jobs.elapsedBase, now.value))
const cancelling = ref(false)

onMounted(() => {
  timer = window.setInterval(() => {
    now.value = Date.now()
  }, 1000)
})

onBeforeUnmount(() => {
  if (timer !== null) window.clearInterval(timer)
})

function viewTask(): void {
  void router.push({ name: 'logs' })
}

async function cancel(): Promise<void> {
  if (cancelling.value) return
  cancelling.value = true
  try {
    await jobs.cancel()
    notify.info('已请求取消当前任务，正在收尾。')
  } catch (caught) {
    notify.error(describeApiError(caught, '取消任务'))
  } finally {
    cancelling.value = false
  }
}
</script>

<template>
  <div v-if="jobs.active" class="card content-card task-strip" data-testid="task-strip">
    <span class="task-spinner" aria-hidden="true"></span>
    <span class="task-strip__text" data-testid="task-strip-phase">
      任务进行中：{{ jobs.typeLabel }} · {{ jobs.progressText }}
    </span>
    <span class="task-strip__elapsed">{{ elapsed }}</span>
    <button class="btn btn-secondary btn--small" type="button" @click="viewTask">查看任务</button>
    <button class="btn btn-subtle btn--small" type="button" data-testid="task-strip-cancel" @click="cancel">
      取消
    </button>
  </div>
</template>
