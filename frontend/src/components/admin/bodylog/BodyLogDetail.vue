<template>
  <div class="space-y-5">
    <div class="flex flex-wrap items-start gap-4">
      <div class="min-w-0 flex-1">
        <div class="flex items-center gap-2">
          <span class="rounded px-2 py-1 font-mono text-xs font-semibold" :class="record.status >= 400 ? 'bg-red-50 text-red-600 dark:bg-red-950/30' : 'bg-emerald-50 text-emerald-700 dark:bg-emerald-950/30'">{{ record.status || '—' }}</span>
          <span class="font-mono text-sm text-gray-800 dark:text-gray-100">{{ record.method }}</span>
          <span class="truncate font-mono text-sm text-gray-500">{{ record.path }}</span>
        </div>
        <p class="mt-2 break-all font-mono text-xs text-gray-400">{{ record.id }} · {{ date(record.created_at) }} · {{ duration }}</p>
      </div>
      <span class="rounded-full bg-gray-100 px-3 py-1 text-xs dark:bg-dark-800">{{ record.error === "context canceled" ? t("bodyLogs.connectionClosed") : state(record.state) }}</span>
    </div>
    <div v-if="record.error" class="rounded-lg bg-amber-50 p-3 text-sm text-amber-700 dark:bg-amber-950/20">{{ record.error === "context canceled" ? t("bodyLogs.connectionClosedHint") : record.error }}</div>
    <p v-if="record.metadata?.ws_first_message_error" class="text-sm text-amber-700">{{ t("bodyLogs.wsFirstMessage") }}: {{ record.metadata.ws_first_message_error }}</p>
    <div class="grid gap-2 rounded-xl border border-gray-100 bg-gray-50/70 p-3 text-xs dark:border-dark-800 dark:bg-dark-900/40 sm:grid-cols-3">
      <div><span class="text-gray-400">{{ t('bodyLogs.connection') }}</span><p class="mt-1 font-medium">{{ connectionLabel }}</p></div>
      <div><span class="text-gray-400">{{ t('bodyLogs.interaction') }}</span><p class="mt-1 font-medium">{{ interactionLabel }}</p></div>
      <div><span class="text-gray-400">{{ t('bodyLogs.savedBody') }}</span><p class="mt-1 font-medium">{{ savedBytes }}</p></div>
    </div>
    <div class="flex flex-wrap items-center gap-2 border-b border-gray-200 pb-3 dark:border-dark-700">
      <button v-for="tab in tabs" :key="tab.key" type="button" class="rounded-lg px-3 py-2 text-sm transition-colors" :class="activeTab === tab.key ? 'bg-primary-50 font-medium text-primary-700 dark:bg-primary-950/30 dark:text-primary-300' : 'text-gray-500 hover:bg-gray-50 dark:hover:bg-dark-800'" @click="activeTab = tab.key">{{ tab.label }}</button>
    </div>
    <div v-if="activeTab === 'conversation' || activeTab === 'tools'" class="grid gap-7 xl:grid-cols-2">
      <section v-for="direction in ['request', 'response']" :key="direction" class="min-w-0">
        <h3 class="mb-4 flex items-center gap-2 text-sm font-semibold">
          <span class="h-1.5 w-1.5 rounded-full" :class="direction === 'request' ? 'bg-sky-500' : 'bg-primary-500'"></span>
          {{ t(direction === 'request' ? 'bodyLogs.input' : 'bodyLogs.output') }}
        </h3>
        <template v-for="body in record.bodies.filter(b => b.direction.endsWith(direction))" :key="body.id">
          <BodyLogContent :record-id="record.id" :body="body" :tools-only="activeTab === 'tools'" @original="openOriginal" />
        </template>
        <div v-if="!record.bodies.some(b => b.direction.endsWith(direction))" class="rounded-lg bg-gray-50 px-4 py-10 text-sm text-gray-400 dark:bg-dark-950">{{ t('bodyLogs.noBody') }}</div>
      </section>
    </div>
    <div v-else-if="activeTab === 'metadata'" class="space-y-5">
      <section>
        <h3 class="mb-3 text-sm font-semibold">{{ t('bodyLogs.details') }}</h3>
        <dl class="divide-y divide-gray-100 text-xs dark:divide-dark-800">
          <div v-for="(value, key) in recordMetadata" :key="key" class="grid grid-cols-[minmax(8rem,1fr)_3fr] gap-4 py-2.5">
            <dt class="break-all font-mono text-gray-400">{{ key }}</dt><dd class="whitespace-pre-wrap break-words font-mono">{{ format(value) }}</dd>
          </div>
        </dl>
      </section>
      <details v-for="body in record.bodies" :key="body.id" class="rounded-lg border border-gray-200 p-4 dark:border-dark-700">
        <summary class="cursor-pointer text-xs font-medium">{{ body.direction }} · {{ body.content_type || t('bodyLogs.unknown') }} · {{ state(body.state) }} · {{ bytes(body.saved_bytes) }}</summary>
        <pre class="mt-3 max-h-96 overflow-auto whitespace-pre-wrap break-words text-xs">{{ format(body.headers || {}) }}</pre>
      </details>
    </div>
    <div v-else class="space-y-4">
      <div class="flex flex-wrap items-center gap-3">
        <select v-model="selectedBody" class="input w-auto max-w-full text-sm" :aria-label="t('bodyLogs.original')">
          <option v-for="body in record.bodies" :key="body.id" :value="body.id">{{ body.direction }} · {{ bytes(body.saved_bytes) }} · {{ state(body.state) }}</option>
        </select>
        <button v-if="selectedBody" type="button" class="btn btn-secondary ml-auto" :disabled="downloading" @click="download">{{ t('bodyLogs.download') }}</button>
      </div>
      <p v-if="selected && (!selected.content_type.startsWith('text/') && !selected.content_type.includes('json'))" class="text-xs text-gray-500">{{ t('bodyLogs.binary') }}</p>
      <p class="text-xs text-gray-400">{{ t('bodyLogs.rawOffset') }}: {{ rawOffset.toLocaleString() }}</p>
      <div v-if="rawError" role="alert" class="text-sm text-red-600">{{ t('bodyLogs.error') }}</div>
      <pre v-else class="max-h-[60vh] overflow-auto whitespace-pre-wrap break-words rounded-xl bg-gray-50 p-5 font-mono text-xs leading-relaxed dark:bg-dark-950">{{ rawContent || t('bodyLogs.noBody') }}</pre>
      <div class="flex gap-2">
        <button v-if="rawHistory.length" class="btn btn-secondary" :disabled="rawLoading" type="button" @click="previousRaw">{{ t('bodyLogs.previous') }}</button>
        <button v-if="rawHasMore" class="btn btn-secondary" :disabled="rawLoading" type="button" @click="loadRaw(true)">{{ rawLoading ? t('bodyLogs.loading') : t('bodyLogs.more') }}</button>
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { buildApiUrl } from '@/api/client'
import { bodyLogsAPI, type BodyLogRecord } from '@/api/admin/bodyLogs'
import { useAppStore } from '@/stores/app'
import BodyLogContent from './BodyLogContent.vue'

