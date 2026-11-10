import { request, runtimeBase } from './index'

export interface Project { id: string; name: string; path: string }
export interface Chat { id: string; projectId: string; title: string; created: string }
export interface ChatItem {
  id: string
  type: string
  text?: string
  command?: string
  aggregatedOutput?: string
  content?: { text?: string }[]
  changes?: unknown
  error?: unknown
  status?: string
}
export interface Approval {
  id: string
  method: string
  params: {
    threadId: string
    itemId?: string
    command?: string
    cwd?: string
    reason?: string
    permissions?: unknown
    availableDecisions?: unknown[]
    questions?: { id: string; question: string; isSecret?: boolean; options?: { label: string; description: string }[] }[]
  }
}
export interface DeviceLogin { type: 'chatgptDeviceCode'; loginId: string; verificationUrl: string; userCode: string }
export interface BrowserLogin { type: 'chatgpt'; loginId: string; authUrl: string }
export type PendingLogin = DeviceLogin | BrowserLogin
interface AccountState {
  account?: { type: string; email?: string } | null
  requiresOpenaiAuth?: boolean
  pendingLogin?: PendingLogin | null
  loginError?: string
}
export interface WorkbenchStatus { ready: boolean; error?: string; activeThread: string; activeTurn: string }
const prefix = '/api/workbench/'
function get<T>(path: string) { return request<T>(prefix + path) }
function post<T>(path: string, body: unknown) { return request<T>(prefix + path, 'POST', body) }
export const workbench = {
  status: () => get<WorkbenchStatus>('status'),
  projects: () => get<Project[]>('projects'),
  addProject: (name: string, path: string) => post<Project>('projects', { name, path }),
  chats: () => get<Chat[]>('threads'),
  createChat: (projectId: string, model: string) => post<Chat>('threads', { projectId, model }),
  history: (id: string, cursor?: string | null) => get<{ items: ChatItem[]; nextCursor: string | null }>(
    'thread?id=' + encodeURIComponent(id) + (cursor ? '&cursor=' + encodeURIComponent(cursor) : '')
  ),
  send: (threadId: string, text: string, model: string, requestId: string) => post('turn', { threadId, text, model, requestId }),
  interrupt: () => post('interrupt', {}),
  approvals: () => get<Approval[]>('approvals'),
  decide: (id: string, decision: string, answers?: Record<string, { answers: string[] }>) => post('approvals', { id, decision, answers }),
  account: () => get<AccountState>('account'),
  loginWithBrowser: () => post<BrowserLogin>('account/login', { type: 'chatgpt' }),
  completeBrowserLogin: (loginId: string, callbackUrl: string) =>
    post<{ ok: boolean }>('account/login/callback', { loginId, callbackUrl }),
  cancelLogin: (loginId: string) => post<{ ok: boolean }>('account/login/cancel', { loginId }),
  loginWithDevice: () => post<DeviceLogin>('account/login', { type: 'chatgptDeviceCode' }),
  loginWithApiKey: (apiKey: string) => post<{ type: 'apiKey' }>('account/login', { type: 'apiKey', apiKey }),
  logout: () => post('account/logout', {}),
  models: () => get<{ data: { id: string; model: string; displayName?: string }[] }>('models'),
  events: () => new EventSource(runtimeBase() + prefix + 'events'),
}
