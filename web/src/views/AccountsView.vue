<script setup lang="ts">
import { computed, onMounted, ref, watch } from 'vue'

import { accountsApi, instancesApi } from '@/api'
import type { AccountInput, AccountSummary, ProviderPreset } from '@/api/types'
import AccountDialog from '@/components/AccountDialog.vue'
import AppIcon from '@/components/AppIcon.vue'
import AppNotice from '@/components/AppNotice.vue'
import ConfirmDialog from '@/components/ConfirmDialog.vue'
import EmptyState from '@/components/EmptyState.vue'
import { useNotifyStore } from '@/stores/notify'
import { useAppStateStore } from '@/stores/state'
import { describeApiError } from '@/utils/errors'

const appState = useAppStateStore()
const notify = useNotifyStore()

const providers = ref<ProviderPreset[]>([])
const providersError = ref('')
const providersLoading = ref(false)
const refreshing = ref(false)

const dialogMode = ref<'create' | 'edit' | null>(null)
const editing = ref<AccountSummary | null>(null)
const saving = ref(false)
const dialogError = ref('')

const deleteTarget = ref<AccountSummary | null>(null)
const deleting = ref(false)
const binding = ref(false)
const bindAccountId = ref('')

const accounts = computed(() => appState.accounts)
const current = computed(() => appState.currentInstance)
const boundAccount = computed(() => {
  const instance = current.value
  if (!instance || !instance.accountId) return null
  return accounts.value.find((account) => account.id === instance.accountId) ?? null
})
const busy = computed(() => saving.value || deleting.value || binding.value || refreshing.value)

onMounted(() => {
  void appState.refresh()
  void loadProviders()
  void refreshAccounts()
})

watch(
  () => current.value?.accountId,
  (accountId) => {
    bindAccountId.value = accountId ?? ''
  },
  { immediate: true },
)

async function loadProviders(): Promise<void> {
  if (providersLoading.value) return
  providersLoading.value = true
  providersError.value = ''
  try {
    providers.value = await accountsApi.providers()
  } catch (caught) {
    providers.value = []
    providersError.value = describeApiError(caught, '读取提供商列表')
  } finally {
    providersLoading.value = false
  }
}

/** Fast refresh through GET /api/accounts; state polling remains the fallback. */
async function refreshAccounts(): Promise<void> {
  refreshing.value = true
  try {
    appState.replaceAccounts(await accountsApi.list())
  } catch {
    // The aggregated /api/state poll still refreshes this list; keep last known data.
  } finally {
    refreshing.value = false
  }
}

function openCreate(): void {
  if (busy.value) return
  dialogMode.value = 'create'
  editing.value = null
  dialogError.value = ''
}

function openEdit(account: AccountSummary): void {
  if (busy.value) return
  dialogMode.value = 'edit'
  editing.value = account
  dialogError.value = ''
}

function closeDialog(): void {
  if (saving.value) return
  dialogMode.value = null
  editing.value = null
  dialogError.value = ''
}

async function submitAccount(input: AccountInput): Promise<void> {
  if (saving.value || dialogMode.value === null) return
  saving.value = true
  dialogError.value = ''
  const creating = dialogMode.value === 'create'
  try {
    if (creating) await accountsApi.create(input)
    else if (editing.value) await accountsApi.update(editing.value.id, input)
    notify.success('账户已保存；启动实例时应用所选账户，尚未验证 API 连接。')
    dialogMode.value = null
    editing.value = null
    await refreshAccounts()
    await appState.refresh()
  } catch (caught) {
    dialogError.value = describeApiError(caught, '保存账户')
  } finally {
    saving.value = false
  }
}

async function confirmDelete(): Promise<void> {
  const target = deleteTarget.value
  if (!target || deleting.value) return
  deleting.value = true
  try {
    await accountsApi.remove(target.id)
    notify.success('账户已删除。')
    deleteTarget.value = null
    await refreshAccounts()
    await appState.refresh()
  } catch (caught) {
    // Close the confirmation so a 409 (still referenced) does not block the page.
    deleteTarget.value = null
    notify.error(describeApiError(caught, '删除账户'))
  } finally {
    deleting.value = false
  }
}

async function bindAccount(): Promise<void> {
  if (!current.value || binding.value) return
  binding.value = true
  try {
    await instancesApi.bindAccount(current.value.id, bindAccountId.value)
    notify.success('实例账户已更新，将在下次启动时生效。')
    await appState.refresh()
  } catch (caught) {
    notify.error(describeApiError(caught, '绑定实例账户'))
  } finally {
    binding.value = false
  }
}
</script>

