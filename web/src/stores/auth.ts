import { defineStore } from 'pinia'
import { computed, ref } from 'vue'

import { authApi } from '@/api'
import { ApiError } from '@/api/client'
import type { AuthMe } from '@/api/types'

export const useAuthStore = defineStore('auth', () => {
  const authenticated = ref(false)
  const checked = ref(false)
  const loading = ref(false)
  const version = ref('0.3.2')
  const initialPasswordPending = ref(false)
  const csrfToken = ref<string | null>(null)
  const backendError = ref<string | null>(null)
  let pendingCheck: Promise<boolean> | null = null

  const backendUnavailable = computed(() => backendError.value !== null && !authenticated.value)

  function apply(me: AuthMe): void {
    authenticated.value = me.authenticated === true
    checked.value = true
    if (me.version) version.value = me.version
    initialPasswordPending.value = me.initialPasswordPending === true
    csrfToken.value = me.csrfToken ?? null
    backendError.value = null
  }

  function clear(): void {
    authenticated.value = false
    checked.value = true
    initialPasswordPending.value = false
    csrfToken.value = null
  }

  async function refresh(): Promise<boolean> {
    loading.value = true
    try {
      const me = await authApi.me()
      apply(me)
      return me.authenticated
    } catch (error) {
      if (error instanceof ApiError && error.status === 401) {
        clear()
        backendError.value = null
        return false
      }
      // Backend unavailable or not implemented: keep the session unchecked so a
      // later navigation or the login retry button can try again.
      backendError.value = error instanceof ApiError ? error.message : '无法连接后端服务。'
      checked.value = false
      return false
    } finally {
      loading.value = false
    }
  }

  function ensureSession(): Promise<boolean> {
    if (checked.value) return Promise.resolve(authenticated.value)
    if (pendingCheck) return pendingCheck
    pendingCheck = refresh().finally(() => {
      pendingCheck = null
    })
    return pendingCheck
  }

  async function login(password: string): Promise<AuthMe> {
    loading.value = true
    try {
      await authApi.login(password)
      const me = await authApi.me()
      apply(me)
      if (!me.authenticated) throw new ApiError(401, 'UNAUTHORIZED', '登录状态未能建立，请重试。')
      return me
    } finally {
      loading.value = false
    }
  }

  async function changePassword(oldPassword: string, newPassword: string): Promise<void> {
    await authApi.changePassword(oldPassword, newPassword)
    await refresh()
  }

  async function logout(): Promise<void> {
    try {
      await authApi.logout()
    } finally {
      clear()
    }
  }

  return {
    authenticated,
    checked,
    loading,
    version,
    initialPasswordPending,
    csrfToken,
    backendError,
    backendUnavailable,
    apply,
    clear,
    refresh,
    ensureSession,
    login,
    changePassword,
    logout,
  }
})
