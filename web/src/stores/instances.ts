import { defineStore } from 'pinia'
import { ref } from 'vue'

import { instancesApi } from '@/api'
import { ApiError } from '@/api/client'
import type { InstanceSummary } from '@/api/types'

export const useInstancesStore = defineStore('instances', () => {
  const items = ref<InstanceSummary[]>([])
  const loading = ref(false)
  const error = ref<string | null>(null)
  const errorStatus = ref<number | null>(null)
  const loadedOnce = ref(false)
  let inFlight = false

  async function load(): Promise<void> {
    if (inFlight) return
    inFlight = true
    loading.value = true
    try {
      items.value = await instancesApi.list()
      error.value = null
      errorStatus.value = null
      loadedOnce.value = true
    } catch (caught) {
      error.value = caught instanceof ApiError ? caught.message : '读取实例列表失败。'
      errorStatus.value = caught instanceof ApiError ? caught.status : null
    } finally {
      loading.value = false
      inFlight = false
    }
  }

  return { items, loading, error, errorStatus, loadedOnce, load }
})
