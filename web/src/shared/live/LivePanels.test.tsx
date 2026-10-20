import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { act, cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react'
import { MemoryRouter } from 'react-router-dom'
import { afterEach, describe, expect, it, vi } from 'vitest'

import { LabbitApiProvider } from '../api/LabbitApiProvider'
import { mockLabbitApi } from '../api/mockLabbitApi'
import { HttpError } from '../api/httpClient'
import type { LabbitApi } from '../api/labbitApi'
import type { LiveSession } from '../api/contracts'
import { LiveInstructorControls } from './LiveInstructorControls'
import { LiveStudentPanel } from './LiveStudentPanel'

const mockTerminalWrite = vi.fn()
const mockTerminalOptions = vi.fn()
vi.mock('@xterm/xterm', () => ({
  Terminal: class {
    constructor(options: unknown) { mockTerminalOptions(options) }
    loadAddon() {}
    open() {}
    reset() {}
    write(value: Uint8Array) { mockTerminalWrite(value) }
    dispose() {}
  },
}))
vi.mock('@xterm/addon-fit', () => ({ FitAddon: class { fit() {} } }))

class FakeSocket extends EventTarget {
  static instances: FakeSocket[] = []
  static CLOSED = 3
  readyState = 0
  sent: string[] = []
  binaryType = 'blob'
  constructor() {
    super()
    FakeSocket.instances.push(this)
  }
  send(s: string) { this.sent.push(s) }
  close() { this.readyState = 3; this.dispatchEvent(new CloseEvent('close', { code: 1000 })) }
  open() { this.readyState = 1; this.dispatchEvent(new Event('open')) }
  message(data: unknown) { this.dispatchEvent(new MessageEvent('message', { data })) }
  ack(id = 'live-a') {
    const reply = JSON.parse(this.sent[0]) as { messageId: string }
    this.message(JSON.stringify({ type: 'LIVE_SUBSCRIBED', liveSessionId: id, replyToMessageId: reply.messageId, payload: { historyAvailable: false } }))
  }
}

const live: LiveSession = {
  id: 'live-a',
  classId: 'class-a',
  sourceTerminalSessionId: 'terminal-a',
  createdAt: '2026-10-20T00:00:00Z',
}
function mount(element: React.ReactElement, overrides: Partial<LabbitApi> = {}) {
  const api = { ...mockLabbitApi, ...overrides } satisfies LabbitApi
  const query = new QueryClient({ defaultOptions: { queries: { retry: false, gcTime: 0 } } })
  const view = render(<MemoryRouter><QueryClientProvider client={query}>
    <LabbitApiProvider api={api}>{element}</LabbitApiProvider>
  </QueryClientProvider></MemoryRouter>)
  return { api, query, view }
}

afterEach(() => {
  cleanup()
  FakeSocket.instances = []
  mockTerminalWrite.mockClear()
  mockTerminalOptions.mockClear()
  vi.unstubAllGlobals()
})

describe('LBT-26 instructor Live controls', () => {
  it('requires attached source Terminal and creates/closes Live by opaque ID without touching PTY', async () => {
    let active: LiveSession | null = null
    const getActiveLiveSession = vi.fn(async () => {
      if (!active) throw new HttpError(404)
      return active
    })
    const createLiveSession = vi.fn(async () => { active = live; return live })
    const closeLiveSession = vi.fn(async () => { active = null })
    const { view, api, query } = mount(<LiveInstructorControls classId="class-a" sourceTerminalSessionId={null} />,
      { getActiveLiveSession, createLiveSession, closeLiveSession })

    const start = await screen.findByRole('button', { name: '화면 공유 시작' })
    await waitFor(() => expect(start).toBeDisabled())
    view.rerender(<MemoryRouter><QueryClientProvider client={query}>
      <LabbitApiProvider api={api}>
        <LiveInstructorControls classId="class-a" sourceTerminalSessionId="terminal-a" />
      </LabbitApiProvider>
    </QueryClientProvider></MemoryRouter>)
    await waitFor(() => expect(screen.getByRole('button', { name: '화면 공유 시작' })).toBeEnabled())
    fireEvent.click(screen.getByRole('button', { name: '화면 공유 시작' }))
    await waitFor(() => expect(createLiveSession).toHaveBeenCalledWith('terminal-a'))
    await waitFor(() => expect(screen.getByRole('button', { name: '공유 종료' })).toBeEnabled())
    fireEvent.click(screen.getByRole('button', { name: '공유 종료' }))
    await waitFor(() => expect(closeLiveSession).toHaveBeenCalledWith('live-a'))
  })

  it('keeps a best-effort STOP after transient 503 without claiming an active status', async () => {
    let lookupFails = false
    const getActiveLiveSession = vi.fn(async () => {
      if (lookupFails) throw new HttpError(503)
      return live
    })
    const closeLiveSession = vi.fn(async () => {})
    const { query } = mount(
      <LiveInstructorControls classId="class-a" sourceTerminalSessionId="terminal-a" />,
      { getActiveLiveSession, closeLiveSession },
    )
    expect(await screen.findByRole('button', { name: '공유 종료' })).toBeEnabled()

    lookupFails = true
    await query.invalidateQueries({ queryKey: ['classes', 'class-a', 'live-session'] })
    expect(await screen.findByText(/공유 상태 확인 불가/)).toBeInTheDocument()
    expect(screen.queryByText('화면 공유 중')).not.toBeInTheDocument()
    expect(screen.queryByRole('button', { name: '화면 공유 시작' })).not.toBeInTheDocument()
    fireEvent.click(screen.getByRole('button', { name: '공유 종료' }))
    await waitFor(() => expect(closeLiveSession).toHaveBeenCalledWith('live-a'))
    await waitFor(() => expect(screen.queryByRole('button', { name: '공유 종료' })).not.toBeInTheDocument())
  })

  it.each([403, 404])('never exposes stale STOP after an authoritative %i lookup failure', async (status) => {
    let denied = false
    const { query } = mount(
      <LiveInstructorControls classId="class-a" sourceTerminalSessionId="terminal-a" />,
      {
        getActiveLiveSession: async () => {
          if (denied) throw new HttpError(status)
          return live
        },
      },
    )
    expect(await screen.findByRole('button', { name: '공유 종료' })).toBeEnabled()
    denied = true
    await query.invalidateQueries({ queryKey: ['classes', 'class-a', 'live-session'] })
    await waitFor(() => expect(screen.queryByRole('button', { name: '공유 종료' })).not.toBeInTheDocument())
    expect(screen.queryByText('공유 상태 확인 불가 · 마지막 세션 종료 가능')).not.toBeInTheDocument()
  })

  it.each<[number, '503' | 'network']>([
    [403, '503'],
    [404, '503'],
    [403, 'network'],
    [404, 'network'],
  ])('does not revive a rejected Live after %i then %s, including remount, until a fresh success', async (status, failure) => {
    let lookupFailure: Error | null = null
    const getActiveLiveSession = vi.fn(async () => {
      if (lookupFailure) throw lookupFailure
      return live
    })
    const closeLiveSession = vi.fn(async () => {})
    const { query, view, api } = mount(
      <LiveInstructorControls classId="class-a" sourceTerminalSessionId="terminal-a" />,
      { getActiveLiveSession, closeLiveSession },
    )
    const queryKey = ['classes', 'class-a', 'live-session']
    const refresh = () => act(async () => { await query.invalidateQueries({ queryKey }) })
    expect(await screen.findByRole('button', { name: '공유 종료' })).toBeEnabled()

    lookupFailure = new HttpError(status)
    await refresh()
    await waitFor(() => expect(screen.queryByRole('button', { name: '공유 종료' })).not.toBeInTheDocument())

    lookupFailure = failure === '503' ? new HttpError(503) : new TypeError('Network unavailable')
    await refresh()
    await waitFor(() => expect(screen.getByRole('alert')).toHaveTextContent(failure === '503'
      ? 'Live 서비스를 현재 사용할 수 없습니다.'
      : 'Live 요청을 처리하지 못했습니다. 다시 확인해 주세요.'))
    expect(screen.queryByRole('button', { name: '공유 종료' })).not.toBeInTheDocument()
    expect(screen.queryByText('공유 상태 확인 불가 · 마지막 세션 종료 가능')).not.toBeInTheDocument()
    expect(screen.getByRole('button', { name: '화면 공유 시작' })).toBeDisabled()
    expect(closeLiveSession).not.toHaveBeenCalled()

    // Retained Query data must not regain authority when the controls remount.
    const callsBeforeRemount = getActiveLiveSession.mock.calls.length
    view.rerender(<MemoryRouter><QueryClientProvider client={query}>
      <LabbitApiProvider api={api}>
        <LiveInstructorControls key="remounted" classId="class-a" sourceTerminalSessionId="terminal-a" />
      </LabbitApiProvider>
    </QueryClientProvider></MemoryRouter>)
    await waitFor(() => expect(getActiveLiveSession).toHaveBeenCalledTimes(callsBeforeRemount + 1))
    await waitFor(() => expect(query.getQueryState(queryKey)?.fetchStatus).toBe('idle'))
    expect(screen.queryByRole('button', { name: '공유 종료' })).not.toBeInTheDocument()

    // A new 200 may re-authorize even the same ID, including later best-effort STOP.
    lookupFailure = null
    await refresh()
    expect(await screen.findByRole('button', { name: '공유 종료' })).toBeEnabled()
    lookupFailure = new HttpError(503)
    await refresh()
    expect(await screen.findByText('공유 상태 확인 불가 · 마지막 세션 종료 가능')).toBeInTheDocument()
    fireEvent.click(screen.getByRole('button', { name: '공유 종료' }))
    await waitFor(() => expect(closeLiveSession).toHaveBeenCalledExactlyOnceWith('live-a'))
  })

  it.each([403, 404])('keeps a %i denial effective when subsequent failures are batched before render', async (status) => {
    let lookupFailure: Error | null = null
    const { query } = mount(
      <LiveInstructorControls classId="class-a" sourceTerminalSessionId="terminal-a" />,
      { getActiveLiveSession: async () => {
        if (lookupFailure) throw lookupFailure
        return live
      } },
    )
    expect(await screen.findByRole('button', { name: '공유 종료' })).toBeEnabled()
    await act(async () => {
      lookupFailure = new HttpError(status)
      await query.invalidateQueries()
      lookupFailure = new HttpError(503)
      await query.invalidateQueries()
    })
    await waitFor(() => expect(screen.getByRole('alert')).toHaveTextContent('Live 서비스를 현재 사용할 수 없습니다.'))
    expect(screen.queryByRole('button', { name: '공유 종료' })).not.toBeInTheDocument()
  })

  it('keeps a newly created Live stoppable when the first follow-up lookup is unavailable', async () => {
    let created = false
    const closeLiveSession = vi.fn(async () => {})
    mount(<LiveInstructorControls classId="class-a" sourceTerminalSessionId="terminal-a" />, {
      getActiveLiveSession: async () => { throw new HttpError(created ? 503 : 404) },
      createLiveSession: async () => { created = true; return live },
      closeLiveSession,
    })
    await waitFor(() => expect(screen.getByRole('button', { name: '화면 공유 시작' })).toBeEnabled())
    fireEvent.click(screen.getByRole('button', { name: '화면 공유 시작' }))
    expect(await screen.findByText('공유 상태 확인 불가 · 마지막 세션 종료 가능')).toBeInTheDocument()
    fireEvent.click(screen.getByRole('button', { name: '공유 종료' }))
    await waitFor(() => expect(closeLiveSession).toHaveBeenCalledExactlyOnceWith('live-a'))
  })

  it('ignores a cancelled lookup that resolves after a later authoritative denial', async () => {
    let lookup: 'success' | 'pending' | 'denied' | 'unavailable' = 'success'
    let resolveLookup!: (session: LiveSession) => void
    const { query } = mount(
      <LiveInstructorControls classId="class-a" sourceTerminalSessionId="terminal-a" />,
      { getActiveLiveSession: async () => {
        if (lookup === 'pending') return new Promise<LiveSession>(resolve => { resolveLookup = resolve })
        if (lookup === 'denied') throw new HttpError(403)
        if (lookup === 'unavailable') throw new HttpError(503)
        return live
      } },
    )
    expect(await screen.findByRole('button', { name: '공유 종료' })).toBeEnabled()
    lookup = 'pending'
    const pendingRefresh = query.invalidateQueries()
    await waitFor(() => expect(resolveLookup).toBeDefined())
    await act(async () => { await query.cancelQueries() })
    lookup = 'denied'
    await act(async () => { await query.invalidateQueries() })
    await waitFor(() => expect(screen.queryByRole('button', { name: '공유 종료' })).not.toBeInTheDocument())

    await act(async () => { resolveLookup(live); await pendingRefresh })
    lookup = 'unavailable'
    await act(async () => { await query.invalidateQueries() })
    await waitFor(() => expect(screen.getByRole('alert')).toHaveTextContent('Live 서비스를 현재 사용할 수 없습니다.'))
    expect(screen.queryByRole('button', { name: '공유 종료' })).not.toBeInTheDocument()
  })

  it('does not allow starting a new Live when Class already has an active one', async () => {
    const createLiveSession = vi.fn(async () => live)
    mount(<LiveInstructorControls classId="class-a" sourceTerminalSessionId="terminal-b" />,
      { getActiveLiveSession: async () => live, createLiveSession })
    expect(await screen.findByRole('button', { name: '공유 종료' })).toBeInTheDocument()
    expect(screen.queryByRole('button', { name: '화면 공유 시작' })).not.toBeInTheDocument()
    expect(createLiveSession).not.toHaveBeenCalled()
  })
})

describe('LBT-26 student read-only Class Live', () => {
  it('shows empty state for GET 404 without attempting WSS or creating a Workspace', async () => {
    vi.stubGlobal('WebSocket', FakeSocket)
    mount(<LiveStudentPanel classId="class-a" />, {
      getActiveLiveSession: async () => { throw new HttpError(404) },
    })
    expect(await screen.findByText(/진행 중인 강사 화면 공유가 없습니다/)).toBeInTheDocument()
    expect(FakeSocket.instances).toHaveLength(0)
  })

  it('uses read-only xterm with no student INPUT, receives only subscribed OUTPUT', async () => {
    vi.stubGlobal('WebSocket', FakeSocket)
    mount(<LiveStudentPanel classId="class-a" />, { getActiveLiveSession: async () => live })
    await waitFor(() => expect(FakeSocket.instances).toHaveLength(1))
    const ws = FakeSocket.instances[0]
    ws.open()
    expect(JSON.parse(ws.sent[0])).toMatchObject({ type: 'LIVE_SUBSCRIBE', liveSessionId: 'live-a', payload: {} })
    ws.message(new Uint8Array([65]).buffer)
    expect(mockTerminalWrite).not.toHaveBeenCalled()
    ws.ack()
    ws.message(new Uint8Array([66]).buffer)
    await waitFor(() => expect(mockTerminalWrite).toHaveBeenCalledOnce())
    expect(mockTerminalOptions).toHaveBeenCalledWith(expect.objectContaining({ disableStdin: true, scrollback: 0 }))
    expect(ws.sent).toHaveLength(1)
    expect(screen.getByText(/입력·크기 변경·강사 Terminal 종료는 지원하지 않습니다/)).toBeInTheDocument()
  })

  it('removes a formerly active student Live on later 404 rather than showing stale WSS', async () => {
    vi.stubGlobal('WebSocket', FakeSocket)
    let exists = true
    const { query } = mount(<LiveStudentPanel classId="class-a" />, {
      getActiveLiveSession: async () => {
        if (!exists) throw new HttpError(404)
        return live
      },
    })
    await waitFor(() => expect(FakeSocket.instances).toHaveLength(1))
    exists = false
    await query.invalidateQueries()
    expect(await screen.findByText(/진행 중인 강사 화면 공유가 없습니다/)).toBeInTheDocument()
    expect(screen.queryByLabelText('강사 화면 읽기 전용 출력')).not.toBeInTheDocument()
  })

  it.each(['503', 'network'] as const)(
    'preserves subscribed student Live WSS and receives output after transient %s refetch failure',
    async (failure) => {
      vi.stubGlobal('WebSocket', FakeSocket)
      let unavailable = false
      const { query } = mount(<LiveStudentPanel classId="class-a" />, {
        getActiveLiveSession: async () => {
          if (unavailable) {
            if (failure === '503') throw new HttpError(503)
            throw new TypeError('Network offline')
          }
          return live
        },
      })
      await waitFor(() => expect(FakeSocket.instances).toHaveLength(1))
      const ws = FakeSocket.instances[0]
      ws.open()
      ws.ack()
      expect(await screen.findByText('읽기 전용 연결됨')).toBeInTheDocument()
      const viewer = screen.getByLabelText('강사 화면 읽기 전용 출력')

      unavailable = true
      await act(async () => { await query.invalidateQueries() })
      expect(await screen.findByRole('alert')).toHaveTextContent('기존 읽기 전용 연결을 유지합니다.')
      expect(screen.getByLabelText('강사 화면 읽기 전용 출력')).toBe(viewer)
      expect(FakeSocket.instances).toHaveLength(1)
      expect(ws.readyState).toBe(1)

      ws.message(new Uint8Array([77]).buffer)
      await waitFor(() => expect(mockTerminalWrite).toHaveBeenCalledOnce())
      expect(mockTerminalWrite).toHaveBeenCalledWith(new Uint8Array([77]))
    },
  )

  it.each([403, 404])(
    'disconnects student Live after %i and never revives stale Viewer on a following 503',
    async (status) => {
      vi.stubGlobal('WebSocket', FakeSocket)
      let failure: Error | null = null
      const { query } = mount(<LiveStudentPanel classId="class-a" />, {
        getActiveLiveSession: async () => {
          if (failure) throw failure
          return live
        },
      })
      await waitFor(() => expect(FakeSocket.instances).toHaveLength(1))
      const ws = FakeSocket.instances[0]
      ws.open()
      ws.ack()
      expect(await screen.findByText('읽기 전용 연결됨')).toBeInTheDocument()

      failure = new HttpError(status)
      await act(async () => { await query.invalidateQueries() })
      await waitFor(() => expect(screen.queryByLabelText('강사 화면 읽기 전용 출력')).not.toBeInTheDocument())
      expect(ws.readyState).toBe(3)

      failure = new HttpError(503)
      await act(async () => { await query.invalidateQueries() })
      expect(screen.queryByLabelText('강사 화면 읽기 전용 출력')).not.toBeInTheDocument()
      expect(FakeSocket.instances).toHaveLength(1)
      expect(screen.queryByText('읽기 전용 연결됨')).not.toBeInTheDocument()

      // A new authorized GET 200, not a transient error or cached data, can
      // establish a fresh blank/no-history subscription.
      failure = null
      await act(async () => { await query.invalidateQueries() })
      await waitFor(() => expect(FakeSocket.instances).toHaveLength(2))
      expect(FakeSocket.instances[1]).not.toBe(ws)
    },
  )

  it('does not revive student Live if 403 and 503 complete before React renders', async () => {
    vi.stubGlobal('WebSocket', FakeSocket)
    let failure: Error | null = null
    const { query } = mount(<LiveStudentPanel classId="class-a" />, {
      getActiveLiveSession: async () => {
        if (failure) throw failure
        return live
      },
    })
    await waitFor(() => expect(FakeSocket.instances).toHaveLength(1))
    const ws = FakeSocket.instances[0]
    await act(async () => {
      failure = new HttpError(403)
      await query.invalidateQueries()
      failure = new HttpError(503)
      await query.invalidateQueries()
    })
    expect(screen.queryByLabelText('강사 화면 읽기 전용 출력')).not.toBeInTheDocument()
    expect(ws.readyState).toBe(3)
    expect(FakeSocket.instances).toHaveLength(1)
  })

  it('denies invalid class membership without opening live socket', async () => {
    vi.stubGlobal('WebSocket', FakeSocket)
    mount(<LiveStudentPanel classId="class-a" />, {
      getActiveLiveSession: async () => { throw new HttpError(403) },
    })
    expect(await screen.findByRole('alert')).toBeInTheDocument()
    expect(FakeSocket.instances).toHaveLength(0)
  })
})
