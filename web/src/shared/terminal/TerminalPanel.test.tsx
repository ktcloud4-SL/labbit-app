import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { act, cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react'
import { StrictMode } from 'react'
import { MemoryRouter } from 'react-router-dom'
import { afterEach, describe, expect, it, vi } from 'vitest'

import type { TerminalSession } from '../api/contracts'
import { HttpError } from '../api/httpClient'
import { LabbitApiProvider } from '../api/LabbitApiProvider'
import { labbitQueryKeys, type LabbitApi } from '../api/labbitApi'
import { mockLabbitApi } from '../api/mockLabbitApi'
import { TerminalPanel } from './TerminalPanel'
import { clearTerminalResumeState, readTerminalResumeState, saveTerminalResumeState } from './terminalResumeStorage'

vi.mock('@xterm/xterm', () => ({
  Terminal: class {
    cols = 100
    rows = 24
    loadAddon() {}
    open() {}
    onData() { return { dispose() {} } }
    onBinary() { return { dispose() {} } }
    onResize() { return { dispose() {} } }
    reset() {}
    clear() {}
    focus() {}
    dispose() {}
  },
}))
vi.mock('@xterm/addon-fit', () => ({ FitAddon: class { fit() {} } }))

class FakeWebSocket extends EventTarget {
  static instances: FakeWebSocket[] = []
  static OPEN = 1
  readyState = 0
  binaryType = 'blob'
  sent: string[] = []
  constructor() {
    super()
    FakeWebSocket.instances.push(this)
  }
  send(data: string) { this.sent.push(data) }
  close() { this.readyState = 3 }
  attach() {
    this.readyState = 1
    this.dispatchEvent(new Event('open'))
    const attach = JSON.parse(this.sent[0]) as {
      terminalSessionId: string
      messageId: string
    }
    this.dispatchEvent(new MessageEvent('message', {
      data: JSON.stringify({
        type: 'TERMINAL_ATTACHED',
        terminalSessionId: attach.terminalSessionId,
        replyToMessageId: attach.messageId,
        payload: { resumed: false, historyAvailable: false },
      }),
    }))
  }
}

function deferred<T>() {
  let resolve!: (value: T) => void
  let reject!: (reason: unknown) => void
  const promise = new Promise<T>((res, rej) => { resolve = res; reject = rej })
  return { promise, resolve, reject }
}

function session(generation = 1): TerminalSession {
  return {
    id: `synthetic-session-${generation}`,
    generation,
    sessionToken: 'synthetic-test-token',
    tokenExpiresAt: new Date(Date.now() + 60_000).toISOString(),
  }
}

function setup() {
  vi.stubGlobal('WebSocket', FakeWebSocket)
  const pending = deferred<TerminalSession>()
  const api: LabbitApi = {
    ...mockLabbitApi,
    listTerminalTargets: vi.fn(async () => ({
      generation: 1, workspaceVmKey: 'synthetic-vm',
      items: [{ vmKey: 'synthetic-vm', role: 'workspace', instanceIndex: 0 }],
    })),
    createTerminalSession: vi.fn(() => pending.promise),
  }
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  function panel(generation = 1) {
    return <StrictMode><MemoryRouter><QueryClientProvider client={queryClient}>
      <LabbitApiProvider api={api}>
        <TerminalPanel labInstanceId="synthetic-lab" generation={generation} />
      </LabbitApiProvider>
    </QueryClientProvider></MemoryRouter></StrictMode>
  }
  const view = render(panel())
  async function start() {
    const button = await screen.findByRole('button', { name: '터미널 연결' })
    await waitFor(() => expect(button).toBeEnabled())
    fireEvent.click(button)
    await waitFor(() => expect(api.createTerminalSession).toHaveBeenCalledOnce())
  }
  return { pending, api, queryClient, panel, view, start }
}

afterEach(() => {
  cleanup()
  clearTerminalResumeState()
  FakeWebSocket.instances = []
  vi.unstubAllGlobals()
})

describe('TerminalPanel create lifecycle', () => {
  it('does not restore cleared resume state after unmount', async () => {
    const { pending, view, start } = setup()
    await start()
    view.unmount()
    clearTerminalResumeState()
    await act(async () => { pending.resolve(session()) })
    expect(readTerminalResumeState({ labInstanceId: 'synthetic-lab', generation: 1 })).toBeNull()
    expect(FakeWebSocket.instances).toHaveLength(0)
  })

  it.each(['success', 'failure'] as const)('ignores late %s after a generation change', async (outcome) => {
    const { pending, queryClient, panel, view, start } = setup()
    await start()
    view.rerender(panel(2))
    const current = session(2)
    saveTerminalResumeState({ ...current, labInstanceId: 'synthetic-lab', terminalSessionId: current.id })
    await act(async () => {
      if (outcome === 'success') pending.resolve(session())
      else pending.reject(new Error('synthetic late failure'))
    })
    expect(readTerminalResumeState({ labInstanceId: 'synthetic-lab', generation: 2 })?.terminalSessionId).toBe(current.id)
    expect(FakeWebSocket.instances).toHaveLength(0)
    queryClient.clear()
  })

  it.each(['success', 'failure'] as const)('preserves a new panel session after an old request %s', async (outcome) => {
    const old = setup()
    await old.start()
    old.view.unmount()
    const current = setup()
    await current.start()
    const newSession = { ...session(), id: 'synthetic-new-session' }
    await act(async () => { current.pending.resolve(newSession) })
    await waitFor(() => expect(FakeWebSocket.instances).toHaveLength(1))
    await act(async () => { FakeWebSocket.instances[0].attach() })
    await act(async () => {
      if (outcome === 'success') old.pending.resolve(session())
      else old.pending.reject(new Error('synthetic late failure'))
    })
    expect(readTerminalResumeState({ labInstanceId: 'synthetic-lab', generation: 1 })?.terminalSessionId).toBe(newSession.id)
    expect(screen.getByText('연결됨')).toBeInTheDocument()
    expect(FakeWebSocket.instances).toHaveLength(1)
  })

  it.each(['success', 'failure'] as const)('ignores late %s after target authentication expires', async (outcome) => {
    const { pending, api, queryClient, start } = setup()
    await start()
    vi.mocked(api.listTerminalTargets).mockRejectedValue(new HttpError(401))
    await act(async () => {
      await queryClient.refetchQueries({ queryKey: labbitQueryKeys.terminalTargets('synthetic-lab', 1) })
    })
    await waitFor(() => expect(screen.queryByRole('button', { name: '연결 중...' })).not.toBeInTheDocument())
    await act(async () => {
      if (outcome === 'success') pending.resolve(session())
      else pending.reject(new Error('synthetic late failure'))
    })
    expect(readTerminalResumeState({ labInstanceId: 'synthetic-lab', generation: 1 })).toBeNull()
    expect(FakeWebSocket.instances).toHaveLength(0)
  })

  it('creates and attaches normally under StrictMode', async () => {
    const { pending, start } = setup()
    await start()
    await act(async () => { pending.resolve(session()) })
    await waitFor(() => expect(FakeWebSocket.instances).toHaveLength(1))
    await act(async () => { FakeWebSocket.instances[0].attach() })
    expect(screen.getByText('연결됨')).toBeInTheDocument()
    expect(JSON.parse(FakeWebSocket.instances[0].sent[0]).type).toBe('TERMINAL_ATTACH')
    expect(readTerminalResumeState({ labInstanceId: 'synthetic-lab', generation: 1 })?.terminalSessionId).toBe('synthetic-session-1')
  })
})
