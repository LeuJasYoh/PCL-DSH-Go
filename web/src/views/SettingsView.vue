<script setup lang="ts">
import { computed, onMounted, ref, watch } from 'vue'

import { ApiError, settingsApi } from '@/api'
import type { AppSettings } from '@/api/types'
import AppIcon from '@/components/AppIcon.vue'
import AppNotice from '@/components/AppNotice.vue'
import BaseModal from '@/components/BaseModal.vue'
import TaskProgressStrip from '@/components/TaskProgressStrip.vue'
import { useAuthStore } from '@/stores/auth'
import { useNotifyStore } from '@/stores/notify'
import { useAppStateStore } from '@/stores/state'
import {
  BACKGROUNDS,
  DEFAULT_BACKGROUND,
  backgroundPosition,
  findBackground,
  isCustomBackground,
  resolveBackground,
} from '@/utils/backgrounds'
import { describeApiError } from '@/utils/errors'
import { validateSettings } from '@/utils/validation'

const appState = useAppStateStore()
const auth = useAuthStore()
const notify = useNotifyStore()

const form = ref<AppSettings>({
  proxy: '',
  registry: 'https://registry.npmjs.org',
  background: DEFAULT_BACKGROUND,
})
const dirty = ref(false)
const initialized = ref(false)
const loading = ref(false)
const loadError = ref('')
const saving = ref(false)
const validationError = ref('')
const uploadBusy = ref(false)
const uploadNote = ref('')
const fileInput = ref<HTMLInputElement | null>(null)

const backgroundValue = computed(() => findBackground(form.value.background))
const customBackground = computed(() => Boolean(form.value.background) && backgroundValue.value === null)
const customBackgroundDisplayable = computed(() => isCustomBackground(form.value.background))
const customPreview = computed(() => resolveBackground(form.value.background))
const backgroundPreviewStyle = computed(() => ({
  backgroundImage: 'url("' + customPreview.value + '")',
  backgroundPosition: backgroundPosition(form.value.background),
}))
const registryPlaceholder = computed(() => 'https://registry.npmjs.org')

onMounted(() => {
  if (appState.lastUpdatedAt !== null) {
    initialize(appState.settings)
    return
  }
  void loadSettings()
})

// The 1.5s state polling must never clobber a form the user is editing (plan 5.3).
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
  form.value = { ...source }
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

function chooseBackground(value: string): void {
  form.value.background = value
  uploadNote.value = ''
  markDirty()
}

function useDefaultBackground(): void {
  form.value.background = DEFAULT_BACKGROUND
  uploadNote.value = ''
  markDirty()
}

async function save(): Promise<void> {
  if (saving.value) return
  validationError.value = validateSettings(form.value.proxy, form.value.registry) ?? ''
  if (validationError.value) return
  saving.value = true
  try {
    const saved = await settingsApi.update({
      proxy: form.value.proxy.trim(),
      registry: form.value.registry.trim(),
      background: form.value.background.trim(),
    })
    appState.replaceSettings(saved)
    initialize(saved)
    dirty.value = false
    notify.success('设置已保存。将用于后续任务。')
  } catch (caught) {
    validationError.value = describeApiError(caught, '保存设置')
  } finally {
    saving.value = false
  }
}

function pickBackgroundFile(): void {
  fileInput.value?.click()
}

