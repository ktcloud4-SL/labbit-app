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

let terminalResizeHandler: ((size: { cols: number; rows: number }) => void) | null = null

vi.mock('@xterm/xterm', () => ({
  Terminal: class {
    cols = 100
    rows = 24
    loadAddon() {}
    open() {}
    onData() { return { dispose() {} } }
    onBinary() { return { dispose() {} } }
    onResize(handler: (size: { cols: number; rows: number }) => void) {
      terminalResizeHandler = handler
      return { dispose() { if (terminalResizeHandler === handler) terminalResizeHandler = null } }
    }
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
    closeTerminalSession: vi.fn(async () => undefined),
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
  terminalResizeHandler = null
  vi.unstubAllGlobals()
})

describe('TerminalPanel HTTP 503 feedback', () => {
  it.each([
    {
      code: 'connector_unavailable',
      message: '실습 환경 Connector에 연결할 수 없습니다. 연결 상태를 확인한 뒤 다시 시도해 주세요.',
    },
    {
      code: 'terminal_open_failed',
      message: 'VM 터미널을 열지 못했습니다. 계속 발생하면 운영 담당자에게 문의해 주세요.',
    },
    {
      code: 'terminal_unavailable',
      message: '현재 터미널 기능을 사용할 수 없습니다. 계속 발생하면 운영 담당자에게 문의해 주세요.',
    },
    {
      code: 'unrecognized_code',
      message: '터미널 연결 경로를 지금 사용할 수 없습니다. 잠시 후 다시 시도해 주세요.',
    },
    {
      code: '',
      message: '터미널 연결 경로를 지금 사용할 수 없습니다. 잠시 후 다시 시도해 주세요.',
    },
  ])('shows safe guidance for 503 / $code without attaching or persisting a session', async ({ code, message }) => {
    const { pending, api, start } = setup()
    await start()
    await act(async () => {
      pending.reject(new HttpError(503, code
        ? { type: 'about:blank', title: 'Service unavailable', status: 503, code, requestId: 'fixture-request' }
        : undefined))
    })
    expect(await screen.findByText(message)).toBeInTheDocument()
    expect(screen.getByRole('button', { name: '새 터미널 열기' })).toBeEnabled()
    expect(api.createTerminalSession).toHaveBeenCalledOnce()
    expect(FakeWebSocket.instances).toHaveLength(0)
    expect(readTerminalResumeState({ labInstanceId: 'synthetic-lab', generation: 1 })).toBeNull()
  })
})

describe('TerminalPanel create lifecycle', () => {
  it('does not restore cleared resume state after unmount', async () => {
    const { pending, api, view, start } = setup()
    await start()
    view.unmount()
    clearTerminalResumeState()
    await act(async () => { pending.resolve(session()) })
    expect(readTerminalResumeState({ labInstanceId: 'synthetic-lab', generation: 1 })).toBeNull()
    expect(FakeWebSocket.instances).toHaveLength(0)
    expect(api.closeTerminalSession).toHaveBeenCalledExactlyOnceWith('synthetic-session-1')
  })

  it.each(['success', 'failure'] as const)('ignores late %s after a generation change', async (outcome) => {
    const { pending, api, queryClient, panel, view, start } = setup()
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
    if (outcome === 'success') {
      expect(api.closeTerminalSession).toHaveBeenCalledExactlyOnceWith('synthetic-session-1')
    } else {
      expect(api.closeTerminalSession).not.toHaveBeenCalled()
    }
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
    if (outcome === 'success') {
      expect(old.api.closeTerminalSession).toHaveBeenCalledExactlyOnceWith('synthetic-session-1')
    } else {
      expect(old.api.closeTerminalSession).not.toHaveBeenCalled()
    }
    expect(current.api.closeTerminalSession).not.toHaveBeenCalled()
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
    if (outcome === 'success') {
      expect(api.closeTerminalSession).toHaveBeenCalledExactlyOnceWith('synthetic-session-1')
    } else {
      expect(api.closeTerminalSession).not.toHaveBeenCalled()
    }
  })

  it('closes a successful TerminalSession if the server returned an unexpected generation', async () => {
    const { pending, api, start } = setup()
    await start()
    await act(async () => { pending.resolve(session(2)) })
    expect(screen.getByText(/generation이 변경되었습니다/)).toBeInTheDocument()
    expect(api.closeTerminalSession).toHaveBeenCalledExactlyOnceWith('synthetic-session-2')
    expect(readTerminalResumeState({ labInstanceId: 'synthetic-lab', generation: 1 })).toBeNull()
    expect(FakeWebSocket.instances).toHaveLength(0)
  })

  it('does not fail or expose a stale session when best-effort server cleanup rejects', async () => {
    const { pending, api, view, start } = setup()
    vi.mocked(api.closeTerminalSession).mockRejectedValue(new HttpError(404))
    await start()
    view.unmount()
    await act(async () => { pending.resolve(session()) })
    expect(api.closeTerminalSession).toHaveBeenCalledExactlyOnceWith('synthetic-session-1')
    expect(readTerminalResumeState({ labInstanceId: 'synthetic-lab', generation: 1 })).toBeNull()
    expect(FakeWebSocket.instances).toHaveLength(0)
  })

  it('sends TERMINAL_RESIZE on the attached WSS without creating another TerminalSession', async () => {
    const { pending, start, api } = setup()
    await start()
    await act(async () => { pending.resolve(session()) })
    await waitFor(() => expect(FakeWebSocket.instances).toHaveLength(1))
    const socket = FakeWebSocket.instances[0]
    await act(async () => { socket.attach() })
    expect(screen.getByText('연결됨')).toBeInTheDocument()

    await act(async () => { terminalResizeHandler?.({ cols: 120, rows: 42 }) })
    expect(JSON.parse(socket.sent.at(-1) ?? '{}')).toMatchObject({
      type: 'TERMINAL_RESIZE',
      terminalSessionId: 'synthetic-session-1',
      payload: { cols: 120, rows: 42 },
    })
    expect(api.createTerminalSession).toHaveBeenCalledTimes(1)
    expect(FakeWebSocket.instances).toHaveLength(1)
  })

  it('creates and attaches normally under StrictMode', async () => {
    const { pending, start, queryClient } = setup()
    await start()
    await act(async () => { pending.resolve(session()) })
    await waitFor(() => expect(FakeWebSocket.instances).toHaveLength(1))
    await act(async () => { FakeWebSocket.instances[0].attach() })
    expect(screen.getByText('연결됨')).toBeInTheDocument()
    expect(JSON.parse(FakeWebSocket.instances[0].sent[0]).type).toBe('TERMINAL_ATTACH')
    expect(readTerminalResumeState({ labInstanceId: 'synthetic-lab', generation: 1 })?.terminalSessionId).toBe('synthetic-session-1')
    // The session token is needed for reconnect, not for Query's mutation cache.
    expect(queryClient.getMutationCache().getAll()).toHaveLength(0)
  })
})
