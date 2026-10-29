<script setup lang="ts">
import { onBeforeUnmount, onMounted, watch } from 'vue'

import { useAuthStore } from '@/stores/auth'
import { useJobStore } from '@/stores/jobs'
import { useNotifyStore } from '@/stores/notify'
import { POLL_BUSY_MS, POLL_IDLE_MS, useAppStateStore } from '@/stores/state'
import { useUiStore } from '@/stores/ui'

import NextActivityPanel from './NextActivityPanel.vue'
import NextCommandBar from './NextCommandBar.vue'
import NextCommandPalette from './NextCommandPalette.vue'
import NextContextSidebar from './NextContextSidebar.vue'
import NextNavRail from './NextNavRail.vue'

const appState = useAppStateStore()
const auth = useAuthStore()
const jobs = useJobStore()
const notify = useNotifyStore()
const ui = useUiStore()
let restoredJob = false

ui.applyTheme()

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
      void jobs.restore()
    }
  },
  { immediate: true },
)

watch(
  () => jobs.active,
  (active) => {
    appState.setPollInterval(active ? POLL_BUSY_MS : POLL_IDLE_MS)
    if (active) ui.setActivityOpen(true)
  },
  { immediate: true },
)

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

function onKeydown(event: KeyboardEvent): void {
  const modifier = event.ctrlKey || event.metaKey
  if (modifier && event.key.toLowerCase() === 'k') {
    event.preventDefault()
    ui.setPaletteOpen(!ui.paletteOpen)
    return
  }
  if (modifier && event.key.toLowerCase() === 'j') {
    event.preventDefault()
    ui.toggleActivity()
    return
  }
  if (event.key === 'Escape') ui.setPaletteOpen(false)
}

onMounted(() => {
  if (auth.authenticated) appState.startPolling()
  document.addEventListener('keydown', onKeydown)
})

onBeforeUnmount(() => {
  appState.stopPolling()
  document.removeEventListener('keydown', onKeydown)
})
</script>

<template>
  <div class="next-shell">
    <NextCommandBar />
    <div class="next-body">
      <NextNavRail />
      <NextContextSidebar />
      <main class="next-main" data-testid="shell-content">
        <slot />
      </main>
    </div>
    <NextActivityPanel />
    <NextCommandPalette />
  </div>
</template>
