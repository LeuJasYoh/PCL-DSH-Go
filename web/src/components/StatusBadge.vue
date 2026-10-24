<script setup lang="ts">
import { computed } from 'vue'

import { displayInstanceStatus, instanceStatusKind } from '@/utils/display'

const props = defineProps<{ status: string }>()

const kind = computed(() => instanceStatusKind(props.status))
const label = computed(() => displayInstanceStatus(props.status))
const badgeClass = computed(() => {
  switch (kind.value) {
    case 'running':
      return 'badge--green'
    case 'starting':
      return 'badge--blue'
    case 'error':
      return 'badge--red'
    default:
      return 'badge--gray'
  }
})
</script>

<template>
  <span class="badge" :class="badgeClass" :data-status-kind="kind">{{ label }}</span>
</template>
