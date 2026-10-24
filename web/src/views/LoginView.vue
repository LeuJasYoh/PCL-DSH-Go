<script setup lang="ts">
import { computed, nextTick, onMounted, ref, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'

import { ApiError } from '@/api/client'
import iconUrl from '@/assets/icons/hdsl.webp'
import { useAuthStore } from '@/stores/auth'
import { useNotifyStore } from '@/stores/notify'
import { resolveBackground } from '@/utils/backgrounds'
import { safeRedirect } from '@/utils/display'
import { describeApiError } from '@/utils/errors'

const auth = useAuthStore()
const notify = useNotifyStore()
const router = useRouter()
const route = useRoute()

const step = ref<'password' | 'change'>('password')
const password = ref('')
const newPassword = ref('')
const confirmPassword = ref('')
const error = ref('')
const busy = ref(false)

const passwordInput = ref<HTMLInputElement | null>(null)
const newPasswordInput = ref<HTMLInputElement | null>(null)

const background = computed(() => 'url("' + resolveBackground('builtin:whale-01') + '")')
const backendDown = computed(() => auth.backendError !== null)

function focusCurrent(): void {
  void nextTick(() => {
    if (step.value === 'password') passwordInput.value?.focus()
    else newPasswordInput.value?.focus()
  })
}

watch(step, focusCurrent)

onMounted(() => {
  focusCurrent()
  // The navigation guard already probed /api/auth/me once; retry quietly if
  // the backend was still starting, so a reload is not required.
  if (backendDown.value) void auth.refresh()
})

function loginErrorMessage(caught: unknown): string {
  if (caught instanceof ApiError) {
    if (caught.status === 401) return '密码错误，请重试。'
    if (caught.status === 423) return '登录尝试次数过多，账户已暂时锁定，请 60 秒后再试。'
    if (caught.status === 403) return caught.code === 'PASSWORD_CHANGE_REQUIRED' ? '' : (caught.message || '没有权限登录。')
    if (caught.status === 404) return '登录接口尚未实现（后端开发中），无法登录。'
    if (caught.status === 0) return '无法连接后端服务，请确认服务已在 127.0.0.1:8080 启动。'
  }
  return describeApiError(caught, '登录')
}

async function enter(): Promise<void> {
  const redirect = safeRedirect(route.query.redirect)
  await router.replace(redirect ?? '/')
}

async function submitPassword(): Promise<void> {
  error.value = ''
  if (!password.value) {
    error.value = '请输入密码。'
    return
  }
  busy.value = true
  try {
    const me = await auth.login(password.value)
    if (me.initialPasswordPending) {
      step.value = 'change'
      notify.info('首次登录需要先修改初始密码。')
      return
    }
    await enter()
  } catch (caught) {
    if (caught instanceof ApiError && caught.code === 'PASSWORD_CHANGE_REQUIRED') {
      step.value = 'change'
      notify.info('首次登录需要先修改初始密码。')
      return
    }
    error.value = loginErrorMessage(caught)
  } finally {
    busy.value = false
  }
}

async function submitChange(): Promise<void> {
  error.value = ''
  if (!newPassword.value) {
    error.value = '请输入新密码。'
    return
  }
  if (newPassword.value !== confirmPassword.value) {
    error.value = '两次输入的新密码不一致。'
    return
  }
  busy.value = true
  try {
    await auth.changePassword(password.value, newPassword.value)
    if (!auth.authenticated) {
      step.value = 'password'
      password.value = ''
      error.value = '密码已修改，请使用新密码重新登录。'
      return
    }
    notify.success('密码已修改。')
    await enter()
  } catch (caught) {
    error.value = describeApiError(caught, '修改密码')
  } finally {
    busy.value = false
  }
}

async function retryBackend(): Promise<void> {
  const ok = await auth.refresh()
  if (ok) await enter()
}
</script>

<template>
  <div class="login-page" data-testid="page-login" :style="{ backgroundImage: background }">
    <div class="login-page__scrim" aria-hidden="true"></div>
    <div class="card login-card">
      <div class="login-brand">
        <img class="login-logo" :src="iconUrl" alt="PCL-DSH-Linux" />
        <div>
          <p class="login-title">PCL-DSH-Linux</p>
          <p class="login-subtitle">Web 管理界面 · {{ auth.version }}</p>
        </div>
      </div>

      <p class="paragraph">
        请输入管理员密码以继续。单用户系统不提供注册；忘记密码时请在服务器上使用重置命令。
      </p>

      <div v-if="backendDown" class="login-error" role="alert" data-testid="login-backend-error">
        <p>{{ auth.backendError }}</p>
        <div class="login-actions">
          <button class="btn btn-secondary btn--small" type="button" :disabled="busy" @click="retryBackend">
            重试连接
          </button>
        </div>
      </div>

      <form v-if="step === 'password'" class="login-form" @submit.prevent="submitPassword">
        <label class="form-field">
          <span class="field-label">密码</span>
          <input
            ref="passwordInput"
            v-model="password"
            class="input"
            type="password"
            autocomplete="current-password"
            data-testid="login-password"
            :disabled="busy"
          />
        </label>
        <p v-if="error" class="login-error" role="alert" data-testid="login-error">{{ error }}</p>
        <button class="btn btn-primary btn--block" type="submit" data-testid="login-submit" :disabled="busy">
          {{ busy ? '正在登录…' : '登录' }}
        </button>
        <p class="login-note">
          连续多次密码错误会暂时锁定登录。初始密码由服务端首次启动时生成，保存在 data/initial-password。
        </p>
      </form>

      <form v-else class="login-form" @submit.prevent="submitChange">
        <p class="paragraph">首次登录需要修改初始密码，修改后使用新密码继续。</p>
        <label class="form-field">
          <span class="field-label">当前密码</span>
          <input class="input" type="password" :value="password" disabled data-testid="login-current-password" />
        </label>
        <label class="form-field">
          <span class="field-label">新密码</span>
          <input
            ref="newPasswordInput"
            v-model="newPassword"
            class="input"
            type="password"
            autocomplete="new-password"
            data-testid="login-new-password"
            :disabled="busy"
          />
        </label>
        <label class="form-field">
          <span class="field-label">确认新密码</span>
          <input
            v-model="confirmPassword"
            class="input"
            type="password"
            autocomplete="new-password"
            data-testid="login-confirm-password"
            :disabled="busy"
          />
        </label>
        <p v-if="error" class="login-error" role="alert" data-testid="login-error">{{ error }}</p>
        <button class="btn btn-primary btn--block" type="submit" data-testid="login-change-submit" :disabled="busy">
          {{ busy ? '正在保存…' : '修改密码并进入' }}
        </button>
      </form>
    </div>

    <footer class="login-footer">
      <p>界面改编自 Hello Minecraft! Launcher · 版权所有 © 2013-2026 huangyuhui 及贡献者</p>
      <p>PCL-DSH-Linux 2026 · GPLv3 或后续版本 · 含 HMCL 附加条款</p>
    </footer>
  </div>
</template>
