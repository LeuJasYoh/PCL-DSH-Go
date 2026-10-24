<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, onMounted, ref, watch } from 'vue'

import { jobsApi, logsApi } from '@/api'
import type { JobView } from '@/api/types'
import AppIcon from '@/components/AppIcon.vue'
import AppNotice from '@/components/AppNotice.vue'
import BaseModal from '@/components/BaseModal.vue'
import EmptyState from '@/components/EmptyState.vue'
import { useJobStore } from '@/stores/jobs'
import { useNotifyStore } from '@/stores/notify'
import { copyText } from '@/utils/clipboard'
import { elapsedText } from '@/utils/display'
import { describeApiError } from '@/utils/errors'
import { emptyJobHint, jobStateLabel, jobTypeLabel } from '@/utils/jobs'

/**
 * Tasks and logs:
 *  - the current Job is followed through the jobs store (SSE, cancel, reconnect);
 *  - the recent Job list (GET /api/jobs, max 20) can be selected for replay;
 *  - GET /api/logs/tail provides the manager log tail. Instance process logs are
 *    not exposed by the Go API yet (documented M4 limitation), so the tail is
 *    labelled "manager 日志".
 */
const jobs = useJobStore()
const notify = useNotifyStore()

const logArea = ref<HTMLElement | null>(null)
const stickToBottom = ref(true)
const clearedSeq = ref(0)
const copyFallback = ref<string | null>(null)
const fallbackArea = ref<HTMLTextAreaElement | null>(null)
const cancelling = ref(false)

const tailLines = ref<string[]>([])
const tailLoading = ref(false)
const tailError = ref('')
const tailStatus = ref('')
const tailCount = ref(800)

const recentJobs = ref<JobView[]>([])
const recentLoading = ref(false)
const recentError = ref('')

const now = ref(Date.now())
let clock: number | null = null

const visibleLogs = computed(() =>
  jobs.logs.filter((entry) => entry.seq > clearedSeq.value).map((entry) => entry.line),
)
const visibleLogText = computed(() => visibleLogs.value.join('\n'))
// The summary row already has a "已用时" label, so only the HH:MM value is shown.
const elapsed = computed(() => elapsedText(jobs.elapsedBase, now.value).replace('已用时 ', ''))
const hasJob = computed(() => jobs.jobId !== null)
const resultText = computed(() => {
  const value = jobs.result
  if (value === null || value === undefined) return ''
  if (typeof value === 'string') return value
  try {
    return JSON.stringify(value)
  } catch {
    return String(value)
  }
})

onMounted(() => {
  clock = window.setInterval(() => {
    now.value = Date.now()
  }, 1000)
  void loadTail()
  void loadRecentJobs()
})

onBeforeUnmount(() => {
  if (clock !== null) window.clearInterval(clock)
})

watch(
  () => jobs.jobId,
  () => {
    clearedSeq.value = 0
    stickToBottom.value = true
    void nextTick(scrollToBottom)
  },
)

watch(
  () => visibleLogs.value.length,
  () => {
    if (stickToBottom.value) void nextTick(scrollToBottom)
  },
)

watch(copyFallback, (value) => {
  if (!value) return
  void nextTick(() => {
    fallbackArea.value?.focus()
    fallbackArea.value?.select()
  })
})

function scrollToBottom(): void {
  const element = logArea.value
  if (!element) return
  element.scrollTop = element.scrollHeight
}

function onScroll(): void {
  const element = logArea.value
  if (!element) return
  const distance = element.scrollHeight - element.scrollTop - element.clientHeight
  stickToBottom.value = distance < 24
}

function clearDisplay(): void {
  const maxSeq = jobs.logs.reduce((max, entry) => Math.max(max, entry.seq), 0)
  clearedSeq.value = Math.max(clearedSeq.value, maxSeq)
  notify.info('已清空当前显示；后台任务与日志记录未受影响。')
}

