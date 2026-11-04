<template>
  <article :id="domId" class="border-l-2 py-3 pl-4" :class="tone">
    <div class="mb-2 flex flex-wrap items-center gap-2 text-xs">
      <span class="font-semibold text-gray-900 dark:text-gray-100">{{ roleLabel }}</span>
      <span class="rounded bg-gray-100 px-1.5 py-0.5 dark:bg-dark-800">{{ kindLabel }}</span>
      <span v-if="item.history" class="text-gray-500">{{ t('bodyLogs.history') }}</span>
      <span v-if="item.name" class="font-mono text-gray-700 dark:text-gray-200">{{ item.name }}</span>
      <span v-if="item.call_id" class="font-mono text-gray-400">{{ item.call_id }}</span>
      <span v-if="item.is_error" class="text-red-600">{{ t('bodyLogs.errorState') }}</span>
      <button class="ml-auto text-gray-500 hover:text-primary-600" type="button" @click="copy">{{ t('bodyLogs.copy') }}</button>
    </div>
    <details v-if="item.text && isAttachment" class="text-xs text-gray-500">
      <summary class="cursor-pointer">{{ t('bodyLogs.attachment') }}</summary>
      <pre class="mt-2 max-h-80 overflow-auto whitespace-pre-wrap break-words rounded-lg bg-gray-50 p-3 font-mono leading-relaxed dark:bg-dark-950">{{ pretty(item.text) }}</pre>
    </details>
    <div v-else-if="item.text" class="space-y-2">
      <!-- Log content is untrusted. Markdown is sanitized and media is opt-in. -->
      <div v-if="isMarkdown" class="bodylog-markdown text-sm" v-html="rendered"></div>
      <pre v-else class="max-h-[28rem] overflow-auto whitespace-pre-wrap break-words rounded-lg bg-gray-50 p-3 font-mono text-xs leading-relaxed dark:bg-dark-950">{{ pretty(item.text) }}</pre>
    </div>
    <details v-if="item.arguments" open class="mt-2">
      <summary class="cursor-pointer text-xs font-medium text-gray-500">{{ t('bodyLogs.arguments') }}</summary>
      <pre class="mt-2 max-h-80 overflow-auto whitespace-pre-wrap break-words rounded-lg bg-gray-50 p-3 font-mono text-xs leading-relaxed dark:bg-dark-950">{{ pretty(item.arguments) }}</pre>
    </details>
    <div v-if="item.kind === 'tool_call'" class="mt-2 text-xs">
      <button v-if="result" class="text-primary-600 hover:underline" type="button" @click="emit('result', result)">{{ t('bodyLogs.readResult') }}</button>
      <span v-else class="text-gray-400">{{ item.call_id ? t('bodyLogs.noResult') : t('bodyLogs.unknownLink') }}</span>
    </div>
    <p v-if="item.has_more" class="mt-2 text-xs text-amber-600">{{ t('bodyLogs.preview') }}</p>
    <details class="mt-2 text-xs text-gray-400">
      <summary class="cursor-pointer">{{ t('bodyLogs.source') }}</summary>
      <code class="break-all">{{ item.source }}</code>
      <p v-if="item.original_role && item.original_role !== item.role">{{ t('bodyLogs.originalRole') }}: {{ item.original_role }}</p>
    </details>
  </article>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import { marked } from 'marked'
import DOMPurify from 'dompurify'
import { useClipboard } from '@/composables/useClipboard'
import type { BodyLogItem } from '@/api/admin/bodyLogs'

const props = defineProps<{ item: BodyLogItem; result?: BodyLogItem; idPrefix: string }>()
const emit = defineEmits<{ result: [item: BodyLogItem] }>()
const { t } = useI18n()
const { copyToClipboard } = useClipboard()
const domId = computed(() => `bodylog-item-${props.idPrefix}-${encodeURIComponent(props.item.id)}`)
const roleLabel = computed(() => ['user', 'assistant', 'tool', 'system', 'developer'].includes(props.item.role) ? t(`bodyLogs.${props.item.role}`) : (props.item.role || t('bodyLogs.data')))
const kindLabel = computed(() => {
  const names: Record<string, string> = { tool_call: 'toolCall', tool_result: 'toolResult', instruction: 'instruction', attachment: 'attachment', reasoning: 'reasoning', message: 'message', data: 'data' }
  return names[props.item.kind] ? t(`bodyLogs.${names[props.item.kind]}`) : props.item.kind
})
const tone = computed(() => props.item.kind.startsWith('tool_') ? 'border-amber-300 dark:border-amber-800' : props.item.role === 'assistant' ? 'border-primary-400' : 'border-gray-200 dark:border-dark-700')
const isMarkdown = computed(() => props.item.kind === 'message' || props.item.kind === 'instruction' || props.item.kind === 'reasoning')
const isAttachment = computed(() => ['attachment', 'image', 'image_url', 'input_image', 'input_audio', 'audio', 'file', 'input_file', 'document', 'video'].includes(props.item.kind))
const rendered = computed(() => DOMPurify.sanitize(marked.parse(props.item.text || '', { async: false, gfm: true }) as string, {
  FORBID_TAGS: ['img', 'video', 'audio', 'iframe', 'style', 'script'],
  FORBID_ATTR: ['style']
}))
function pretty(value: string) {
  try { return JSON.stringify(JSON.parse(value), null, 2) } catch { return value }
}
function copy() { void copyToClipboard(props.item.arguments || props.item.text || '') }
</script>

<style scoped>
.bodylog-markdown { overflow-wrap: anywhere; line-height: 1.75; }
.bodylog-markdown :deep(p + p), .bodylog-markdown :deep(pre), .bodylog-markdown :deep(ul), .bodylog-markdown :deep(ol) { margin-top: .65rem; }
.bodylog-markdown :deep(pre) { overflow: auto; max-height: 28rem; border-radius: .5rem; padding: .85rem; background: rgb(148 163 184 / .09); font-size: .8rem; }
.bodylog-markdown :deep(code) { font-family: ui-monospace, monospace; font-size: .85em; }
.bodylog-markdown :deep(ul) { list-style: disc; padding-left: 1.4rem; }
.bodylog-markdown :deep(ol) { list-style: decimal; padding-left: 1.4rem; }
.bodylog-markdown :deep(a) { color: #2563eb; text-decoration: underline; }
.bodylog-markdown :deep(h1), .bodylog-markdown :deep(h2), .bodylog-markdown :deep(h3) { font-weight: 600; margin-top: 1rem; }
</style>
