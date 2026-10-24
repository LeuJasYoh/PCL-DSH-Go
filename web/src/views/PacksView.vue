<script setup lang="ts">
import { computed, onMounted, ref, watch } from 'vue'
import { useRouter } from 'vue-router'

import { artifactsApi, instancesApi, packsApi } from '@/api'
import { isRecord } from '@/api/normalize'
import type { ArtifactRef, PackExportMode, PackPreview } from '@/api/types'
import AppIcon from '@/components/AppIcon.vue'
import AppNotice from '@/components/AppNotice.vue'
import TaskProgressStrip from '@/components/TaskProgressStrip.vue'
import { useJobStore } from '@/stores/jobs'
import { useNotifyStore } from '@/stores/notify'
import { useAppStateStore } from '@/stores/state'
import { formatBytes, isRunningStatus } from '@/utils/display'
import { triggerDownload } from '@/utils/download'
import { describeApiError } from '@/utils/errors'
import { validateInstanceName, validateVersion } from '@/utils/validation'

const appState = useAppStateStore()
const jobs = useJobStore()
const notify = useNotifyStore()
const router = useRouter()

// ---------------------------------------------------------------- upload / preview
const fileInput = ref<HTMLInputElement | null>(null)
const selectedFile = ref<File | null>(null)
const uploading = ref(false)
const uploadPercent = ref<number | null>(null)
const uploadError = ref('')
const preview = ref<PackPreview | null>(null)
const uploadController = ref<AbortController | null>(null)

// ---------------------------------------------------------------- import
const importName = ref('')
const importVersion = ref('')
const importError = ref('')
const importSubmitting = ref(false)
let handledImportJob: string | null = null

// ---------------------------------------------------------------- export
const exportInstanceId = ref('')
const exportMode = ref<PackExportMode>('profile')
const exportError = ref('')
const exportSubmitting = ref(false)
const lastExport = ref<ArtifactRef | null>(null)
let handledExportJob: string | null = null

const instances = computed(() => appState.instances)
const exportInstance = computed(
  () => instances.value.find((instance) => instance.id === exportInstanceId.value) ?? null,
)
const exportBlocked = computed(
  () => exportInstance.value !== null && isRunningStatus(exportInstance.value.status),
)
// Backend rule: "home" export requires a non-web, initialized profile; web instances
// only support exporting the current profile (desktop behaves the same way).
const homeModeAllowed = computed(
  () => exportInstance.value !== null && exportInstance.value.profile !== 'web',
)

onMounted(() => {
  void appState.refresh()
  exportInstanceId.value = appState.currentInstanceId ?? appState.instances[0]?.id ?? ''
})

watch(
  () => appState.currentInstanceId,
  () => {
    if (!exportInstanceId.value || !instances.value.some((item) => item.id === exportInstanceId.value)) {
      exportInstanceId.value = appState.currentInstanceId ?? instances.value[0]?.id ?? ''
    }
  },
)

watch(
  homeModeAllowed,
  (allowed) => {
    if (!allowed && exportMode.value === 'home') exportMode.value = 'profile'
  },
)

// Watch result as well: the terminal "state" event arrives before the "result" event.
watch(
  [() => jobs.state, () => jobs.result, () => jobs.type],
  () => {
    if (jobs.state !== 'SUCCEEDED' || !jobs.jobId) return
    const result = isRecord(jobs.result) ? jobs.result : null
    if (jobs.type === 'IMPORT_PACK' && result && typeof result.instanceId === 'string') {
      if (handledImportJob === jobs.jobId) return
      handledImportJob = jobs.jobId
      void finishImport(result)
    } else if (jobs.type === 'EXPORT_PACK' && result) {
      // The terminal "state" event arrives before the "result" payload; wait for it.
      if (handledExportJob === jobs.jobId) return
      handledExportJob = jobs.jobId
      finishExport(result)
    }
  },
)

async function finishImport(result: Record<string, unknown>): Promise<void> {
  const instanceId = typeof result.instanceId === 'string' ? result.instanceId : ''
  const name = typeof result.name === 'string' ? result.name : ''
  notify.success('整合包导入完成' + (name ? '：' + name : '') + '。')
  preview.value = null
  selectedFile.value = null
  importName.value = ''
  importVersion.value = ''
  await appState.refresh()
  if (instanceId) {
    try {
      await instancesApi.select(instanceId)
      appState.setCurrentInstanceId(instanceId)
    } catch {
      // The new instance still appears in the list even if selecting it failed.
    }
    await appState.refresh()
  }
  void router.push({ name: 'instances' })
}

