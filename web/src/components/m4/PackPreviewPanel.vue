<script setup lang="ts">
import type { PackPreviewDto } from '@/api/types'
import { formatBytes } from '@/utils/display'

/** Read-only preview card for a parsed integration pack (M3 preview DTO). */
defineProps<{ preview: PackPreviewDto }>()
const emit = defineEmits<{ reset: [] }>()
</script>

<template>
  <div class="pack-preview" data-testid="pack-preview">
    <div class="pack-preview__heading">
      <span class="card-title">包预览</span>
      <button class="btn btn-subtle btn--small" type="button" data-testid="pack-reset" @click="emit('reset')">
        重新选择
      </button>
    </div>
    <dl class="pack-preview__grid">
      <div class="pack-preview__row">
        <dt>名称</dt>
        <dd data-testid="pack-preview-name">{{ preview.name || '未声明' }}</dd>
      </div>
      <div class="pack-preview__row">
        <dt>格式</dt>
        <dd data-testid="pack-preview-format">{{ preview.format || '未声明' }}</dd>
      </div>
      <div class="pack-preview__row">
        <dt>包版本</dt>
        <dd>{{ preview.version || '未声明' }}</dd>
      </div>
      <div class="pack-preview__row">
        <dt>Harness 版本</dt>
        <dd data-testid="pack-preview-dsh-version">{{ preview.dshVersion || '未声明' }}</dd>
      </div>
      <div class="pack-preview__row">
        <dt>Profile</dt>
        <dd>{{ preview.profiles.length ? preview.profiles.join('、') : '未声明' }}</dd>
      </div>
      <div class="pack-preview__row">
        <dt>解压体积约</dt>
        <dd data-testid="pack-preview-bytes">{{ formatBytes(preview.unpackedBytes) }}</dd>
      </div>
    </dl>
    <div v-if="preview.warnings.length" class="pack-warning" data-testid="pack-warning" role="alert">
      <p class="pack-warning__title">兼容性 / 安全提示（请确认后再导入）</p>
      <ul>
        <li v-for="warning in preview.warnings" :key="warning">{{ warning }}</li>
      </ul>
    </div>
  </div>
</template>
