<script setup lang="ts">
import { computed, onMounted, ref, watch } from 'vue'

import { ApiError, catalogApi, pluginsApi, runtimesApi } from '@/api'
import type { CatalogItem, CatalogPage, PluginDetails } from '@/api/types'
import AppIcon from '@/components/AppIcon.vue'
import AppNotice from '@/components/AppNotice.vue'
import BaseModal from '@/components/BaseModal.vue'
import ConfirmDialog from '@/components/ConfirmDialog.vue'
import EmptyState from '@/components/EmptyState.vue'
import TaskProgressStrip from '@/components/TaskProgressStrip.vue'
import { useJobStore } from '@/stores/jobs'
import { useNotifyStore } from '@/stores/notify'
import { useAppStateStore } from '@/stores/state'
import { describeApiError } from '@/utils/errors'
import { validateVersion } from '@/utils/validation'

const appState = useAppStateStore()
const jobs = useJobStore()
const notify = useNotifyStore()

const activeTab = ref<'runtimes' | 'catalog'>('runtimes')

// ---------------------------------------------------------------- runtimes tab
const available = ref<string[]>([])
const availableStatus = ref('')
const availableError = ref(false)
const querying = ref(false)
const versionInput = ref('')
const validationError = ref('')
const pending = ref(false)
const removeTarget = ref<string | null>(null)
const removing = ref(false)
/** Compatibility summaries returned by install/probe job results (state.runtimes may lag). */
const compatibilityOverrides = ref<Record<string, string>>({})

const installedRuntimes = computed(() => appState.runtimes.filter((runtime) => runtime.installed))
const versionChoices = computed(() => {
  const set = new Set<string>()
  for (const runtime of appState.runtimes) if (runtime.version) set.add(runtime.version)
  for (const version of available.value) if (version) set.add(version)
  return Array.from(set)
})
const busy = computed(() => jobs.active || querying.value || pending.value || removing.value)

function isInstalled(version: string): boolean {
  return installedRuntimes.value.some((runtime) => runtime.version === version)
}

function compatibilityText(version: string, compatibility: string): string {
  const value = (compatibilityOverrides.value[version] || compatibility || '').trim()
  if (!value || value === '待检测') return '安装后会检测可用命令与能力。'
  return value
}

// Probe/install jobs carry {"version","compatibility"} in their result (contract with server-dev2).
// Watch the result as well: the terminal "state" event arrives before the "result" event.
watch(
  [() => jobs.state, () => jobs.result],
  () => {
    if (jobs.state !== 'SUCCEEDED') return
    const payload = jobs.result
    if (!payload || typeof payload !== 'object') return
    const record = payload as Record<string, unknown>
    const version = typeof record.version === 'string' ? record.version : ''
    const compatibility = typeof record.compatibility === 'string' ? record.compatibility : ''
    if (version && compatibility) {
      compatibilityOverrides.value = { ...compatibilityOverrides.value, [version]: compatibility }
    }
  },
)

async function refreshAvailable(): Promise<void> {
  if (querying.value) return
  querying.value = true
  availableError.value = false
  availableStatus.value = '正在获取发布版本…'
  try {
    const result = await runtimesApi.available()
    available.value = result.versions
    availableStatus.value = '已获取 ' + String(result.versions.length) + ' 个发布版本。'
  } catch (caught) {
    availableError.value = true
    const base = describeApiError(caught, '获取版本列表')
    availableStatus.value = caught instanceof ApiError && caught.status === 404
      ? '获取版本列表接口尚未实现（后端开发中）；可手动输入完整版本号安装。'
      : base + ' 可重试，或手动输入完整版本号。'
  } finally {
    querying.value = false
  }
}

onMounted(() => {
  void appState.refresh()
  void refreshAvailable()
})

function startJob(job: Awaited<ReturnType<typeof runtimesApi.install>>, context: { version?: string }): void {
  jobs.begin(job, context)
  notify.success('已提交任务：' + jobs.typeLabel + '（' + job.id + '）。')
}