async function onBackgroundFile(event: Event): Promise<void> {
  const input = event.target as HTMLInputElement
  const file = input.files?.[0]
  input.value = ''
  if (!file) return
  uploadNote.value = ''
  if (file.type !== 'image/png' && file.type !== 'image/jpeg') {
    uploadNote.value = '请选择 PNG 或 JPEG 图片。'
    return
  }
  if (file.size > 20 * 1024 * 1024) {
    uploadNote.value = '背景图片不能超过 20 MB。'
    return
  }
  uploadBusy.value = true
  try {
    const updated = await settingsApi.uploadBackground(file)
    if (!updated.background) {
      uploadNote.value = '上传接口已接受文件，但没有返回背景资源地址；请刷新后确认。'
      return
    }
    // Upload stores the asset; applying means putting its URL into the settings form.
    form.value.background = updated.background
    uploadNote.value = '背景已上传并应用；点击“保存设置”后写入配置。'
    markDirty()
    notify.success('自定义背景已上传；点击“保存设置”后保存。')
  } catch (caught) {
    if (caught instanceof ApiError && (caught.status === 404 || caught.status === 405 || caught.status === 501)) {
      uploadNote.value = '自定义背景上传接口尚未实现（开发中）；当前可选择 5 张内置背景并保存。'
    } else {
      uploadNote.value = describeApiError(caught, '上传背景')
    }
  } finally {
    uploadBusy.value = false
  }
}

// ---------------------------------------------------------------- about / licenses
type LicenseTab = 'gpl' | 'hmcl'

const licenseTab = ref<LicenseTab | null>(null)
const licenseText = ref('')
const licenseLoading = ref(false)
const licenseError = ref('')

const LICENSE_FILES: Record<LicenseTab, { url: string; title: string }> = {
  gpl: { url: '/licenses/GPL-3.0.txt', title: 'GPL 第 3 版' },
  hmcl: { url: '/licenses/HMCL-ADDITIONAL-TERMS.md', title: 'HMCL 附加条款' },
}

async function openLicense(tab: LicenseTab): Promise<void> {
  licenseTab.value = tab
  await loadLicense(tab)
}

async function loadLicense(tab: LicenseTab): Promise<void> {
  licenseText.value = ''
  licenseError.value = ''
  licenseLoading.value = true
  try {
    const response = await fetch(LICENSE_FILES[tab].url, { credentials: 'include' })
    if (!response.ok) throw new Error('HTTP ' + String(response.status))
    licenseText.value = await response.text()
  } catch {
    licenseError.value = '无法读取许可文本，请查看随程序提供的 LICENSE 文件。'
  } finally {
    licenseLoading.value = false
  }
}

