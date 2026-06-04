<template>
  <BaseDialog
    :show="show"
    :title="title"
    width="full"
    :close-on-click-outside="true"
    @close="emit('close')"
  >
    <div v-if="loading" class="flex items-center justify-center py-20">
      <div class="flex flex-col items-center gap-3">
        <div class="h-8 w-8 animate-spin rounded-full border-b-2 border-primary-600"></div>
        <div class="text-sm text-gray-500 dark:text-gray-400">{{ t('common.processing') }}</div>
      </div>
    </div>

    <div v-else-if="!bodyLog" class="py-12 text-center text-sm text-gray-500 dark:text-gray-400">
      {{ t('admin.usage.emptyBodyLog') }}
    </div>

    <div v-else class="space-y-6">
      <div class="grid grid-cols-1 gap-4 sm:grid-cols-2 xl:grid-cols-4">
        <div class="rounded-xl bg-gray-50 p-4 dark:bg-dark-900">
          <div class="text-xs font-bold uppercase tracking-wider text-gray-400">{{ t('admin.usage.requestId') }}</div>
          <div class="mt-1 break-all font-mono text-sm font-medium text-gray-900 dark:text-white">{{ bodyLog.request_id }}</div>
        </div>
        <div class="rounded-xl bg-gray-50 p-4 dark:bg-dark-900">
          <div class="text-xs font-bold uppercase tracking-wider text-gray-400">{{ t('admin.usage.statusCode') }}</div>
          <div class="mt-1 text-sm font-medium text-gray-900 dark:text-white">{{ bodyLog.status_code }}</div>
        </div>
        <div class="rounded-xl bg-gray-50 p-4 dark:bg-dark-900">
          <div class="text-xs font-bold uppercase tracking-wider text-gray-400">{{ t('admin.usage.requestMethod') }}</div>
          <div class="mt-1 font-mono text-sm font-medium text-gray-900 dark:text-white">{{ bodyLog.request_method }}</div>
        </div>
        <div class="rounded-xl bg-gray-50 p-4 dark:bg-dark-900">
          <div class="text-xs font-bold uppercase tracking-wider text-gray-400">{{ t('admin.usage.requestPath') }}</div>
          <div class="mt-1 break-all font-mono text-sm font-medium text-gray-900 dark:text-white">{{ bodyLog.request_path }}</div>
        </div>
        <div class="rounded-xl bg-gray-50 p-4 dark:bg-dark-900">
          <div class="text-xs font-bold uppercase tracking-wider text-gray-400">{{ t('usage.model') }}</div>
          <div class="mt-1 break-all text-sm font-medium text-gray-900 dark:text-white">{{ bodyLog.model }}</div>
        </div>
        <div class="rounded-xl bg-gray-50 p-4 dark:bg-dark-900">
          <div class="text-xs font-bold uppercase tracking-wider text-gray-400">{{ t('usage.type') }}</div>
          <div class="mt-1 text-sm font-medium text-gray-900 dark:text-white">{{ bodyLog.request_type }}</div>
        </div>
        <div class="rounded-xl bg-gray-50 p-4 dark:bg-dark-900">
          <div class="text-xs font-bold uppercase tracking-wider text-gray-400">{{ t('admin.usage.platform') }}</div>
          <div class="mt-1 text-sm font-medium text-gray-900 dark:text-white">{{ bodyLog.platform }}</div>
        </div>
        <div class="rounded-xl bg-gray-50 p-4 dark:bg-dark-900">
          <div class="text-xs font-bold uppercase tracking-wider text-gray-400">{{ t('admin.usage.storageKind') }}</div>
          <div class="mt-1 text-sm font-medium text-gray-900 dark:text-white">{{ bodyLog.storage_kind }}</div>
        </div>
        <div class="rounded-xl bg-gray-50 p-4 dark:bg-dark-900">
          <div class="text-xs font-bold uppercase tracking-wider text-gray-400">{{ t('admin.usage.user') }}</div>
          <div class="mt-1 text-sm font-medium text-gray-900 dark:text-white">#{{ bodyLog.user_id }}</div>
        </div>
        <div class="rounded-xl bg-gray-50 p-4 dark:bg-dark-900">
          <div class="text-xs font-bold uppercase tracking-wider text-gray-400">{{ t('admin.usage.apiKeyId') }}</div>
          <div class="mt-1 text-sm font-medium text-gray-900 dark:text-white">#{{ bodyLog.api_key_id }}</div>
        </div>
        <div class="rounded-xl bg-gray-50 p-4 dark:bg-dark-900">
          <div class="text-xs font-bold uppercase tracking-wider text-gray-400">{{ t('admin.usage.accountId') }}</div>
          <div class="mt-1 text-sm font-medium text-gray-900 dark:text-white">{{ bodyLog.account_id ? `#${bodyLog.account_id}` : '-' }}</div>
        </div>
        <div class="rounded-xl bg-gray-50 p-4 dark:bg-dark-900">
          <div class="text-xs font-bold uppercase tracking-wider text-gray-400">{{ t('admin.usage.clientRequestId') }}</div>
          <div class="mt-1 break-all font-mono text-sm font-medium text-gray-900 dark:text-white">{{ bodyLog.client_request_id || '-' }}</div>
        </div>
        <div class="rounded-xl bg-gray-50 p-4 dark:bg-dark-900">
          <div class="text-xs font-bold uppercase tracking-wider text-gray-400">{{ t('usage.inbound') }}</div>
          <div class="mt-1 break-all font-mono text-sm font-medium text-gray-900 dark:text-white">{{ bodyLog.inbound_endpoint || '-' }}</div>
        </div>
        <div class="rounded-xl bg-gray-50 p-4 dark:bg-dark-900">
          <div class="text-xs font-bold uppercase tracking-wider text-gray-400">{{ t('usage.upstream') }}</div>
          <div class="mt-1 break-all font-mono text-sm font-medium text-gray-900 dark:text-white">{{ bodyLog.upstream_endpoint || '-' }}</div>
        </div>
        <div class="rounded-xl bg-gray-50 p-4 dark:bg-dark-900">
          <div class="text-xs font-bold uppercase tracking-wider text-gray-400">{{ t('admin.usage.requestContentType') }}</div>
          <div class="mt-1 break-all font-mono text-sm font-medium text-gray-900 dark:text-white">{{ bodyLog.request_content_type }}</div>
        </div>
        <div class="rounded-xl bg-gray-50 p-4 dark:bg-dark-900">
          <div class="text-xs font-bold uppercase tracking-wider text-gray-400">{{ t('admin.usage.responseContentType') }}</div>
          <div class="mt-1 break-all font-mono text-sm font-medium text-gray-900 dark:text-white">{{ bodyLog.response_content_type }}</div>
        </div>
        <div class="rounded-xl bg-gray-50 p-4 dark:bg-dark-900">
          <div class="text-xs font-bold uppercase tracking-wider text-gray-400">{{ t('admin.usage.requestBytes') }}</div>
          <div class="mt-1 text-sm font-medium text-gray-900 dark:text-white">{{ formatBytes(bodyLog.request_body_bytes) }}</div>
        </div>
        <div class="rounded-xl bg-gray-50 p-4 dark:bg-dark-900">
          <div class="text-xs font-bold uppercase tracking-wider text-gray-400">{{ t('admin.usage.responseBytes') }}</div>
          <div class="mt-1 text-sm font-medium text-gray-900 dark:text-white">{{ formatBytes(bodyLog.response_body_bytes) }}</div>
        </div>
        <div class="rounded-xl bg-gray-50 p-4 dark:bg-dark-900">
          <div class="text-xs font-bold uppercase tracking-wider text-gray-400">{{ t('admin.usage.requestTruncated') }}</div>
          <div class="mt-1 text-sm font-medium text-gray-900 dark:text-white">{{ boolText(bodyLog.request_truncated) }}</div>
        </div>
        <div class="rounded-xl bg-gray-50 p-4 dark:bg-dark-900">
          <div class="text-xs font-bold uppercase tracking-wider text-gray-400">{{ t('admin.usage.responseTruncated') }}</div>
          <div class="mt-1 text-sm font-medium text-gray-900 dark:text-white">{{ boolText(bodyLog.response_truncated) }}</div>
        </div>
      </div>

      <div class="grid grid-cols-1 gap-6 xl:grid-cols-2">
        <section class="rounded-xl border border-gray-200 bg-white dark:border-dark-700 dark:bg-dark-900">
          <div class="flex items-center justify-between border-b border-gray-200 px-4 py-3 dark:border-dark-700">
            <h3 class="text-sm font-semibold text-gray-900 dark:text-white">{{ t('admin.usage.requestHeaders') }}</h3>
            <button type="button" class="btn btn-secondary btn-sm" @click="copyText(requestHeadersText)">
              {{ t('common.copy') }}
            </button>
          </div>
          <pre class="max-h-80 overflow-auto p-4 text-xs text-gray-800 dark:text-gray-100"><code>{{ requestHeadersText || '-' }}</code></pre>
        </section>

        <section class="rounded-xl border border-gray-200 bg-white dark:border-dark-700 dark:bg-dark-900">
          <div class="flex items-center justify-between border-b border-gray-200 px-4 py-3 dark:border-dark-700">
            <h3 class="text-sm font-semibold text-gray-900 dark:text-white">{{ t('admin.usage.responseHeaders') }}</h3>
            <button type="button" class="btn btn-secondary btn-sm" @click="copyText(responseHeadersText)">
              {{ t('common.copy') }}
            </button>
          </div>
          <pre class="max-h-80 overflow-auto p-4 text-xs text-gray-800 dark:text-gray-100"><code>{{ responseHeadersText || '-' }}</code></pre>
        </section>
      </div>

      <div class="grid grid-cols-1 gap-6 xl:grid-cols-2">
        <section class="rounded-xl border border-gray-200 bg-white dark:border-dark-700 dark:bg-dark-900">
          <div class="flex items-center justify-between border-b border-gray-200 px-4 py-3 dark:border-dark-700">
            <h3 class="text-sm font-semibold text-gray-900 dark:text-white">{{ t('admin.usage.requestBody') }}</h3>
            <button type="button" class="btn btn-secondary btn-sm" @click="copyText(requestBodyText)">
              {{ t('common.copy') }}
            </button>
          </div>
          <pre class="max-h-[28rem] overflow-auto p-4 text-xs text-gray-800 dark:text-gray-100"><code>{{ requestBodyText || '-' }}</code></pre>
        </section>

        <section class="rounded-xl border border-gray-200 bg-white dark:border-dark-700 dark:bg-dark-900">
          <div class="flex items-center justify-between border-b border-gray-200 px-4 py-3 dark:border-dark-700">
            <h3 class="text-sm font-semibold text-gray-900 dark:text-white">{{ t('admin.usage.responseBody') }}</h3>
            <button type="button" class="btn btn-secondary btn-sm" @click="copyText(responseBodyText)">
              {{ t('common.copy') }}
            </button>
          </div>
          <pre class="max-h-[28rem] overflow-auto p-4 text-xs text-gray-800 dark:text-gray-100"><code>{{ responseBodyText || '-' }}</code></pre>
        </section>
      </div>
    </div>
  </BaseDialog>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import BaseDialog from '@/components/common/BaseDialog.vue'
