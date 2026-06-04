import { describe, expect, it, vi, beforeEach, afterEach } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import { defineComponent, h } from 'vue'

import UsageView from '../UsageView.vue'

const { list, getStats, getSnapshotV2, getModelStats, getById, getBodyLog, showError } = vi.hoisted(() => {
	vi.stubGlobal('localStorage', {
		getItem: vi.fn(() => null),
		setItem: vi.fn(),
    removeItem: vi.fn(),
  })

  return {
    list: vi.fn(),
    getStats: vi.fn(),
    getSnapshotV2: vi.fn(),
		getModelStats: vi.fn(),
		getById: vi.fn(),
		getBodyLog: vi.fn(),
		showError: vi.fn(),
	}
})

const messages: Record<string, string> = {
  'admin.dashboard.timeRange': 'Time Range',
  'admin.dashboard.day': 'Day',
  'admin.dashboard.hour': 'Hour',
  'admin.usage.failedToLoadUser': 'Failed to load user',
  'admin.usage.failedToLoadBodyLog': 'Failed to load body log',
}

const formatLocalDate = (date: Date): string => {
  const year = date.getFullYear()
  const month = String(date.getMonth() + 1).padStart(2, '0')
  const day = String(date.getDate()).padStart(2, '0')
  return `${year}-${month}-${day}`
}

vi.mock('@/api/admin', () => ({
  adminAPI: {
    usage: {
      list,
      getStats,
      getBodyLog,
    },
    dashboard: {
      getSnapshotV2,
      getModelStats,
    },
    users: {
      getById,
    },
  },
}))

vi.mock('@/api/admin/usage', () => ({
  adminUsageAPI: {
    list: vi.fn(),
  },
}))

vi.mock('@/stores/app', () => ({
	useAppStore: () => ({
		showError,
		showWarning: vi.fn(),
		showSuccess: vi.fn(),
		showInfo: vi.fn(),
  }),
}))

vi.mock('@/utils/format', () => ({
  formatReasoningEffort: (value: string | null | undefined) => value ?? '-',
}))

vi.mock('vue-i18n', async () => {
  const actual = await vi.importActual<typeof import('vue-i18n')>('vue-i18n')
  return {
    ...actual,
    useI18n: () => ({
      t: (key: string) => messages[key] ?? key,
    }),
  }
})

vi.mock('vue-router', () => ({
  useRoute: () => ({
    query: {}
  })
}))

const AppLayoutStub = { template: '<div><slot /></div>' }
const UsageFiltersStub = { template: '<div><slot name="after-reset" /></div>' }
const ModelDistributionChartStub = {
  props: ['metric'],
  emits: ['update:metric'],
  template: `
    <div data-test="model-chart">
      <span class="metric">{{ metric }}</span>
      <button class="switch-metric" @click="$emit('update:metric', 'actual_cost')">switch</button>
    </div>
  `,
}
const GroupDistributionChartStub = {
  props: ['metric'],
  emits: ['update:metric'],
  template: `
    <div data-test="group-chart">
      <span class="metric">{{ metric }}</span>
      <button class="switch-metric" @click="$emit('update:metric', 'actual_cost')">switch</button>
    </div>
  `,
}

const UsageTableBodyLogStub = defineComponent({
  props: {
    data: {
      type: Array,
      default: () => [],
    },
  },
  emits: ['bodyLogClick'],
  setup(props, { emit }) {
    return () =>
      h(
        'button',
        {
          class: 'body-log-trigger',
          onClick: () => emit('bodyLogClick', (props.data as Array<Record<string, unknown>>)[0]),
        },
        'open body log',
      )
  },
})

const UsageBodyLogModalStub = defineComponent({
  props: {
    show: {
      type: Boolean,
      default: false,
    },
    loading: {
      type: Boolean,
      default: false,
    },
    bodyLog: {
      type: Object,
      default: null,
    },
  },
  setup(props) {
    return () =>
      h(
        'div',
        {
          'data-test': 'body-log-modal',
        },
        `${props.show ? 'open' : 'closed'}|${props.loading ? 'loading' : 'idle'}|${(props.bodyLog as Record<string, unknown> | null)?.request_id ?? ''}`,
      )
  },
})

