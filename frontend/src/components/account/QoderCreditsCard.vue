<template>
  <article class="overflow-hidden rounded-2xl border border-gray-200 bg-white shadow-sm dark:border-dark-700 dark:bg-dark-900" :aria-busy="busy">
    <header class="flex items-start justify-between gap-4 border-b border-gray-100 p-5 dark:border-dark-700 sm:p-6">
      <div class="flex min-w-0 items-center gap-3">
        <div class="flex h-11 w-11 shrink-0 items-center justify-center rounded-xl bg-gray-100 dark:bg-dark-800">
          <PlatformIcon platform="qoder" size="lg" />
        </div>
        <div class="min-w-0">
          <h2 class="break-words text-lg font-semibold text-gray-900 dark:text-gray-100">{{ account.name }}</h2>
          <div class="mt-1 flex flex-wrap items-center gap-2 text-sm text-gray-600 dark:text-gray-400">
            <span>#{{ account.id }} · Qoder</span>
            <span v-if="account.credits?.plan" class="rounded-md bg-primary-50 px-2 py-0.5 text-primary-700 dark:bg-primary-950/50 dark:text-primary-300">{{ account.credits.plan }}</span>
            <span>{{ account.status === 'active' && account.schedulable ? t('admin.subscriptionQuota.active') : t('admin.subscriptionQuota.paused') }}</span>
          </div>
        </div>
      </div>
      <button class="btn btn-secondary min-h-11 min-w-11 shrink-0" :disabled="busy" :aria-label="t('admin.subscriptionQuota.refreshAccount')" :title="t('admin.subscriptionQuota.refreshAccount')" @click="$emit('refresh')">
        <Icon name="refresh" size="md" :class="{ 'animate-spin motion-reduce:animate-none': busy }" />
      </button>
    </header>

    <div class="p-5 sm:p-6">
      <div v-if="account.state === 'stale'" class="mb-5 rounded-lg bg-amber-50 p-3 text-sm text-amber-900 dark:bg-amber-950/40 dark:text-amber-200" role="status">
        <strong>{{ t('admin.subscriptionQuota.stale') }}</strong> · {{ t('admin.subscriptionQuota.staleNote') }}
      </div>
      <p v-if="account.error" class="mb-4 text-sm text-red-700 dark:text-red-300" role="alert">{{ account.error }}</p>
      <p v-if="account.credits?.warning" class="mb-4 text-sm text-amber-800 dark:text-amber-200" role="status">{{ account.credits.warning }}</p>

      <div class="grid gap-6 sm:grid-cols-[1.1fr_1fr]">
        <div>
          <div class="text-sm font-medium text-gray-600 dark:text-gray-400">{{ t('admin.subscriptionQuota.remaining') }}</div>
          <div class="mt-2 flex flex-wrap items-baseline gap-2">
            <span data-testid="subscription-remaining" class="break-all text-4xl font-semibold tabular-nums tracking-tight text-gray-900 dark:text-gray-100 sm:text-5xl">{{ formatCredits(account.credits?.subscription.remaining) }}</span>
            <span class="text-sm text-gray-600 dark:text-gray-400">Credits</span>
          </div>
          <p v-if="!account.credits" class="mt-3 text-sm text-gray-600 dark:text-gray-400" role="status">{{ busy ? t('admin.subscriptionQuota.querying') : t('admin.subscriptionQuota.unavailable') }}</p>
          <p v-if="account.credits?.exceeded" class="mt-3 text-sm font-medium text-red-700 dark:text-red-300">{{ t('admin.subscriptionQuota.exhausted') }}</p>
          <p v-if="account.credits?.prorated" class="mt-3 text-sm text-amber-800 dark:text-amber-200">{{ t('admin.subscriptionQuota.prorated') }}</p>
        </div>
        <dl class="grid grid-cols-2 gap-x-4 gap-y-5 rounded-xl bg-gray-50 p-4 dark:bg-dark-800/70">
          <div><dt class="text-sm text-gray-600 dark:text-gray-400">{{ t('admin.subscriptionQuota.total') }}</dt><dd class="mt-1 text-lg font-medium tabular-nums text-gray-900 dark:text-gray-100">{{ formatCredits(account.credits?.subscription.total) }}</dd></div>
          <div><dt class="text-sm text-gray-600 dark:text-gray-400">{{ t('admin.subscriptionQuota.used') }}</dt><dd class="mt-1 text-lg font-medium tabular-nums text-gray-900 dark:text-gray-100">{{ formatCredits(account.credits?.subscription.used) }}</dd></div>
          <div class="col-span-2"><dt class="text-sm text-gray-600 dark:text-gray-400">{{ t('admin.subscriptionQuota.periodEnd') }}</dt><dd class="mt-1 text-sm font-medium text-gray-900 dark:text-gray-100">{{ account.credits?.period_end_at ? formatDate(account.credits.period_end_at) : t('admin.subscriptionQuota.unknownDate') }}</dd></div>
        </dl>
      </div>

      <div v-if="account.credits" class="mt-6">
        <div class="mb-2 flex justify-between gap-4 text-sm text-gray-600 dark:text-gray-400"><span>{{ t('admin.subscriptionQuota.progress') }}</span><span class="tabular-nums">{{ percentage.toLocaleString('zh-CN', { maximumFractionDigits: 2 }) }}%</span></div>
        <div class="h-2 overflow-hidden rounded-full bg-gray-100 dark:bg-dark-700" role="progressbar" :aria-label="t('admin.subscriptionQuota.progress')" :aria-valuenow="percentage" aria-valuemin="0" aria-valuemax="100">
          <div class="h-full rounded-full bg-primary-500" :style="{ width: `${percentage}%` }"></div>
        </div>
      </div>

      <div v-if="account.credits?.other_pools.length" class="mt-6 grid gap-3 md:grid-cols-2">
        <section v-for="pool in account.credits.other_pools" :key="pool.kind" class="rounded-xl border border-gray-200 p-4 dark:border-dark-700">
          <h3 class="text-sm font-medium text-gray-900 dark:text-gray-100">{{ t(`admin.subscriptionQuota.${pool.kind === 'shared' ? 'shared' : 'addon'}`) }}</h3>
          <p v-if="pool.available === false" class="mt-2 text-sm text-gray-600 dark:text-gray-400">{{ t('admin.subscriptionQuota.notAvailable') }}</p>
          <template v-else>
            <p class="mt-2 text-xl font-semibold tabular-nums text-gray-900 dark:text-gray-100">{{ formatCredits(pool.remaining) }} <span class="text-sm font-normal text-gray-600 dark:text-gray-400">Credits {{ t('admin.subscriptionQuota.otherRemaining') }}</span></p>
            <p class="mt-1 text-sm text-gray-600 dark:text-gray-400">{{ t('admin.subscriptionQuota.otherUsed') }} {{ formatCredits(pool.used) }}<template v-if="pool.cap !== undefined"> · {{ t('admin.subscriptionQuota.sharedCap') }} {{ formatCredits(pool.cap) }}</template></p>
          </template>
          <p v-if="pool.expires_at" class="mt-2 text-sm text-gray-600 dark:text-gray-400">{{ t('admin.subscriptionQuota.expiry') }} {{ formatDate(pool.expires_at) }}</p>
        </section>
      </div>
      <div class="mt-6 flex flex-wrap items-center justify-between gap-3 border-t border-gray-100 pt-4 text-xs text-gray-600 dark:border-dark-700 dark:text-gray-400">
        <span>{{ account.credits ? t('admin.subscriptionQuota.updated', { time: formatDate(account.credits.fetched_at) }) : t('admin.subscriptionQuota.pending') }}</span>
        <a href="https://qoder.com.cn/account/usage" target="_blank" rel="noopener noreferrer" class="font-medium text-primary-700 hover:underline dark:text-primary-300">{{ t('admin.subscriptionQuota.officialUsage') }}</a>
      </div>
    </div>
  </article>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import PlatformIcon from '@/components/common/PlatformIcon.vue'
import Icon from '@/components/icons/Icon.vue'
import type { QoderCreditsAccount } from '@/api/admin/qoderCredits'

const props = defineProps<{ account: QoderCreditsAccount; busy: boolean }>()
defineEmits<{ refresh: [] }>()
const { t } = useI18n()
const percentage = computed(() => {
  const pool = props.account.credits?.subscription
  return pool?.total && pool.total > 0 ? Math.max(0, Math.min(100, pool.used / pool.total * 100)) : 0
})
function formatCredits(value?: number) {
  if (value === undefined || !Number.isFinite(value)) return '—'
  if (value > 0 && value < 0.000001) return '<0.000001'
  return value.toLocaleString('zh-CN', { maximumFractionDigits: 6 })
}
function formatDate(value: string) {
  const date = new Date(value)
  if (!Number.isFinite(date.getTime())) return t('admin.subscriptionQuota.unknownDate')
  return `${date.toLocaleString('zh-CN', { timeZone: 'Asia/Shanghai', hour12: false })} ${t('admin.subscriptionQuota.beijingTime')}`
}
</script>
