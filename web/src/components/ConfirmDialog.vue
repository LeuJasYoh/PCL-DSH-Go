<script setup lang="ts">
import BaseModal from './BaseModal.vue'

withDefaults(
  defineProps<{
    title: string
    question: string
    detail?: string
    confirmText?: string
    busy?: boolean
  }>(),
  { detail: '', confirmText: '确认', busy: false },
)

const emit = defineEmits<{ confirm: []; cancel: [] }>()
</script>

<template>
  <BaseModal :title="title" testid="confirm-dialog" :closable="!busy" @close="emit('cancel')">
    <p class="confirm-question">{{ question }}</p>
    <p v-if="detail" class="paragraph confirm-detail">{{ detail }}</p>
    <template #footer>
      <button class="btn btn-secondary" type="button" :disabled="busy" @click="emit('cancel')">
        取消
      </button>
      <button
        class="btn btn-primary"
        type="button"
        data-testid="confirm-ok"
        :disabled="busy"
        @click="emit('confirm')"
      >
        {{ busy ? '处理中…' : confirmText }}
      </button>
    </template>
  </BaseModal>
</template>