function switchLicense(tab: LicenseTab): void {
  if (tab === licenseTab.value) return
  licenseTab.value = tab
  void loadLicense(tab)
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
          <p class="paragraph">代理用于下载和版本查询。留空关闭启动器的代理设置。</p>
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
              :placeholder="registryPlaceholder"
              :disabled="saving"
              @input="markDirty"
            />
          </div>
          <p class="small-muted">示例：http://127.0.0.1:7890；留空使用直连。不要填写包含账号密码的地址。</p>
          <p v-if="validationError" class="validation-error" data-testid="settings-validation" role="alert">
            {{ validationError }}
          </p>
        </section>

        <section class="card content-card">
          <p class="card-title">外观</p>
          <p class="paragraph">
            默认使用「晴空招手」。内置背景按右对齐显示，自定义背景居中显示；选择或上传背景后点击“保存设置”生效。
          </p>
          <div class="background-grid" data-testid="background-list">
            <button
              v-for="entry in BACKGROUNDS"
              :key="entry.id"
              class="background-choice"
              :class="{ 'is-selected': form.background === entry.value }"
              type="button"
              :data-testid="'background-' + entry.id"
              :aria-pressed="form.background === entry.value"
              :disabled="saving"
              @click="chooseBackground(entry.value)"
            >
              <img class="background-thumb" :src="entry.src" :alt="entry.title" :data-testid="'thumbnail-' + entry.id" />
              <span class="background-choice__label">{{ entry.title }}</span>
            </button>
            <button
              v-if="customBackground"
              class="background-choice"
              :class="{ 'is-selected': customBackground }"
              type="button"
              data-testid="background-custom"
              aria-pressed="true"
              :disabled="saving"
            >
              <span class="background-thumb background-thumb--custom" :style="backgroundPreviewStyle" aria-hidden="true"></span>
              <span class="background-choice__label">自定义背景</span>
            </button>
          </div>
          <div class="form-field">
            <span class="field-label">自定义背景（本地图片）</span>
            <div class="inline-field">
              <input
                ref="fileInput"
                class="sr-only"
                type="file"
                accept="image/png,image/jpeg"
                data-testid="background-file"
                @change="onBackgroundFile"
              />
              <button
                class="btn btn-secondary"
                type="button"
                data-testid="background-upload"
                :disabled="saving || uploadBusy"
                @click="pickBackgroundFile"
              >
                <AppIcon name="folder" :size="15" />{{ uploadBusy ? '正在上传…' : '选择图片' }}
              </button>
              <button
                class="btn btn-subtle"
                type="button"
                data-testid="background-reset"
                :disabled="saving"
                @click="useDefaultBackground"
              >
                <AppIcon name="refresh" :size="15" />使用默认背景
              </button>
              <span class="small-muted">PNG / JPEG，≤ 20 MB，尺寸 1–8192。</span>
            </div>
            <p v-if="customBackground && !customBackgroundDisplayable" class="form-hint" data-testid="custom-background-hint">
              当前配置是旧版本地路径（{{ form.background }}），浏览器无法显示，已回退默认背景；请上传图片或选择内置背景。
            </p>
            <p v-else-if="customBackground" class="form-hint" data-testid="custom-background-hint">
              当前使用自定义背景资源（{{ form.background }}）。
            </p>
            <p v-if="uploadNote" class="form-hint" data-testid="background-upload-note">{{ uploadNote }}</p>
          </div>
        </section>

        <section class="card content-card" data-testid="about-card">
          <p class="card-title">关于 PCL-DSH-Linux</p>
          <div class="about-lines paragraph">
            <p>界面改编自 Hello Minecraft! Launcher（HMCL）</p>
            <p>版权所有 © 2013-2026 huangyuhui 及贡献者</p>
            <p>HDSL 桌面版版权归 jiefing 及贡献者所有；PCL-DSH-Linux 保留上游可见版权，并以独立名称和版本区别于原版。</p>
            <p>PCL-DSH-Linux {{ auth.version }} · Copyright (c) 2026 jiefing 及贡献者 · GPL-3.0-or-later（GNU GPL 第 3 版或后续版本）</p>
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

    <BaseModal
      v-if="licenseTab"
      :title="licenseTab === 'gpl' ? 'GNU General Public License' : 'HMCL 附加条款'"
      testid="license-dialog"
      wide
      @close="licenseTab = null"
    >
      <div class="page-tabs" role="tablist" aria-label="许可">
        <button
          class="page-tab"
          :class="{ 'is-active': licenseTab === 'gpl' }"
          type="button"
          role="tab"
          :aria-selected="licenseTab === 'gpl'"
          data-testid="license-tab-gpl"
          @click="switchLicense('gpl')"
        >
          GPL 第 3 版
        </button>
        <button
          class="page-tab"
          :class="{ 'is-active': licenseTab === 'hmcl' }"
          type="button"
          role="tab"
          :aria-selected="licenseTab === 'hmcl'"
          data-testid="license-tab-hmcl"
          @click="switchLicense('hmcl')"
        >
          HMCL 附加条款
        </button>
      </div>
      <div v-if="licenseLoading" class="loading-row"><span class="spinner"></span>正在读取许可文本…</div>
      <p v-else-if="licenseError" class="validation-error" data-testid="license-error">{{ licenseError }}</p>
      <pre v-else class="license-text" data-testid="license-text">{{ licenseText }}</pre>
      <template #footer>
        <a class="btn btn-secondary btn--small" :href="LICENSE_FILES[licenseTab].url" target="_blank" rel="noopener noreferrer">
          在新标签页打开
        </a>
        <button class="btn btn-primary" type="button" @click="licenseTab = null">关闭</button>
      </template>
    </BaseModal>
  </div>
</template>