import { useClipboard } from '@/composables/useClipboard'
import type { AdminUsageBodyLogDetail } from '@/api/admin/usage'

interface Props {
  show: boolean
  loading?: boolean
  bodyLog: AdminUsageBodyLogDetail | null
}

const props = withDefaults(defineProps<Props>(), {
  loading: false,
})

const emit = defineEmits<{
  (e: 'close'): void
}>()

const { t } = useI18n()
const { copyToClipboard } = useClipboard()

const title = computed(() => {
  const requestId = props.bodyLog?.request_id
  return requestId ? `${t('admin.usage.viewBodyLog')} · ${requestId}` : t('admin.usage.viewBodyLog')
})

const requestHeadersText = computed(() => formatStructured(props.bodyLog?.request_headers))
const responseHeadersText = computed(() => formatStructured(props.bodyLog?.response_headers))
const requestBodyText = computed(() => formatBody(props.bodyLog?.request_body))
const responseBodyText = computed(() => formatBody(props.bodyLog?.response_body))

function formatBody(value: string | null | undefined): string {
  const text = value?.trim() ?? ''
  if (!text) return ''
  try {
    return JSON.stringify(JSON.parse(text), null, 2)
  } catch {
    return value ?? ''
  }
}

function formatStructured(value: Record<string, string[]> | null | undefined): string {
  if (!value) return ''
  try {
    return JSON.stringify(value, null, 2)
  } catch {
    return String(value)
  }
}

function formatBytes(value: number): string {
  return `${value.toLocaleString()} B`
}

function boolText(value: boolean): string {
  return value ? t('common.yes') : t('common.no')
}

function copyText(value: string) {
  if (!value) return
  void copyToClipboard(value)
}
</script>
