<script setup lang="ts">
import { computed, nextTick, ref, watch } from 'vue'
import { useRouter } from 'vue-router'
import { uiErrText, useI18n } from '@/composables/useI18n'
import { useWorkbench } from '@/composables/useWorkbench'
import type { ChatItem } from '@/serverapi/workbench'
import WorkbenchApproval from '@/components/WorkbenchApproval.vue'
import MarkdownText from '@/components/MarkdownText.vue'

const { t } = useI18n()
const router = useRouter()
const {
  installed, status, error, refreshError, working, workspaces, projects, chats, selected, workspace, model, models,
  account, login, submittedLoginId, signInError, items, cursor, prompt, signedIn, current, pending, initialize, selectChat, send,
  startLogin, completeLogin, cancelLogin, signInWithKey, signOut,
  respond, interrupt, older,
} = useWorkbench()
const accountOpen = ref(false)
const keyFormOpen = ref(false)
const keyInput = ref<HTMLInputElement | null>(null)
const callbackInput = ref<HTMLInputElement | null>(null)
const deviceDenied = computed(() => (signInError.value as { code?: string } | null)?.code === 'err_device_code_forbidden')
const authBusy = computed(() => working.value || !!status.value.activeThread)
const accountLabel = computed(() => {
  const user = account.value?.account
  if (user) return user.type === 'apiKey' ? t('api_key_label') : user.email || user.type
  return t(signedIn.value ? 'auth_not_required' : 'sign_in_prompt')
})
const transcript = ref<HTMLElement | null>(null)
const active = computed(() => !!status.value.activeThread && status.value.activeThread === selected.value)
const activeProject = computed(() => projects.value.find(p => p.id === current.value?.projectId))
const canSend = computed(() => !working.value && status.value.ready && signedIn.value && !status.value.activeThread
  && !!prompt.value.trim() && !!(selected.value || workspace.value))
const signInURL = computed(() => {
  const attempt = login.value
  const raw = attempt?.type === 'chatgpt' ? attempt.authUrl : attempt?.verificationUrl
  if (!raw) return ''
  try {
    const u = new URL(raw)
    const official = ['https://auth.openai.com', 'https://openai.com', 'https://chatgpt.com']
    return official.includes(u.origin) && !u.username && !u.password ? u.href : ''
  } catch { return '' }
})
function versions() {
  void router.replace({ path: '/', query: { view: 'overview' } })
}
async function toggleKeyForm(open: boolean) {
  keyFormOpen.value = open
  signInError.value = null
  if (open) {
    await nextTick()
    keyInput.value?.focus()
  }
}
function submitApiKey() {
  const field = keyInput.value
  if (!field || authBusy.value || signedIn.value) return
  const key = field.value.trim()
  field.value = ''
  if (!key) { field.reportValidity(); return }
  accountOpen.value = true
  void signInWithKey(key)
}
function submitCallback() {
  const field = callbackInput.value
  const attempt = login.value
  if (!field || attempt?.type !== 'chatgpt' || authBusy.value) return
  const url = field.value.trim()
  field.value = ''
  if (!url) { field.reportValidity(); return }
  accountOpen.value = true
  void completeLogin(attempt.loginId, url)
}
watch(signedIn, value => { if (value) keyFormOpen.value = false })
function itemText(item: ChatItem) {
  return item.type === 'userMessage' ? item.content?.map(part => part.text).filter(Boolean).join('\n') : item.text || item.command
}
function label(item: ChatItem) {
  if (item.type === 'userMessage') return t('conversation_user')
  if (item.type === 'agentMessage') return 'Codex'
  if (item.type === 'commandExecution') return t('command_run')
  if (item.type === 'fileChange') return t('file_changes')
  return item.type
}
watch(items, async () => {
  const el = transcript.value
  if (!el || el.scrollHeight - el.scrollTop - el.clientHeight > 72) return
  await nextTick()
  el.scrollTop = el.scrollHeight
})
</script>

