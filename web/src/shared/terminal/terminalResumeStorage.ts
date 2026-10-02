export interface TerminalResumeState {
  labInstanceId: string
  generation: number
  terminalSessionId: string
  sessionToken: string
  tokenExpiresAt: string
}

const STORAGE_KEY = 'labbit.terminal.resume.v1'

function isResumeState(value: unknown): value is TerminalResumeState {
  if (!value || typeof value !== 'object') return false

  const state = value as Record<string, unknown>
  return (
    typeof state.labInstanceId === 'string' &&
    state.labInstanceId.length > 0 &&
    typeof state.generation === 'number' &&
    Number.isInteger(state.generation) &&
    state.generation > 0 &&
    typeof state.terminalSessionId === 'string' &&
    state.terminalSessionId.length > 0 &&
    typeof state.sessionToken === 'string' &&
    state.sessionToken.length > 0 &&
    typeof state.tokenExpiresAt === 'string' &&
    state.tokenExpiresAt.length > 0
  )
}

export function saveTerminalResumeState(state: TerminalResumeState) {
  window.sessionStorage.setItem(STORAGE_KEY, JSON.stringify(state))
}

export function clearTerminalResumeState() {
  window.sessionStorage.removeItem(STORAGE_KEY)
}

export function readTerminalResumeState(
  expected: Pick<TerminalResumeState, 'labInstanceId' | 'generation'>,
  now = Date.now(),
): TerminalResumeState | null {
  const raw = window.sessionStorage.getItem(STORAGE_KEY)
  if (!raw) return null

  let parsed: unknown
  try {
    parsed = JSON.parse(raw)
  } catch {
    clearTerminalResumeState()
    return null
  }

  if (!isResumeState(parsed)) {
    clearTerminalResumeState()
    return null
  }

  if (
    parsed.labInstanceId !== expected.labInstanceId ||
    parsed.generation !== expected.generation
  ) {
    clearTerminalResumeState()
    return null
  }

  const expiresAt = Date.parse(parsed.tokenExpiresAt)
  if (!Number.isFinite(expiresAt) || expiresAt <= now) {
    clearTerminalResumeState()
    return null
  }

  return parsed
}
