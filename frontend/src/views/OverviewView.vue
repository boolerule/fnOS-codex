<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'
import { useRouter } from 'vue-router'
import { api, type CodexStatus, type LocalCodexVersions, type SessionInfo, type TerminalMode, type Workspace } from '@/serverapi'
import { useI18n, uiErrText } from '@/composables/useI18n'
import { requestLaunch } from '@/composables/useTerminalLaunch'
import { useToastStore } from '@/stores/toast'
import { fnosAppNeedsWebDirectoryAuth } from '@/utils/fnosApp'
import { trimAppReady } from '@/utils/trimApp'
import { buildFnosAuthURL, fnosAuthReturnURL, readFnosAuthReturn, rememberFnosAuthState, stripFnosAuthReturn, takeFnosAuthState } from '@/utils/fnosAuth'
import { randomId } from '@/utils/randomId'
import ConfirmDialog from '@/components/ConfirmDialog.vue'
import CodexVersionDialog from '@/components/CodexVersionDialog.vue'

const router = useRouter()
const { t, locale } = useI18n()
const toast = useToastStore()
const terminalEnabled = __TERMINAL_ENABLED__
const status = ref<CodexStatus | null>(null)
const localVersions = ref<LocalCodexVersions | null>(null)
const scanError = ref('')
const scanning = ref(false)
const activating = ref(false)
const installations = computed(() => [
  ...(localVersions.value?.managed || []).map(v => ({
    ...v, source: t('managed_install'), systemPath: undefined, selectable: true,
    active: status.value?.source === 'managed' && status.value.version === v.version,
  })),
  ...(localVersions.value?.system || []).map(v => ({
    ...v, source: v.kind === 'binary' ? t('system_binary') : `${t('system_install')} · ${v.kind}`,
    systemPath: v.path, active: status.value?.source === 'path' && status.value.systemPath === v.path,
  })),
])
let scanRequest = 0
const workspaces = ref<Workspace[]>([])
const sessions = ref<SessionInfo[]>([])
const selected = ref('')
const fnos = ref(false)
const appName = ref('')
const refreshing = ref(false)
const versionDialog = ref(false)
const picking = ref(false)
const statusError = ref('')
const workspaceError = ref('')
const sessionError = ref('')
const confirmEnd = ref(false)
const endTarget = ref<SessionInfo | null>(null)
const canStart = computed(() => status.value?.installed && selected.value && !workspaceError.value)
let disposed = false
let pollTimer: number | undefined

async function loadStatus(afterInstall = false) {
  window.clearTimeout(pollTimer)
  try {
    const wasInstalling = status.value?.installing
    status.value = await api.status()
    statusError.value = ''
    if (!disposed && !status.value.installing && (wasInstalling || afterInstall)) void scanVersions()
  } catch (err) {
    statusError.value = uiErrText(err)
  }
  if (!disposed && status.value?.installing) pollTimer = window.setTimeout(() => { void loadStatus() }, 1500)
}

async function scanVersions(force = false) {
  const request = ++scanRequest
  scanning.value = true
  scanError.value = ''
  try {
    const result = await api.localVersions(force)
    if (disposed || request !== scanRequest) return
    localVersions.value = result
    scanError.value = result.error || ''
    if (force) await loadStatus()
  } catch (err) {
    if (!disposed && request === scanRequest) scanError.value = uiErrText(err)
  } finally {
    if (request === scanRequest) scanning.value = false
  }
}

async function activate(version: string, systemPath?: string) {
  if (activating.value || status.value?.installing) return
  activating.value = true
  try {
    await api.install(version, systemPath)
    await loadStatus(true)
  } catch (err) {
    toast.show(uiErrText(err), 'error')
    await Promise.all([loadStatus(), scanVersions()])
  } finally {
    activating.value = false
  }
}

async function loadWorkspaces() {
  try {
    const result = await api.workspaces()
    fnos.value = result.fnos
    appName.value = result.appName
    workspaces.value = result.workspaces || []
    if (!workspaces.value.some((w) => w.path === selected.value)) selected.value = workspaces.value[0]?.path || ''
    workspaceError.value = ''
  } catch (err) {
    workspaceError.value = uiErrText(err)
  }
}

async function loadSessions() {
  try {
    sessions.value = (await api.sessions()).sessions || []
    sessionError.value = ''
  } catch (err) {
    sessionError.value = uiErrText(err)
  }
}

async function refresh() {
  refreshing.value = true
  await Promise.all([loadStatus(), scanVersions(), loadWorkspaces(), ...(terminalEnabled ? [loadSessions()] : [])])
  refreshing.value = false
}

