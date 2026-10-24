<script setup lang="ts">
import { onMounted, ref, watch } from 'vue'

import { settingsApi } from '@/api'
import type { AppSettings } from '@/api/types'
import AppIcon from '@/components/AppIcon.vue'
import AppNotice from '@/components/AppNotice.vue'
import TaskProgressStrip from '@/components/TaskProgressStrip.vue'
import LicenseDialog from '@/components/m4/LicenseDialog.vue'
import { useAuthStore } from '@/stores/auth'
import { useNotifyStore } from '@/stores/notify'
import { useAppStateStore } from '@/stores/state'
import { describeApiError } from '@/utils/errors'
import { validateSettings } from '@/utils/validation'

/**
 * Manager settings: proxy, npm registry and the direct-access SSH user.
 *
 * The M4 scope removes every appearance/background/upload block (backgrounds
 * are fixed) and keeps the about/licenses section. PUT /api/settings strictly
 * accepts {proxy, registry, directAccessSshUser} (Go DisallowUnknownFields), so
 * the payload must never grow extra fields.
 */
const appState = useAppStateStore()
const auth = useAuthStore()
const notify = useNotifyStore()

type LicenseTab = 'gpl' | 'hmcl'

const DEFAULT_SSH_USER = 'root'
const SSH_USER_PATTERN = /^[A-Za-z0-9._-]{1,32}$/

const form = ref<AppSettings>({
  proxy: '',
  registry: 'https://registry.npmjs.org',
  directAccessSshUser: DEFAULT_SSH_USER,
})
const dirty = ref(false)
const initialized = ref(false)
const loading = ref(false)
const loadError = ref('')
const saving = ref(false)
const validationError = ref('')
const licenseTab = ref<LicenseTab | null>(null)

onMounted(() => {
  if (appState.lastUpdatedAt !== null) {
    initialize(appState.settings)
    return
  }
  void loadSettings()
})

// The 1.5s state polling must never clobber a form the user is editing.
watch(
  () => appState.settings,
  (next) => {
    if (!initialized.value) {
      initialize(next)
      return
    }
    if (!dirty.value) initialize(next)
  },
  { deep: true },
)

function initialize(source: AppSettings): void {
  form.value = {
    proxy: source.proxy ?? '',
    registry: source.registry ?? '',
    directAccessSshUser: (source.directAccessSshUser ?? '').trim() || DEFAULT_SSH_USER,
  }
  initialized.value = true
  validationError.value = ''
}

async function loadSettings(): Promise<void> {
  loading.value = true
  loadError.value = ''
  try {
    const result = await settingsApi.get()
    appState.replaceSettings(result)
    initialize(result)
  } catch (caught) {
    loadError.value = describeApiError(caught, '读取设置')
  } finally {
    loading.value = false
  }
}

function markDirty(): void {
  dirty.value = true
  validationError.value = ''
}

/** Empty means "let the backend apply its default (root)". */
function validateDirectAccessUser(value: string): string | null {
  const user = value.trim()
  if (!user) return null
  if (!SSH_USER_PATTERN.test(user)) {
    return 'SSH 用户名只允许字母、数字、点、下划线和连字符，长度 1-32。'
  }
  return null
}

async function save(): Promise<void> {
  if (saving.value) return
  validationError.value =
    validateSettings(form.value.proxy, form.value.registry) ??
    validateDirectAccessUser(form.value.directAccessSshUser) ??
    ''
  if (validationError.value) return
  saving.value = true
  try {
    const saved = await settingsApi.update({
      proxy: form.value.proxy.trim(),
      registry: form.value.registry.trim(),
      directAccessSshUser: form.value.directAccessSshUser.trim(),
    })
    appState.replaceSettings(saved)
    initialize(saved)
    dirty.value = false
    notify.success('设置已保存。将用于后续任务；直连登录的 SSH 用户会立即回显。')
  } catch (caught) {
    validationError.value = describeApiError(caught, '保存设置')
  } finally {
    saving.value = false
  }
}

function openLicense(tab: LicenseTab): void {
  licenseTab.value = tab
}
</script>

