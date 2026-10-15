<template>
  <div class="min-w-0 max-w-[240px] space-y-1" :aria-busy="loading" data-testid="qoder-usage-cell">
    <template v-if="snapshot">
      <UsageProgressBar
        v-if="utilization !== null"
        label="Credits"
        label-width="auto"
        :utilization="utilization"
        :resets-at="snapshot.period_end_at"
        color="indigo"
      />
      <div class="text-xs tabular-nums text-gray-600 dark:text-gray-400" data-testid="qoder-remaining">
        {{ t('admin.qoderUsage.remaining') }} {{ formatCredits(snapshot.subscription.remaining) }} Credits
      </div>
      <div class="text-[10px] tabular-nums text-gray-500 dark:text-gray-400">
        {{ t('admin.qoderUsage.used') }} {{ formatCredits(snapshot.subscription.used) }} / {{ formatCredits(snapshot.subscription.total) }} Credits
      </div>
      <div v-if="snapshot.period_end_at" class="text-[10px] text-gray-500 dark:text-gray-400">
        {{ t('admin.qoderUsage.periodEnd') }} {{ formatDate(snapshot.period_end_at) }}
      </div>
      <div v-for="pool in snapshot.other_pools" :key="pool.kind" class="text-[10px] text-gray-500 dark:text-gray-400" data-testid="qoder-other-pool">
        {{ t(`admin.qoderUsage.${pool.kind === 'shared' ? 'shared' : 'addon'}`) }}:
        <template v-if="pool.available === false">{{ t('admin.qoderUsage.notAvailable') }}</template>
        <template v-else>{{ t('admin.qoderUsage.remaining') }} {{ formatCredits(pool.remaining) }} Credits</template>
      </div>
      <div v-if="snapshot.exceeded" class="text-xs text-red-600 dark:text-red-400">{{ t('admin.qoderUsage.exhausted') }}</div>
      <div v-if="snapshot.warning" class="break-words text-xs text-amber-600 dark:text-amber-400">{{ snapshot.warning }}</div>
      <div v-if="result?.state === 'stale'" class="text-xs text-amber-600 dark:text-amber-400" role="status">{{ t('admin.qoderUsage.stale') }}</div>
      <div class="text-[10px] text-gray-400">{{ t('admin.qoderUsage.updated', { time: formatDate(snapshot.fetched_at) }) }}</div>
    </template>
    <div v-else class="text-xs text-gray-400" role="status">{{ loading ? t('admin.qoderUsage.querying') : '—' }}</div>
    <div v-if="result?.error" class="break-words text-xs text-red-500" role="alert">{{ result.error }}</div>
    <button
      type="button"
      class="inline-flex items-center gap-0.5 rounded px-1.5 py-0.5 text-[10px] font-medium text-blue-600 transition-colors hover:bg-blue-50 disabled:cursor-not-allowed disabled:opacity-50 dark:text-blue-400 dark:hover:bg-blue-900/30"
      :disabled="loading"
      @click="loadCredits(true)"
    >
      <Icon name="refresh" size="xs" :class="{ 'animate-spin motion-reduce:animate-none': loading }" />
      {{ t('admin.accounts.usageWindow.activeQuery') }}
    </button>
  </div>
</template>

<script setup lang="ts">
import { computed, onBeforeUnmount, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { getAccountCredits, type QoderCreditsAccount } from '@/api/admin/qoderCredits'
import type { Account } from '@/types'
import { formatDateTimeToMinute } from '@/utils/format'
import Icon from '@/components/icons/Icon.vue'
import UsageProgressBar from './UsageProgressBar.vue'

const props = withDefaults(defineProps<{ account: Account; manualRefreshToken?: number }>(), { manualRefreshToken: 0 })
const { t, locale } = useI18n()
const result = ref<QoderCreditsAccount | null>(null)
const loading = ref(false)
const snapshot = computed(() => result.value?.credits)
const utilization = computed(() => {
  const pool = snapshot.value?.subscription
  return pool?.total && pool.total > 0 ? pool.used / pool.total * 100 : null
})
let controller: AbortController | undefined

function formatCredits(value?: number) {
  if (value === undefined || !Number.isFinite(value)) return '—'
  if (value > 0 && value < 0.000001) return '<0.000001'
  return value.toLocaleString(locale.value, { maximumFractionDigits: 6 })
}

function formatDate(value: string) {
  const date = new Date(value)
  return Number.isFinite(date.getTime()) ? formatDateTimeToMinute(date, locale.value) : '—'
}

async function loadCredits(force = false) {
  if (loading.value) return
  const request = new AbortController()
  controller = request
  loading.value = true
  try {
    const row = await getAccountCredits(props.account.id, force, request.signal)
    if (!request.signal.aborted) result.value = row
  } catch {
    if (!request.signal.aborted) {
      result.value = {
        id: props.account.id, name: props.account.name, status: props.account.status,
        schedulable: props.account.schedulable, credits: result.value?.credits,
        state: result.value?.credits ? 'stale' : 'error', error: t('admin.qoderUsage.requestFailed')
      }
    }
  } finally {
    if (!request.signal.aborted) loading.value = false
  }
}

// Virtualized rows can be reused for another account or a new authorization.
watch([() => props.account.id, () => props.account.updated_at], () => {
  controller?.abort()
  loading.value = false
  result.value = null
  void loadCredits()
}, { immediate: true })
watch(() => props.manualRefreshToken, () => { void loadCredits(true) })
onBeforeUnmount(() => controller?.abort())
</script>