async function install(version: string): Promise<void> {
  if (jobs.active || pending.value) {
    notify.info('当前有任务正在执行，请等待完成或取消。')
    return
  }
  const error = validateVersion(version)
  if (error) {
    validationError.value = error
    return
  }
  validationError.value = ''
  pending.value = true
  try {
    const job = await runtimesApi.install(version.trim())
    startJob(job, { version: version.trim() })
    versionInput.value = ''
    await appState.refresh()
  } catch (caught) {
    validationError.value = describeApiError(caught, '安装运行时')
    notify.error(validationError.value)
  } finally {
    pending.value = false
  }
}

async function installInput(): Promise<void> {
  await install(versionInput.value)
}

async function probe(version: string): Promise<void> {
  if (jobs.active || pending.value) {
    notify.info('当前有任务正在执行，请等待完成或取消。')
    return
  }
  pending.value = true
  try {
    const job = await runtimesApi.probe(version)
    startJob(job, { version })
  } catch (caught) {
    notify.error(describeApiError(caught, '检测运行时能力'))
  } finally {
    pending.value = false
  }
}

async function confirmRemove(): Promise<void> {
  const version = removeTarget.value
  if (!version || removing.value) return
  removing.value = true
  try {
    const result = await runtimesApi.remove(version)
    notify.success(
      result.trashedPath
        ? '运行时已移入回收目录：' + result.trashedPath
        : '运行时已移入回收目录。',
    )
    removeTarget.value = null
    await appState.refresh()
  } catch (caught) {
    // Close the confirmation on failure so the page is not blocked by a stale dialog.
    removeTarget.value = null
    notify.error(describeApiError(caught, '移除运行时'))
  } finally {
    removing.value = false
  }
}

// ---------------------------------------------------------------- catalog tab
const catalogQuery = ref('')
const catalog = ref<CatalogPage | null>(null)
const catalogLoading = ref(false)
const catalogSearched = ref(false)
const catalogError = ref('')
const catalogErrorStatus = ref<number | null>(null)
const detailsByRepo = ref<Record<string, PluginDetails>>({})
const detailsLoadingRepo = ref<string | null>(null)
const detailsErrors = ref<Record<string, string>>({})

const installDetails = ref<PluginDetails | null>(null)
const installInstanceId = ref('')
const installTargetSpec = ref('')
const installing = ref(false)
const installError = ref('')

const installTargets = computed(() => installDetails.value?.targets ?? [])
const selectedTarget = computed(
  () => installTargets.value.find((target) => target.spec === installTargetSpec.value) ?? null,
)
const installInstance = computed(
  () => appState.instances.find((instance) => instance.id === installInstanceId.value) ?? null,
)

function statusText(item: CatalogItem): string {
  if (item.status === 'INSTALLABLE') return '可安装'
  if (item.status === 'VIEW_PROJECT') return '仅可查看项目'
  return item.status ? '未解析' : ''
}

function statusClass(item: CatalogItem): string {
  if (item.status === 'INSTALLABLE') return 'badge--green'
  if (item.status === 'VIEW_PROJECT') return 'badge--blue'
  return 'badge--gray'
}

async function searchCatalog(page = 1, refresh = false): Promise<void> {
  if (catalogLoading.value) return
  catalogLoading.value = true
  catalogError.value = ''
  catalogErrorStatus.value = null
  try {
    catalog.value = await catalogApi.search(catalogQuery.value, page, refresh)
    catalogSearched.value = true
    detailsByRepo.value = {}
    detailsErrors.value = {}
  } catch (caught) {
    catalogError.value = describeApiError(caught, '获取插件目录')
    catalogErrorStatus.value = caught instanceof ApiError ? caught.status : null
    catalogSearched.value = true
  } finally {
    catalogLoading.value = false
  }
}

function submitSearch(): void {
  void searchCatalog(1, false)
}