function finishExport(result: unknown): void {
  const artifact = packsApi.artifactFromResult(result)
  if (!artifact) {
    notify.error('导出任务完成，但没有返回可下载的产物信息。请稍后在任务页或数据目录查看。')
    return
  }
  lastExport.value = artifact
  triggerDownload(artifactsApi.url(artifact.artifactId), artifact.filename)
  notify.success('导出完成，已开始下载' + (artifact.filename ? '：' + artifact.filename : '。'))
}

function pickFile(): void {
  if (uploading.value) return
  fileInput.value?.click()
}

function onFileChange(event: Event): void {
  const input = event.target as HTMLInputElement
  const file = input.files?.[0]
  input.value = ''
  if (!file) return
  if (!/\.(dspack|zip)$/i.test(file.name)) {
    uploadError.value = '请选择 .dspack 或兼容的快照 ZIP 整合包。'
    return
  }
  void startUpload(file)
}

function defaultInstanceName(filename: string): string {
  return filename.replace(/\.[^.]+$/, '')
}

async function startUpload(file: File): Promise<void> {
  cancelUpload()
  preview.value = null
  uploadError.value = ''
  selectedFile.value = file
  const controller = new AbortController()
  uploadController.value = controller
  uploading.value = true
  uploadPercent.value = null
  try {
    const result = await packsApi.preview(file, {
      signal: controller.signal,
      onProgress: (percent) => {
        uploadPercent.value = percent
      },
    })
    if (controller.signal.aborted) return
    preview.value = result
    uploadPercent.value = 100
    importError.value = ''
    importName.value = result.name || defaultInstanceName(file.name)
    importVersion.value = ''
  } catch (caught) {
    if (caught instanceof DOMException && caught.name === 'AbortError') {
      uploadError.value = '已取消上传，可重新选择整合包。'
    } else {
      uploadError.value = describeApiError(caught, '上传整合包')
    }
  } finally {
    if (uploadController.value === controller) {
      uploadController.value = null
      uploading.value = false
    }
  }
}

function cancelUpload(): void {
  const controller = uploadController.value
  if (controller) {
    controller.abort()
    uploadController.value = null
  }
  uploading.value = false
  uploadPercent.value = null
}

function resetPreview(): void {
  cancelUpload()
  preview.value = null
  selectedFile.value = null
  uploadError.value = ''
  importError.value = ''
  importName.value = ''
  importVersion.value = ''
}

function progressText(): string {
  if (uploadPercent.value === null) return '正在上传整合包…（大文件可能需要较长时间）'
  if (uploadPercent.value >= 100) return '上传完成，正在检查整合包…'
  return '正在上传整合包… ' + String(uploadPercent.value) + '%'
}

// ---------------------------------------------------------------- import / export
async function importPack(): Promise<void> {
  if (!preview.value || importSubmitting.value) return
  if (jobs.active) {
    notify.info('当前有任务正在执行，请等待完成或取消。')
    return
  }
  importError.value = ''
  const name = importName.value.trim()
  const nameError = validateInstanceName(name)
  if (nameError) {
    importError.value = nameError
    return
  }
  const version = importVersion.value.trim()
  if (version) {
    const versionError = validateVersion(version)
    if (versionError) {
      importError.value = versionError
      return
    }
  }
  importSubmitting.value = true
  try {
    const job = await packsApi.importPack({
      previewId: preview.value.previewId,
      name,
      version: version || undefined,
    })
    handledImportJob = null
    jobs.begin(job, { version: version || preview.value.dshVersion })
    notify.success('已提交任务：' + jobs.typeLabel + '（' + job.id + '）。')
  } catch (caught) {
    importError.value = describeApiError(caught, '导入整合包')
  } finally {
    importSubmitting.value = false
  }
}

async function exportPack(): Promise<void> {
  if (exportSubmitting.value) return
  if (jobs.active) {
    notify.info('当前有任务正在执行，请等待完成或取消。')
    return
  }
  const instance = exportInstance.value
  if (!instance) {
    exportError.value = '请先选择要导出的实例。'
    return
  }
  if (isRunningStatus(instance.status)) {
    exportError.value = '请先停止实例，再导出整合包。'
    return
  }
  exportError.value = ''
  exportSubmitting.value = true
  try {
    const job = await packsApi.exportPack({ instanceId: instance.id, mode: exportMode.value })
    handledExportJob = null
    jobs.begin(job, { instanceId: instance.id })
    notify.success('已提交任务：' + jobs.typeLabel + '（' + job.id + '）。')
  } catch (caught) {
    exportError.value = describeApiError(caught, '导出整合包')
  } finally {
    exportSubmitting.value = false
  }
}

