import { computed, onMounted, onScopeDispose, ref, shallowRef } from 'vue'
import { api, type Workspace } from '@/serverapi'
import { workbench as wb, type Approval, type Chat, type ChatItem, type PendingLogin, type Project, type WorkbenchStatus } from '@/serverapi/workbench'
import { uiErrText, useI18n } from './useI18n'
import { randomId } from '@/utils/randomId'

const storageKey = 'codex-workbench-thread'
function rememberedChat() {
  try { return localStorage.getItem(storageKey) || '' } catch { return '' }
}

export function useWorkbench() {
  const { t } = useI18n()
  const installed = ref<boolean | null>(null)
  const status = ref<WorkbenchStatus>({ ready: false, activeThread: '', activeTurn: '' })
  const error = ref('')
  const refreshError = ref('')
  const working = ref(false)
  const workspaces = ref<Workspace[]>([])
  const projects = ref<Project[]>([])
  const chats = ref<Chat[]>([])
  const selected = ref(rememberedChat())
  const workspace = ref('')
  const model = ref('')
  const models = ref<Awaited<ReturnType<typeof wb.models>>['data']>([])
  const account = ref<Awaited<ReturnType<typeof wb.account>> | null>(null)
  const login = ref<PendingLogin | null>(null)
  const submittedLoginId = ref('')
  const signInError = shallowRef<unknown>(null)
  const items = ref<ChatItem[]>([])
  const cursor = ref<string | null>(null)
  const approvals = ref<Approval[]>([])
  const prompt = ref('')
  const signedIn = computed(() => !!account.value?.account || account.value?.requiresOpenaiAuth === false)
  const current = computed(() => chats.value.find(chat => chat.id === selected.value))
  const pending = computed(() => approvals.value.filter(entry => entry.params.threadId === selected.value))
  let submission: { id: string; text: string; thread: string; model: string } | null = null
  let stream: EventSource | null = null
  let poll: number | undefined
  let debounce: number | undefined
  let disposed = false
  let refreshing = false
  let queued = false
  let historyGeneration = 0
  let accountGeneration = 0
  let gotOlder = false
  let reloadContext = true

  function changeChat(id: string) {
    selected.value = id
    try { localStorage.setItem(storageKey, id) } catch { /* Storage is optional. */ }
    historyGeneration++
    items.value = []
    cursor.value = null
    gotOlder = false
  }

  async function readHistory(older = false) {
    const id = selected.value
    const generation = historyGeneration
    if (!id || (older && !cursor.value)) return
    const page = await wb.history(id, older ? cursor.value : null)
    if (generation !== historyGeneration || disposed) return
    const ordered = older ? [...page.items, ...items.value] : [...items.value, ...page.items]
    items.value = [...new Map(ordered.map(item => [item.id, item])).values()]
    if (older) gotOlder = true
    if (older || !gotOlder) cursor.value = page.nextCursor
  }

  async function refresh() {
    if (disposed || !installed.value) return
    if (refreshing) { queued = true; return }
    refreshing = true
    try {
      const state = await wb.status()
      status.value = state
      if (!state.ready || disposed) {
        reloadContext = true
        refreshError.value = state.error || ''
        return
      }
      const initialSelection = selected.value
      const [list, projectList, waiting] = await Promise.all([wb.chats(), wb.projects(), wb.approvals()])
      if (initialSelection !== selected.value) { queued = true; return }
      chats.value = list || []
      projects.value = projectList || []
      approvals.value = waiting || []
      if (selected.value && !chats.value.some(chat => chat.id === selected.value)) changeChat('')
      if (selected.value) await readHistory()
      if (!stream && !disposed) {
        stream = wb.events()
        stream.onmessage = () => {
          if (debounce === undefined) debounce = window.setTimeout(() => { debounce = undefined; void refresh() }, 250)
        }
      }
      if (reloadContext) {
        reloadContext = false
        await Promise.all([
          api.workspaces().then(result => {
            workspaces.value = result.workspaces || []
            if (!workspaces.value.some(w => w.path === workspace.value)) workspace.value = workspaces.value[0]?.path || ''
          }),
          refreshAccount(),
          wb.models().then(result => { models.value = result.data || [] }),
        ])
      }
      refreshError.value = ''
    } catch (err) { refreshError.value = uiErrText(err) }
    finally {
      refreshing = false
      if (queued && !disposed) { queued = false; void refresh() }
    }
  }

  async function refreshAccount() {
    const generation = ++accountGeneration
    const result = await wb.account()
    if (disposed || generation !== accountGeneration) return
    account.value = result
    login.value = result.account ? null : result.pendingLogin || null
    if (signedIn.value) signInError.value = null
    else if (result.loginError) signInError.value = { code: result.loginError }
  }

  async function action(fn: () => Promise<void>) {
    if (working.value) return
    working.value = true
    error.value = ''
    try { await fn() } catch (err) { error.value = uiErrText(err) }
    finally { working.value = false }
  }

  async function initialize() {
    await action(async () => {
      installed.value = (await api.status()).installed
      if (!installed.value) return
      reloadContext = true
      await refresh()
    })
  }

  async function selectChat(id: string) {
    if (working.value) return
    changeChat(id)
    prompt.value = ''
    submission = null
    await action(() => readHistory())
  }

  async function send() {
    if (!prompt.value.trim() || !signedIn.value || status.value.activeThread || !status.value.ready) return
    await action(async () => {
      if (!selected.value) {
        const dir = workspaces.value.find(w => w.path === workspace.value)
        if (!dir) throw new Error(t('please_select_workspace'))
        const project = projects.value.find(p => p.path === dir.path) || await wb.addProject(dir.name, dir.path)
        const chat = await wb.createChat(project.id, model.value)
        if (!projects.value.some(p => p.id === project.id)) projects.value.push(project)
        if (!chats.value.some(c => c.id === chat.id)) chats.value.push(chat)
        changeChat(chat.id)
      }
      if (!submission || submission.text !== prompt.value || submission.thread !== selected.value || submission.model !== model.value) {
        submission = { id: randomId(), text: prompt.value, model: model.value, thread: selected.value }
      }
      const entry = submission
      await wb.send(entry.thread, entry.text, entry.model, entry.id)
      prompt.value = ''
      submission = null
      await refresh()
    })
  }

  async function changeLogin(change: () => Promise<void>) {
    await action(async () => {
      ++accountGeneration
      signInError.value = null
      try {
        await change()
      } catch (err) {
        // A new attempt can replace an older one before the start or callback fails.
        await refreshAccount().catch(() => {})
        if (!signedIn.value) signInError.value = err
        return
      }
      await refreshAccount()
    })
  }
  function startLogin(type: PendingLogin['type']) {
    return changeLogin(async () => {
      const previous = login.value
      if (type === 'chatgpt' && previous?.type === 'chatgpt') {
        await wb.cancelLogin(previous.loginId)
        login.value = null
      }
      submittedLoginId.value = ''
      const result = type === 'chatgpt' ? await wb.loginWithBrowser() : await wb.loginWithDevice()
      if (result.type === 'chatgpt') {
        if (!result.loginId || !result.authUrl) throw new Error(t('err_browser_login'))
      } else if (!result.userCode || !result.verificationUrl) {
        throw new Error(t('account_signin_failed'))
      }
      login.value = result
    })
  }
  function completeLogin(loginId: string, callbackUrl: string) {
    return changeLogin(async () => {
      await wb.completeBrowserLogin(loginId, callbackUrl)
      submittedLoginId.value = loginId
    })
  }
  function cancelLogin(loginId: string) {
    return changeLogin(async () => { await wb.cancelLogin(loginId); login.value = null })
  }
  function signInWithKey(apiKey: string) {
    return changeLogin(async () => { await wb.loginWithApiKey(apiKey); login.value = null })
  }
  function signOut() {
    return action(async () => { await wb.logout(); login.value = null; await refreshAccount() })
  }
  async function respond(id: string, decision: string, answers?: Record<string, { answers: string[] }>) {
    await action(async () => { await wb.decide(id, decision, answers); await refresh() })
  }
  async function interrupt() {
    await action(async () => { await wb.interrupt(); await refresh() })
  }

  onMounted(() => {
    void initialize()
    poll = window.setInterval(() => {
      void refresh()
      if (login.value && status.value.ready && !working.value) void refreshAccount().catch(err => { error.value = uiErrText(err) })
    }, 4000)
  })
  onScopeDispose(() => {
    disposed = true
    stream?.close()
    window.clearInterval(poll)
    window.clearTimeout(debounce)
  })
  return { installed, status, error, refreshError, working, workspaces, projects, chats, selected, workspace, model, models,
    account, login, submittedLoginId, signInError, items, cursor, prompt, signedIn, current, pending, initialize, selectChat, send,
    startLogin, completeLogin, cancelLogin, signInWithKey, signOut,
    respond, interrupt, older: () => action(() => readHistory(true)) }
}