async function inspect(item: CatalogItem): Promise<void> {
  if (detailsLoadingRepo.value !== null) return
  detailsLoadingRepo.value = item.repo
  const nextErrors = { ...detailsErrors.value }
  delete nextErrors[item.repo]
  detailsErrors.value = nextErrors
  try {
    const details = await catalogApi.resolve(item.repo, false)
    detailsByRepo.value = { ...detailsByRepo.value, [item.repo]: details }
  } catch (caught) {
    detailsErrors.value = {
      ...detailsErrors.value,
      [item.repo]: describeApiError(caught, '检查插件安装目标'),
    }
  } finally {
    detailsLoadingRepo.value = null
  }
}

function openProject(item: CatalogItem): void {
  const raw = item.url || 'https://github.com/' + item.repo
  try {
    const url = new URL(raw)
    if (url.protocol !== 'https:' && url.protocol !== 'http:') throw new Error('invalid url')
    const opened = window.open(url.toString(), '_blank', 'noopener')
    if (!opened) notify.info('浏览器阻止了新窗口，请允许本页弹出窗口后重试。')
  } catch {
    notify.error('项目地址无效。')
  }
}

function openInstall(item: CatalogItem): void {
  const details = detailsByRepo.value[item.repo]
  if (!details) return
  if (appState.instances.length === 0) {
    notify.info('请先创建一个实例，再安装插件。')
    return
  }
  installDetails.value = details
  installInstanceId.value = appState.currentInstanceId ?? appState.instances[0].id
  installTargetSpec.value = details.targets[0]?.spec ?? ''
  installError.value = ''
}

function closeInstall(): void {
  if (installing.value) return
  installDetails.value = null
}

async function confirmInstall(): Promise<void> {
  const details = installDetails.value
  if (!details || installing.value) return
  if (!installInstanceId.value) {
    installError.value = '请先选择实例。'
    return
  }
  if (!installTargetSpec.value) {
    installError.value = '请选择已核对的安装目标。'
    return
  }
  installing.value = true
  installError.value = ''
  try {
    const job = await pluginsApi.add(installInstanceId.value, installTargetSpec.value)
    jobs.begin(job, { instanceId: installInstanceId.value })
    notify.success('已提交任务：' + jobs.typeLabel + '（' + job.id + '）。')
    installDetails.value = null
  } catch (caught) {
    installError.value = describeApiError(caught, '安装插件')
  } finally {
    installing.value = false
  }
}
</script>