function redownload(): void {
  if (!lastExport.value) return
  triggerDownload(artifactsApi.url(lastExport.value.artifactId), lastExport.value.filename)
}
</script>

<template>
  <div class="page page--solid" data-testid="page-packs">
    <div class="card-list">
      <div class="toolbar">
        <span class="section-title">整合包</span>
        <span class="small-muted">.dspack / 兼容快照 ZIP</span>
        <span class="toolbar__spacer"></span>
      </div>

      <TaskProgressStrip />

      <AppNotice title="先预览，再导入" tone="info">
        <p class="paragraph">
          导入会读取清单并检查文件路径。带平台依赖的快照可能只能在对应系统运行；请查看预览中的兼容提示。
        </p>
      </AppNotice>
      <AppNotice title="分享包的导出范围" tone="info">
        <p class="paragraph">
          分享包不包含凭据、会话和可能含密钥的 patch 配置；导入后可能需要重新配置。
        </p>
      </AppNotice>

      <section class="card content-card" data-testid="pack-upload-card">
        <p class="card-title">导入整合包</p>
        <p class="paragraph">选择 .dspack 或兼容的快照 ZIP，先检查包信息，再导入为新实例。</p>
        <input
          ref="fileInput"
          class="sr-only"
          type="file"
          accept=".dspack,.zip,application/zip"
          data-testid="pack-file"
          @change="onFileChange"
        />
        <div class="toolbar">
          <button
            class="btn btn-primary"
            type="button"
            data-testid="pack-upload"
            :disabled="uploading || jobs.active"
            @click="pickFile"
          >
            <AppIcon name="download" :size="15" />{{ selectedFile ? '重新选择整合包' : '选择整合包' }}
          </button>
          <button
            v-if="uploading"
            class="btn btn-secondary"
            type="button"
            data-testid="pack-cancel-upload"
            @click="cancelUpload"
          >
            取消上传
          </button>
          <span v-if="selectedFile" class="small-muted" data-testid="pack-filename">{{ selectedFile.name }}</span>
        </div>

        <div v-if="uploading" class="pack-progress" data-testid="pack-upload-progress">
          <div class="pack-progress__track">
            <div
              class="pack-progress__bar"
              :class="{ 'is-indeterminate': uploadPercent === null }"
              :style="uploadPercent === null ? {} : { width: uploadPercent + '%' }"
            ></div>
          </div>
          <p class="small-muted">{{ progressText() }}</p>
        </div>
        <p v-if="uploadError" class="validation-error" data-testid="pack-upload-error" role="alert">{{ uploadError }}</p>

        <div v-if="preview" class="pack-preview" data-testid="pack-preview">
          <div class="pack-preview__heading">
            <span class="card-title">包预览</span>
            <button class="btn btn-subtle btn--small" type="button" data-testid="pack-reset" @click="resetPreview">
              重新选择
            </button>
          </div>
          <dl class="pack-preview__grid">
            <div class="pack-preview__row"><dt>名称</dt><dd data-testid="pack-preview-name">{{ preview.name || '未声明' }}</dd></div>
            <div class="pack-preview__row"><dt>格式</dt><dd>{{ preview.format || '未声明' }}</dd></div>
            <div class="pack-preview__row"><dt>包版本</dt><dd>{{ preview.version || '未声明' }}</dd></div>
            <div class="pack-preview__row"><dt>Harness 版本</dt><dd>{{ preview.dshVersion || '未声明' }}</dd></div>
            <div class="pack-preview__row">
              <dt>Profile</dt>
              <dd>{{ preview.profiles.length ? preview.profiles.join('、') : '未声明' }}</dd>
            </div>
            <div class="pack-preview__row"><dt>解压体积约</dt><dd>{{ formatBytes(preview.unpackedBytes) }}</dd></div>
          </dl>
          <div v-if="preview.warnings.length" class="pack-warning" data-testid="pack-warning" role="alert">
            <p class="pack-warning__title">兼容性 / 安全提示（请确认后再导入）</p>
            <ul>
              <li v-for="warning in preview.warnings" :key="warning">{{ warning }}</li>
            </ul>
          </div>
        </div>
      </section>

      <section v-if="preview" class="card content-card" data-testid="pack-import-card">
        <p class="card-title">导入为新实例</p>
        <p class="paragraph">导入会创建独立实例，并在需要时安装包声明的 Harness 运行时。</p>
        <div class="form-field">
          <label class="field-label" for="pack-import-name">实例名称</label>
          <input
            id="pack-import-name"
            v-model="importName"
            class="input"
            data-testid="pack-import-name"
            placeholder="例如 从分享包导入的实例"
            :disabled="importSubmitting || jobs.active"
          />
        </div>
        <div class="form-field">
          <label class="field-label" for="pack-import-version">Harness 版本（可选）</label>
          <input
            id="pack-import-version"
            v-model="importVersion"
            class="input"
            data-testid="pack-import-version"
            :placeholder="preview.dshVersion ? '留空使用包声明的 ' + preview.dshVersion : '留空使用包声明或已安装的兼容版本'"
            :disabled="importSubmitting || jobs.active"
          />
          <p class="form-hint">请输入精确版本号，例如 0.1.0-rc.6；留空时由后端按包声明与已安装版本解析。</p>
        </div>
        <p v-if="importError" class="validation-error" data-testid="pack-import-error" role="alert">{{ importError }}</p>
        <div class="toolbar">
          <button
            class="btn btn-primary"
            type="button"
            data-testid="pack-import"
            :disabled="importSubmitting || jobs.active"
            @click="importPack"
          >
            <AppIcon name="download" :size="15" />{{ importSubmitting ? '正在提交…' : '导入为新实例' }}
          </button>
          <span class="small-muted">预览在 30 分钟内有效；失败会在任务日志中显示原因。</span>
        </div>
      </section>

      <section class="card content-card" data-testid="pack-export-card">
        <p class="card-title">导出整合包</p>
        <p class="paragraph">
          选择实例与范围后导出；导出的是可分享配置（不含凭据、会话与可能含密钥的 patch 配置）。
        </p>
        <div class="form-field">
          <label class="field-label" for="pack-export-instance">实例</label>
          <select
            id="pack-export-instance"
            v-model="exportInstanceId"
            class="input"
            data-testid="pack-export-instance"
            :disabled="exportSubmitting || jobs.active || instances.length === 0"
          >
            <option value="" disabled>选择实例</option>
            <option v-for="instance in instances" :key="instance.id" :value="instance.id">
              {{ instance.name }} · {{ instance.profile }} · {{ instance.version }}
            </option>
          </select>
          <p v-if="instances.length === 0" class="form-hint">还没有实例；先创建或导入一个实例，再导出整合包。</p>
        </div>
        <div class="form-field">
          <span class="field-label">导出范围</span>
          <div class="choice-row" role="radiogroup" aria-label="导出范围">
            <label class="choice-item">
              <input v-model="exportMode" type="radio" value="profile" data-testid="pack-export-mode-profile" />
              <span>当前 profile</span>
            </label>
            <label class="choice-item" :class="{ 'is-disabled': !homeModeAllowed }">
              <input
                v-model="exportMode"
                type="radio"
                value="home"
                data-testid="pack-export-mode-home"
                :disabled="!homeModeAllowed"
              />
              <span>整个实例</span>
            </label>
          </div>
          <p class="form-hint">
            {{ exportMode === 'profile'
              ? '仅导出所选实例当前 profile 的可分享配置与插件。'
              : '导出整个实例目录（仍不包含凭据、会话与数据库缓存）。' }}
          </p>
          <p v-if="!homeModeAllowed" class="form-hint" data-testid="pack-export-home-hint">
            web 实例只能导出当前 profile；整个实例导出要求该实例使用非 web 的已初始化 profile。
          </p>
        </div>
        <p v-if="exportBlocked" class="validation-error" data-testid="pack-export-running" role="alert">
          实例正在运行，请先停止该实例后再导出。
        </p>
        <p v-if="exportError" class="validation-error" data-testid="pack-export-error" role="alert">{{ exportError }}</p>
        <div class="toolbar">
          <button
            class="btn btn-primary"
            type="button"
            data-testid="pack-export"
            :disabled="exportSubmitting || jobs.active || !exportInstance || exportBlocked"
            @click="exportPack"
          >
            <AppIcon name="upload" :size="15" />{{ exportSubmitting ? '正在提交…' : '导出 .dspack' }}
          </button>
          <span class="small-muted">导出为长任务（≤10 分钟）；完成后自动开始下载。</span>
        </div>
        <div v-if="lastExport" class="pack-export-result" data-testid="pack-export-result">
          <p class="paragraph">
            上次导出：{{ lastExport.filename || '整合包' }}（{{ formatBytes(lastExport.size) }}）
          </p>
          <button class="btn btn-secondary btn--small" type="button" data-testid="pack-export-download" @click="redownload">
            <AppIcon name="download" :size="14" />重新下载
          </button>
        </div>
      </section>
    </div>
  </div>
</template>
