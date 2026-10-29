<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'
import { useRouter } from 'vue-router'

import { useJobStore } from '@/stores/jobs'
import { useNotifyStore } from '@/stores/notify'
import { useUiStore } from '@/stores/ui'
import { elapsedText } from '@/utils/display'
import { describeApiError } from '@/utils/errors'

const jobs = useJobStore()
const notify = useNotifyStore()
const ui = useUiStore()
const router = useRouter()
const now = ref(Date.now())
const cancelling = ref(false)
let timer: number | null = null

const elapsed = computed(() => elapsedText(jobs.elapsedBase, now.value))
const tail = computed(() => jobs.logs.slice(-40).map((entry) => entry.line).join('\n'))
const barState = computed(() => {
  if (jobs.active) return jobs.stateLabel || '运行中'
  if (jobs.jobId) return jobs.stateLabel || '已完成'
  return '空闲'
})

onMounted(() => {
  timer = window.setInterval(() => { now.value = Date.now() }, 1000)
})
onBeforeUnmount(() => {
  if (timer !== null) window.clearInterval(timer)
})

function toggle(): void {
  ui.toggleActivity()
}

function openLogs(): void {
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
  <section class="next-activity" :data-open="ui.activityOpen" aria-label="活动">
    <div
      class="next-activity__bar"
      :data-testid="jobs.active ? 'task-strip' : undefined"
      @click="toggle"
    >
      <span class="next-activity__title">活动</span>
      <span class="next-badge" :class="{ 'is-live': jobs.active, 'is-ok': !jobs.active && jobs.jobId }">{{ barState }}</span>
      <span class="next-activity__text">
        <template v-if="jobs.active">{{ jobs.typeLabel }} · {{ jobs.progressText }}</template>
        <template v-else-if="jobs.jobId">{{ jobs.typeLabel }} · {{ jobs.stateLabel }}</template>
        <template v-else>暂无任务；启动实例后会在这里显示进度。</template>
      </span>
      <span v-if="jobs.active" class="next-activity__elapsed">{{ elapsed }}</span>
      <span class="next-activity__spacer"></span>
      <button class="next-activity__link" type="button" data-testid="nav-logs" @click.stop="openLogs">完整日志</button>
      <button class="next-activity__toggle" type="button" :aria-expanded="ui.activityOpen" @click.stop="toggle">
        {{ ui.activityOpen ? '收起' : '展开' }}
      </button>
    </div>
    <div v-if="ui.activityOpen" class="next-activity__body">
      <div class="next-activity__summary">
        <span class="next-activity__type">{{ jobs.typeLabel || '任务' }}</span>
        <span v-if="jobs.jobId" class="next-mono">{{ jobs.jobId }}</span>
        <span v-if="jobs.active" class="next-activity__spacer"></span>
        <button v-if="jobs.running" class="btn btn-secondary btn--small" type="button" :disabled="cancelling" @click="cancel">
          取消任务
        </button>
      </div>
      <p v-if="jobs.error" class="next-activity__error">{{ jobs.error.message }}（{{ jobs.error.code }}）</p>
      <p v-else-if="jobs.connectionHint" class="next-activity__hint">{{ jobs.connectionHint }}</p>
      <pre class="next-activity__log">{{ tail || '暂无日志。' }}</pre>
    </div>
  </section>
</template>
