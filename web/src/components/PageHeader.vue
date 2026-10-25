<script setup lang="ts">
import { computed } from 'vue'
import { useRoute, useRouter } from 'vue-router'

import { useAuthStore } from '@/stores/auth'
import { useJobStore } from '@/stores/jobs'
import { useAppStateStore } from '@/stores/state'
import { shortStatus } from '@/utils/display'

import AppIcon from './AppIcon.vue'

const route = useRoute()
const router = useRouter()
const auth = useAuthStore()
const appState = useAppStateStore()
const jobs = useJobStore()

const isHome = computed(() => route.name === 'home')
const title = computed(
  () => (auth.version ? 'PCL-DSH-Go ' + auth.version : 'PCL-DSH-Go')
    + (isHome.value ? '' : '  /  ' + String(route.meta.title ?? '')),
)
const statusText = computed(() => {
  const text = jobs.active ? '进行中：' + jobs.progressText : appState.statusText
  return shortStatus(text, 160)
})

function goHome(): void {
  if (!isHome.value) void router.push({ name: 'home' })
}
</script>

<template>
  <header class="page-header" data-testid="page-header">
    <button
      v-if="!isHome"
      class="page-header__home"
      type="button"
      title="返回启动页"
      aria-label="返回启动页"
      data-testid="window-home"
      @click="goHome"
    >
      <AppIcon name="home" :size="19" />
    </button>
    <span class="page-header__title" :title="title">{{ title }}</span>
    <div class="page-header__status" data-testid="task-status">
      <span class="page-header__status-text" :title="statusText">{{ statusText }}</span>
    </div>
  </header>
</template>
