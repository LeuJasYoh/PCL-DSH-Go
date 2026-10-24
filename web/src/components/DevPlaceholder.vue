<script setup lang="ts">
import AppIcon from './AppIcon.vue'
import AppNotice from './AppNotice.vue'

withDefaults(
  defineProps<{
    title: string
    description: string
    planned: string[]
    api?: string[]
    icon?: string
  }>(),
  { icon: 'layers' },
)
</script>

<template>
  <AppNotice title="开发中" tone="dev">
    <p class="paragraph">
      该页面本增量只提供结构与视觉占位，不展示伪造数据，也不会触发尚未实现的写操作。
    </p>
  </AppNotice>
  <div class="empty-card dev-placeholder__card">
    <AppIcon class="empty-card__icon" :name="icon" :size="34" />
    <p class="empty-card__title">{{ title }}</p>
    <p class="empty-card__description">{{ description }}</p>
    <div class="dev-placeholder__planned">
      <p class="field-label">计划接入</p>
      <ul class="dev-list">
        <li v-for="item in planned" :key="item">{{ item }}</li>
      </ul>
      <p v-if="api && api.length" class="small-muted">后端接口：{{ api.join('、') }}</p>
    </div>
  </div>
</template>

<style scoped>
.dev-placeholder__planned {
  width: 100%;
  max-width: 560px;
  text-align: left;
}
</style>
