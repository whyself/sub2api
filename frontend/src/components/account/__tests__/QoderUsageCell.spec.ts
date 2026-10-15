import { beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import QoderUsageCell from '../QoderUsageCell.vue'
import type { Account } from '@/types'
import type { QoderCreditsAccount } from '@/api/admin/qoderCredits'

const getAccountCredits = vi.hoisted(() => vi.fn())
vi.mock('@/api/admin/qoderCredits', () => ({ getAccountCredits }))
vi.mock('vue-i18n', async (importOriginal) => ({
  ...await importOriginal<typeof import('vue-i18n')>(),
  useI18n: () => ({ t: (key: string) => key, locale: { value: 'en' } })
}))

const account = { id: 2, name: 'Qoder', platform: 'qoder', type: 'oauth', status: 'active', schedulable: true, updated_at: '2026-10-16T00:00:00Z' } as Account
const row: QoderCreditsAccount = {
  id: 2, name: 'Qoder', status: 'active', schedulable: true, state: 'fresh',
  credits: {
    unit: 'credits', subscription: { kind: 'subscription', total: 100, used: 1.25, remaining: 98.75 },
    other_pools: [{ kind: 'shared', used: 15, remaining: 500, available: true }],
    period_end_at: '2026-10-24T00:00:00Z', exceeded: false, prorated: false, fetched_at: '2026-10-16T00:00:00Z'
  }
}

describe('QoderUsageCell', () => {
  beforeEach(() => getAccountCredits.mockReset())

  it('loads subscription usage separately from shared credits and supports active queries', async () => {
    getAccountCredits.mockResolvedValue(row)
    const wrapper = mount(QoderUsageCell, { props: { account } })
    await flushPromises()
    expect(getAccountCredits).toHaveBeenCalledWith(2, false, expect.any(AbortSignal))
    expect(wrapper.get('[data-testid="qoder-remaining"]').text()).toContain('98.75 Credits')
    expect(wrapper.get('[data-testid="qoder-other-pool"]').text()).toContain('500 Credits')
    expect(wrapper.findComponent({ name: 'UsageProgressBar' }).props('utilization')).toBe(1.25)
    await wrapper.get('button').trigger('click')
    await flushPromises()
    expect(getAccountCredits).toHaveBeenLastCalledWith(2, true, expect.any(AbortSignal))
    wrapper.unmount()
  })

  it('shows unknown on first failure and keeps the last successful credits after refresh fails', async () => {
    getAccountCredits.mockRejectedValueOnce(new Error('offline')).mockResolvedValueOnce(row).mockRejectedValueOnce(new Error('offline'))
    const wrapper = mount(QoderUsageCell, { props: { account } })
    await flushPromises()
    expect(wrapper.text()).toContain('—')
    expect(wrapper.find('[data-testid="qoder-remaining"]').exists()).toBe(false)
    await wrapper.get('button').trigger('click')
    await flushPromises()
    await wrapper.get('button').trigger('click')
    await flushPromises()
    expect(wrapper.get('[data-testid="qoder-remaining"]').text()).toContain('98.75')
    expect(wrapper.text()).toContain('admin.qoderUsage.stale')
    wrapper.unmount()
  })

  it('discards an old account response when a virtual row is reused', async () => {
    let finishOld!: (value: QoderCreditsAccount) => void
    getAccountCredits.mockReturnValueOnce(new Promise(resolve => { finishOld = resolve })).mockResolvedValueOnce({ ...row, id: 3, credits: undefined })
    const wrapper = mount(QoderUsageCell, { props: { account } })
    const oldSignal = getAccountCredits.mock.calls[0]![2] as AbortSignal
    await wrapper.setProps({ account: { ...account, id: 3 } })
    await flushPromises()
    finishOld(row)
    await flushPromises()
    expect(oldSignal.aborted).toBe(true)
    expect(wrapper.find('[data-testid="qoder-remaining"]').exists()).toBe(false)
    expect(getAccountCredits).toHaveBeenLastCalledWith(3, false, expect.any(AbortSignal))
    await wrapper.setProps({ manualRefreshToken: 1 })
    await flushPromises()
    expect(getAccountCredits).toHaveBeenLastCalledWith(3, true, expect.any(AbortSignal))
    wrapper.unmount()
  })
})
