<template>
  <section>
    <div v-if="loading && !items.length" class="py-12 text-center text-sm text-gray-400">{{ t('bodyLogs.loading') }}</div>
    <div v-else-if="error" role="alert" class="rounded-lg bg-red-50 p-4 text-sm text-red-700 dark:bg-red-950/20">
      {{ t('bodyLogs.error') }}
      <button class="ml-3 underline" type="button" @click="load(false)">{{ t('bodyLogs.refresh') }}</button>
    </div>
    <template v-else>
      <div class="mb-4 flex flex-wrap items-center gap-2 text-xs text-gray-500">
        <span class="font-medium">{{ t('bodyLogs.protocol') }}: {{ protocol || t('bodyLogs.unknown') }}</span>
        <span v-if="protocolState !== 'not_applicable' && protocolState" class="text-gray-500">{{ t('bodyLogs.' + protocolState) }}</span>
        <span>·</span>
        <span :class="state !== 'ready' ? 'text-amber-600' : ''">{{ stateLabel }}</span>
        <span v-if="body.state !== 'complete' && body.state !== 'empty'" class="text-amber-600">{{ body.state === 'partial' ? t('bodyLogs.noEOF') : statusLabel(body.state) }}</span>
        <button class="ml-auto text-primary-600 hover:underline" type="button" @click="emit('original', body.id)">{{ t('bodyLogs.original') }}</button>
      </div>
      <details v-if="Object.keys(parameters).length" class="mb-4 rounded-lg bg-gray-50 p-3 dark:bg-dark-950">
        <summary class="cursor-pointer text-xs font-medium text-gray-500">{{ t('bodyLogs.parameters') }}</summary>
        <dl class="mt-3 space-y-2 text-xs">
          <div v-for="(value, key) in parameters" :key="key" class="grid grid-cols-[minmax(7rem,1fr)_3fr] gap-3">
            <dt class="break-all font-mono text-gray-500">{{ key }}</dt>
            <dd class="whitespace-pre-wrap break-words font-mono">{{ format(value) }}</dd>
          </div>
        </dl>
      </details>
      <div v-if="visibleItems.length" class="mb-3 flex flex-wrap gap-3 text-xs">
        <button type="button" class="text-primary-600" @click="jumpLatest">{{ t('bodyLogs.latestMessage') }}</button>
        <button v-for="role in ['user', 'assistant', 'tool']" :key="role" type="button" class="text-gray-500" @click="jumpRole(role)">{{ statusLabel(role) }}</button>
      </div>
      <div v-if="!visibleItems.length" class="py-10 text-sm text-gray-400">{{ t('bodyLogs.noItems') }}</div>
      <div v-else ref="scrollElement" class="max-h-[65vh] overflow-auto pr-2">
        <div :style="virtual ? { height: `${virtualizer.getTotalSize()}px`, position: 'relative', width: '100%' } : undefined">
          <div
            v-for="row in renderedRows" :key="row.item.id" :ref="row.virtual ? measure : undefined"
            :data-index="row.index"
            :style="row.virtual ? { position: 'absolute', top: 0, left: 0, width: '100%', transform: `translateY(${row.start}px)` } : undefined"
          >
            <details v-if="row.item.history || (row.item.kind === 'tool_result' && (row.item.text?.length || 0) > 4096)" :id="'bodylog-collapsed-' + body.id + '-' + encodeURIComponent(row.item.id)">
              <summary class="cursor-pointer py-3 text-sm text-gray-500">{{ statusLabel(row.item.role) }} · {{ row.item.name || statusLabel(row.item.kind) }}</summary>
              <BodyLogMessage :item="row.item" :id-prefix="body.id" :result="findResult(row.item)" @result="goToResult" />
            </details>
            <BodyLogMessage v-else :item="row.item" :id-prefix="body.id" :result="findResult(row.item)" @result="goToResult" />
          </div>
        </div>
      </div>
      <button v-if="hasMore" class="btn btn-secondary mt-4 w-full" :disabled="loading" type="button" @click="load(true)">
        {{ loading ? t('bodyLogs.loading') : t('bodyLogs.more') }}
      </button>
      <p v-if="parseReason" class="mt-3 text-xs text-amber-600">{{ reasonLabel }}</p>
      <p v-if="hasMore" class="mt-3 text-xs text-gray-400">{{ t('bodyLogs.preview') }}</p>
    </template>
  </section>
</template>

