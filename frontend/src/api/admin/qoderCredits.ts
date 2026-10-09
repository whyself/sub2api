import { apiClient } from '../client'

export interface QoderCreditPool {
  kind: 'subscription' | 'addon' | 'shared'
  remaining: number
  used: number
  total?: number
  cap?: number
  available?: boolean
  expires_at?: string
}

export interface QoderCreditsSnapshot {
  unit: 'credits'
  plan?: string
  user_type?: string
  subscription: QoderCreditPool
  other_pools: QoderCreditPool[]
  period_end_at?: string
  exceeded: boolean
  prorated: boolean
  fetched_at: string
  warning?: string
}

export interface QoderCreditsAccount {
  id: number
  name: string
  status: string
  schedulable: boolean
  state: 'loading' | 'fresh' | 'cached' | 'stale' | 'error'
  credits?: QoderCreditsSnapshot
  error?: string
}

export async function listCreditsAccounts(signal?: AbortSignal): Promise<QoderCreditsAccount[]> {
  const { data } = await apiClient.get<{ items: QoderCreditsAccount[] }>('/admin/qoder/credits/accounts', { signal })
  return data.items
}

export async function getAccountCredits(id: number, refresh = false, signal?: AbortSignal): Promise<QoderCreditsAccount> {
  const { data } = await apiClient.get<QoderCreditsAccount>(`/admin/qoder/credits/accounts/${id}`, {
    params: refresh ? { refresh: 1 } : undefined,
    signal,
    timeout: 40000
  })
  return data
}
