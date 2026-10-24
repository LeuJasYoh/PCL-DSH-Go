<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, watch } from 'vue'

import { useAuthStore } from '@/stores/auth'
import { useJobStore } from '@/stores/jobs'
import { useNotifyStore } from '@/stores/notify'
import { POLL_BUSY_MS, POLL_IDLE_MS, useAppStateStore } from '@/stores/state'
import { backgroundPosition, resolveBackground } from '@/utils/backgrounds'

import FooterBar from './FooterBar.vue'
import PageHeader from './PageHeader.vue'
import SideNav from './SideNav.vue'

const appState = useAppStateStore()
const auth = useAuthStore()
const jobs = useJobStore()
const notify = useNotifyStore()
let restoredJob = false

const backdropStyle = computed(() => {
  const background = appState.settings.background
  return {
    backgroundImage: 'url("' + resolveBackground(background) + '")',
    backgroundPosition: backgroundPosition(background),
  }
})

// Polling only exists for authenticated sessions: the login page (or the brief
// pre-seed render before the router guard resolves) must not hit /api/state.
watch(
  () => auth.authenticated,
  (authenticated) => {
    if (!authenticated) {
      appState.stopPolling()
      return
    }
    appState.startPolling()
    if (!restoredJob) {
      restoredJob = true
      // A page refresh during a long task restores the SSE subscription.
      void jobs.restore()
    }
  },
  { immediate: true },
)

// Plan 5.3: idle polling is 1.5s; while a background task runs it degrades to 3s
// because SSE is the source of truth for the task itself.
watch(
  () => jobs.active,
  (active) => {
    appState.setPollInterval(active ? POLL_BUSY_MS : POLL_IDLE_MS)
  },
  { immediate: true },
)

// Central task completion notices + consistency refresh (SSE is authoritative).
watch(
  () => jobs.state,
  (next, previous) => {
    if (next === previous) return
    if (next === 'SUCCEEDED') {
      notify.success('任务完成：' + jobs.typeLabel + '。')
      void appState.refresh()
    } else if (next === 'FAILED') {
      notify.error('任务未完成：' + (jobs.error?.message || jobs.message || jobs.typeLabel))
      void appState.refresh()
    } else if (next === 'CANCELLED') {
      notify.info('任务已取消。')
      void appState.refresh()
    }
  },
)

onMounted(() => {
  // The auth watcher above starts polling once the session is confirmed.
  if (auth.authenticated) appState.startPolling()
})

onBeforeUnmount(() => {
  appState.stopPolling()
})
</script>

<template>
  <div class="app-shell">
    <div class="shell-backdrop" :style="backdropStyle" aria-hidden="true"></div>
    <PageHeader />
    <div class="shell-body">
      <SideNav />
      <main class="shell-main" data-testid="shell-content">
        <div class="shell-content">
          <slot />
        </div>
      </main>
    </div>
    <FooterBar />
  </div>
</template>