<template>
  <div class="wb mx-auto w-full max-w-5xl px-4 py-6 sm:px-8 sm:py-10">
    <header class="mb-5 flex flex-wrap items-center justify-between gap-3">
      <h1 class="font-display text-2xl font-semibold tracking-tight sm:text-3xl">{{ t('workbench') }}</h1>
      <div class="flex gap-2">
        <button v-if="status.ready && signedIn" class="g-btn-secondary" :aria-expanded="accountOpen" @click="accountOpen = !accountOpen">{{ t('account') }}</button>
        <button class="g-btn-secondary" :disabled="working" @click="initialize">{{ t('refresh') }}</button>
      </div>
    </header>
    <p v-if="error || refreshError" class="mb-3 break-words text-sm text-danger-ink" role="alert">{{ error || refreshError }}</p>
    <section v-if="!status.ready" class="g-card p-6">
      <p role="status">{{ t(installed === false ? 'cli_unavailable' : working ? 'loading_workbench' : 'workspace_unavailable') }}</p>
      <button class="g-btn-secondary mt-4" @click="versions">{{ t('go_install') }}</button>
    </section>
    <template v-else>
      <section v-if="accountOpen || !signedIn || login" class="g-card mb-4 p-4 sm:p-5">
        <div class="flex flex-wrap items-center justify-between gap-3">
          <div>
            <h2 class="font-semibold">{{ t('account') }}</h2>
            <p class="wb-meta">{{ accountLabel }}</p>
          </div>
          <div v-if="!signedIn && !keyFormOpen" class="flex flex-wrap gap-2">
            <button class="g-btn-primary" :disabled="authBusy" @click="accountOpen = true; startLogin('chatgpt')">{{ t(login?.type === 'chatgpt' ? 'restart_browser_login' : 'browser_login') }}</button>
            <button class="g-btn-secondary" :disabled="authBusy" @click="accountOpen = true; startLogin('chatgptDeviceCode')">{{ t(deviceDenied ? 'retry_device_login' : 'device_login') }}</button>
            <button class="g-btn-secondary" :disabled="authBusy" @click="toggleKeyForm(true)">{{ t('api_key_login') }}</button>
          </div>
          <button v-if="account?.account" class="g-btn-secondary" :disabled="authBusy" @click="signOut">{{ t('logout') }}</button>
        </div>
        <div v-if="signInError" role="alert" class="mt-3 text-sm">
          <p class="break-words text-danger-ink">{{ uiErrText(signInError) }}</p>
          <a v-if="deviceDenied" class="mt-1 inline-block underline" href="https://developers.openai.com/codex/auth/#login-on-headless-devices" target="_blank" rel="noopener noreferrer">{{ t('device_login_help') }} ↗</a>
        </div>
        <form v-if="keyFormOpen && !signedIn" class="mt-3" autocomplete="off" @submit.prevent="submitApiKey">
          <label class="flex flex-col gap-1 text-sm">
            {{ t('api_key_label') }}
            <input ref="keyInput" class="g-input" type="password" required autocomplete="off" autocapitalize="none" :spellcheck="false" :disabled="authBusy" aria-describedby="key-billing">
          </label>
          <p id="key-billing" class="wb-meta mt-1">{{ t('api_key_billing') }}</p>
          <div class="mt-3 flex flex-wrap gap-2">
            <button type="submit" class="g-btn-secondary" :disabled="authBusy">{{ t('api_key_login') }}</button>
            <button type="button" class="g-btn-secondary" :disabled="authBusy" @click="toggleKeyForm(false)">{{ t('cancel') }}</button>
          </div>
        </form>
        <div v-else-if="login?.type === 'chatgpt'" :key="login.loginId" class="mt-4 text-sm">
          <div v-if="submittedLoginId === login.loginId" class="flex flex-wrap items-center gap-3">
            <p role="status">{{ t('waiting_browser_login') }}</p>
            <button class="g-btn-ghost" :disabled="authBusy" @click="cancelLogin(login.loginId)">{{ t('cancel') }}</button>
          </div>
          <form v-else-if="signInURL" class="space-y-3" autocomplete="off" @submit.prevent="submitCallback">
            <a class="g-btn-secondary" :href="signInURL" target="_blank" rel="noopener noreferrer">{{ t('open_signin') }} ↗</a>
            <p id="callback-help" class="wb-meta">{{ t('browser_callback_help') }}</p>
            <label class="flex flex-col gap-1">
              {{ t('browser_callback_label') }}
              <input ref="callbackInput" class="g-input" type="password" required autocomplete="off" autocapitalize="none" :spellcheck="false" :disabled="authBusy" aria-describedby="callback-help">
            </label>
            <div class="flex gap-2">
              <button type="submit" class="g-btn-secondary" :disabled="authBusy">{{ t('complete_browser_login') }}</button>
              <button type="button" class="g-btn-ghost" :disabled="authBusy" @click="cancelLogin(login.loginId)">{{ t('cancel') }}</button>
            </div>
          </form>
          <p v-else role="alert" class="text-danger-ink">{{ t('err_browser_login') }}</p>
        </div>
        <p v-else-if="login?.type === 'chatgptDeviceCode' && login.userCode" class="mt-3 flex flex-wrap items-center gap-3">
          <a v-if="signInURL" class="g-btn-secondary" :href="signInURL" target="_blank" rel="noopener noreferrer">{{ t('open_signin') }} ↗</a>
          <span>{{ t('device_code') }} <code class="select-all">{{ login.userCode }}</code></span>
        </p>
      </section>
      <div class="g-card overflow-hidden">
        <div class="wb-bar">
          <label class="flex min-w-0 flex-1 flex-col gap-1 text-xs">
            {{ t('previous_chats') }}
            <select class="g-input" :value="selected" :disabled="working" @change="selectChat(($event.target as HTMLSelectElement).value)">
              <option value="">{{ t('new_chat') }}</option>
              <option v-for="chat in [...chats].reverse()" :key="chat.id" :value="chat.id">
                {{ chat.title || t('new_chat') }} · {{ new Date(chat.created).toLocaleString() }}
              </option>
            </select>
          </label>
          <button class="g-btn-secondary" :disabled="working || !selected" @click="selectChat('')">+ {{ t('new_chat') }}</button>
        </div>
        <div v-if="!selected" class="wb-bar">
          <label class="flex min-w-0 flex-1 flex-col gap-1 text-xs">{{ t('cli_workspace') }}
            <select v-model="workspace" class="g-input" :disabled="working">
              <option v-for="w in workspaces" :key="w.path" :value="w.path">{{ w.path }}</option>
            </select>
          </label>
          <button class="g-btn-ghost" @click="versions">{{ t('change_workspace') }}</button>
        </div>
        <div v-else-if="activeProject" class="border-b border-line px-5 py-2 font-mono text-xs break-all dark:border-[#2A2A32]">{{ activeProject.path }}</div>
        <section ref="transcript" class="wb-transcript" :aria-label="t('previous_chats')">
          <button v-if="cursor" class="g-btn-secondary mb-3" :disabled="working" @click="older">{{ t('older') }}</button>
          <div v-if="!selected" class="wb-empty">
            <h2 class="text-2xl font-semibold">{{ t('new_topic') }}</h2>
            <p class="wb-meta mt-2">{{ t('chat_intro') }}</p>
          </div>
          <template v-for="item in items" :key="item.id">
            <article v-if="item.type !== 'turnStatus' || item.error" class="wb-item" :class="{ 'wb-user': item.type === 'userMessage' }">
              <h3 class="wb-meta">{{ label(item) }} <span v-if="item.status">· {{ item.status }}</span></h3>
              <MarkdownText v-if="item.type === 'agentMessage' && item.text" :source="item.text" />
              <p v-else-if="itemText(item)" class="whitespace-pre-wrap">{{ itemText(item) }}</p>
              <details v-if="item.aggregatedOutput || item.changes || item.error" :open="item.type === 'commandExecution' || !!item.error">
                <summary>{{ t(item.aggregatedOutput ? 'command_output' : 'details') }}</summary>
                <pre>{{ item.aggregatedOutput || JSON.stringify(item.error || item.changes, null, 2) }}</pre>
              </details>
            </article>
          </template>
          <WorkbenchApproval v-for="a in pending" :key="a.id" :approval="a" :item="items.find(item => item.id === a.params.itemId)" :busy="working" @decide="respond" />
          <p v-if="active" class="wb-meta my-2" role="status">{{ t(pending.length ? 'awaiting_approval' : 'agent_running') }}</p>
        </section>
        <form class="border-t border-line p-4 sm:p-5 dark:border-[#2A2A32]" @submit.prevent="send">
          <textarea v-model="prompt" rows="3" :disabled="working" :aria-label="t('message_prompt')" :placeholder="t('message_prompt')"
            class="g-input w-full resize-y" @keydown.ctrl.enter.prevent="send" @keydown.meta.enter.prevent="send" />
          <div class="mt-3 flex flex-wrap items-center justify-between gap-2">
            <select v-model="model" class="g-input max-w-full" :disabled="working || active" :aria-label="t('model')">
              <option value="">{{ t('default_model') }}</option>
              <option v-for="m in models" :key="m.id" :value="m.model">{{ m.displayName || m.model }}</option>
            </select>
            <button v-if="active" class="g-btn-danger" type="button" :disabled="working || !status.activeTurn" @click="interrupt">{{ t('interrupt') }}</button>
            <button v-else-if="status.activeThread" class="g-btn-secondary" type="button" :disabled="working" @click="selectChat(status.activeThread)">{{ t('show_active_chat') }}</button>
            <button v-else class="g-btn-primary" type="submit" :disabled="!canSend">{{ t('send') }} ↑</button>
          </div>
        </form>
      </div>
    </template>
  </div>