const props = defineProps<{ record: BodyLogRecord }>()
const { t, te } = useI18n()
const app = useAppStore()
const activeTab = ref('conversation')
const selectedBody = ref('')
const rawContent = ref('')
const rawOffset = ref(0)
const nextOffset = ref(0)
const rawHistory = ref<number[]>([])
const rawHasMore = ref(false)
const rawLoading = ref(false)
const rawError = ref(false)
const downloading = ref(false)
let rawGeneration = 0
const tabs = computed(() => [
  { key: 'conversation', label: t('bodyLogs.inputOutput') }, { key: 'tools', label: t('bodyLogs.tools') },
  { key: 'metadata', label: t('bodyLogs.metadata') }, { key: 'original', label: t('bodyLogs.original') }
])
const selected = computed(() => props.record.bodies.find(b => b.id === selectedBody.value))
const recordMetadata = computed(() => {
  const { bodies: _bodies, ...rest } = props.record
  return rest
})
const connectionLabel = computed(() => {
  const key = props.record.connection_state === 'first_message_timeout' ? 'firstMessageTimeout' : props.record.connection_state === 'closed' ? 'connectionClosed' : props.record.connection_state || 'unknown'
  return te(`bodyLogs.${key}`) ? t(`bodyLogs.${key}`) : key
})
const interactionLabel = computed(() => {
  const key = props.record.interaction_state === 'not_started' ? 'notStarted' : props.record.interaction_state === 'completed' ? 'interactionCompleted' : props.record.interaction_state || 'unknown'
  return te(`bodyLogs.${key}`) ? t(`bodyLogs.${key}`) : key
})
const savedBytes = computed(() => bytes(props.record.bodies.reduce((sum, body) => sum + body.saved_bytes, 0)))
const duration = computed(() => props.record.ended_at ? `${Math.max(0, Date.parse(props.record.ended_at) - Date.parse(props.record.created_at)).toLocaleString()} ms` : t('bodyLogs.open'))
function format(value: unknown) { return value == null ? t('bodyLogs.unknown') : typeof value === 'string' ? value : JSON.stringify(value, null, 2) }
function date(value: string) { return new Date(value).toLocaleString() }
function bytes(n: number) { return n >= 1048576 ? `${(n / 1048576).toFixed(1)} MiB` : `${(n / 1024).toFixed(1)} KiB` }
function state(value: string) {
  const map: Record<string, string> = { not_read: 'notRead', empty: 'emptyBody', error: 'errorState' }
  const key = `bodyLogs.${map[value] || value}`
  return te(key) ? t(key) : value
}
function openOriginal(id: string) { selectedBody.value = id; activeTab.value = 'original' }
async function loadRaw(next: boolean, previous?: number) {
  if (!selectedBody.value) return
  const current = ++rawGeneration
  rawLoading.value = true; rawError.value = false
  const offset = previous ?? (next ? nextOffset.value : 0)
  try {
    const page = await bodyLogsAPI.raw(props.record.id, selectedBody.value, offset)
    if (current !== rawGeneration) return
    if (previous !== undefined) { if (previous !== rawOffset.value) rawHistory.value.pop() }
    else if (next) rawHistory.value.push(rawOffset.value)
    else rawHistory.value = []
    // A page replaces the last page rather than growing a browser-sized copy.
    rawContent.value = page.content; rawOffset.value = offset
    nextOffset.value = page.next_offset; rawHasMore.value = page.has_more
  } catch { if (current === rawGeneration) rawError.value = true }
  finally { if (current === rawGeneration) rawLoading.value = false }
}
function previousRaw() { void loadRaw(false, rawHistory.value.at(-1)) }
async function download() {
  if (!selectedBody.value) return
  downloading.value = true
  try {
    const path = await bodyLogsAPI.download(props.record.id, selectedBody.value)
    const link = document.createElement('a')
    link.href = buildApiUrl(path); link.rel = 'noreferrer'; link.download = `${props.record.id}.bin`
    link.click()
  } catch (error) {
    if (!(error instanceof DOMException && error.name === 'AbortError')) app.showError(t('bodyLogs.error'))
  } finally { downloading.value = false }
}
watch(() => props.record.id, () => { selectedBody.value = props.record.bodies[0]?.id || ''; activeTab.value = 'conversation' }, { immediate: true })
watch(() => props.record, (record, previous) => {
 if (record.id === previous.id && activeTab.value === "original") void loadRaw(false, rawOffset.value)
})
watch([selectedBody, activeTab], () => { if (activeTab.value === 'original') void loadRaw(false) })
</script>
