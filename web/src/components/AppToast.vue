<script setup lang="ts">
import { useNotifyStore } from '@/stores/notify'

const notify = useNotifyStore()
</script>

<template>
  <div class="toast-stack" aria-live="polite" aria-atomic="false">
    <div
      v-for="item in notify.items"
      :key="item.id"
      class="toast"
      :class="'toast--' + item.kind"
      role="status"
    >
      <span class="toast__text">{{ item.text }}</span>
      <button
        class="toast__close"
        type="button"
        aria-label="关闭提示"
        @click="notify.dismiss(item.id)"
      >
        ×
      </button>
    </div>
  </div>
</template>
