// Resolve relative to the mount injected by the Go server, including on fnOS.
export function runtimeBase(): string {
  return new URL(document.baseURI).pathname.replace(/\/$/, '')
}

export async function request<T>(path: string, method = 'GET', payload?: unknown): Promise<T> {
  const response = await fetch(runtimeBase() + path, {
    method,
    body: payload === undefined ? undefined : JSON.stringify(payload),
    headers: { Accept: 'application/json', ...(method === 'GET' ? {} : { 'Content-Type': 'application/json' }) },
  })
  const body = await response.json().catch(() => null)
  if (!response.ok || !body) {
    throw Object.assign(new Error(body?.detail || body?.error || body?.message || `HTTP ${response.status}`), {
      code: body?.code, params: body?.params,
    })
  }
  return body as T
}

export type TerminalMode = 'codex' | 'login'
export interface CodexStatus {
  installed: boolean
  version: string
  source: '' | 'managed' | 'path'
  systemPath?: string
  installing: boolean
  error: string
}
export interface Workspace { path: string; name: string }
export interface CodexInstallation {
  version: string
  path: string
}
export interface SystemCodexInstallation extends CodexInstallation {
  kind: 'npm' | 'pnpm' | 'binary'
  selectable: boolean
}
export interface LocalCodexVersions {
  managed: CodexInstallation[]
  system: SystemCodexInstallation[]
  error?: string
}
export interface CodexVersions {
  latest: string
  versions: string[]
  installed: string[]
  error?: string
}
export interface SessionInfo {
  id: string
  workspace: string
  mode: TerminalMode
  created: string
  exited: boolean
}
export const api = {
  status: () => request<CodexStatus>('/api/codex/status'),
  versions: () => request<CodexVersions>('/api/codex/versions'),
  localVersions: (refresh = false) =>
    request<LocalCodexVersions>(`/api/codex/local-versions${refresh ? '?refresh=1' : ''}`),
  install: (version: string, systemPath?: string) => request('/api/codex/install', 'POST', { version, systemPath }),
  workspaces: () => request<{ workspaces: Workspace[]; fnos: boolean; appName: string }>('/api/workspaces'),
  sessions: () => request<{ sessions: SessionInfo[] }>('/api/sessions'),
  endSession: (id: string) => request('/api/session?id=' + encodeURIComponent(id), 'DELETE'),
  clearSessionHistory: (id: string) => request('/api/session/clear?id=' + encodeURIComponent(id), 'POST'),
}