async function cancelJob(): Promise<void> {
  if (!jobs.running || cancelling.value) return
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

async function reconnect(): Promise<void> {
  try {
    await jobs.reconnect()
    notify.info('已尝试重新连接任务事件。')
  } catch (caught) {
    notify.error(describeApiError(caught, '重新连接任务'))
  }
}

async function copyLogs(): Promise<void> {
  const text = visibleLogText.value || jobs.logText
  if (!text) {
    notify.info('当前没有可复制的日志。')
    return
  }
  const outcome = await copyText(text)
  if (outcome === 'copied') {
    notify.success('日志已复制到剪贴板。')
  } else {
    // Clipboard API is unavailable outside localhost/HTTPS; show a manual fallback.
    copyFallback.value = text
  }
}

async function loadTail(): Promise<void> {
  if (tailLoading.value) return
  tailLoading.value = true
  tailStatus.value = '正在读取 manager 日志…'
  try {
    const result = await logsApi.tail(tailCount.value)
    tailLines.value = result.lines
    tailError.value = ''
    tailStatus.value = result.lines.length
      ? '已读取 ' + String(result.lines.length) + ' 行（manager 日志）。'
      : '暂无 manager 日志。'
  } catch (caught) {
    tailError.value = describeApiError(caught, '读取 manager 日志')
    tailStatus.value = ''
  } finally {
    tailLoading.value = false
  }
}

async function loadRecentJobs(): Promise<void> {
  if (recentLoading.value) return
  recentLoading.value = true
  recentError.value = ''
  try {
    recentJobs.value = await jobsApi.list()
  } catch (caught) {
    recentError.value = describeApiError(caught, '读取任务列表')
  } finally {
    recentLoading.value = false
  }
}

function followJob(job: JobView): void {
  jobs.begin(job)
  clearedSeq.value = 0
  stickToBottom.value = true
  notify.info('正在跟随任务 ' + job.id + ' 的实时输出。')
  void nextTick(scrollToBottom)
}

function jobBadgeClass(job: JobView): string {
  if (job.state === 'RUNNING') return 'badge--blue'
  if (job.state === 'SUCCEEDED') return 'badge--green'
  return 'badge--red'
}

function jobTime(job: JobView): string {
  const value = job.createdAt ?? job.startedAt
  if (value === null || value === undefined) return '—'
  const date = new Date(value)
  if (Number.isNaN(date.getTime())) return '—'
  return date.toLocaleString('zh-CN', { hour12: false })
}
</script>

<template>
  <div class="page page--solid" data-testid="page-logs">
    <div class="card-list">
      <div class="toolbar">
        <span class="section-title">当前任务输出</span>
        <span v-if="hasJob" class="badge" :class="jobs.running ? 'badge--blue' : jobs.state === 'SUCCEEDED' ? 'badge--green' : 'badge--red'">
          {{ jobs.typeLabel }} · {{ jobs.stateLabel }}
        </span>
        <span class="toolbar__spacer"></span>
        <button
          class="btn btn-secondary"
          type="button"
          data-testid="cancel-task"
          :disabled="!jobs.running || cancelling"
          @click="cancelJob"
        >
          <AppIcon name="stop" :size="15" />取消当前任务
        </button>
        <button
          class="btn btn-secondary"
          type="button"
          data-testid="copy-logs"
          :disabled="visibleLogs.length === 0"
          @click="copyLogs"
        >
          <AppIcon name="copy" :size="15" />复制日志
        </button>
        <button
          class="btn btn-secondary"
          type="button"
          data-testid="clear-logs"
          :disabled="visibleLogs.length === 0"
          @click="clearDisplay"
        >
          清空显示
        </button>
        <button
          class="btn btn-secondary"
          type="button"
          data-testid="refresh-logs"
          :disabled="tailLoading"
          @click="loadTail"
        >
          <AppIcon name="refresh" :size="15" />刷新 manager 日志
        </button>
      </div>

      <section v-if="hasJob" class="card content-card task-summary" data-testid="task-summary">
        <div class="task-summary__row">
          <span class="small-muted">任务</span>
          <span class="mono" data-testid="task-job-id">{{ jobs.jobId }}</span>
          <span class="small-muted">类型</span>
          <span>{{ jobs.typeLabel }}</span>
          <span class="small-muted">状态</span>
          <span data-testid="task-state">{{ jobs.stateLabel }}</span>
          <span class="small-muted">已用时</span>
          <span data-testid="task-elapsed">{{ elapsed }}</span>
        </div>
        <p class="task-summary__phase" data-testid="task-phase">{{ jobs.progressText || '等待任务事件…' }}</p>
        <p v-if="!jobs.connected && jobs.connectionHint" class="validation-error" data-testid="task-connection">
          {{ jobs.connectionHint }}
          <button class="btn btn-subtle btn--small" type="button" data-testid="reconnect-job" @click="reconnect">
            重新连接
          </button>
        </p>
        <p v-if="jobs.error" class="validation-error" data-testid="task-error" role="alert">
          {{ jobs.error.message }}（{{ jobs.error.code }}）
        </p>
        <p v-if="resultText" class="small-muted task-summary__result" data-testid="task-result">
          结果：{{ resultText }}
        </p>
      </section>

      <div
        ref="logArea"
        class="log-area log-area--live"
        data-testid="log-area"
        role="log"
        aria-live="polite"
        aria-label="当前任务实时日志"
        @scroll.passive="onScroll"
      ><template v-if="visibleLogs.length">{{ visibleLogText }}</template><template v-else>{{ emptyJobHint() }}</template></div>
      <button
        v-if="!stickToBottom"
        class="btn btn-secondary btn--small log-bottom-button"
        type="button"
        @click="stickToBottom = true; scrollToBottom()"
      >
        回到底部
      </button>

      <div class="toolbar">
        <span class="section-title">任务列表</span>
        <span class="small-muted" data-testid="job-list-status">
          {{ recentLoading ? '正在读取…' : recentJobs.length ? '最近 ' + String(recentJobs.length) + ' 个任务' : '暂无任务记录' }}
        </span>
        <span class="toolbar__spacer"></span>
        <button class="btn btn-secondary btn--small" type="button" :disabled="recentLoading" @click="loadRecentJobs">
          <AppIcon name="refresh" :size="14" />刷新列表
        </button>
      </div>
      <AppNotice v-if="recentError" title="任务列表读取失败" tone="warn">
        <p class="paragraph">{{ recentError }}</p>
      </AppNotice>
      <div v-else-if="recentJobs.length" class="card-list" data-testid="job-list">
        <div v-for="job in recentJobs" :key="job.id" class="card job-list-row" :data-testid="'job-list-item-' + job.id">
          <div class="job-list-row__info">
            <span class="badge" :class="jobBadgeClass(job)">{{ jobStateLabel(job.state) }}</span>
            <span>{{ jobTypeLabel(job.type) }}</span>
            <span class="mono small-muted">{{ job.id }}</span>
            <span class="small-muted">{{ jobTime(job) }}</span>
          </div>
          <div class="instance-card__actions">
            <button
              class="btn btn-secondary btn--small"
              type="button"
              :data-testid="'job-follow-' + job.id"
              @click="followJob(job)"
            >
              查看输出
            </button>
          </div>
        </div>
      </div>

      <div class="toolbar">
        <span class="section-title">manager 日志</span>
        <span class="small-muted" data-testid="log-tail-status">{{ tailStatus }}</span>
        <span class="toolbar__spacer"></span>
        <select v-model.number="tailCount" class="input log-tail-lines" aria-label="日志行数" :disabled="tailLoading" @change="loadTail">
          <option :value="200">200 行</option>
          <option :value="800">800 行</option>
          <option :value="2000">2000 行</option>
        </select>
        <button class="btn btn-secondary btn--small" type="button" :disabled="tailLoading" @click="loadTail">
          <AppIcon name="refresh" :size="14" />刷新
        </button>
      </div>
      <AppNotice v-if="tailError" title="manager 日志读取失败" tone="warn">
        <p class="paragraph">{{ tailError }}</p>
        <div class="toolbar">
          <button class="btn btn-secondary btn--small" type="button" @click="loadTail">重试</button>
          <span class="small-muted">GET /api/logs/tail?lines={{ tailCount }}</span>
        </div>
      </AppNotice>
      <pre v-else class="log-area log-area--tail" data-testid="log-tail" aria-label="manager 日志尾部">{{ tailLines.length ? tailLines.join('\n') : '暂无 manager 日志。' }}</pre>
      <p class="small-muted">
        说明：后端目前仅提供 manager 日志 tail（已脱敏）；实例进程日志暂不通过 API 提供，请通过任务输出查看启动/运行阶段日志。
      </p>

      <EmptyState
        v-if="!hasJob"
        title="还没有正在跟随的任务"
        description="安装运行时、检测能力、导入整合包或启动实例后，这里会实时显示任务阶段与日志；也可以从上方任务列表选择历史任务查看输出。"
        icon="terminal"
      />
    </div>

    <BaseModal v-if="copyFallback !== null" title="复制日志" testid="copy-fallback" wide @close="copyFallback = null">
      <p class="paragraph">
        浏览器不允许直接写入剪贴板（通常因为页面不是 localhost 或 HTTPS）。下面的文本已自动全选，请按
        Ctrl+C 或右键复制。
      </p>
      <textarea
        ref="fallbackArea"
        class="input log-copy-area"
        readonly
        :value="copyFallback"
        data-testid="copy-fallback-text"
        rows="14"
      ></textarea>
      <template #footer>
        <button class="btn btn-primary" type="button" @click="copyFallback = null">完成</button>
      </template>
    </BaseModal>
  </div>
</template>

<style scoped>
.job-list-row {
  display: flex;
  flex-wrap: wrap;
  gap: 8px;
  align-items: center;
  justify-content: space-between;
}

.job-list-row__info {
  display: flex;
  flex-wrap: wrap;
  gap: 8px;
  align-items: center;
}

.log-tail-lines {
  flex: 0 0 auto;
  width: 110px;
}
</style>
