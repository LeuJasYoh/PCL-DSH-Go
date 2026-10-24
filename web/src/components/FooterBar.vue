<script setup lang="ts">
import { computed } from 'vue'

import { useAuthStore } from '@/stores/auth'
import { useJobStore } from '@/stores/jobs'
import { useAppStateStore } from '@/stores/state'
import { shortStatus } from '@/utils/display'
import { backendLabel } from '@/utils/errors'

const appState = useAppStateStore()
const auth = useAuthStore()
const jobs = useJobStore()

const message = computed(() => {
  if (jobs.active) return shortStatus('进行中：' + jobs.progressText, 160)
  return shortStatus(appState.statusText, 160) || '正在读取工作空间…'
})
const backend = computed(() => appState.backendState)
const label = computed(() => backendLabel(appState.errorStatus, appState.lastUpdatedAt !== null))
const dotClass = computed(() => {
  switch (backend.value) {
    case 'ok':
      return 'is-ok'
    case 'not-implemented':
      return 'is-warn'
    case 'error':
      return 'is-error'
    default:
      return ''
  }
})
</script>

<template>
  <footer class="footer-status" data-testid="footer-status">
    <span class="footer-status__message" :title="message">{{ message }}</span>
    <span class="footer-status__right">
      <span class="footer-status__backend" title="GET /api/state 轮询状态">
        <span class="backend-dot" :class="dotClass"></span>{{ label }}
      </span>
      <span>PCL-DSH-Linux {{ auth.version }}</span>
      <span>GPLv3 · HMCL 附加条款</span>
    </span>
  </footer>
</template>
