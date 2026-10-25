<script setup lang="ts">
import { nextTick, onMounted, ref } from 'vue'

import { instancesApi } from '@/api'
import type { DirectAccess, InstanceSummary } from '@/api/types'
import AppIcon from '@/components/AppIcon.vue'
import AppNotice from '@/components/AppNotice.vue'
import BaseModal from '@/components/BaseModal.vue'
import { useNotifyStore } from '@/stores/notify'
import { copyText } from '@/utils/clipboard'
import { describeApiError } from '@/utils/errors'

/**
 * 远程完成 DeepSeek 账号网页登录的帮助对话框。
 *
 * 上游 DSH 的账号 OAuth 回调只接受回环地址（127.0.0.1/localhost），浏览器在远程访问时
 * 无法直接完成登录；这里给出「SSH 隧道 + 回环直连地址」两步操作，登录一次后凭据保存在
 * 服务器实例目录，后续仍通过 /workbench/ 正常访问。
 *
 * Contract: docs/M4_INTERFACES.md 4 (GET /api/instances/{id}/direct-access) and
 * docs/DSH_INTEGRATION.md 6.4. The one-time token only appears in directUrl, which
 * is never logged by this dialog.
 */
const props = defineProps<{ instance: InstanceSummary }>()
const emit = defineEmits<{ close: [] }>()

const notify = useNotifyStore()
const loading = ref(true)
const error = ref('')
const data = ref<DirectAccess | null>(null)
const manualCopy = ref<{ label: string; text: string } | null>(null)
const manualCopyInput = ref<HTMLTextAreaElement | null>(null)

onMounted(() => {
  void load()
})

async function load(): Promise<void> {
  loading.value = true
  error.value = ''
  try {
    data.value = await instancesApi.directAccess(props.instance.id)
  } catch (caught) {
    error.value = describeApiError(caught, '读取直连信息')
  } finally {
    loading.value = false
  }
}

async function copy(value: string, label: string): Promise<void> {
  if (!value) return
  const outcome = await copyText(value)
  if (outcome === 'copied') {
    manualCopy.value = null
    notify.success('已复制到剪贴板。')
    return
  }
  manualCopy.value = { label, text: value }
  await nextTick()
  const input = manualCopyInput.value
  input?.focus()
  input?.select()
  input?.scrollIntoView({ block: 'nearest' })
}
</script>

<template>
  <BaseModal
    title="直连登录（远程完成 DeepSeek 账号登录）"
    testid="direct-access-dialog"
    wide
    @close="emit('close')"
  >
    <p class="paragraph">
      DeepSeek 账号的网页登录由 DSH 使用 OAuth 完成，回调地址只接受本机回环地址
      （<code>127.0.0.1</code>）；远程浏览器直接访问时无法完成登录。按下面两步操作即可完成一次登录，
      登录凭据会保存在服务器实例目录，之后继续用「打开 Web」正常工作。
    </p>

    <div v-if="loading" class="loading-row">
      <span class="spinner"></span>正在读取直连信息…
    </div>

    <AppNotice v-else-if="error" title="读取失败" tone="warn">
      <p class="paragraph">{{ error }}</p>
      <div class="toolbar">
        <button class="btn btn-secondary btn--small" type="button" @click="load">重试</button>
      </div>
    </AppNotice>

    <template v-else-if="data">
      <div class="direct-access-meta">
        <span class="badge" :class="data.ready ? 'badge--green' : 'badge--gray'">
          {{ data.ready ? '可以直连' : '暂不可直连' }}
        </span>
        <span class="small-muted">实例端口 {{ data.port }}</span>
        <span class="small-muted" data-testid="direct-access-ssh">
          SSH {{ data.sshUser }}@{{ data.sshHost }}
        </span>
      </div>

      <div class="form-field">
        <label class="field-label" for="direct-access-tunnel">
          步骤 1 · 在本机终端执行 SSH 隧道命令
        </label>
        <div class="direct-access-row">
          <code id="direct-access-tunnel" class="direct-access-value" data-testid="direct-access-tunnel">{{ data.tunnelCommand || '（实例就绪后显示）' }}</code>
          <button
            class="btn btn-secondary"
            type="button"
            data-testid="direct-access-copy-tunnel"
            :disabled="!data.tunnelCommand"
            @click="copy(data.tunnelCommand, 'SSH 隧道命令')"
          >
            <AppIcon name="copy" :size="15" />复制
          </button>
        </div>
      </div>

      <div class="form-field">
        <label class="field-label" for="direct-access-url">
          步骤 2 · 隧道建立后，在浏览器打开直连地址并完成登录
        </label>
        <div class="direct-access-row">
          <code
            id="direct-access-url"
            class="direct-access-value"
            :class="{ 'is-disabled': !data.directUrl }"
            data-testid="direct-access-url"
          >{{ data.directUrl || '（实例就绪后显示）' }}</code>
          <button
            class="btn btn-secondary"
            type="button"
            :disabled="!data.directUrl"
            data-testid="direct-access-copy-url"
            @click="copy(data.directUrl || '', '直连地址')"
          >
            <AppIcon name="copy" :size="15" />复制
          </button>
        </div>
      </div>

      <AppNotice v-if="data.note" :title="data.ready ? '使用说明' : '暂不可用'" :tone="data.ready ? 'info' : 'warn'">
        <p class="paragraph" data-testid="direct-access-note">{{ data.note }}</p>
      </AppNotice>

      <p class="small-muted">
        提示：直连地址包含一次性访问 token，仅在当前这次实例运行期间有效；SSH 隧道只是把本机
        <code>127.0.0.1:{{ data.port }}</code> 端口映射到服务器的实例端口，不会改变服务器监听。
        如果实例正在重启，请等待状态变为「运行中」后重试。
      </p>

      <div v-if="manualCopy" class="form-field">
        <label class="field-label" for="direct-access-manual-copy">手动复制 · {{ manualCopy.label }}</label>
        <textarea
          id="direct-access-manual-copy"
          ref="manualCopyInput"
          class="input direct-access-input"
          rows="2"
          readonly
          :value="manualCopy.text"
          data-testid="direct-access-manual-copy"
          @focus="manualCopyInput?.select()"
        ></textarea>
        <p class="small-muted">文本已选中，请按 Ctrl+C；触屏可长按文本复制。</p>
      </div>
    </template>

    <template #footer>
      <button class="btn btn-secondary" type="button" data-testid="direct-access-close" @click="emit('close')">
        关闭
      </button>
    </template>
  </BaseModal>
</template>

<style scoped>
.direct-access-meta {
  display: flex;
  flex-wrap: wrap;
  gap: 8px;
  align-items: center;
  margin-bottom: 12px;
}

.direct-access-row {
  display: flex;
  gap: 8px;
  align-items: center;
}

.direct-access-value {
  flex: 1 1 auto;
  padding: 8px 10px;
  border: 1px solid var(--border, #d8d8d8);
  border-radius: 6px;
  background: var(--surface-2, #f6f6f6);
  font-family: ui-monospace, SFMono-Regular, Menlo, Consolas, monospace;
  font-size: 12px;
  line-height: 1.5;
  word-break: break-all;
  user-select: all;
}

.direct-access-value.is-disabled {
  color: var(--text-muted, #999);
}

.direct-access-input {
  flex: 1 1 auto;
  font-family: ui-monospace, SFMono-Regular, Menlo, Consolas, monospace;
  font-size: 12px;
}

.paragraph code {
  font-family: ui-monospace, SFMono-Regular, Menlo, Consolas, monospace;
}
</style>
