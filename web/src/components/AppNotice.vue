<script setup lang="ts">
withDefaults(defineProps<{ title: string; tone?: 'info' | 'dev' | 'warn' }>(), {
  tone: 'info',
})
</script>

<template>
  <div class="note-card" :class="{ 'note-card--dev': tone === 'dev' }">
    <div class="note-title">
      {{ title }}
      <span v-if="tone === 'dev'" class="badge badge--blue">开发中</span>
    </div>
    <slot />
  </div>
</template>
