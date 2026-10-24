import { defineStore } from 'pinia'
import { ref } from 'vue'

import { settingsApi } from '@/api'
import { ApiError } from '@/api/client'
import type { AppSettings } from '@/api/types'

export const useSettingsStore = defineStore('settings', () => {
  const settings = ref<AppSettings | null>(null)
  const loading = ref(false)
  const error = ref<string | null>(null)
  const errorStatus = ref<number | null>(null)

  async function load(): Promise<void> {
    loading.value = true
    try {
      settings.value = await settingsApi.get()
      error.value = null
      errorStatus.value = null
    } catch (caught) {
      error.value = caught instanceof ApiError ? caught.message : '读取设置失败。'
      errorStatus.value = caught instanceof ApiError ? caught.status : null
    } finally {
      loading.value = false
    }
  }

  return { settings, loading, error, errorStatus, load }
})