<script setup lang="ts">
import { computed, nextTick, ref, watch, type ComponentPublicInstance } from 'vue'
import { useI18n } from 'vue-i18n'
import { useVirtualizer } from '@tanstack/vue-virtual'
import { bodyLogsAPI, type BodyLogBody, type BodyLogItem } from '@/api/admin/bodyLogs'
import BodyLogMessage from './BodyLogMessage.vue'

const props = defineProps<{ recordId: string; body: BodyLogBody; toolsOnly?: boolean }>()
const emit = defineEmits<{ original: [id: string] }>()
const { t, te } = useI18n()
const items = ref<BodyLogItem[]>([])
const parameters = ref<Record<string, unknown>>({})
const state = ref('pending')
const protocol = ref('')
const protocolState = ref('')
const reasonLabel = computed(() => { const key = 'bodyLogs.reason_' + parseReason.value.split(';')[0]; return te(key) ? t(key) : parseReason.value })
const parseReason = ref('')
const cursor = ref(0)
const hasMore = ref(false)
const error = ref(false)
const loading = ref(false)
const scrollElement = ref<HTMLElement | null>(null)
let generation = 0
const visibleItems = computed(() => props.toolsOnly ? items.value.filter(item => item.kind === 'tool_call' || item.kind === 'tool_result') : items.value)
const virtual = computed(() => visibleItems.value.length > 100)
const virtualizer = useVirtualizer(computed(() => ({
  count: virtual.value ? visibleItems.value.length : 0,
  getScrollElement: () => scrollElement.value,
  estimateSize: () => 180,
  overscan: 4,
  getItemKey: (index: number) => visibleItems.value[index]?.id ?? index
})))
const renderedRows = computed(() => virtual.value
  ? virtualizer.value.getVirtualItems().map(row => ({ item: visibleItems.value[row.index], index: row.index, start: row.start, virtual: true }))
  : visibleItems.value.map((item, index) => ({ item, index, start: 0, virtual: false })))
const stateLabel = computed(() => statusLabel(state.value))
function statusLabel(value: string) {
  const keys: Record<string, string> = { empty: 'emptyBody', not_read: 'notRead', error: 'errorState' }
  const key = `bodyLogs.${keys[value] || value}`
  return te(key) ? t(key) : value
}
function format(value: unknown) { return typeof value === 'string' ? value : JSON.stringify(value, null, 2) }
function measure(element: Element | ComponentPublicInstance | null) {
  if (element instanceof HTMLElement) virtualizer.value.measureElement(element)
}
function findResult(item: BodyLogItem) {
  if (item.kind !== 'tool_call' || !item.call_id) return undefined
  const matches = items.value.filter(candidate => candidate.kind === 'tool_result' && candidate.call_id === item.call_id)
  return matches.length === 1 ? matches[0] : undefined
}
function jumpRole(role: string) {
  const target = [...visibleItems.value].reverse().find(item => item.role === role)
  if (target) void goToResult(target)
}
function jumpLatest() {
  const target = [...visibleItems.value].reverse().find(item => item.kind === 'message') || visibleItems.value.at(-1)
  if (target) void goToResult(target)
}
async function goToResult(item: BodyLogItem) {
  const index = visibleItems.value.findIndex(candidate => candidate.id === item.id)
  if (index >= 0 && virtual.value) {
    virtualizer.value.scrollToIndex(index, { align: 'start' })
    await nextTick()
  }
  const collapsed = document.getElementById(`bodylog-collapsed-${props.body.id}-${encodeURIComponent(item.id)}`)
  if (collapsed instanceof HTMLDetailsElement) collapsed.open = true
  document.getElementById(`bodylog-item-${props.body.id}-${encodeURIComponent(item.id)}`)?.scrollIntoView({ block: 'nearest', behavior: 'smooth' })
}
async function load(append: boolean) {
  const current = generation
  loading.value = true; error.value = false
  try {
    const parsed = await bodyLogsAPI.parse(props.recordId, props.body.id, append ? cursor.value : 0)
    if (current !== generation) return
    items.value = append ? [...items.value, ...parsed.items] : parsed.items
    parameters.value = parsed.parameters; state.value = parsed.state
    protocolState.value = parsed.protocol_state || ""; protocol.value = parsed.protocol; parseReason.value = parsed.reason || ''
    cursor.value = parsed.next_cursor; hasMore.value = parsed.has_more
  } catch {
    if (current === generation) error.value = true
  } finally {
    if (current === generation) loading.value = false
  }
}
watch(() => [props.recordId, props.body.id, props.body.saved_bytes, props.body.state], () => {
  generation++; items.value = []; cursor.value = 0
  void load(false)
}, { immediate: true })
</script>
