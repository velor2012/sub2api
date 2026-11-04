import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import type { VueWrapper } from '@vue/test-utils'
import type * as VueI18n from 'vue-i18n'
import SubscriptionsView from '../SubscriptionsView.vue'

const { list } = vi.hoisted(() => ({ list: vi.fn() }))

vi.mock('@/api/admin', () => ({
  adminAPI: {
    subscriptions: { list },
    groups: { getAll: vi.fn().mockResolvedValue([]) }
  }
}))
vi.mock('@/stores/app', () => ({
  useAppStore: () => ({ showError: vi.fn(), showSuccess: vi.fn() })
}))
vi.mock('vue-i18n', async () => ({
  ...await vi.importActual<typeof VueI18n>('vue-i18n'),
  useI18n: () => ({ t: (key: string) => key })
}))

const subscription = {
  id: 1, user_id: 1, group_id: 1, status: 'active',
  starts_at: '2026-01-01T00:00:00Z', expires_at: '2099-01-01T00:00:00Z',
  daily_usage_usd: 11, weekly_usage_usd: 11, monthly_usage_usd: 11,
  group: { daily_limit_usd: 20, weekly_limit_usd: 80, monthly_limit_usd: null },
  quota_limits: {
    dynamic_enabled: true, abnormal: false, positions: 2,
    dynamic_daily_limit_usd: 10, dynamic_weekly_limit_usd: 10,
    effective_daily_limit_usd: 10, effective_weekly_limit_usd: 10
  }
}

function mountView() {
  return mount(SubscriptionsView, {
    global: {
      stubs: {
        AppLayout: { template: '<div><slot /></div>' },
        TablePageLayout: { template: '<div><slot name="table" /></div>' },
        DataTable: {
          props: ['data'],
          template: '<div><div v-for="row in data" :key="row.id"><slot name="cell-usage" :row="row" /></div></div>'
        },
        BaseDialog: { template: '<div />' },
        ConfirmDialog: true, Select: true, Pagination: true, EmptyState: true,
        GroupBadge: true, GroupOptionItem: true, Icon: true, Teleport: true, RouterLink: true
      }
    }
  })
}

let wrapper: VueWrapper | undefined
beforeEach(() => {
  vi.clearAllMocks()
  localStorage.clear()
})
afterEach(() => wrapper?.unmount())

describe('admin subscription effective quotas', () => {
  it('shows reduced caps without reducing real usage and marks over-limit progress', async () => {
    list.mockResolvedValue({ items: [subscription], total: 1, pages: 1 })
    wrapper = mountView()
    await flushPromises()

    expect(wrapper.findAll('.usage-amount').map(amount => amount.text().replace(/\s+/g, ' ')))
      .toEqual(['$11.00 / $10.00', '$11.00 / $10.00'])
    for (const progress of wrapper.findAll('.usage-row .transition-all')) {
      expect(progress.attributes('style')).toContain('width: 100%')
      expect(progress.classes()).toContain('bg-red-500')
    }
  })

  it('shows a dynamic weekly cap when fixed limits are absent and keeps the day unlimited', async () => {
    list.mockResolvedValue({ items: [{
      ...subscription,
      group: { daily_limit_usd: null, weekly_limit_usd: null, monthly_limit_usd: null },
      quota_limits: {
        ...subscription.quota_limits,
        dynamic_daily_limit_usd: 0, dynamic_weekly_limit_usd: 15,
        effective_daily_limit_usd: null, effective_weekly_limit_usd: 15
      }
    }], total: 1, pages: 1 })
    wrapper = mountView()
    await flushPromises()

    expect(wrapper.findAll('.usage-amount').map(amount => amount.text().replace(/\s+/g, ' ')))
      .toEqual(['$11.00 / $15.00'])
  })
})