function installStarted() {
  versionDialog.value = false
  void loadStatus(true)
}

function start(mode: TerminalMode) {
  if (!canStart.value) return
  requestLaunch(selected.value, mode)
  void router.replace({ path: '/', query: { view: 'terminal' } })
}

function openSession(session: SessionInfo) {
  void router.replace({ path: '/', query: { view: 'terminal', id: session.id } })
}

function confirmSession(session: SessionInfo) {
  endTarget.value = session
  confirmEnd.value = true
}

async function endSession() {
  const session = endTarget.value
  if (!session) return
  try {
    await api.endSession(session.id)
    await loadSessions()
  } catch (err) {
    toast.show(uiErrText(err), 'error')
  }
}

function authorizeInBrowser() {
  if (!appName.value) throw new Error(t('directory_appid_missing'))
  const state = randomId()
  rememberFnosAuthState(state)
  window.location.replace(buildFnosAuthURL({ appName: appName.value, state, directory: true, redirectUri: fnosAuthReturnURL() }))
}

async function authorize() {
  picking.value = true
  try {
    if (fnosAppNeedsWebDirectoryAuth()) {
      authorizeInBrowser()
      return
    }
    const sdk = await trimAppReady()
    if (!sdk) throw new Error(t('directory_host_bridge_lost'))
    if (sdk.isStandaloneWeb) {
      authorizeInBrowser()
      return
    }
    const result = await sdk.pickUserFile({
      directory: true, title: t('directory_pick_title'), okText: t('directory_pick_ok'),
      sidebarGroup: ['myFiles', 'external', 'favorites', 'team'],
    })
    if (result?.data?.length) await loadWorkspaces()
  } catch (err) {
    toast.show(uiErrText(err), 'error')
  } finally {
    picking.value = false
  }
}

onMounted(() => {
  const result = readFnosAuthReturn()
  if (result) {
    const expected = takeFnosAuthState()
    stripFnosAuthReturn()
    if (!expected || expected !== result.state || result.status === 'error') toast.show(t('directory_auth_failed'), 'error')
    else if (result.status === 'success') toast.show(t('directory_auth_success'), 'success')
  }
  void refresh()
})
onBeforeUnmount(() => {
  disposed = true
  window.clearTimeout(pollTimer)
})
</script>

