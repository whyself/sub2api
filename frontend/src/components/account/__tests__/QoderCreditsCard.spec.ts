import { describe, expect, it, vi } from 'vitest'
import { mount } from '@vue/test-utils'
import QoderCreditsCard from '../QoderCreditsCard.vue'
import type { QoderCreditsAccount } from '@/api/admin/qoderCredits'

vi.mock('vue-i18n', () => ({ useI18n: () => ({ t: (key: string) => key }) }))

const row: QoderCreditsAccount = {
  id: 2, name: '测试订阅', status: 'active', schedulable: true, state: 'fresh',
  credits: {
    unit: 'credits', subscription: { kind: 'subscription', total: 100, used: 1.25, remaining: 98.75 },
    other_pools: [{ kind: 'shared', used: 15, remaining: 500, available: true }],
    exceeded: false, prorated: false, fetched_at: '2026-10-09T14:00:00Z'
  }
}
describe('Qoder Credits 额度卡片', () => {
  it('独立显示订阅余额与共享额度，不合并余额或丢弃小数', () => {
    const wrapper = mount(QoderCreditsCard, { props: { account: row, busy: false } })
    expect(wrapper.get('[data-testid="subscription-remaining"]').text()).toBe('98.75')
    expect(wrapper.text()).toContain('500')
    expect(wrapper.get('[role="progressbar"]').attributes('aria-valuenow')).toBe('1.25')
  })
  it('首次查询失败显示未知值，刷新失败标注旧余额', () => {
    const unknown = mount(QoderCreditsCard, { props: { account: { ...row, credits: undefined, state: 'error', error: '查询失败' }, busy: false } })
    expect(unknown.get('[data-testid="subscription-remaining"]').text()).toBe('—')
    const stale = mount(QoderCreditsCard, { props: { account: { ...row, state: 'stale', error: '查询失败' }, busy: false } })
    expect(stale.get('[data-testid="subscription-remaining"]').text()).toBe('98.75')
    expect(stale.text()).toContain('admin.subscriptionQuota.staleNote')
  })
})