describe('admin UsageView distribution metric toggles', () => {
  beforeEach(() => {
    vi.useFakeTimers()
    list.mockReset()
    getStats.mockReset()
    getSnapshotV2.mockReset()
		getModelStats.mockReset()
		getById.mockReset()
		getBodyLog.mockReset()
		showError.mockReset()

    list.mockResolvedValue({
      items: [],
      total: 0,
      pages: 0,
    })
    getStats.mockResolvedValue({
      total_requests: 0,
      total_input_tokens: 0,
      total_output_tokens: 0,
      total_cache_tokens: 0,
      total_tokens: 0,
      total_cost: 0,
      total_actual_cost: 0,
      average_duration_ms: 0,
    })
    getSnapshotV2.mockResolvedValue({
      trend: [],
      models: [],
      groups: [],
    })
    getModelStats.mockResolvedValue({
      models: [],
    })
    getBodyLog.mockResolvedValue({
      request_id: 'req-body-log-default',
      request_body: '{}',
      response_body: '{}',
      status_code: 200,
    })
  })

  afterEach(() => {
    vi.useRealTimers()
  })

  it('keeps model and group metric toggles independent without refetching chart data', async () => {
    const wrapper = mount(UsageView, {
      global: {
        stubs: {
          AppLayout: AppLayoutStub,
          UsageStatsCards: true,
          UsageFilters: UsageFiltersStub,
          UsageTable: true,
          UsageExportProgress: true,
          UsageCleanupDialog: true,
          UserBalanceHistoryModal: true,
          Pagination: true,
          Select: true,
          DateRangePicker: true,
          Icon: true,
          TokenUsageTrend: true,
          ModelDistributionChart: ModelDistributionChartStub,
          GroupDistributionChart: GroupDistributionChartStub,
        },
      },
    })

    vi.advanceTimersByTime(120)
    await flushPromises()

    expect(getSnapshotV2).toHaveBeenCalledTimes(1)
    const now = new Date()
    const yesterday = new Date(now.getTime() - 24 * 60 * 60 * 1000)
    expect(getSnapshotV2).toHaveBeenCalledWith(expect.objectContaining({
      start_date: formatLocalDate(yesterday),
      end_date: formatLocalDate(now),
      granularity: 'hour'
    }))

    const modelChart = wrapper.find('[data-test="model-chart"]')
    const groupChart = wrapper.find('[data-test="group-chart"]')

    expect(modelChart.find('.metric').text()).toBe('tokens')
    expect(groupChart.find('.metric').text()).toBe('tokens')

    await modelChart.find('.switch-metric').trigger('click')
    await flushPromises()

    expect(modelChart.find('.metric').text()).toBe('actual_cost')
    expect(groupChart.find('.metric').text()).toBe('tokens')
    expect(getSnapshotV2).toHaveBeenCalledTimes(1)

    await groupChart.find('.switch-metric').trigger('click')
    await flushPromises()

    expect(modelChart.find('.metric').text()).toBe('actual_cost')
    expect(groupChart.find('.metric').text()).toBe('actual_cost')
    expect(getSnapshotV2).toHaveBeenCalledTimes(1)
  })

	it('loads usage body log details when requested from the table', async () => {
    list.mockResolvedValueOnce({
      items: [
        {
          id: 42,
          user_id: 1,
          api_key_id: 2,
          account_id: null,
          request_id: 'req-body-log-42',
          model: 'gpt-4.1',
          group_id: null,
          subscription_id: null,
          input_tokens: 0,
          output_tokens: 0,
          cache_creation_tokens: 0,
          cache_read_tokens: 0,
          cache_creation_5m_tokens: 0,
          cache_creation_1h_tokens: 0,
          input_cost: 0,
          output_cost: 0,
          cache_creation_cost: 0,
          cache_read_cost: 0,
          total_cost: 0,
          actual_cost: 0,
          rate_multiplier: 1,
          billing_type: 0,
          stream: false,
          duration_ms: 0,
          first_token_ms: null,
          image_count: 0,
          image_size: null,
          image_input_size: null,
          image_output_size: null,
          image_size_source: null,
          image_size_breakdown: null,
          user_agent: null,
          cache_ttl_overridden: false,
          created_at: '2026-06-03T00:00:00Z',
        },
      ],
      total: 1,
      pages: 1,
    })
    getBodyLog.mockResolvedValueOnce({
      request_id: 'req-body-log-42',
      request_body: '{"prompt":"hi"}',
      response_body: '{"text":"ok"}',
      status_code: 200,
    })

    const wrapper = mount(UsageView, {
      global: {
        stubs: {
          AppLayout: AppLayoutStub,
          UsageStatsCards: true,
          UsageFilters: UsageFiltersStub,
          UsageTable: UsageTableBodyLogStub,
          UsageBodyLogModal: UsageBodyLogModalStub,
          UsageExportProgress: true,
          UsageCleanupDialog: true,
          UserBalanceHistoryModal: true,
          Pagination: true,
          Select: true,
          DateRangePicker: true,
          Icon: true,
          TokenUsageTrend: true,
          ModelDistributionChart: ModelDistributionChartStub,
          GroupDistributionChart: GroupDistributionChartStub,
        },
      },
    })

    vi.advanceTimersByTime(120)
    await flushPromises()

    await wrapper.get('.body-log-trigger').trigger('click')
    await flushPromises()

    expect(getBodyLog).toHaveBeenCalledWith(42)
    expect(wrapper.get('[data-test="body-log-modal"]').text()).toContain('open')
		expect(wrapper.get('[data-test="body-log-modal"]').text()).toContain('req-body-log-42')
	})

	it('keeps usage body log modal open with empty state when body log is missing', async () => {
		list.mockResolvedValueOnce({
			items: [
				{
					id: 43,
					user_id: 1,
					api_key_id: 2,
					account_id: null,
					request_id: 'req-body-log-missing',
					model: 'gpt-4.1',
					group_id: null,
					subscription_id: null,
					input_tokens: 0,
					output_tokens: 0,
					cache_creation_tokens: 0,
					cache_read_tokens: 0,
					cache_creation_5m_tokens: 0,
					cache_creation_1h_tokens: 0,
					input_cost: 0,
					output_cost: 0,
					cache_creation_cost: 0,
					cache_read_cost: 0,
					total_cost: 0,
					actual_cost: 0,
					rate_multiplier: 1,
					billing_type: 0,
					stream: false,
					duration_ms: 0,
					first_token_ms: null,
					image_count: 0,
					image_size: null,
					image_input_size: null,
					image_output_size: null,
					image_size_source: null,
					image_size_breakdown: null,
					user_agent: null,
					cache_ttl_overridden: false,
					created_at: '2026-06-03T00:00:00Z',
				},
			],
			total: 1,
			pages: 1,
		})
		getBodyLog.mockRejectedValueOnce({ status: 404, message: 'not found' })

		const wrapper = mount(UsageView, {
			global: {
				stubs: {
					AppLayout: AppLayoutStub,
					UsageStatsCards: true,
					UsageFilters: UsageFiltersStub,
					UsageTable: UsageTableBodyLogStub,
					UsageBodyLogModal: UsageBodyLogModalStub,
					UsageExportProgress: true,
					UsageCleanupDialog: true,
					UserBalanceHistoryModal: true,
					Pagination: true,
					Select: true,
					DateRangePicker: true,
					Icon: true,
					TokenUsageTrend: true,
					ModelDistributionChart: ModelDistributionChartStub,
					GroupDistributionChart: GroupDistributionChartStub,
				},
			},
		})

		vi.advanceTimersByTime(120)
		await flushPromises()

		await wrapper.get('.body-log-trigger').trigger('click')
		await flushPromises()

		expect(getBodyLog).toHaveBeenCalledWith(43)
		expect(wrapper.get('[data-test="body-log-modal"]').text()).toContain('open|idle|')
		expect(showError).not.toHaveBeenCalled()
	})
})
