<script setup lang="ts">
import { onMounted, ref } from 'vue'

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
 */
const props = defineProps<{ instance: InstanceSummary }>()
const emit = defineEmits<{ close: [] }>()

const notify = useNotifyStore()
const loading = ref(true)
const error = ref('')
const data = ref<DirectAccess | null>(null)
const manualCopy = ref<{ label: string; text: string } | null>(null)

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
  manualCopy.value = null
  const outcome = await copyText(value)
  if (outcome === 'copied') {
    notify.success('已复制到剪贴板。')
    return
  }
  manualCopy.value = { label, text: value }
  notify.info('当前页面无法直接写入剪贴板，请手动复制下方文本。')
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
      <div class="form-field">
        <label class="field-label" for="direct-access-tunnel">
          步骤 1 · 在本机终端执行 SSH 隧道命令
        </label>
        <div class="direct-access-row">
          <input
            id="direct-access-tunnel"
            class="input direct-access-input"
            readonly
            :value="data.tunnelCommand"
            data-testid="direct-access-tunnel"
          />
          <button
            class="btn btn-secondary"
            type="button"
            data-testid="direct-access-copy-tunnel"
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
          <input
            id="direct-access-url"
            class="input direct-access-input"
            readonly
            :disabled="!data.directUrl"
            :value="data.directUrl || '（实例就绪后显示）'"
            data-testid="direct-access-url"
          />
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
        <p class="paragraph">{{ data.note }}</p>
      </AppNotice>

      <p class="small-muted">
        提示：直连地址包含一次性访问 token，仅在当前这次实例运行期间有效；SSH 隧道只是把本机
        <code>127.0.0.1:{{ data.port }}</code> 端口映射到服务器的实例端口，不会改变服务器监听。
        如果只是需要用模型跑任务，也可以直接使用「添加 API Key」方式，无需网页登录。
      </p>

      <textarea
        v-if="manualCopy"
        class="input direct-access-input"
        rows="2"
        readonly
        :value="manualCopy.text"
        :aria-label="manualCopy.label"
        data-testid="direct-access-manual-copy"
      ></textarea>
    </template>

    <template #footer>
      <button class="btn btn-secondary" type="button" data-testid="direct-access-close" @click="emit('close')">
        关闭
      </button>
    </template>
  </BaseModal>
</template>

<style scoped>
.direct-access-row {
  display: flex;
  gap: 8px;
  align-items: center;
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
