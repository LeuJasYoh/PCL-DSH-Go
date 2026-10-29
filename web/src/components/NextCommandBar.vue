<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'
import { useRoute, useRouter } from 'vue-router'

import { useInstanceActions } from '@/composables/useInstanceActions'
import { useJobStore } from '@/stores/jobs'
import { useAppStateStore } from '@/stores/state'
import { useUiStore } from '@/stores/ui'
import { displayPort, isRunningStatus } from '@/utils/display'

const route = useRoute()
const router = useRouter()
const appState = useAppStateStore()
const jobs = useJobStore()
const ui = useUiStore()
const actions = useInstanceActions()
const { current, running, primaryLabel, primaryHint, pending, primary, stopCurrent, openWeb, selectInstance } = actions

const instanceMenu = ref(false)
const moreMenu = ref(false)

const routeTitle = computed(() => String(route.meta.title ?? '启动'))
const statusText = computed(() => (jobs.active ? '任务进行中：' + (jobs.progressText || '…') : appState.statusText))
const themeLabel = computed(() => (ui.theme === 'dark' ? '亮色' : '暗色'))

function goHome(): void {
  void router.push({ name: 'home' })
}

function openLogs(): void {
  moreMenu.value = false
  void router.push({ name: 'logs' })
}

function closeMenus(event: MouseEvent): void {
  const target = event.target as HTMLElement | null
  if (!target?.closest('.next-command__instance-wrap')) instanceMenu.value = false
  if (!target?.closest('.next-command__more-wrap')) moreMenu.value = false
}

onMounted(() => document.addEventListener('click', closeMenus))
onBeforeUnmount(() => document.removeEventListener('click', closeMenus))

async function chooseInstance(id: string): Promise<void> {
  instanceMenu.value = false
  await selectInstance(id)
}
</script>

<template>
  <header class="next-command" data-testid="page-header">
    <button class="next-command__brand" type="button" data-testid="window-home" @click="goHome">
      PCL-DSH-Go
    </button>
    <span class="next-command__route page-header__title">{{ routeTitle }}</span>

    <div class="next-command__instance-wrap">
      <button
        type="button"
        class="next-command__instance"
        :aria-expanded="instanceMenu"
        @click.stop="instanceMenu = !instanceMenu"
      >
        <span class="next-dot" :class="{ 'is-ok': current && isRunningStatus(current.status) }"></span>
        <span class="next-command__instance-text">
          <strong>{{ current?.name ?? '未选择实例' }}</strong>
          <small v-if="current">{{ current.version }} · {{ displayPort(current) }}</small>
          <small v-else>打开实例管理创建</small>
        </span>
        <span class="next-chevron">⌄</span>
      </button>
      <div v-if="instanceMenu" class="next-pop" @click.stop>
        <div class="next-pop__head"><span>切换实例</span><button type="button" @click="router.push({ name: 'instances' }); instanceMenu = false">管理</button></div>
        <button
          v-for="instance in appState.instances"
          :key="instance.id"
          type="button"
          class="next-pop__instance"
          :class="{ 'is-current': instance.id === appState.currentInstanceId }"
          :disabled="pending"
          @click="chooseInstance(instance.id)"
        >
          <span class="next-dot" :class="{ 'is-ok': isRunningStatus(instance.status) }"></span>
          <span><strong>{{ instance.name }}</strong><small>{{ instance.version }} · {{ displayPort(instance) }}</small></span>
          <span v-if="instance.id === appState.currentInstanceId" class="next-pop__check">当前</span>
        </button>
        <p v-if="!appState.instances.length" class="next-pop__empty">还没有实例。</p>
      </div>
    </div>

    <button type="button" class="next-command__search" @click="ui.setPaletteOpen(true)">
      <span>搜索或执行命令</span><kbd>Ctrl K</kbd>
    </button>

    <span class="next-command__status" data-testid="task-status" :title="statusText">
      <span class="next-dot" :class="{ 'is-ok': appState.backendState === 'ok', 'is-busy': jobs.active }"></span>
      <span>{{ statusText }}</span>
    </span>

    <button
      type="button"
      class="next-primary"
      data-testid="primary-launch"
      :disabled="pending"
      :title="primaryHint"
      @click="primary"
    >
      <span>{{ primaryLabel }}</span>
      <span v-if="jobs.active" class="next-primary__spinner" aria-hidden="true"></span>
    </button>

    <div class="next-command__more-wrap">
      <button type="button" class="next-command__more" aria-label="更多操作" :aria-expanded="moreMenu" @click.stop="moreMenu = !moreMenu">更多</button>
      <div v-if="moreMenu" class="next-pop next-pop--right" @click.stop>
        <button type="button" class="next-pop__action" :disabled="!running" @click="stopCurrent(); moreMenu = false">停止实例</button>
        <button type="button" class="next-pop__action" :disabled="!running" @click="openWeb(); moreMenu = false">打开 Web</button>
        <button type="button" class="next-pop__action" @click="openLogs">完整日志</button>
      </div>
    </div>

    <button type="button" class="next-command__theme" :title="'切换到' + (ui.theme === 'dark' ? '亮色' : '暗色')" @click="ui.toggleTheme()">
      {{ themeLabel }}
    </button>
  </header>
</template>