<template>
  <div class="page page--solid" data-testid="page-settings">
    <div class="card-list">
      <div class="toolbar">
        <span class="section-title">设置</span>
        <span v-if="dirty" class="badge badge--blue" data-testid="settings-dirty">有未保存的修改</span>
        <span class="toolbar__spacer"></span>
        <button
          class="btn btn-primary"
          type="button"
          data-testid="settings-save"
          :disabled="saving || (!dirty && initialized)"
          @click="save"
        >
          <AppIcon name="check" :size="15" />{{ saving ? '正在保存…' : '保存设置' }}
        </button>
      </div>

      <TaskProgressStrip />

      <div v-if="loading" class="card card--flat">
        <div class="loading-row"><span class="spinner"></span>正在读取设置…</div>
      </div>

      <AppNotice v-else-if="loadError" title="加载失败" tone="warn">
        <p class="paragraph">{{ loadError }}</p>
        <div class="toolbar">
          <button class="btn btn-secondary btn--small" type="button" @click="loadSettings">重试</button>
          <span class="small-muted">GET /api/settings</span>
        </div>
      </AppNotice>

      <template v-else>
        <section class="card content-card">
          <p class="card-title">网络与下载</p>
          <p class="paragraph">代理用于下载运行时和查询版本；留空表示直连。不要填写包含账号密码的地址。</p>
          <div class="form-field">
            <label class="field-label" for="settings-proxy">HTTP / HTTPS 代理</label>
            <input
              id="settings-proxy"
              v-model="form.proxy"
              class="input"
              data-testid="settings-proxy"
              placeholder="例如 http://127.0.0.1:7890；留空使用直连"
              :disabled="saving"
              @input="markDirty"
            />
          </div>
          <div class="form-field">
            <label class="field-label" for="settings-registry">npm 软件源</label>
            <input
              id="settings-registry"
              v-model="form.registry"
              class="input"
              data-testid="settings-registry"
              placeholder="https://registry.npmjs.org"
              :disabled="saving"
              @input="markDirty"
            />
          </div>
          <p class="small-muted">代理示例：http://127.0.0.1:7890；软件源需为 HTTP(S) 地址且不含凭据。</p>
        </section>

        <section class="card content-card">
          <p class="card-title">直连登录</p>
          <p class="paragraph">
            远程完成 DeepSeek 账号网页登录时，SSH 隧道命令使用的服务器登录用户名。实例页/首页的「直连登录」会立即使用这里的配置。
          </p>
          <div class="form-field">
            <label class="field-label" for="settings-ssh-user">SSH 用户名</label>
            <input
              id="settings-ssh-user"
              v-model="form.directAccessSshUser"
              class="input"
              data-testid="settings-ssh-user"
              :placeholder="DEFAULT_SSH_USER"
              :disabled="saving"
              @input="markDirty"
            />
            <p class="form-hint">
              只允许字母、数字、点、下划线和连字符，长度 1-32；留空保存后默认为 {{ DEFAULT_SSH_USER }}。
            </p>
          </div>
          <p v-if="validationError" class="validation-error" data-testid="settings-validation" role="alert">
            {{ validationError }}
          </p>
        </section>

        <section class="card content-card" data-testid="about-card">
          <p class="card-title">关于 PCL-DSH-Go</p>
          <div class="about-lines paragraph">
            <p>界面改编自 Hello Minecraft! Launcher（HMCL）</p>
            <p>版权所有 © 2013-2026 huangyuhui 及贡献者</p>
            <p>HDSL 桌面版版权归 jiefing 及贡献者所有；PCL-DSH-Go 保留上游可见版权，并以独立名称和版本区别于原版。</p>
            <p>PCL-DSH-Go {{ auth.version }} · Copyright (c) 2026 jiefing 及贡献者 · GPL-3.0-or-later（GNU GPL 第 3 版或后续版本）</p>
          </div>
          <div class="about-links">
            <button class="btn btn-secondary btn--small" type="button" data-testid="license-gpl" @click="openLicense('gpl')">
              查看 GPL 许可
            </button>
            <button class="btn btn-secondary btn--small" type="button" data-testid="license-hmcl" @click="openLicense('hmcl')">
              HMCL 附加条款
            </button>
            <a class="btn btn-subtle btn--small" href="/licenses/GPL-3.0.txt" target="_blank" rel="noopener noreferrer">
              在新标签页打开 GPL 全文
            </a>
          </div>
        </section>
      </template>
    </div>

    <LicenseDialog v-if="licenseTab" :tab="licenseTab" @close="licenseTab = null" />
  </div>
</template>
