import type { TerminalMode } from '@/serverapi'

interface TerminalLaunch { mode: TerminalMode; workspace: string }

// Only an explicit user action creates a process. Reloaded links can reattach by ID.
let pending: TerminalLaunch | null = null

export function requestLaunch(workspace: string, mode: TerminalMode) {
  pending = { workspace, mode }
}

export function takeLaunch(): TerminalLaunch | null {
  const launch = pending
  pending = null
  return launch
}
