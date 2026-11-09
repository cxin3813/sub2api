<template>
  <AppLayout>
    <div class="space-y-5">
      <header class="flex flex-wrap items-start justify-between gap-4">
        <div>
          <h1 class="text-xl font-semibold tracking-tight text-gray-900 dark:text-white">{{ t('bodyLogs.title') }}</h1>
          <p class="mt-1 text-sm text-gray-500">{{ t('bodyLogs.description') }}</p>
        </div>
        <div class="flex gap-2">
          <button class="btn btn-secondary" :disabled="loading" type="button" @click="refresh"><Icon name="refresh" size="sm" />{{ t('bodyLogs.refresh') }}</button>
          <button class="btn btn-secondary" type="button" @click="showSettings = true"><Icon name="cog" size="sm" />{{ t('bodyLogs.settings') }}</button>
        </div>
      </header>
      <div v-if="settings && !settings.enabled" class="flex flex-wrap items-center justify-between gap-3 rounded-xl border border-blue-100 bg-blue-50/70 px-5 py-4 dark:border-blue-900/40 dark:bg-blue-950/20">
        <div><p class="text-sm font-medium text-blue-800 dark:text-blue-200">{{ t('bodyLogs.disabled') }}</p><p class="mt-1 text-xs text-blue-600 dark:text-blue-400">{{ t('bodyLogs.disabledHint') }}</p></div>
        <button type="button" class="text-sm font-medium text-primary-600 hover:underline" @click="showSettings = true">{{ t('bodyLogs.settings') }}</button>
      </div>
      <div v-if="stats" class="flex flex-wrap gap-x-6 gap-y-2 text-xs text-gray-500">
        <span>{{ t('bodyLogs.used') }} <strong class="ml-1 font-mono font-medium text-gray-800 dark:text-gray-200">{{ bytes(stats.used_bytes) }}</strong><span v-if="settings"> / {{ bytes(settings.storage_budget_bytes) }}</span></span>
        <span>{{ t('bodyLogs.failures') }} <strong class="ml-1 font-mono font-medium" :class="stats.failure_count ? 'text-amber-600' : 'text-gray-800 dark:text-gray-200'">{{ stats.failure_count }}</strong></span>
        <span v-if="stats.last_error" class="break-all text-amber-600">{{ stats.last_error }}</span>
      </div>

      <div v-if="selected" class="space-y-4">
        <button type="button" class="inline-flex items-center gap-2 text-sm text-gray-500 hover:text-primary-600" @click="back"><span aria-hidden="true">←</span>{{ t('bodyLogs.back') }}</button>
        <div class="flex flex-wrap items-center gap-2">
          <span class="mr-1 text-xs text-gray-400">{{ t('bodyLogs.attempts') }}</span>
          <button v-for="entry in family" :key="entry.id" type="button" class="rounded-lg border px-3 py-1.5 text-xs" :class="selected.id === entry.id ? 'border-primary-300 bg-primary-50 text-primary-700 dark:border-primary-800 dark:bg-primary-950/30 dark:text-primary-300' : 'border-gray-200 text-gray-500 dark:border-dark-700'" @click="select(entry)">{{ entry.kind === 'gateway_http' ? t('bodyLogs.client') : `${entry.method} ${entry.account_id ? '#' + entry.account_id : ''}` }} · {{ entry.status || (entry.error === "context canceled" ? t("bodyLogs.connectionClosed") : state(entry.state)) }}</button>
        </div>
        <div class="card p-5 md:p-7"><BodyLogDetail :record="selected" /></div>
      </div>
      <template v-else-if="legacy">
        <button type="button" class="text-sm text-gray-500 hover:text-primary-600" @click="back">← {{ t('bodyLogs.back') }}</button>
        <div class="card space-y-4 p-5"><h2 class="font-semibold">{{ t('bodyLogs.legacy') }}</h2><p class="text-sm text-gray-500">{{ t('bodyLogs.legacyHint') }}</p><pre class="max-h-[65vh] overflow-auto whitespace-pre-wrap break-words text-xs">{{ format(legacy) }}</pre></div>
      </template>
      <template v-else>
        <form class="flex flex-wrap items-end gap-3" @submit.prevent="load(false)">
          <label class="text-xs text-gray-500">{{ t('bodyLogs.userFilter') }}<input v-model="filters.user" type="number" min="1" class="input mt-1 w-36" /></label>
          <label class="text-xs text-gray-500">{{ t('bodyLogs.keyFilter') }}<input v-model="filters.key" type="number" min="1" class="input mt-1 w-36" /></label>
          <label class="text-xs text-gray-500">{{ t('bodyLogs.statusFilter') }}<input v-model="filters.status" type="number" min="100" max="599" class="input mt-1 w-36" /></label>
          <label class="text-xs text-gray-500">{{ t('bodyLogs.requestId') }}<input v-model="filters.request" class="input mt-1 w-48" /></label>
          <label class="text-xs text-gray-500">{{ t('bodyLogs.model') }}<input v-model="filters.model" class="input mt-1 w-40" /></label>
          <label class="text-xs text-gray-500">{{ t('bodyLogs.after') }}<input v-model="filters.after" type="datetime-local" class="input mt-1" /></label>
          <label class="text-xs text-gray-500">{{ t('bodyLogs.until') }}<input v-model="filters.until" type="datetime-local" class="input mt-1" /></label>
          <label class="text-xs text-gray-500">{{ t('bodyLogs.account') }}<input v-model="filters.account" type="number" min="1" class="input mt-1 w-36" /></label>
          <label class="text-xs text-gray-500">{{ t('bodyLogs.platform') }}<input v-model="filters.platform" class="input mt-1 w-36" /></label>
          <label class="flex items-center gap-2 text-sm"><input v-model="allExchanges" type="checkbox" />{{ t('bodyLogs.allExchanges') }}</label>
          <button type="submit" class="btn btn-secondary">{{ t('bodyLogs.apply') }}</button>
          <button type="button" class="px-2 py-2 text-xs text-gray-500 hover:text-primary-600" @click="clear">{{ t('bodyLogs.clear') }}</button>
        </form>
        <div v-if="loadError" role="alert" class="rounded-lg bg-red-50 p-4 text-sm text-red-600 dark:bg-red-950/20">{{ t('bodyLogs.error') }}</div>
        <div class="card overflow-hidden">
          <div class="overflow-x-auto">
            <table class="w-full text-left text-sm">
              <thead class="border-b border-gray-100 bg-gray-50/70 text-xs text-gray-400 dark:border-dark-800 dark:bg-dark-900"><tr><th class="px-5 py-3 font-medium">{{ t('bodyLogs.time') }}</th><th class="px-5 py-3 font-medium">{{ t('bodyLogs.endpoint') }}</th><th class="px-5 py-3 font-medium">{{ t('bodyLogs.status') }}</th><th class="px-5 py-3 font-medium">{{ t('bodyLogs.size') }}</th><th class="px-5 py-3 font-medium">{{ t('bodyLogs.capture') }}</th><th class="px-5 py-3"><span class="sr-only">{{ t('bodyLogs.view') }}</span></th></tr></thead>
              <tbody class="divide-y divide-gray-100 dark:divide-dark-800">
                <tr v-for="row in rows" :key="row.id" class="hover:bg-gray-50/70 dark:hover:bg-dark-900/50">
                  <td class="whitespace-nowrap px-5 py-4 text-xs text-gray-500">{{ new Date(row.created_at).toLocaleString() }}</td>
                  <td class="max-w-md px-5 py-4"><span class="mr-2 font-mono text-xs font-semibold text-gray-700 dark:text-gray-300">{{ row.method }}</span><span class="break-all font-mono text-xs">{{ row.path }}</span><div class="mt-1 text-xs text-gray-400">{{ row.model || t('bodyLogs.unknown') }} · {{ row.platform || row.kind }}<span v-if="row.api_key_id"> · Key #{{ row.api_key_id }}</span></div></td>
                  <td class="px-5 py-4"><span class="rounded-md px-2 py-1 font-mono text-xs" :class="row.status >= 400 ? 'bg-red-50 text-red-600 dark:bg-red-950/30' : 'bg-gray-100 text-gray-600 dark:bg-dark-800 dark:text-gray-300'">{{ row.status || '—' }}</span></td>
                  <td class="whitespace-nowrap px-5 py-4 font-mono text-xs text-gray-500">{{ bytes(row.bodies.reduce((sum, b) => sum + b.saved_bytes, 0)) }}</td>
                  <td class="px-5 py-4 text-xs" :class="row.error ? 'text-amber-600' : 'text-gray-500'">{{ row.error === "context canceled" ? t("bodyLogs.connectionClosed") : state(row.state) }}</td>
                  <td class="whitespace-nowrap px-5 py-4"><button type="button" class="text-xs font-medium text-primary-600 hover:underline" @click="select(row)">{{ t('bodyLogs.view') }} <span aria-hidden="true">→</span></button></td>
                </tr>
              </tbody>
            </table>
          </div>
          <div v-if="!rows.length" class="py-20 text-center text-sm text-gray-400">{{ loading ? t('bodyLogs.loading') : t('bodyLogs.empty') }}</div>
          <div v-if="hasMore" class="border-t border-gray-100 p-3 text-center dark:border-dark-800"><button type="button" class="btn btn-secondary" :disabled="loading" @click="load(true)">{{ t('bodyLogs.more') }}</button></div>
        </div>
      </template>
    </div>
    <BaseDialog :show="showSettings" :title="t('bodyLogs.settings')" width="wide" @close="showSettings = false">
      <form v-if="form" class="space-y-5" @submit.prevent="save">
        <label class="flex items-center justify-between text-sm font-medium">{{ t('bodyLogs.enabled') }}<input v-model="form.enabled" type="checkbox" class="h-4 w-4 accent-primary-600" /></label>
        <div class="flex flex-wrap gap-5 text-sm"><label class="flex items-center gap-2"><input v-model="form.capture_request" type="checkbox" class="accent-primary-600" />{{ t('bodyLogs.request') }}</label><label class="flex items-center gap-2"><input v-model="form.capture_response" type="checkbox" class="accent-primary-600" />{{ t('bodyLogs.response') }}</label></div>
        <div class="grid gap-4 sm:grid-cols-2">
          <label class="text-xs text-gray-500">{{ t('bodyLogs.days') }}<input v-model.number="form.retention_days" type="number" min="1" max="36500" required class="input mt-1.5" /></label>
          <label class="text-xs text-gray-500">{{ t('bodyLogs.budget') }}<input v-model.number="budgetGiB" type="number" min="0.001" step="0.001" required class="input mt-1.5" /></label>
        </div>
        <label class="block text-xs text-gray-500">{{ t('bodyLogs.captureLimit') }}<input v-model.number="limitMiB" type="number" min="0" step="1" required class="input mt-1.5" /></label>
        <label class="block text-xs text-gray-500">{{ t('bodyLogs.capacity') }}<select v-model="form.capacity_policy" class="input mt-1.5"><option value="delete_oldest_completed">{{ t('bodyLogs.deleteOldest') }}</option><option value="pause_capture">{{ t('bodyLogs.pause') }}</option></select></label>
        <p class="text-xs leading-relaxed text-gray-500">{{ t('bodyLogs.policyHint') }}</p>
        <p v-if="settings && (form.retention_days < settings.retention_days || budgetGiB * 1073741824 < settings.storage_budget_bytes)" class="rounded-lg bg-amber-50 p-3 text-xs leading-relaxed text-amber-700 dark:bg-amber-950/20">{{ t('bodyLogs.changedPolicy') }}</p>
        <div class="flex justify-end gap-2 border-t border-gray-100 pt-4 dark:border-dark-800"><button type="button" class="btn btn-secondary" @click="showSettings = false">{{ t('bodyLogs.close') }}</button><button type="submit" class="btn btn-primary" :disabled="saving">{{ t(saving ? 'bodyLogs.saving' : 'bodyLogs.save') }}</button></div>
      </form>
      <p v-else class="py-8 text-sm text-gray-400">{{ t('bodyLogs.settingsLoading') }}</p>
    </BaseDialog>
  </AppLayout>
