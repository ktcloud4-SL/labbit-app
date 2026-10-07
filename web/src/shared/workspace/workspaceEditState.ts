export interface WorkspaceEditState {
  dirty: boolean
  savePending: boolean
}

const EMPTY_STATE: WorkspaceEditState = {
  dirty: false,
  savePending: false,
}

let state = EMPTY_STATE
const listeners = new Set<() => void>()

export function getWorkspaceEditState() {
  return state
}

export function subscribeWorkspaceEditState(listener: () => void) {
  listeners.add(listener)
  return () => listeners.delete(listener)
}

export function setWorkspaceEditState(next: WorkspaceEditState) {
  if (state.dirty === next.dirty && state.savePending === next.savePending) {
    return
  }

  state = next
  listeners.forEach((listener) => listener())
}

export function clearWorkspaceEditState() {
  setWorkspaceEditState(EMPTY_STATE)
}
