import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react'
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

  it('denies invalid class membership without opening live socket', async () => {
    vi.stubGlobal('WebSocket', FakeSocket)
    mount(<LiveStudentPanel classId="class-a" />, {
      getActiveLiveSession: async () => { throw new HttpError(403) },
    })
    expect(await screen.findByRole('alert')).toBeInTheDocument()
    expect(FakeSocket.instances).toHaveLength(0)
  })
})
