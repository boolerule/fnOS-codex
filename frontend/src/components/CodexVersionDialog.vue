<script setup lang="ts">
import { computed, onMounted, ref, watch } from 'vue'
import { api, type CodexVersions } from '@/serverapi'
import { useI18n, uiErrText } from '@/composables/useI18n'
import { useBodyScrollLock } from '@/composables/useBodyScrollLock'
import { useDialogA11y } from '@/composables/useDialogA11y'
import DialogCloseButton from './DialogCloseButton.vue'

defineProps<{ currentVersion: string }>()
const emit = defineEmits<{ close: []; started: [] }>()
const { t } = useI18n()
const panel = ref<HTMLElement | null>(null)
const catalog = ref<CodexVersions>({ latest: '', installed: [], versions: [] })
const loading = ref(false)
const catalogError = ref('')
const submitError = ref('')
const submitting = ref(false)
const selected = ref('latest')
const filter = ref('')
const includePrereleases = ref(false)
const pageSize = 25
const limit = ref(pageSize)
const query = computed(() => filter.value.trim())
const exactInput = computed(() => /^\d+\.\d+\.\d+(?:-[\da-zA-Z-]+(?:\.[\da-zA-Z-]+)*)?(?:\+[\da-zA-Z-]+(?:\.[\da-zA-Z-]+)*)?$/.test(query.value))
const installed = computed(() => new Set(catalog.value.installed))
const matches = computed(() => {
  const versions = new Set([...installed.value, ...catalog.value.versions])
  const result = [...versions].filter(version =>
    version.toLowerCase().includes(query.value.toLowerCase()) &&
    (installed.value.has(version) || includePrereleases.value || query.value.includes('-') || !version.includes('-'))
  )
  // An exact number may still be installable when the registry list is unavailable.
  if (exactInput.value) return [query.value, ...result.filter(v => v !== query.value)]
  return result
})
const confirmText = computed(() => {
  if (selected.value === 'latest') return t('install_latest')
  if (!selected.value) return t('install')
  return t(installed.value.has(selected.value) ? 'switch_to_version' : 'install_version', { version: selected.value })
})

watch(query, () => {
  limit.value = pageSize
  selected.value = query.value ? (exactInput.value ? query.value : '') : 'latest'
  submitError.value = ''
})
watch(includePrereleases, () => {
  limit.value = pageSize
  if (selected.value !== 'latest' && !matches.value.includes(selected.value)) selected.value = ''
})

async function loadVersions() {
  if (loading.value) return
  loading.value = true
  catalogError.value = ''
  try {
    catalog.value = await api.versions()
    catalogError.value = catalog.value.error || ''
  } catch (err) {
    catalogError.value = uiErrText(err)
  } finally {
    loading.value = false
  }
}
function close() {
  if (!submitting.value) emit('close')
}
async function install() {
  if (!selected.value || submitting.value) return
  submitting.value = true
  submitError.value = ''
  try {
    await api.install(selected.value)
    emit('started')
  } catch (err) {
    submitError.value = uiErrText(err)
  } finally {
    submitting.value = false
  }
}
useBodyScrollLock(() => true)
useDialogA11y(() => true, panel, { onClose: close })
onMounted(loadVersions)
</script>

<template>
  <Teleport to="body">
    <div class="fixed inset-0 z-50 flex items-center justify-center p-4 backdrop-blur-sm">
      <div class="g-modal-mask" @click="close"></div>
      <div ref="panel" role="dialog" aria-modal="true" aria-labelledby="codex-version-title" tabindex="-1"
        class="relative flex max-h-[90dvh] w-full max-w-md flex-col overflow-y-auto rounded-xl border border-line bg-white p-6 shadow-card outline-none dark:border-[#2A2A32] dark:bg-[#16161B]">
        <DialogCloseButton :label="t('dialog_close')" :disabled="submitting" @close="close" />
        <h3 id="codex-version-title" class="g-dialog-title mb-4">{{ t('choose_codex_version') }}</h3>
        <input v-model="filter" type="search" maxlength="128" :disabled="submitting" autocomplete="off" spellcheck="false"
          class="g-input w-full" :aria-label="t('search_version')" :placeholder="t('search_version')" />
        <label class="my-3 inline-flex items-center gap-2 text-sm text-ink-soft dark:text-[#A6A6AD]">
          <input v-model="includePrereleases" type="checkbox" :disabled="submitting" />
          {{ t('include_prereleases') }}
        </label>
        <p v-if="loading" role="status" class="mb-2 text-sm text-ink-soft dark:text-[#A6A6AD]">{{ t('loading_versions') }}</p>
        <div v-else-if="catalogError" class="mb-2 flex flex-wrap items-center gap-2">
          <span role="alert" class="text-sm text-danger-ink break-words">{{ t('versions_unavailable') }} {{ catalogError }}</span>
          <button type="button" class="g-btn-secondary" :disabled="submitting" @click="loadVersions">{{ t('retry_versions') }}</button>
        </div>
        <fieldset :disabled="submitting" class="min-h-0 max-h-64 overflow-y-auto rounded-lg border border-line dark:border-[#2A2A32]">
          <legend class="sr-only">{{ t('choose_codex_version') }}</legend>
          <label v-if="!query" class="flex cursor-pointer items-center gap-3 border-b border-line px-3 py-2 text-sm dark:border-[#2A2A32]">
            <input v-model="selected" name="codex-version" type="radio" value="latest" />
            {{ t('latest_version') }}
            <span v-if="catalog.latest" class="font-mono">{{ catalog.latest }}</span>
          </label>
          <label v-for="version in matches.slice(0, limit)" :key="version" class="flex cursor-pointer items-center gap-3 px-3 py-2 text-sm">
            <input v-model="selected" name="codex-version" type="radio" :value="version" />
            <span class="font-mono break-all">{{ version }}</span>
            <span v-if="version === currentVersion || installed.has(version)" class="ml-auto shrink-0 text-xs text-ink-faint dark:text-[#8A8A92]">
              {{ t(version === currentVersion ? 'current_version' : 'installed') }}
            </span>
          </label>
          <button v-if="matches.length > limit" type="button" class="g-btn-secondary m-2" @click="limit += pageSize">{{ t('more_versions') }}</button>
          <p v-if="query && !matches.length && !loading" class="p-3 text-sm text-ink-soft dark:text-[#A6A6AD]">{{ t('no_version_matches') }}</p>
        </fieldset>
        <p v-if="submitError" role="alert" class="mt-3 break-words text-sm text-danger-ink">{{ submitError }}</p>
        <div class="g-dialog-actions mt-4">
          <button class="g-btn-secondary" :disabled="submitting" @click="close">{{ t('cancel') }}</button>
          <button class="g-btn-secondary" :disabled="submitting || !selected" @click="install">{{ confirmText }}</button>
        </div>
      </div>
    </div>
  </Teleport>
</template>
