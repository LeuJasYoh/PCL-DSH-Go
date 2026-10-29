<script setup lang="ts">
import { computed } from 'vue'
import { useRoute, useRouter } from 'vue-router'

import { useInstanceActions } from '@/composables/useInstanceActions'
import { useAppStateStore } from '@/stores/state'
import { displayPort, isRunningStatus } from '@/utils/display'

import AppIcon from './AppIcon.vue'

const route = useRoute()
const router = useRouter()
const appState = useAppStateStore()
const actions = useInstanceActions()
const { current, pending, selectInstance } = actions

const mode = computed(() => {
  const name = String(route.name ?? '')
  if (name === 'runtimes' || name === 'packs' || name === 'plugins') return 'resources'
  if (name === 'settings') return 'settings'
  if (name === 'logs') return 'logs'
  return 'instances'
})

const resourceItems = [
  { name: 'runtimes', label: '运行时', icon: 'cpu' },
  { name: 'packs', label: '整合包', icon: 'box' },
  { name: 'plugins', label: '插件', icon: 'puzzle' },
] as const

function go(name: string): void {
  void router.push({ name })
}
</script>

<template>
  <aside class="next-context" data-testid="sidebar">
    <div v-if="mode === 'instances'" class="next-context__body">
      <div class="next-context__head">
        <span>实例</span>
        <span class="next-context__count">{{ appState.instances.length }}</span>
      </div>
      <div v-if="appState.instances.length" class="next-context__list">
        <button
          v-for="instance in appState.instances"
          :key="instance.id"
          type="button"
          class="next-instance"
          :class="{ 'is-current': instance.id === appState.currentInstanceId }"
          :data-testid="instance.id === appState.currentInstanceId ? 'current-instance-item' : 'context-instance-' + instance.id"
          :disabled="pending"
          @click="selectInstance(instance.id)"
        >
          <span class="next-dot" :class="{ 'is-ok': isRunningStatus(instance.status) }"></span>
          <span class="next-instance__body">
            <strong>{{ instance.name }}</strong>
            <small>{{ instance.version }} · {{ displayPort(instance) }}</small>
          </span>
          <span v-if="instance.id === appState.currentInstanceId" class="next-instance__mark">当前</span>
        </button>
      </div>
      <p v-else class="next-context__note">还没有实例。打开实例管理创建第一个。</p>
      <button class="next-context__action" type="button" @click="go('instances')">
        <AppIcon name="layers" :size="14" />管理实例
      </button>
      <p class="next-context__note">切换实例会同步顶栏主操作和所有页面的作用域。</p>
    </div>

    <div v-else-if="mode === 'resources'" class="next-context__body">
      <div class="next-context__head"><span>资源库</span></div>
      <button
        v-for="item in resourceItems"
        :key="item.name"
        type="button"
        class="next-context__link"
        :class="{ 'is-active': route.name === item.name }"
        @click="go(item.name)"
      >
        <AppIcon :name="item.icon" :size="15" /><span>{{ item.label }}</span>
      </button>
      <div class="next-scope">
        <span>作用域</span>
        <strong>{{ current?.name ?? '未选择实例' }}</strong>
        <p>运行时全局共享；整合包和插件属于当前实例。</p>
      </div>
    </div>

    <div v-else-if="mode === 'settings'" class="next-context__body">
      <div class="next-context__head"><span>设置</span></div>
      <button type="button" class="next-context__link" :class="{ 'is-active': route.name === 'settings' }" @click="go('settings')">
        <AppIcon name="settings" :size="15" /><span>管理器设置</span>
      </button>
      <button type="button" class="next-context__link" @click="go('instances')">
        <AppIcon name="layers" :size="15" /><span>实例管理</span>
      </button>
      <p class="next-context__note">模型凭据与 Provider 仍由工作台管理，管理器只保存运行配置。</p>
    </div>

    <div v-else class="next-context__body">
      <div class="next-context__head"><span>活动</span></div>
      <button type="button" class="next-context__link" :class="{ 'is-active': route.name === 'logs' }" @click="go('logs')">
        <AppIcon name="terminal" :size="15" /><span>完整日志</span>
      </button>
      <button type="button" class="next-context__link" @click="go('home')">
        <AppIcon name="home" :size="15" /><span>返回概览</span>
      </button>
      <p class="next-context__note">底部活动面板会保留最近任务和日志入口。</p>
    </div>
  </aside>
</template>
