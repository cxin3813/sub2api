import { apiClient } from '../client'

export interface BodyLogConfig {
  enabled: boolean
  capture_request: boolean
  capture_response: boolean
  capture_limit_bytes: number
  retention_days: number
  storage_budget_bytes: number
  capacity_policy: 'delete_oldest_completed' | 'pause_capture'
}

export interface BodyLogBody {
  id: string
  direction: string
  content_type: string
  content_encoding?: string
  headers?: Record<string, string[]>
  bytes: number
  saved_bytes: number
  state: string
  error?: string
  parts: { key: string; bytes: number; stored_bytes: number; codec: string; sealed: boolean }[]
}

export interface BodyLogRecord {
  id: string
  parent_id?: string
  kind: string
  created_at: string
  ended_at?: string
  method: string
  path: string
  route?: string
  status: number
  user_id?: number
  api_key_id?: number
  account_id?: number
  group_id?: number
  model?: string
  platform?: string
  usage_log_ids?: number[]
  state: string
  persistence: string
  error?: string
  metadata?: Record<string, unknown>
  bodies: BodyLogBody[]
}

export interface BodyLogItem {
  id: string
  kind: string
  role: string
  original_role?: string
  history: boolean
  text?: string
  name?: string
  call_id?: string
  arguments?: string
  is_error?: boolean
  source: string
  choice?: number
  has_more: boolean
}

export interface BodyLogParsed {
  parser_version: number
  protocol: string
  state: string
  reason?: string
  parameters: Record<string, unknown>
  items: BodyLogItem[]
  next_cursor: number
  has_more: boolean
}

export interface BodyLogRaw {
  content: string
  next_offset: number
  has_more: boolean
  saved_bytes: number
  valid_utf8: boolean
}

export const bodyLogsAPI = {
  async config() {
    const { data } = await apiClient.get<{ config: BodyLogConfig; stats: { used_bytes: number; failure_count: number; last_error: string; queued_records: number } }>('/admin/body-logs/config')
    return data
  },
  async save(config: BodyLogConfig) {
    const { data } = await apiClient.put<BodyLogConfig>('/admin/body-logs/config', config)
    return data
  },
  async list(params: Record<string, string | number | undefined> = {}) {
    const { data } = await apiClient.get<{ items: BodyLogRecord[] }>('/admin/body-logs', { params })
    return data.items ?? []
  },
  async get(id: string) {
    const { data } = await apiClient.get<BodyLogRecord>(`/admin/body-logs/${id}`)
    return data
  },
  async parse(id: string, body: string, cursor = 0) {
    const { data } = await apiClient.get<BodyLogParsed>(`/admin/body-logs/${id}/parsed/${body}`, { params: { cursor } })
    return data
  },
  async raw(id: string, body: string, offset = 0) {
    const { data } = await apiClient.get<BodyLogRaw>(`/admin/body-logs/${id}/bodies/${body}`, { params: { offset } })
    return data
  },
  async legacy(usage: number) {
    const { data } = await apiClient.get<Record<string, unknown>>(`/admin/usage/${usage}/body-log`)
    return data
  },
  async download(id: string, body: string) {
    const { data } = await apiClient.post<{ path: string }>(`/admin/body-logs/${id}/download/${body}`)
    return data.path
  }
}
