import { beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import { createI18n } from 'vue-i18n'
import BodyLogsView from '../BodyLogsView.vue'
import messages from '@/i18n/locales/en/bodyLogs'

const { config, list, save, showSuccess, showError } = vi.hoisted(() => ({
  config: vi.fn(), list: vi.fn(), save: vi.fn(), showSuccess: vi.fn(), showError: vi.fn()
}))
vi.mock('@/api/admin/bodyLogs', () => ({ bodyLogsAPI: { config, list, save } }))
vi.mock('@/stores/app', () => ({ useAppStore: () => ({ showSuccess, showError }) }))
vi.mock('vue-router', () => ({ useRoute: () => ({ query: {} }), useRouter: () => ({ replace: vi.fn() }) }))
const original = { enabled: true, capture_request: true, capture_response: true, capture_limit_bytes: 0, retention_days: 7, storage_budget_bytes: 10 * 1073741824, capacity_policy: 'delete_oldest_completed' }

beforeEach(() => {
  vi.clearAllMocks()
  config.mockResolvedValue({ config: original, stats: { used_bytes: 1048576, failure_count: 0, last_error: '' } })
  list.mockResolvedValue([])
  save.mockImplementation(value => Promise.resolve({ ...value }))
})

describe('Body log configuration', () => {
  it('waits for current settings before exposing an editable policy', async () => {
    const wrapper = mount(BodyLogsView, {
      global: {
        plugins: [createI18n({ legacy: false, locale: 'en', messages: { en: { bodyLogs: messages } } })],
        stubs: { AppLayout: { template: '<div><slot /></div>' }, BaseDialog: { props: ['show'], template: '<div v-if="show"><slot /></div>' }, Icon: true, BodyLogDetail: true }
      }
    })
    await flushPromises()
    let resolveSettings!: (value: unknown) => void
    config.mockImplementationOnce(() => new Promise(resolve => { resolveSettings = resolve }))
    await wrapper.findAll('button').find(button => button.text() === 'Capture settings')!.trigger('click')
    expect(wrapper.findAll('form')).toHaveLength(1)
    expect(wrapper.text()).toContain(messages.settingsLoading)
    resolveSettings({ config: { ...original, retention_days: 9 }, stats: { used_bytes: 0, failure_count: 0, last_error: '' } })
    await flushPromises()
    const retention = wrapper.findAll('input[type="number"]')[3]
    expect((retention.element as HTMLInputElement).value).toBe('9')
    await retention.setValue(8)
    await wrapper.findAll('form')[1].trigger('submit')
    await flushPromises()
    expect(save).toHaveBeenCalledWith(expect.objectContaining({ retention_days: 8 }))
  })

  it('lets administrators change retention, capacity and the complete-capture setting', async () => {
    const wrapper = mount(BodyLogsView, {
      global: {
        plugins: [createI18n({ legacy: false, locale: 'en', messages: { en: { bodyLogs: messages } } })],
        stubs: { AppLayout: { template: '<div><slot /></div>' }, BaseDialog: { props: ['show'], template: '<div v-if="show"><slot /></div>' }, Icon: true, BodyLogDetail: true }
      }
    })
    await flushPromises()
    await wrapper.findAll('button').find(button => button.text() === 'Capture settings')!.trigger('click')
    await flushPromises()
    const numbers = wrapper.findAll('input[type="number"]')
    // Three list filters precede the retention, budget and capture-limit inputs.
    await numbers[3].setValue(3)
    await numbers[4].setValue(5)
    expect(wrapper.text()).toContain('Reducing age or capacity limits can remove existing new logs')
    await wrapper.findAll('form')[1].trigger('submit')
    await flushPromises()
    expect(save).toHaveBeenCalledWith(expect.objectContaining({
      retention_days: 3, storage_budget_bytes: 5 * 1073741824, capture_limit_bytes: 0
    }))
    expect(showSuccess).toHaveBeenCalledWith('Settings saved')
    expect(showError).not.toHaveBeenCalled()
  })
})