</template>

<style>
.wb-bar { display:flex; flex-wrap:wrap; align-items:end; gap:.75rem; padding:.9rem 1.2rem; border-bottom:1px solid var(--color-line); }
.wb-meta { font-size:.8125rem; opacity:.74; }
.wb-transcript { min-height:12rem; max-height:52dvh; overflow:auto; overscroll-behavior:contain; padding:1.25rem; }
.wb-empty { padding:2rem .5rem; }
.wb-item,.wb-approval { margin:0 0 1.25rem; overflow-wrap:anywhere; user-select:text; }
.wb-item>h3,.wb-approval>h3 { margin-bottom:.35rem; font-weight:600; }
.wb-user { background:rgba(128,128,128,.09); border-radius:.75rem; padding:.75rem 1rem; }
.wb-approval { border:1px solid #a37a35; border-radius:.75rem; padding:1rem; }
.wb-approval>*+* { margin-top:.5rem; }
.wb details { margin:.45rem 0; font-size:.875rem; }
.wb summary { cursor:pointer; }
.wb pre { white-space:pre-wrap; overflow-wrap:anywhere; max-height:16rem; overflow:auto; font-size:.825rem; padding:.6rem; border-radius:.4rem; background:rgba(128,128,128,.08); }
.dark .wb-bar { border-color:#2A2A32; }
</style>
