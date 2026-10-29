<script setup lang="ts">
import { computed, onMounted } from 'vue'
import { useRouter } from 'vue-router'

import StatusBadge from '@/components/StatusBadge.vue'
import { useJobStore } from '@/stores/jobs'
import { useAppStateStore } from '@/stores/state'
import { displayInstanceStatus, displayPort, isRunningStatus } from '@/utils/display'

const router = useRouter()
const appState = useAppStateStore()
const jobs = useJobStore()

const current = computed(() => appState.currentInstance)
const running = computed(() => current.value !== null && isRunningStatus(current.value.status))
const installedRuntime = computed(() => {
  const instance = current.value
  if (!instance) return null
  return appState.runtimes.find((runtime) => runtime.installed && runtime.version === instance.version) ?? null
})
const installedCount = computed(() => appState.runtimes.filter((runtime) => runtime.installed).length)
const recentLine = computed(() => {
  if (jobs.active) return jobs.typeLabel + ' · ' + jobs.progressText
  if (jobs.jobId) return jobs.typeLabel + ' · ' + jobs.stateLabel
  return '暂无任务记录'
})

onMounted(() => {
  void appState.refresh()
})

function go(name: string): void {
  void router.push({ name })
}
</script>

<template>
  <div class="page next-page" data-testid="page-home">
    <header class="next-page-head">
      <div>
        <p class="next-eyebrow">实例 / 概览</p>
        <div class="next-title-row">
          <h1 class="next-title">{{ current?.name ?? '未选择实例' }}</h1>
          <StatusBadge v-if="current" :status="current.status" />
        </div>
        <p v-if="current" class="next-sub">
          {{ current.version }} · {{ current.profile }} · 端口 {{ displayPort(current) }}
        </p>
        <p v-else class="next-sub">创建实例后，这里会显示运行状态、资源和最近任务。</p>
      </div>
    </header>

    <div v-if="appState.error" class="card next-empty" role="alert">
      <p class="next-empty__title">读取工作空间失败</p>
      <p class="next-empty__text">{{ appState.error }}</p>
      <button class="btn btn-secondary btn--small" type="button" @click="appState.refresh()">重试</button>
    </div>

    <div v-else-if="!appState.loading && !current" class="card next-empty" data-testid="home-no-instance-note">
      <p class="next-empty__title">还没有实例</p>
      <p class="next-empty__text">打开实例管理，创建第一个 Harness 工作区；创建后顶栏会同步显示启动操作。</p>
      <button class="btn btn-primary btn--small" type="button" @click="go('instances')">创建实例</button>
    </div>

    <template v-else-if="current">
      <section class="card next-workspace" data-testid="home-current-instance">
        <div class="next-workspace__main">
          <div class="next-workspace__heading">
            <span class="next-workspace__name" data-testid="home-instance-name">{{ current.name }}</span>
            <span class="next-badge">{{ current.version }}</span>
            <span v-if="running" class="next-badge is-ok">运行中</span>
          </div>
          <p class="next-workspace__meta" data-testid="home-instance-meta">
            Harness {{ current.version }} · {{ current.profile }} · 端口 {{ displayPort(current) }} · {{ displayInstanceStatus(current.status) }}
          </p>
          <p class="next-workspace__path">{{ current.path || '工作目录由后端返回' }}</p>
          <p class="next-workspace__hint">
            顶栏的主操作始终作用于当前实例；实例切换、资源库和设置会同步更新作用域。
          </p>
        </div>
        <div class="next-workspace__side">
          <div class="next-metric"><span>运行时</span><strong>{{ installedRuntime ? '已安装' : '未安装' }}</strong></div>
          <div class="next-metric"><span>当前任务</span><strong>{{ jobs.active ? '进行中' : (jobs.jobId ? '已结束' : '空闲') }}</strong></div>
          <div class="next-metric"><span>内置工具</span><strong>{{ installedCount }} 个运行时</strong></div>
        </div>
      </section>

      <div class="next-overview-grid">
        <section class="card next-panel">
          <header class="next-panel__head">
            <span>最近活动</span>
            <button class="next-link" type="button" @click="go('logs')">完整日志 →</button>
          </header>
          <p class="next-panel__lead">{{ recentLine }}</p>
          <p v-if="jobs.error" class="next-error">{{ jobs.error.message }}（{{ jobs.error.code }}）</p>
          <pre v-if="jobs.logTail.length" class="next-mini-log">{{ jobs.logTail.join('\n') }}</pre>
          <p v-else class="next-muted">启动实例或安装运行时后，最近几行任务输出会显示在这里。</p>
        </section>

        <section class="card next-panel">
          <header class="next-panel__head"><span>快捷入口</span></header>
          <button class="next-quick" type="button" @click="go('runtimes')"><span>运行时</span><small>安装与版本查询</small><span>→</span></button>
          <button class="next-quick" type="button" @click="go('packs')"><span>整合包</span><small>预览、导入与导出</small><span>→</span></button>
          <button class="next-quick" type="button" @click="go('settings')"><span>设置</span><small>代理、软件源与 SSH 用户</small><span>→</span></button>
        </section>
      </div>

      <div class="next-stats">
        <div><span>实例</span><strong>{{ appState.instances.length }}</strong></div>
        <div><span>运行时</span><strong>{{ installedCount }}</strong></div>
        <div><span>警告</span><strong>{{ appState.warnings.length }}</strong></div>
        <div><span>后端</span><strong>{{ appState.backendState === 'ok' ? '正常' : appState.backendState }}</strong></div>
      </div>

      <p v-if="appState.warnings.length" class="next-warning">{{ appState.warnings.join(' · ') }}</p>
      <p class="next-note" data-testid="home-model-note">
        管理器不保存模型凭据或 API Key；请在启动后的 DSH 工作台内完成模型配置。
      </p>
    </template>
  </div>
</template>
