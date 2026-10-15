<template>
  <AppLayout>
    <div class="mx-auto max-w-6xl space-y-6">
      <div class="flex flex-wrap items-center justify-between gap-4">
        <div class="flex items-center gap-3">
          <div class="flex h-10 w-10 items-center justify-center rounded-xl border border-gray-200 bg-white dark:border-dark-700 dark:bg-dark-900"><PlatformIcon platform="qoder" size="lg" /></div>
          <div><p class="font-medium text-gray-900 dark:text-gray-100">{{ t('admin.subscriptionQuota.provider') }}</p><p class="text-sm text-gray-600 dark:text-gray-400">{{ t('admin.subscriptionQuota.count', { count: accounts.length }) }}</p></div>
        </div>
        <button class="btn btn-primary min-h-11" :disabled="busy" @click="loadPage(true)"><Icon name="refresh" size="md" class="mr-2" :class="{ 'animate-spin motion-reduce:animate-none': busy }" />{{ t('admin.subscriptionQuota.refreshAll') }}</button>
      </div>
      <p class="text-sm leading-relaxed text-gray-600 dark:text-gray-400">{{ t('admin.subscriptionQuota.sourceNote') }} <a href="https://docs.qoder.cn/product-overview/credits" target="_blank" rel="noopener noreferrer" class="font-medium text-primary-700 hover:underline dark:text-primary-300">{{ t('admin.subscriptionQuota.rules') }} ↗</a></p>
      <p v-if="error" class="rounded-xl border border-red-200 bg-red-50 p-4 text-sm text-red-800 dark:border-red-900 dark:bg-red-950/30 dark:text-red-200" role="alert">{{ error }}</p>
      <div v-if="loadingList && !accounts.length" class="rounded-2xl border border-gray-200 bg-white p-8 text-gray-600 dark:border-dark-700 dark:bg-dark-900 dark:text-gray-400" role="status">{{ t('admin.subscriptionQuota.querying') }}</div>
      <section v-else-if="!accounts.length && !error" class="rounded-2xl border border-dashed border-gray-300 bg-white p-8 text-center dark:border-dark-700 dark:bg-dark-900">
        <h2 class="text-lg font-semibold text-gray-900 dark:text-gray-100">{{ t('admin.subscriptionQuota.empty') }}</h2>
        <p class="mx-auto mt-3 max-w-md text-sm text-gray-600 dark:text-gray-400">{{ t('admin.subscriptionQuota.emptyHint') }}</p>
        <RouterLink to="/admin/accounts" class="btn btn-primary mt-6 min-h-11">{{ t('admin.subscriptionQuota.accounts') }}</RouterLink>
      </section>
      <div v-else class="grid items-start gap-6 xl:grid-cols-2">
        <QoderCreditsCard v-for="account in accounts" :key="account.id" :account="account" :busy="pending.has(account.id)" :class="{ 'xl:col-span-2': accounts.length === 1 }" @refresh="refreshAccount(account.id, true)" />
      </div>
      <p v-if="accounts.some(account => account.credits?.other_pools.length)" class="text-sm leading-relaxed text-gray-600 dark:text-gray-400">{{ t('admin.subscriptionQuota.separateNote') }}</p>
    </div>
  </AppLayout>
</template>

<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { RouterLink } from 'vue-router'
import AppLayout from '@/components/layout/AppLayout.vue'
import PlatformIcon from '@/components/common/PlatformIcon.vue'
import Icon from '@/components/icons/Icon.vue'
import QoderCreditsCard from '@/components/account/QoderCreditsCard.vue'
import { getAccountCredits, listCreditsAccounts, type QoderCreditsAccount } from '@/api/admin/qoderCredits'

const { t } = useI18n()
const accounts = ref<QoderCreditsAccount[]>([])
const pending = ref(new Set<number>())
const loadingList = ref(false)
const error = ref('')
const busy = computed(() => loadingList.value || pending.value.size > 0)
const controller = new AbortController()

async function refreshAccount(id: number, refresh: boolean) {
  if (pending.value.has(id) || controller.signal.aborted) return
  pending.value.add(id)
  try {
    const row = await getAccountCredits(id, refresh, controller.signal)
    if (controller.signal.aborted) return
    accounts.value = accounts.value.map(account => account.id === id ? row : account)
  } catch {
    if (controller.signal.aborted) return
    accounts.value = accounts.value.map(account => account.id === id ? { ...account, state: account.credits ? 'stale' : 'error', error: t('admin.subscriptionQuota.requestFailed') } : account)
  } finally {
    pending.value.delete(id)
  }
}

async function loadPage(refresh = false) {
  if (busy.value || controller.signal.aborted) return
  loadingList.value = true
  error.value = ''
  try {
    accounts.value = await listCreditsAccounts(controller.signal)
    // 只同时查询两个账号，避免批量刷新挤占网关资源。
    const queue = accounts.value.map(account => account.id)
    await Promise.all(Array.from({ length: Math.min(2, queue.length) }, async () => {
      while (queue.length && !controller.signal.aborted) {
        const id = queue.shift()
        if (id !== undefined) await refreshAccount(id, refresh)
      }
    }))
  } catch {
    if (!controller.signal.aborted) error.value = t('admin.subscriptionQuota.loadFailed')
  } finally {
    loadingList.value = false
  }
}
onMounted(() => loadPage())
onUnmounted(() => controller.abort())
</script>
