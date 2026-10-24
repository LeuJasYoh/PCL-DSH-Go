<script setup lang="ts">
import { computed, ref } from 'vue'

import type { AccountInput, AccountSummary, ProviderPreset } from '@/api/types'

import BaseModal from './BaseModal.vue'

const props = withDefaults(
  defineProps<{
    mode: 'create' | 'edit'
    account?: AccountSummary | null
    providers: ProviderPreset[]
    submitting?: boolean
    submitError?: string
  }>(),
  { account: null, submitting: false, submitError: '' },
)

const emit = defineEmits<{ submit: [input: AccountInput]; close: [] }>()

const create = computed(() => props.mode === 'create')
const hasProviders = computed(() => props.providers.length > 0)

function defaultProviderId(): string {
  return props.providers.find((preset) => preset.id === 'deepseek-official')?.id ?? props.providers[0]?.id ?? ''
}

const name = ref(props.account?.name ?? '')
const provider = ref(props.account?.provider ?? defaultProviderId())
const baseUrl = ref(props.account?.baseUrl ?? '')
const model = ref(props.account?.model ?? '')
// The API key never leaves this dialog: on close the component unmounts and clears it.
const apiKey = ref('')
const validationError = ref('')

const keyPlaceholder = computed(() => {
  if (!create.value && props.account?.hasKey) return '留空保留已保存的密钥'
  return '输入此提供商的 API key'
})

// Pre-fill baseUrl/model for the initial preset in create mode (desktop default).
if (create.value && hasProviders.value) {
  const preset = props.providers.find((entry) => entry.id === provider.value)
  if (preset) {
    baseUrl.value = preset.baseUrl
    model.value = preset.model
  }
}

function onProviderChange(event: Event): void {
  const value = (event.target as HTMLSelectElement).value
  provider.value = value
  const preset = props.providers.find((entry) => entry.id === value)
  if (preset) {
    // Same behaviour as the desktop dialog: choosing a preset replaces address/model.
    baseUrl.value = preset.baseUrl
    model.value = preset.model
  }
}

function submit(): void {
  validationError.value = ''
  const value = {
    name: name.value.trim(),
    provider: provider.value.trim(),
    baseUrl: baseUrl.value.trim(),
    model: model.value.trim(),
    apiKey: apiKey.value,
  }
  if (!value.name) validationError.value = '请输入账户名称。'
  else if (!value.provider) validationError.value = '请输入提供商。'
  else if (!value.baseUrl) validationError.value = '请输入 API 地址。'
  else if (!value.model) validationError.value = '请输入可使用的模型。'
  else if ((create.value || !props.account?.hasKey) && !apiKey.value.trim()) {
    validationError.value = '请输入 API key。'
  }
  if (validationError.value) return
  // A blank apiKey on edit tells the backend to keep the stored secret.
  emit('submit', { ...value, apiKey: value.apiKey })
}
</script>

<template>
  <BaseModal :title="create ? '添加账户' : '编辑账户'" testid="account-dialog" wide :closable="!submitting" @close="emit('close')">
    <div class="form-field">
      <label class="field-label" for="account-name">账户名称</label>
      <input
        id="account-name"
        v-model="name"
        class="input"
        data-testid="account-name"
        placeholder="例如 DeepSeek 官方"
        :disabled="submitting"
        autofocus
      />
    </div>

    <div class="form-field">
      <label class="field-label" for="account-provider">提供商（可输入官方支持的标识）</label>
      <select
        v-if="hasProviders"
        id="account-provider"
        class="input"
        :value="provider"
        data-testid="account-provider"
        :disabled="submitting"
        @change="onProviderChange"
      >
        <option v-for="preset in providers" :key="preset.id" :value="preset.id">
          {{ preset.name }}（{{ preset.id }}）
        </option>
      </select>
      <input
        v-else
        id="account-provider"
        v-model="provider"
        class="input"
        data-testid="account-provider"
        placeholder="输入提供商标识，例如 deepseek-official"
        :disabled="submitting"
      />
      <p v-if="!hasProviders" class="form-hint">
        无法读取官方提供商列表（接口未就绪）；可手动输入提供商标识、地址和模型。
      </p>
    </div>

    <div class="form-field">
      <label class="field-label" for="account-base-url">API 地址</label>
      <input
        id="account-base-url"
        v-model="baseUrl"
        class="input"
        data-testid="account-base-url"
        placeholder="https://api.deepseek.com"
        :disabled="submitting"
      />
    </div>

    <div class="form-field">
      <label class="field-label" for="account-model">模型</label>
      <input
        id="account-model"
        v-model="model"
        class="input"
        data-testid="account-model"
        placeholder="deepseek-flash"
        :disabled="submitting"
      />
    </div>

    <div class="form-field">
      <label class="field-label" for="account-key">API key</label>
      <input
        id="account-key"
        v-model="apiKey"
        class="input"
        type="password"
        autocomplete="new-password"
        data-testid="account-key"
        :placeholder="keyPlaceholder"
        :disabled="submitting"
      />
      <p class="form-hint">
        保存后显示为「API 连接未验证」。请填写自己可使用的模型；Codex / Copilot 的订阅登录不能作为 API key。
      </p>
    </div>

    <p v-if="validationError || submitError" class="validation-error" data-testid="account-validation" role="alert">
      {{ validationError || submitError }}
    </p>

    <template #footer>
      <button class="btn btn-secondary" type="button" :disabled="submitting" @click="emit('close')">取消</button>
      <button class="btn btn-primary" type="button" data-testid="account-save" :disabled="submitting" @click="submit">
        {{ submitting ? '正在保存…' : '保存账户' }}
      </button>
    </template>
  </BaseModal>
</template>