<template>
  <div class="max-w-4xl mx-auto px-4 sm:px-8 py-6 sm:py-12 space-y-5">
    <header class="flex items-center justify-between gap-4 mb-7">
      <h1 class="font-display text-2xl sm:text-3xl font-semibold tracking-tight">{{ t('version_management') }}</h1>
      <button class="g-btn-secondary" :disabled="refreshing" @click="refresh">{{ t('refresh') }}</button>
    </header>

    <section class="g-card p-5 sm:p-7">
      <div class="flex flex-wrap items-center justify-between gap-4">
        <div>
          <h2 class="text-lg font-semibold">Codex CLI</h2>
          <p class="mt-1 text-sm text-ink-soft dark:text-[#A6A6AD]" role="status">
            {{ !status ? t('status_unknown') : status.installing ? t('installing') : status.installed ? status.version || t('installed') : t('not_installed') }}
            <span v-if="status?.installed"> · {{ t(status.source === 'path' ? 'system_install' : 'managed_install') }}</span>
          </p>
        </div>
        <button class="g-btn-secondary" :disabled="!status || status.installing || activating" @click="versionDialog = true">
          {{ t('install_from_registry') }}
        </button>
      </div>
      <p v-if="statusError || status?.error" class="mt-3 text-sm text-danger-ink break-words" role="alert">{{ statusError || status?.error }}</p>

      <div class="mt-5 border-t border-line pt-4 dark:border-[#2A2A32]">
        <div class="flex flex-wrap items-center justify-between gap-2">
          <h3 class="font-semibold">{{ t('local_installations') }}</h3>
          <button class="g-btn-ghost" :disabled="scanning || activating || status?.installing" @click="scanVersions(true)">
            {{ t(scanning ? 'scanning_versions' : 'rescan_versions') }}
          </button>
        </div>
        <p v-if="scanError" class="mt-2 break-words text-sm text-danger-ink" role="alert">{{ scanError }}</p>
        <p v-if="localVersions && !installations.length && !scanError && !scanning" class="mt-2 text-sm text-ink-soft dark:text-[#A6A6AD]">
          {{ t('no_local_installations') }}
        </p>
        <ul v-if="installations.length" class="mt-2 divide-y divide-line dark:divide-[#2A2A32]">
          <li v-for="entry in installations" :key="entry.path" class="flex items-center justify-between gap-3 py-3">
            <div class="min-w-0">
              <div class="flex flex-wrap items-baseline gap-x-3 gap-y-1">
                <span class="font-mono font-medium">{{ entry.version || t('unknown_version') }}</span>
                <span class="text-xs text-ink-soft dark:text-[#A6A6AD]">{{ entry.source }}</span>
              </div>
              <p class="mt-1 select-text break-all font-mono text-xs text-ink-faint dark:text-[#8A8A92]">{{ entry.path }}</p>
            </div>
            <span v-if="entry.active" class="shrink-0 text-sm font-semibold">{{ t('current_version') }}</span>
            <button v-else-if="entry.selectable" class="g-btn-secondary shrink-0" :disabled="activating || !status || status.installing || scanning"
              :aria-label="t('activate_installation_at', { version: entry.version, path: entry.path })"
              @click="activate(entry.version, entry.systemPath)">{{ t('activate_installation') }}</button>
            <span v-else class="shrink-0 text-xs text-ink-faint dark:text-[#8A8A92]">{{ t('read_only_installation') }}</span>
          </li>
        </ul>
      </div>
    </section>

      <section class="g-card p-5 sm:p-7 space-y-4">
        <div class="flex items-center justify-between gap-3">
          <h2 class="text-lg font-semibold"><label for="project">{{ t('workspace') }}</label></h2>
          <button v-if="fnos" class="g-btn-secondary" :disabled="picking" @click="authorize">{{ t('authorize') }}</button>
        </div>
        <p v-if="workspaceError" class="text-sm text-danger-ink break-words" role="alert">{{ workspaceError }}</p>
        <select v-if="workspaces.length" id="project" v-model="selected" class="g-input w-full font-mono">
          <option v-for="w in workspaces" :key="w.path" :value="w.path">{{ w.name && w.name !== w.path ? w.name + ' · ' : '' }}{{ w.path }}</option>
        </select>
        <p v-else-if="!refreshing && !workspaceError" class="text-sm text-ink-soft dark:text-[#A6A6AD]">{{ t('no_workspaces') }}</p>
        <div v-if="terminalEnabled" class="flex flex-wrap gap-2">
          <button class="g-btn-primary" :disabled="!canStart" @click="start('codex')">{{ t('start_codex') }}</button>
          <button class="g-btn-secondary" :disabled="!canStart" @click="start('login')">{{ t('device_login') }}</button>
        </div>
      </section>

      <section v-if="terminalEnabled" class="g-card p-5 sm:p-7">
        <h2 class="mb-4 text-lg font-semibold">{{ t('sessions') }}</h2>
        <p v-if="sessionError" class="text-sm text-danger-ink break-words" role="alert">{{ sessionError }}</p>
        <p v-else-if="!sessions.length && !refreshing" class="text-sm text-ink-soft dark:text-[#A6A6AD]">{{ t('no_sessions') }}</p>
        <ul class="divide-y divide-line dark:divide-[#2A2A32]">
          <li v-for="s in sessions" :key="s.id" class="flex flex-wrap items-center justify-between py-3 gap-3">
            <div class="min-w-0">
              <p class="font-medium break-all">{{ s.workspace }}</p>
              <p class="text-xs text-ink-faint dark:text-[#8A8A92]">{{ s.mode === 'login' ? t('device_login') : 'Codex CLI' }} · {{ new Date(s.created).toLocaleString(locale) }} · {{ t(s.exited ? 'exited' : 'running') }}</p>
            </div>
            <div class="flex gap-2">
              <button class="g-btn-secondary" @click="openSession(s)">{{ t(s.exited ? 'view_output' : 'resume') }}</button>
              <button class="g-btn-danger" @click="confirmSession(s)">{{ t(s.exited ? 'delete_session' : 'end_session') }}</button>
            </div>
          </li>
        </ul>
      </section>

    <CodexVersionDialog v-if="versionDialog" :current-version="status?.source === 'managed' ? status.version : ''"
      @close="versionDialog = false" @started="installStarted" />
    <ConfirmDialog v-model:visible="confirmEnd" :title="t(endTarget?.exited ? 'delete_session' : 'end_session')"
      :message="t('end_session_hint', { path: endTarget?.workspace || '' })" :cancel-text="t('cancel')" :confirm-text="t(endTarget?.exited ? 'delete_session' : 'end_session')" danger @confirm="endSession" />
  </div>
</template>
