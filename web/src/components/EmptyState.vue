<script setup lang="ts">
import AppIcon from './AppIcon.vue'

withDefaults(defineProps<{ title: string; description: string; icon?: string }>(), {
  icon: 'layers',
})
</script>

<template>
  <div class="empty-card" role="status">
    <AppIcon class="empty-card__icon" :name="icon" :size="34" />
    <p class="empty-card__title">{{ title }}</p>
    <p class="empty-card__description">{{ description }}</p>
    <div v-if="$slots.default" class="empty-card__actions">
      <slot />
    </div>
  </div>
</template>