</template>

<script setup lang="ts">
import { onMounted, ref, reactive, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { useRoute, useRouter } from 'vue-router'
import { useAppStore } from '@/stores/app'
import { bodyLogsAPI, type BodyLogConfig, type BodyLogRecord } from '@/api/admin/bodyLogs'
import AppLayout from '@/components/layout/AppLayout.vue'
import BaseDialog from '@/components/common/BaseDialog.vue'
import Icon from '@/components/icons/Icon.vue'
import BodyLogDetail from '@/components/admin/bodylog/BodyLogDetail.vue'

const { t, te } = useI18n()
const app = useAppStore()
const route = useRoute()
const router = useRouter()
const rows = ref<BodyLogRecord[]>([])
const selected = ref<BodyLogRecord | null>(null)
const family = ref<BodyLogRecord[]>([])
const legacy = ref<Record<string, unknown> | null>(null)
const settings = ref<BodyLogConfig | null>(null)
const form = ref<BodyLogConfig | null>(null)
const stats = ref<{ used_bytes: number; failure_count: number; last_error: string } | null>(null)
const budgetGiB = ref(10)
const limitMiB = ref(0)
const showSettings = ref(false)
const saving = ref(false)
const loading = ref(false)
const loadError = ref(false)
const hasMore = ref(false)
const allExchanges = ref(false)
const filters = reactive({ user: '', key: '', status: '', request: '', model: '', after: '', until: '', account: '', platform: '', kind: '' })
let generation = 0
let settingsGeneration = 0
function bytes(n: number) { return n >= 1073741824 ? `${(n / 1073741824).toFixed(2)} GiB` : n >= 1048576 ? `${(n / 1048576).toFixed(1)} MiB` : `${(n / 1024).toFixed(1)} KiB` }
function state(value: string) { return te(`bodyLogs.${value}`) ? t(`bodyLogs.${value}`) : value }
function format(value: unknown) { return JSON.stringify(value, null, 2) }
async function load(append: boolean) {
  const current = ++generation
  loading.value = true; loadError.value = false
  try {
    const page = await bodyLogsAPI.list({
      roots_only: allExchanges.value ? undefined : "true",
 account_id: filters.account || undefined, platform: filters.platform || undefined, kind: filters.kind || undefined,
 request_id: filters.request || undefined, model: filters.model || undefined,
 after: filters.after ? new Date(filters.after).toISOString() : undefined,
 until: filters.until ? new Date(filters.until).toISOString() : undefined,
 user_id: filters.user || undefined, api_key_id: filters.key || undefined,
      status: filters.status || undefined, before: append ? rows.value.at(-1)?.created_at : undefined, limit: 50,
      before_id: append ? rows.value.at(-1)?.id : undefined,
      usage_log_id: route.query.usage ? String(route.query.usage) : undefined
    })
    if (generation !== current) return
    rows.value = append ? [...rows.value, ...page] : page
    hasMore.value = page.length === 50
    if (route.query.usage && !append && !page.length) {
      try { legacy.value = await bodyLogsAPI.legacy(Number(route.query.usage)) } catch { /* No legacy body was captured for this usage row. */ }
    }
    if (route.query.id && !append) await select(await bodyLogsAPI.get(String(route.query.id)), true)
  } catch { if (current === generation) loadError.value = true }
  finally { if (current === generation) loading.value = false }
}
async function select(record: BodyLogRecord, loadFamily = true) {
  selected.value = record
  void router.replace({ query: { ...route.query, id: record.id } })
  if (loadFamily) {
    try {
      let root = record
      const ancestors = new Set<string>([root.id])
      while (root.parent_id && !ancestors.has(root.parent_id)) {
        root = await bodyLogsAPI.get(root.parent_id)
        ancestors.add(root.id)
      }
      const found = new Map<string, BodyLogRecord>([[root.id, root]])
      const pending = [root.id]
      while (pending.length) {
        const parent = pending.shift()!
        let before: string | undefined
        let beforeId: string | undefined
        for (;;) {
          const children = await bodyLogsAPI.list({ parent_id: parent, limit: 100, before, before_id: beforeId })
          for (const child of children) {
            if (!found.has(child.id)) { found.set(child.id, child); pending.push(child.id) }
          }
          if (children.length < 100) break
          before = children.at(-1)!.created_at; beforeId = children.at(-1)!.id
        }
      }
      if (selected.value?.id === record.id) family.value = [...found.values()].sort((a, b) => a.created_at.localeCompare(b.created_at))

    } catch { family.value = [record] }
  }
}
async function back() { selected.value = null; legacy.value = null; await router.replace({ query: {} }); await load(false) }
function clear() { Object.assign(filters, { user: '', key: '', status: '', request: '', model: '', after: '', until: '', account: '', platform: '', kind: '' }); void load(false) }
async function loadSettings() {
  const current = ++settingsGeneration
  try {
    const result = await bodyLogsAPI.config()
    if (current !== settingsGeneration) return
    settings.value = result.config; form.value = { ...result.config }; stats.value = result.stats
    budgetGiB.value = result.config.storage_budget_bytes / 1073741824
    limitMiB.value = result.config.capture_limit_bytes / 1048576
  } catch { app.showError(t('bodyLogs.error')) }
}
async function save() {
  if (!form.value) return
  saving.value = true
  try {
    form.value.storage_budget_bytes = Math.round(budgetGiB.value * 1073741824)
    form.value.capture_limit_bytes = Math.round(limitMiB.value * 1048576)
    settings.value = await bodyLogsAPI.save(form.value)
    showSettings.value = false; app.showSuccess(t('bodyLogs.saved')); await loadSettings()
  } catch { app.showError(t('bodyLogs.error')) }
  finally { saving.value = false }
}
async function refresh() {
  await loadSettings()
  try {
    if (selected.value) await select(await bodyLogsAPI.get(selected.value.id))
    else await load(false)
  } catch { app.showError(t('bodyLogs.error')) }
}
watch(showSettings, value => {
  if (value) {
    form.value = null
    void loadSettings()
  }
})
onMounted(() => { void loadSettings(); void load(false) })
</script>
