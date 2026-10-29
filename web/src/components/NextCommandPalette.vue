<script setup lang="ts">
import { computed, nextTick, ref, watch } from 'vue'
import { useRouter } from 'vue-router'

import { useInstanceActions } from '@/composables/useInstanceActions'
import { useUiStore } from '@/stores/ui'

interface Command {
  id: string
  label: string
  hint: string
  run: () => void | Promise<void>
}

const router = useRouter()
const ui = useUiStore()
const actions = useInstanceActions()
const { current, running, primaryLabel, primary, stopCurrent, openWeb } = actions

const query = ref('')
const activeIndex = ref(0)
const input = ref<HTMLInputElement | null>(null)

const commands = computed<Command[]>(() => {
  const list: Command[] = [
    { id: 'home', label: '概览', hint: '实例', run: () => { void router.push({ name: 'home' }) } },
    { id: 'instances', label: '实例管理', hint: '实例', run: () => { void router.push({ name: 'instances' }) } },
    { id: 'runtimes', label: '运行时', hint: '资源库', run: () => { void router.push({ name: 'runtimes' }) } },
    { id: 'packs', label: '整合包', hint: '资源库', run: () => { void router.push({ name: 'packs' }) } },
    { id: 'plugins', label: '插件', hint: '资源库', run: () => { void router.push({ name: 'plugins' }) } },
    { id: 'logs', label: '任务与日志', hint: '活动', run: () => { void router.push({ name: 'logs' }) } },
    { id: 'settings', label: '设置', hint: '系统', run: () => { void router.push({ name: 'settings' }) } },
    { id: 'primary', label: primaryLabel.value, hint: current.value?.name ?? '当前实例', run: primary },
  ]
  if (running.value) {
    list.push({ id: 'open', label: '打开 Web 界面', hint: current.value?.name ?? '', run: openWeb })
    list.push({ id: 'stop', label: '停止当前实例', hint: current.value?.name ?? '', run: stopCurrent })
  }
  list.push({ id: 'activity', label: ui.activityOpen ? '收起活动面板' : '展开活动面板', hint: 'Ctrl J', run: () => ui.toggleActivity() })
  list.push({ id: 'theme', label: ui.theme === 'dark' ? '切换到亮色主题' : '切换到暗色主题', hint: '外观', run: () => ui.toggleTheme() })
  return list
})

const filtered = computed(() => {
  const q = query.value.trim().toLowerCase()
  if (!q) return commands.value
  return commands.value.filter((command) => `${command.label} ${command.hint}`.toLowerCase().includes(q))
})

watch(() => ui.paletteOpen, async (open) => {
  if (!open) return
  query.value = ''
  activeIndex.value = 0
  await nextTick()
  input.value?.focus()
})

watch(query, () => { activeIndex.value = 0 })

function close(): void {
  ui.setPaletteOpen(false)
}

function run(command?: Command): void {
  const target = command ?? filtered.value[activeIndex.value]
  if (!target) return
  close()
  void target.run()
}

function onKeydown(event: KeyboardEvent): void {
  if (event.key === 'Escape') {
    event.preventDefault()
    close()
    return
  }
  if (event.key === 'ArrowDown' || event.key === 'ArrowUp') {
    event.preventDefault()
    const count = filtered.value.length
    if (!count) return
    activeIndex.value = (activeIndex.value + (event.key === 'ArrowDown' ? 1 : -1) + count) % count
    return
  }
  if (event.key === 'Enter') {
    event.preventDefault()
    run()
  }
}
</script>

<template>
  <div v-if="ui.paletteOpen" class="next-palette" data-testid="command-palette" @click.self="close">
    <div class="next-palette__box" role="dialog" aria-modal="true" aria-label="命令面板">
      <div class="next-palette__search">
        <span class="next-palette__prompt">›</span>
        <input
          ref="input"
          v-model="query"
          type="text"
          placeholder="搜索页面或执行命令…"
          autocomplete="off"
          @keydown="onKeydown"
        />
        <kbd>Esc</kbd>
      </div>
      <div class="next-palette__list">
        <button
          v-for="(command, index) in filtered"
          :key="command.id"
          type="button"
          class="next-palette__item"
          :class="{ 'is-active': index === activeIndex }"
          @mouseenter="activeIndex = index"
          @click="run(command)"
        >
          <span class="next-palette__label">{{ command.label }}</span>
          <span class="next-palette__hint">{{ command.hint }}</span>
        </button>
        <p v-if="!filtered.length" class="next-palette__empty">没有匹配的命令</p>
      </div>
      <div class="next-palette__foot">
        <span>↑↓ 选择</span><span>Enter 执行</span><span>Esc 关闭</span>
      </div>
    </div>
  </div>
</template>
