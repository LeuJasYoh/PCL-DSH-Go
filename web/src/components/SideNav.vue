<script setup lang="ts">
import { computed } from 'vue'
import { useRoute, useRouter } from 'vue-router'

import { useAppStateStore } from '@/stores/state'
import { displayInstanceStatus } from '@/utils/display'

import AppIcon from './AppIcon.vue'

interface NavEntry {
  route: string
  title: string
  icon: string
}

const instanceNav: NavEntry[] = [
  { route: 'instances', title: '实例列表', icon: 'layers' },
  { route: 'runtimes', title: '下载', icon: 'download' },
  { route: 'packs', title: '整合包', icon: 'box' },
  { route: 'plugins', title: '已装插件', icon: 'puzzle' },
]

const generalNav: NavEntry[] = [
  { route: 'settings', title: '设置', icon: 'settings' },
  { route: 'logs', title: '任务与日志', icon: 'terminal' },
]

const route = useRoute()
const router = useRouter()
const appState = useAppStateStore()

const current = computed(() => appState.currentInstance)
const boundAccount = computed(() => {
  const instance = current.value
  if (!instance || !instance.accountId) return null
  return appState.accounts.find((item) => item.id === instance.accountId) ?? null
})
const accountTitle = computed(() => boundAccount.value?.name ?? '账户管理')
const accountSubtitle = computed(
  () => boundAccount.value?.provider ?? '已保存 ' + String(appState.accounts.length) + ' 个账户',
)
const currentTitle = computed(() => current.value?.name ?? '未选择实例')
const currentSubtitle = computed(() => {
  const instance = current.value
  if (!instance) return '点击创建第一个实例'
  return instance.version + ' · ' + instance.profile + ' · ' + displayInstanceStatus(instance.status)
})

function isActive(name: string): boolean {
  return route.name === name
}

function navigate(name: string): void {
  void router.push({ name })
}

function openCurrent(): void {
  void router.push({ name: 'instances' })
}
</script>

<template>
  <nav class="side-nav" id="hmcl-sidebar" data-testid="sidebar" aria-label="主导航">
    <div class="nav-category">账户</div>
    <button
      class="nav-item"
      :class="{ 'is-active': isActive('accounts') }"
      type="button"
      data-testid="nav-accounts"
      @click="navigate('accounts')"
    >
      <span class="nav-item__icon"><AppIcon name="user" /></span>
      <span class="nav-item__body">
        <span class="nav-item__title">{{ accountTitle }}</span>
        <span class="nav-item__subtitle">{{ accountSubtitle }}</span>
      </span>
    </button>

    <div class="nav-category">实例</div>
    <button
      class="nav-item"
      type="button"
      data-testid="current-instance-item"
      title="打开实例列表"
      @click="openCurrent"
    >
      <span class="nav-item__icon"><AppIcon name="box" /></span>
      <span class="nav-item__body">
        <span class="nav-item__title">{{ currentTitle }}</span>
        <span class="nav-item__subtitle">{{ currentSubtitle }}</span>
      </span>
    </button>
    <button
      v-for="item in instanceNav"
      :key="item.route"
      class="nav-item"
      :class="{ 'is-active': isActive(item.route) }"
      type="button"
      :data-testid="'nav-' + item.route"
      @click="navigate(item.route)"
    >
      <span class="nav-item__icon"><AppIcon :name="item.icon" /></span>
      <span class="nav-item__body">
        <span class="nav-item__title">{{ item.title }}</span>
      </span>
    </button>

    <div class="nav-category">通用</div>
    <button
      v-for="item in generalNav"
      :key="item.route"
      class="nav-item"
      :class="{ 'is-active': isActive(item.route) }"
      type="button"
      :data-testid="'nav-' + item.route"
      @click="navigate(item.route)"
    >
      <span class="nav-item__icon"><AppIcon :name="item.icon" /></span>
      <span class="nav-item__body">
        <span class="nav-item__title">{{ item.title }}</span>
      </span>
    </button>
  </nav>
</template>