<template>
  <div class="page page--solid" data-testid="page-accounts">
    <div class="card-list">
      <div class="toolbar">
        <span class="section-title">账户管理</span>
        <span class="small-muted">{{ accounts.length }} 个账户</span>
        <span class="toolbar__spacer"></span>
        <button class="btn btn-secondary" type="button" :disabled="refreshing || busy" @click="refreshAccounts">
          <AppIcon name="refresh" :size="15" />刷新
        </button>
        <button class="btn btn-primary" type="button" data-testid="account-create" :disabled="busy" @click="openCreate">
          <AppIcon name="plus" :size="15" />添加账户
        </button>
      </div>

      <AppNotice title="账户与实例" tone="info">
        <p class="paragraph">
          保存 API 提供商、地址和模型后，在实例中选择账户。保存配置不会测试 API 连接，也不会发送模型请求。
        </p>
      </AppNotice>

      <AppNotice v-if="providersError" title="提供商列表读取失败" tone="warn">
        <p class="paragraph">
          {{ providersError }} 新增/编辑账户时可手动输入提供商标识、API 地址与模型。
        </p>
        <div class="toolbar">
          <button class="btn btn-secondary btn--small" type="button" :disabled="providersLoading" @click="loadProviders">
            重试读取提供商
          </button>
          <span class="small-muted">GET /api/accounts/providers</span>
        </div>
      </AppNotice>

      <div v-if="current" class="card content-card">
        <p class="card-title">当前实例 · {{ current.name }}</p>
        <p class="paragraph">
          {{ boundAccount
            ? '已绑定账户「' + boundAccount.name + '」（' + (boundAccount.provider || '未知提供商') + '）'
            : '未绑定账户 · 使用实例原有配置' }}
        </p>
        <p class="small-muted">
          不绑定时使用实例原有配置。绑定更改在下次启动时生效，用于默认模型；已有会话可能保留单独选择的模型。
        </p>
        <div class="toolbar">
          <select
            v-model="bindAccountId"
            class="input account-bind-select"
            data-testid="account-bind-select"
            :disabled="busy || accounts.length === 0"
            aria-label="选择要绑定到当前实例的账户"
          >
            <option value="">不绑定 · 使用实例现有配置</option>
            <option v-for="account in accounts" :key="account.id" :value="account.id">
              {{ account.name }} · {{ account.provider || '未知提供商' }}
            </option>
          </select>
          <button
            class="btn btn-secondary"
            type="button"
            data-testid="account-bind"
            :disabled="busy || accounts.length === 0"
            @click="bindAccount"
          >
            <AppIcon name="check" :size="15" />应用到当前实例
          </button>
        </div>
        <p v-if="accounts.length === 0" class="form-hint">先添加一个账户，才能绑定到实例。</p>
      </div>

      <AppNotice v-if="appState.error" title="账户列表读取失败" tone="warn">
        <p class="paragraph">{{ appState.error }}</p>
        <div class="toolbar">
          <button class="btn btn-secondary btn--small" type="button" @click="appState.refresh()">重试</button>
          <span class="small-muted">账户列表来自 GET /api/state 与 GET /api/accounts</span>
        </div>
      </AppNotice>

      <EmptyState
        v-else-if="accounts.length === 0"
        title="尚未保存账户"
        description="添加一个 API 账户，即可供多个实例选择。保存配置不会验证 API 连接。"
        icon="user"
      >
        <button class="btn btn-primary" type="button" data-testid="account-create-empty" :disabled="busy" @click="openCreate">
          <AppIcon name="plus" :size="15" />添加账户
        </button>
      </EmptyState>

      <div v-else class="card-list" data-testid="account-list">
        <div v-for="account in accounts" :key="account.id" class="card account-card">
          <div class="account-card__info">
            <div class="account-card__heading">
              <span class="card-title">{{ account.name || '未命名账户' }}</span>
              <span v-if="account.hasKey" class="badge badge--green">已保存 API key</span>
              <span v-else class="badge badge--gray">未保存 API key</span>
              <span v-if="account.status" class="badge badge--blue">{{ account.status }}</span>
            </div>
            <p class="paragraph">
              {{ account.provider || '未知提供商' }}
              <template v-if="account.model"> · {{ account.model }}</template>
              <template v-if="account.baseUrl"> · {{ account.baseUrl }}</template>
            </p>
            <p class="small-muted">API 连接未验证</p>
          </div>
          <div class="instance-card__actions">
            <button
              class="btn btn-secondary"
              type="button"
              :disabled="busy"
              :data-testid="'account-edit-' + account.id"
              @click="openEdit(account)"
            >
              <AppIcon name="settings" :size="15" />编辑
            </button>
            <button
              class="btn btn-subtle btn--danger"
              type="button"
              :disabled="busy"
              :data-testid="'account-delete-' + account.id"
              @click="deleteTarget = account"
            >
              <AppIcon name="trash" :size="15" />删除
            </button>
          </div>
        </div>
      </div>

      <AppNotice title="账户安全" tone="info">
        <p class="paragraph">
          账户密钥加密保存在服务端，不会通过接口返回给浏览器；列表只显示是否已保存 key。删除被实例引用的账户会返回 409，
          需先解除绑定。
        </p>
      </AppNotice>
    </div>

    <AccountDialog
      v-if="dialogMode"
      :mode="dialogMode"
      :account="editing"
      :providers="providers"
      :submitting="saving"
      :submit-error="dialogError"
      @submit="submitAccount"
      @close="closeDialog"
    />

    <ConfirmDialog
      v-if="deleteTarget"
      title="删除账户"
      :question="'删除「' + deleteTarget.name + '」？'"
      detail="删除前请解除实例对该账户的绑定。账户密钥将从服务端删除，无法恢复。"
      confirm-text="确认删除"
      :busy="deleting"
      @confirm="confirmDelete"
      @cancel="deleteTarget = null"
    />
  </div>
</template>
