import { beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import { createI18n } from 'vue-i18n'
import BodyLogContent from '../BodyLogContent.vue'
import BodyLogMessage from '../BodyLogMessage.vue'
import BodyLogDetail from '../BodyLogDetail.vue'
import messages from '@/i18n/locales/en/bodyLogs'
import type { BodyLogBody, BodyLogItem } from '@/api/admin/bodyLogs'

const { parse, copy, raw } = vi.hoisted(() => ({ parse: vi.fn(), copy: vi.fn(), raw: vi.fn() }))
vi.mock('@/api/admin/bodyLogs', () => ({ bodyLogsAPI: { parse, raw } }))
vi.mock('@/composables/useClipboard', () => ({ useClipboard: () => ({ copyToClipboard: copy }) }))
vi.mock('@/stores/app', () => ({ useAppStore: () => ({ showError: vi.fn() }) }))

const body: BodyLogBody = { id: 'body-1', direction: 'client_request', content_type: 'application/json', bytes: 2000000, saved_bytes: 2000000, state: 'complete', parts: [] }
const plugins = () => [createI18n({ legacy: false, locale: 'en', messages: { en: { bodyLogs: messages } } })]
const item = (values: Partial<BodyLogItem>): BodyLogItem => ({ id: 'item-1', kind: 'message', role: 'user', history: false, source: '/input/0', has_more: false, ...values })

beforeEach(() => { vi.clearAllMocks() })

describe('Body log reading', () => {
  it('shows tool results as tools while preserving their original user role', () => {
    const wrapper = mount(BodyLogMessage, {
      props: { idPrefix: body.id, item: item({ kind: 'tool_result', role: 'tool', original_role: 'user', call_id: 'call-7', text: '{"answer":42}' }) },
      global: { plugins: plugins() }
    })
    expect(wrapper.text()).toContain('Tool result')
    expect(wrapper.text()).toContain('Original role: user')
    expect(wrapper.text()).toContain('"answer": 42')
  })

  it('does not execute logged HTML or load embedded remote images', () => {
    const wrapper = mount(BodyLogMessage, {
      props: { idPrefix: body.id, item: item({ text: '<script>alert(1)</script><img src="https://example.test/tracker"><strong>visible</strong>' }) },
      global: { plugins: plugins() }
    })
    expect(wrapper.find('script').exists()).toBe(false)
    expect(wrapper.find('img').exists()).toBe(false)
    expect(wrapper.text()).toContain('visible')
  })

  it('loads subsequent parsed pages without turning a missing tool result into failure', async () => {
    parse.mockResolvedValueOnce({
      protocol: 'responses', state: 'ready', parameters: { model: 'provider-model' }, items: [
        item({ id: 'call', role: 'assistant', kind: 'tool_call', call_id: 'x', name: 'lookup', arguments: '{"q":"test"}' })
      ], next_cursor: 1, has_more: true
    }).mockResolvedValueOnce({
      protocol: 'responses', state: 'ready', parameters: {}, items: [
        item({ id: 'reply', kind: 'tool_result', role: 'tool', call_id: 'x', text: 'found' })
      ], next_cursor: 2, has_more: false
    })
    const wrapper = mount(BodyLogContent, { props: { recordId: 'request', body }, global: { plugins: plugins() } })
    await flushPromises()
    expect(wrapper.text()).toContain('This record does not contain the result')
    await wrapper.findAll('button').find(button => button.text() === 'Load more')!.trigger('click')
    await flushPromises()
    expect(parse).toHaveBeenLastCalledWith('request', 'body-1', 1)
    expect(wrapper.text()).toContain('Read result')
    expect(wrapper.text()).toContain('found')
  })

  it('keeps raw access visible when semantic parsing is partial', async () => {
    parse.mockResolvedValue({ protocol: 'responses', state: 'partial', reason: 'parse_budget_exceeded', parameters: {}, items: [], next_cursor: 0, has_more: false })
    const wrapper = mount(BodyLogContent, { props: { recordId: 'large', body }, global: { plugins: plugins() } })
    await flushPromises()
    const rawButton = wrapper.findAll('button').find(button => button.text() === 'Original bodies')!
    await rawButton.trigger('click')
    expect(wrapper.emitted('original')).toEqual([['body-1']])
    expect(wrapper.text()).toContain('No conversation items could be parsed')
  })

  it('reads next and previous raw pages without accumulating the entire body', async () => {
    raw.mockImplementation((_id, _body, offset) => Promise.resolve({
      content: offset === 0 ? 'first page' : 'second page',
      next_offset: offset + 65536, has_more: offset === 0, saved_bytes: body.saved_bytes, valid_utf8: true
    }))
    const wrapper = mount(BodyLogDetail, {
      props: { record: { id: 'request', kind: 'gateway_http', method: 'POST', path: '/v1/responses', status: 200, created_at: '2026-11-03T00:00:00Z', state: 'closed', persistence: 'durable_local', bodies: [body] } },
      global: { plugins: plugins(), stubs: { BodyLogContent: true } }
    })
    await wrapper.findAll('button').find(button => button.text() === 'Original bodies')!.trigger('click')
    await flushPromises()
    expect(wrapper.text()).toContain('first page')
    await wrapper.findAll('button').find(button => button.text() === 'Load more')!.trigger('click')
    await flushPromises()
    expect(raw).toHaveBeenLastCalledWith('request', 'body-1', 65536)
    expect(wrapper.text()).toContain('second page')
    expect(wrapper.text()).not.toContain('first page')
    await wrapper.findAll('button').find(button => button.text() === 'Previous page')!.trigger('click')
    await flushPromises()
    expect(raw).toHaveBeenLastCalledWith('request', 'body-1', 0)
    expect(wrapper.text()).toContain('first page')
    expect(wrapper.text()).not.toContain('second page')
  })
  it('refreshes the current raw page when the same record is reloaded', async () => {
    raw.mockResolvedValue({ content: 'old body', next_offset: 65536, has_more: true })
    const record = { id: 'request', kind: 'gateway_http', method: 'POST', path: '/v1/responses', status: 200, created_at: '2026-11-03T00:00:00Z', state: 'closed', persistence: 'durable_local', bodies: [body] }
    const wrapper = mount(BodyLogDetail, { props: { record }, global: { plugins: plugins(), stubs: { BodyLogContent: true } } })
    await wrapper.findAll('button').find(button => button.text() === 'Original bodies')!.trigger('click')
    await flushPromises()
    await wrapper.findAll('button').find(button => button.text() === 'Load more')!.trigger('click')
    await flushPromises()
    raw.mockResolvedValue({ content: 'updated body', next_offset: 131072, has_more: false })
    await wrapper.setProps({ record: { ...record, revision: 2 } })
    await flushPromises()
    expect(raw).toHaveBeenLastCalledWith('request', 'body-1', 65536)
    expect(wrapper.text()).toContain('updated body')
    await wrapper.findAll('button').find(button => button.text() === 'Previous page')!.trigger('click')
    await flushPromises()
    expect(raw).toHaveBeenLastCalledWith('request', 'body-1', 0)
  })

})