<template>
  <div class="page page--solid" data-testid="page-runtimes">
    <div class="card-list">
      <div class="page-tabs" role="tablist" aria-label="下载">
        <button
          class="page-tab"
          :class="{ 'is-active': activeTab === 'runtimes' }"
          type="button"
          role="tab"
          :aria-selected="activeTab === 'runtimes'"
          data-testid="download-tab-runtimes"
          @click="activeTab = 'runtimes'"
        >
          <AppIcon name="cpu" :size="15" />运行时
        </button>
        <button
          class="page-tab"
          :class="{ 'is-active': activeTab === 'catalog' }"
          type="button"
          role="tab"
          :aria-selected="activeTab === 'catalog'"
          data-testid="download-tab-catalog"
          @click="activeTab = 'catalog'"
        >
          <AppIcon name="puzzle" :size="15" />插件目录
        </button>
      </div>

      <TaskProgressStrip />

      <template v-if="activeTab === 'runtimes'">
        <div class="toolbar">
          <span class="section-title">运行时</span>
          <span class="small-muted">{{ installedRuntimes.length }} 个已安装</span>
          <span class="toolbar__spacer"></span>
          <button
            class="btn btn-secondary"
            type="button"
            data-testid="runtimes-refresh"
            :disabled="busy"
            @click="refreshAvailable"
          >
            <AppIcon name="refresh" :size="15" />刷新版本
          </button>
        </div>

        <section class="card content-card">
          <p class="card-title">安装 Harness 运行时</p>
          <p class="paragraph">
            从发布列表选择版本，或直接输入完整版本号。已安装版本会保留，实例升级可以单独进行。
          </p>
          <div class="inline-field">
            <input
              v-model="versionInput"
              class="input"
              data-testid="runtime-version"
              list="runtime-version-options"
              placeholder="输入完整版本号，或刷新版本列表"
              :disabled="busy"
            />
            <datalist id="runtime-version-options">
              <option v-for="version in versionChoices" :key="version" :value="version"></option>
            </datalist>
            <button
              class="btn btn-primary"
              type="button"
              data-testid="runtime-install"
              :disabled="busy"
              @click="installInput"
            >
              <AppIcon name="download" :size="15" />安装
            </button>
          </div>
          <p v-if="validationError" class="validation-error" data-testid="runtime-validation" role="alert">
            {{ validationError }}
          </p>
          <p class="small-muted">安装为长任务（≤20 分钟），进度会显示在首页任务卡与任务与日志页。</p>
        </section>

        <section class="card content-card">
          <div class="toolbar">
            <span class="card-title">可用版本</span>
            <span class="toolbar__spacer"></span>
            <span class="small-muted" data-testid="runtimes-available-status">{{ availableStatus || '尚未获取版本列表' }}</span>
          </div>
          <p v-if="availableError" class="validation-error" role="alert">{{ availableStatus }}</p>
          <div v-else-if="available.length" class="version-list" data-testid="runtimes-available">
            <div v-for="version in available" :key="version" class="version-row">
              <span class="version-row__name">{{ version }}</span>
              <span v-if="isInstalled(version)" class="badge badge--green">已安装</span>
              <span class="toolbar__spacer"></span>
              <button
                class="btn btn-secondary btn--small"
                type="button"
                :disabled="busy || isInstalled(version)"
                @click="install(version)"
              >
                安装
              </button>
            </div>
          </div>
          <p v-else class="paragraph">
            {{ querying ? '正在获取发布版本…' : '暂无可用版本；可点击“刷新版本”，或在上方手动输入完整版本号安装。' }}
          </p>
        </section>

        <span class="section-title">本地运行时</span>
        <EmptyState
          v-if="installedRuntimes.length === 0"
          title="还没有运行时"
          description="输入版本并安装后，可在实例设置中选择它。"
          icon="cpu"
        />
        <div v-else class="card-list" data-testid="runtimes-installed">
          <div v-for="runtime in installedRuntimes" :key="runtime.version" class="card runtime-card">
            <div class="runtime-card__info">
              <div class="runtime-card__heading">
                <span class="card-title">{{ runtime.version }}</span>
                <span class="badge badge--green">已安装</span>
              </div>
              <p class="paragraph" data-testid="runtime-compatibility">{{ compatibilityText(runtime.version, runtime.compatibility) }}</p>
            </div>
            <div class="instance-card__actions">
              <button
                class="btn btn-secondary"
                type="button"
                :disabled="busy"
                :data-testid="'runtime-probe-' + runtime.version"
                @click="probe(runtime.version)"
              >
                <AppIcon name="refresh" :size="15" />检测
              </button>
              <button
                class="btn btn-subtle btn--danger"
                type="button"
                :disabled="busy"
                :data-testid="'runtime-remove-' + runtime.version"
                @click="removeTarget = runtime.version"
              >
                <AppIcon name="trash" :size="15" />移除
              </button>
            </div>
          </div>
        </div>

        <AppNotice v-if="appState.error" title="已安装列表读取失败" tone="warn">
          <p class="paragraph">{{ appState.error }}</p>
          <div class="toolbar">
            <button class="btn btn-secondary btn--small" type="button" @click="appState.refresh()">重试</button>
          </div>
        </AppNotice>

        <AppNotice title="版本兼容" tone="info">
          <p class="paragraph">
            启动器按实际命令能力适配 Harness。未验证的新版本会展示检测结果；实例固定版本后，不会随启动器更新自动升级。
          </p>
        </AppNotice>
      </template>

      <template v-else>
        <div class="toolbar">
          <span class="section-title">插件目录</span>
          <span class="toolbar__spacer"></span>
          <span class="small-muted">github.com/topics/dsh-plugin</span>
        </div>
        <div class="catalog-search">
          <input
            v-model="catalogQuery"
            class="input"
            data-testid="catalog-query"
            placeholder="搜索 dsh-plugin 主题项目"
            :disabled="catalogLoading"
            @keyup.enter="submitSearch"
          />
          <button class="btn btn-primary" type="button" data-testid="catalog-search" :disabled="catalogLoading" @click="submitSearch">
            <AppIcon name="refresh" :size="15" />搜索
          </button>
          <button
            class="btn btn-secondary"
            type="button"
            data-testid="catalog-refresh"
            :disabled="catalogLoading"
            @click="searchCatalog(1, true)"
          >
            重新获取
          </button>
        </div>
        <p class="paragraph">
          来源：github.com/topics/dsh-plugin。先检查项目的安装目标，再选择已发布的 npm 包安装到实例。
        </p>

        <AppNotice v-if="catalog?.warning" title="目录提示" tone="info">
          <p class="paragraph">{{ catalog.warning }}</p>
        </AppNotice>

        <div v-if="catalogLoading" class="card card--flat">
          <div class="loading-row"><span class="spinner"></span>正在获取插件目录…</div>
        </div>

        <AppNotice v-else-if="catalogError" title="插件目录读取失败" tone="warn">
          <p class="paragraph">{{ catalogError }}</p>
          <div class="toolbar">
            <button class="btn btn-secondary btn--small" type="button" :disabled="catalogLoading" @click="submitSearch">重试</button>
            <span class="small-muted">
              GET /api/plugin-catalog{{ catalogErrorStatus === 404 ? ' 尚未实现（后端开发中）' : '' }}
            </span>
          </div>
        </AppNotice>

        <EmptyState
          v-else-if="catalogSearched && (catalog?.items.length ?? 0) === 0"
          title="暂无目录结果"
          description="点击搜索获取项目。仅通过安装目标检查的包可在此安装。"
          icon="puzzle"
        />

        <p v-else-if="!catalogSearched" class="paragraph">
          点击“搜索”获取社区插件项目；未解析的项目只提供查看项目入口。
        </p>

        <div v-else class="card-list" data-testid="catalog-list">
          <div
            v-for="item in catalog?.items ?? []"
            :key="item.repo"
            class="card catalog-card"
            :data-testid="'catalog-item-' + item.repo"
          >
            <div class="catalog-card__info">
              <div class="catalog-card__heading">
                <span class="card-title">{{ item.name }}</span>
                <span v-if="item.status" class="badge" :class="statusClass(item)">{{ statusText(item) }}</span>
              </div>
              <p class="small-muted">{{ item.repo }} · ★ {{ item.stars }}</p>
              <p v-if="item.description" class="paragraph">{{ item.description }}</p>

              <template v-if="detailsByRepo[item.repo]">
                <p class="paragraph" data-testid="catalog-details-message">
                  {{ detailsByRepo[item.repo].message }}
                  <template v-if="detailsByRepo[item.repo].fromCache">（缓存结果）</template>
                </p>
                <p
                  v-if="detailsByRepo[item.repo].status !== 'INSTALLABLE'"
                  class="small-muted"
                >
                  该项目未解析出可安装的 npm 包，仅可查看项目。
                </p>
              </template>
              <p v-if="detailsErrors[item.repo]" class="validation-error" role="alert">
                {{ detailsErrors[item.repo] }}
              </p>
            </div>
            <div class="instance-card__actions">
              <button
                class="btn btn-secondary"
                type="button"
                :disabled="detailsLoadingRepo !== null"
                :data-testid="'catalog-inspect-' + item.repo"
                @click="inspect(item)"
              >
                <AppIcon name="check" :size="15" />检查安装目标
              </button>
              <button class="btn btn-subtle" type="button" :data-testid="'catalog-open-' + item.repo" @click="openProject(item)">
                <AppIcon name="external" :size="15" />查看项目
              </button>
              <button
                v-if="detailsByRepo[item.repo]?.status === 'INSTALLABLE' && (detailsByRepo[item.repo]?.targets.length ?? 0) > 0"
                class="btn btn-primary"
                type="button"
                :data-testid="'catalog-install-' + item.repo"
                @click="openInstall(item)"
              >
                <AppIcon name="download" :size="15" />选择目标并安装
              </button>
            </div>
          </div>
        </div>

        <div v-if="catalog && catalogSearched && !catalogError" class="toolbar">
          <span class="small-muted" data-testid="catalog-page-info">
            第 {{ catalog.page }} 页 · {{ catalog.total }} 个项目{{ catalog.fromCache ? ' · 缓存' : '' }}
          </span>
          <span class="toolbar__spacer"></span>
          <button
            class="btn btn-secondary btn--small"
            type="button"
            :disabled="catalogLoading || catalog.page <= 1"
            data-testid="catalog-prev"
            @click="searchCatalog(catalog.page - 1, false)"
          >
            上一页
          </button>
          <button
            class="btn btn-secondary btn--small"
            type="button"
            :disabled="catalogLoading || !catalog.hasNext"
            data-testid="catalog-next"
            @click="searchCatalog(catalog.page + 1, false)"
          >
            下一页
          </button>
        </div>
      </template>
    </div>

    <ConfirmDialog
      v-if="removeTarget"
      title="移除运行时"
      :question="'移除 Harness ' + removeTarget + '？'"
      detail="只能移除没有实例引用的版本；如有引用，请先调整对应实例。运行时会移入回收目录。"
      confirm-text="确认移除"
      :busy="removing"
      @confirm="confirmRemove"
      @cancel="removeTarget = null"
    />

    <BaseModal
      v-if="installDetails"
      title="安装插件 · 确认目标"
      testid="catalog-install-dialog"
      wide
      :closable="!installing"
      @close="closeInstall"
    >
      <p class="paragraph">项目来源：{{ installDetails.repo }}</p>
      <div class="form-field">
        <label class="field-label" for="catalog-install-instance">安装到实例</label>
        <select
          id="catalog-install-instance"
          v-model="installInstanceId"
          class="input"
          data-testid="catalog-install-instance"
          :disabled="installing"
        >
          <option v-for="instance in appState.instances" :key="instance.id" :value="instance.id">
            {{ instance.name }} · {{ instance.profile }}
          </option>
        </select>
        <p v-if="installInstance" class="form-hint">插件将安装到该实例当前 profile（{{ installInstance.profile }}）。</p>
      </div>
      <div class="form-field">
        <label class="field-label" for="catalog-install-target">已核对的 npm 包与精确版本</label>
        <select
          id="catalog-install-target"
          v-model="installTargetSpec"
          class="input"
          data-testid="catalog-install-target"
          :disabled="installing"
        >
          <option v-for="target in installTargets" :key="target.spec" :value="target.spec">
            {{ target.spec }}
          </option>
        </select>
      </div>
      <div v-if="selectedTarget" class="paragraph">
        <p>仓库路径：{{ selectedTarget.packagePath || '—' }}</p>
        <p>
          核对来源：
          <a v-if="selectedTarget.evidenceUrl" :href="selectedTarget.evidenceUrl" target="_blank" rel="noopener noreferrer">
            {{ selectedTarget.evidenceUrl }}
          </a>
          <span v-else>—</span>
        </p>
      </div>
      <p class="paragraph">插件将在所选实例的当前 profile 中运行。请确认你信任此项目。</p>
      <p v-if="installError" class="validation-error" data-testid="catalog-install-error" role="alert">{{ installError }}</p>
      <template #footer>
        <button class="btn btn-secondary" type="button" :disabled="installing" @click="closeInstall">取消</button>
        <button
          class="btn btn-primary"
          type="button"
          data-testid="catalog-confirm-install"
          :disabled="installing || !installTargetSpec"
          @click="confirmInstall"
        >
          {{ installing ? '正在提交…' : '确认安装' }}
        </button>
      </template>
    </BaseModal>
  </div>
</template>
