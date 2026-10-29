<script setup lang="ts">
import { computed } from 'vue'
import { useRoute, useRouter } from 'vue-router'

import { useAuthStore } from '@/stores/auth'
import { useAppStateStore } from '@/stores/state'

import AppIcon from './AppIcon.vue'

const route = useRoute()
const router = useRouter()
const appState = useAppStateStore()
const auth = useAuthStore()

const items = [
  { key: 'instances', label: '实例', icon: 'layers', to: 'home' },
  { key: 'resources', label: '资源库', icon: 'box', to: 'runtimes' },
  { key: 'settings', label: '设置', icon: 'settings', to: 'settings' },
] as const

const active = computed(() => {
  const name = String(route.name ?? '')
  if (name === 'settings') return 'settings'
  if (name === 'runtimes' || name === 'packs' || name === 'plugins') return 'resources'
  return 'instances'
})

function go(name: string): void {
  void router.push({ name })
}
</script>

<template>
  <nav class="next-rail" aria-label="主导航">
    <button
      v-for="item in items"
      :key="item.key"
      type="button"
      class="next-rail__item"
      :class="{ 'is-active': active === item.key }"
      :data-testid="'nav-' + item.key"
      @click="go(item.to)"
    >
      <AppIcon :name="item.icon" :size="18" />
      <span>{{ item.label }}</span>
    </button>
    <span class="next-rail__spacer"></span>
    <div class="next-rail__foot">
      <span class="next-dot" :class="{ 'is-ok': appState.backendState === 'ok' }" :title="appState.backendState"></span>
      <span>{{ auth.version || appState.version || '—' }}</span>
    </div>
  </nav>
</template>
