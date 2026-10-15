<template>
  <section class="space-y-4" data-testid="qoder-oauth-flow">
    <div class="rounded-lg border border-primary-200 bg-primary-50 p-4 dark:border-primary-800 dark:bg-primary-900/20">
      <h3 class="font-semibold text-gray-900 dark:text-white">{{ t('admin.accounts.qoder.title') }}</h3>
      <p class="mt-2 text-sm text-gray-600 dark:text-gray-300">{{ t('admin.accounts.qoder.description') }}</p>
    </div>
    <div v-if="loading && !session" class="text-sm text-gray-500">{{ t('admin.accounts.qoder.starting') }}</div>
    <div v-if="session" class="space-y-3">
      <div class="flex flex-wrap gap-2">
        <a v-if="session.auth_url" :href="session.auth_url" target="_blank" rel="noopener noreferrer" class="btn btn-primary" data-testid="qoder-open-auth">
          {{ t('admin.accounts.qoder.open') }}
        </a>
        <button v-if="session.status === 'pending' || session.status === 'ready'" type="button" class="btn btn-secondary" @click="cancel">
          {{ t('admin.accounts.qoder.cancel') }}
        </button>
      </div>
      <p class="text-sm text-gray-600 dark:text-gray-300" role="status">{{ statusText }}</p>
      <p v-if="session.email" class="text-sm text-gray-600 dark:text-gray-300">{{ session.email }}</p>
      <p v-if="session.models?.length" class="text-sm text-gray-600 dark:text-gray-300">{{ t('admin.accounts.qoder.models', { count: session.models.length }) }}</p>
    </div>
    <p v-if="error" role="alert" class="text-sm text-red-600 dark:text-red-400">{{ error }}</p>
    <button v-if="error || ['cancelled', 'expired', 'failed'].includes(session?.status || '')" type="button" class="btn btn-primary" @click="retry">
      {{ t('admin.accounts.qoder.retry') }}
    </button>
  </section>
</template>

<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import apiClient from '@/api/client'

interface Session {
  session_id: string
  auth_url?: string
  status: 'pending' | 'ready' | 'completed' | 'cancelled' | 'expired' | 'failed'
  expires_at: string
  account_id?: number
  email?: string
  models?: string[]
}
const props = defineProps<{ input: Record<string, unknown> }>()
const emit = defineEmits<{ completed: [accountId: number] }>()
const { t } = useI18n()
const session = ref<Session | null>(null)
const error = ref('')
const loading = ref(false)
let timer: ReturnType<typeof setTimeout> | null = null
let stopped = false
let generation = 0

const statusText = computed(() => t(`admin.accounts.qoder.${session.value?.status || 'pending'}`))
function clearTimer() { if (timer) clearTimeout(timer); timer = null }
async function cancel() {
  generation++
  clearTimer()
  if (!session.value || session.value.status === 'completed') return
  const id = session.value.session_id
  session.value.status = 'cancelled'
  try {
    const { data } = await apiClient.post<Session>('/admin/qoder/oauth/cancel', { session_id: id })
    if (data.status === 'completed' && data.account_id && !stopped) {
      session.value = data
      emit('completed', data.account_id)
    }
  }
  catch { error.value = t('admin.accounts.qoder.cancelError') }
}
async function poll(id: string, run: number) {
  if (stopped || generation !== run) return
  try {
    const { data } = await apiClient.post<Session>('/admin/qoder/oauth/poll', { session_id: id })
    if (stopped || generation !== run) return
    session.value = data
    if (data.status === 'completed' && data.account_id) {
      emit('completed', data.account_id)
      return
    }
    if (data.status === 'ready') {
      const result = await apiClient.post<Session>('/admin/qoder/oauth/commit', { session_id: id })
      if (stopped || generation !== run) return
      session.value = result.data
      if (result.data.status === 'completed' && result.data.account_id) emit('completed', result.data.account_id)
      return
    }
    if (data.status === 'pending') timer = setTimeout(() => poll(id, run), 2000)
  } catch (failure: any) {
    if (stopped || generation !== run) return
    error.value = failure.response?.data?.message || failure.message || t('admin.accounts.qoder.error')
  }
}
async function retry() {
  if (session.value && ['pending', 'ready'].includes(session.value.status)) {
    error.value = ''
    clearTimer()
    await poll(session.value.session_id, ++generation)
  } else { await start() }
}
async function start() {
  await cancel()
  if (stopped) return
  const run = ++generation
  session.value = null
  error.value = ''
  loading.value = true
  try {
    // 只提交账号配置；访问令牌与秘密校验串均留在后端。
    const { data } = await apiClient.post<Session>('/admin/qoder/oauth/start', { ...props.input })
    if (stopped || generation !== run) {
      await apiClient.post('/admin/qoder/oauth/cancel', { session_id: data.session_id })
      return
    }
    session.value = data
    timer = setTimeout(() => poll(data.session_id, run), 1500)
  } catch (failure: any) {
    if (!stopped) error.value = failure.response?.data?.message || failure.message || t('admin.accounts.qoder.error')
  } finally { loading.value = false }
}
onMounted(start)
onBeforeUnmount(() => { stopped = true; void cancel() })
</script>
