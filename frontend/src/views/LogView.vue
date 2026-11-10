<script setup lang="ts">
import { onMounted, ref } from 'vue'
import { uiErrText, uiText, useI18n } from '@/composables/useI18n'
import { request } from '@/serverapi'

interface LogEntry {
  time: string
  level: 'INFO' | 'WARN' | 'ERROR'
  code: string
  message: string
}

const { t, locale } = useI18n()
const entries = ref<LogEntry[]>([])
const loading = ref(false)
const error = ref('')

async function refresh() {
  if (loading.value) return
  loading.value = true
  error.value = ''
  try {
    const result = await request<{ entries: LogEntry[] }>('/api/logs')
    entries.value = result.entries
  } catch (err) {
    error.value = uiErrText(err)
  } finally {
    loading.value = false
  }
}

onMounted(refresh)
</script>

<template>
  <div class="mx-auto w-full max-w-5xl px-4 py-6 sm:px-8 sm:py-10">
    <header class="mb-5 flex items-center justify-between gap-3">
      <h1 class="font-display text-2xl font-semibold tracking-tight sm:text-3xl">{{ t('nav_logs') }}</h1>
      <button type="button" class="g-btn-secondary" :disabled="loading" @click="refresh">{{ t('refresh') }}</button>
    </header>
    <p v-if="error" role="alert" class="mb-3 text-sm text-danger-ink">{{ error }}</p>
    <section class="g-card p-4 sm:p-5" :aria-busy="loading">
      <p class="mb-3 text-sm text-ink-soft dark:text-[#A6A6AD]">{{ t('log_scope') }}</p>
      <ol v-if="entries.length" class="divide-y divide-line dark:divide-[#2A2A32]">
        <li v-for="(row, i) in entries" :key="row.time + ':' + i" class="flex flex-wrap items-baseline gap-x-3 gap-y-1 py-3 text-sm">
          <time :datetime="row.time" class="text-xs tabular-nums text-ink-soft dark:text-[#A6A6AD]">{{ new Date(row.time).toLocaleString(locale) }}</time>
          <span class="w-12 font-mono text-xs" :class="row.level === 'ERROR' ? 'text-danger-ink' : row.level === 'WARN' ? 'text-amber-700 dark:text-amber-400' : 'text-ink-soft dark:text-[#A6A6AD]'">{{ row.level }}</span>
          <span class="w-full break-words sm:w-auto sm:flex-1">{{ uiText(row.message, { code: 'log_' + row.code }) }}</span>
        </li>
      </ol>
      <p v-else-if="!error" role="status" class="py-3 text-sm">{{ t(loading ? 'log_loading' : 'log_empty') }}</p>
    </section>
  </div>
</template>
