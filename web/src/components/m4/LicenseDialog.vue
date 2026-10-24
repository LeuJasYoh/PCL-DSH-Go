<script setup lang="ts">
import { onMounted, ref, watch } from 'vue'

import BaseModal from '@/components/BaseModal.vue'

type LicenseTab = 'gpl' | 'hmcl'

const props = defineProps<{ tab: LicenseTab }>()
const emit = defineEmits<{ close: [] }>()

const LICENSE_FILES: Record<LicenseTab, { url: string; title: string }> = {
  gpl: { url: '/licenses/GPL-3.0.txt', title: 'GPL 第 3 版' },
  hmcl: { url: '/licenses/HMCL-ADDITIONAL-TERMS.md', title: 'HMCL 附加条款' },
}

const active = ref<LicenseTab>(props.tab)
const licenseText = ref('')
const loading = ref(false)
const error = ref('')

onMounted(() => {
  void load(active.value)
})

watch(
  () => props.tab,
  (next) => {
    if (next === active.value) return
    active.value = next
    void load(next)
  },
)

async function load(tab: LicenseTab): Promise<void> {
  licenseText.value = ''
  error.value = ''
  loading.value = true
  try {
    const response = await fetch(LICENSE_FILES[tab].url, { credentials: 'include' })
    if (!response.ok) throw new Error('HTTP ' + String(response.status))
    licenseText.value = await response.text()
  } catch {
    error.value = '无法读取许可文本，请查看随程序提供的 LICENSE 文件。'
  } finally {
    loading.value = false
  }
}

function switchTab(tab: LicenseTab): void {
  if (tab === active.value) return
  active.value = tab
  void load(tab)
}
</script>

<template>
  <BaseModal
    :title="active === 'gpl' ? 'GNU General Public License' : 'HMCL 附加条款'"
    testid="license-dialog"
    wide
    @close="emit('close')"
  >
    <div class="page-tabs" role="tablist" aria-label="许可">
      <button
        class="page-tab"
        :class="{ 'is-active': active === 'gpl' }"
        type="button"
        role="tab"
        :aria-selected="active === 'gpl'"
        data-testid="license-tab-gpl"
        @click="switchTab('gpl')"
      >
        GPL 第 3 版
      </button>
      <button
        class="page-tab"
        :class="{ 'is-active': active === 'hmcl' }"
        type="button"
        role="tab"
        :aria-selected="active === 'hmcl'"
        data-testid="license-tab-hmcl"
        @click="switchTab('hmcl')"
      >
        HMCL 附加条款
      </button>
    </div>
    <div v-if="loading" class="loading-row"><span class="spinner"></span>正在读取许可文本…</div>
    <p v-else-if="error" class="validation-error" data-testid="license-error">{{ error }}</p>
    <pre v-else class="license-text" data-testid="license-text">{{ licenseText }}</pre>
    <template #footer>
      <a class="btn btn-secondary btn--small" :href="LICENSE_FILES[active].url" target="_blank" rel="noopener noreferrer">
        在新标签页打开
      </a>
      <button class="btn btn-primary" type="button" @click="emit('close')">关闭</button>
    </template>
  </BaseModal>
</template>
