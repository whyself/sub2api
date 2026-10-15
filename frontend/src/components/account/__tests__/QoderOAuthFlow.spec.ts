import { mount, flushPromises } from '@vue/test-utils'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import QoderOAuthFlow from '../QoderOAuthFlow.vue'

const { post } = vi.hoisted(() => ({ post: vi.fn() }))
vi.mock('@/api/client', () => ({ default: { post } }))
vi.mock('vue-i18n', () => ({ useI18n: () => ({ t: (key: string) => key }) }))

describe('Qoder 网页设备授权', () => {
  beforeEach(() => { vi.useFakeTimers(); post.mockReset() })
  afterEach(() => { vi.useRealTimers() })

  it('官方授权完成后提交会话编号，浏览器不接收令牌', async () => {
    post.mockResolvedValueOnce({ data: { session_id: '授权会话', auth_url: 'https://qoder.com.cn/device/selectAccounts?nonce=测试', status: 'pending' } })
      .mockResolvedValueOnce({ data: { session_id: '授权会话', status: 'ready', models: ['qwen3.8-flash'] } })
      .mockResolvedValueOnce({ data: { session_id: '授权会话', status: 'completed', account_id: 12 } })
    const wrapper = mount(QoderOAuthFlow, { props: { input: { name: '账号甲', group_ids: [7] } } })
    await flushPromises()
    expect(wrapper.get('[data-testid="qoder-open-auth"]').attributes('href')).toContain('qoder.com.cn')
    await vi.advanceTimersByTimeAsync(1500)
    await flushPromises()
    expect(post).toHaveBeenNthCalledWith(3, '/admin/qoder/oauth/commit', { session_id: '授权会话' })
    expect(wrapper.emitted('completed')?.[0]).toEqual([12])
    expect(JSON.stringify(post.mock.calls)).not.toContain('access_token')
    wrapper.unmount()
  })

  it('关闭弹窗后取消所属会话，停止轮询', async () => {
    post.mockResolvedValue({ data: { session_id: '取消会话', auth_url: 'https://qoder.com.cn/', status: 'pending' } })
    const wrapper = mount(QoderOAuthFlow, { props: { input: { name: '账号乙' } } })
    await flushPromises()
    wrapper.unmount()
    await flushPromises()
    expect(post).toHaveBeenLastCalledWith('/admin/qoder/oauth/cancel', { session_id: '取消会话' })
    const calls = post.mock.calls.length
    await vi.advanceTimersByTimeAsync(10000)
    expect(post).toHaveBeenCalledTimes(calls)
  })
})
