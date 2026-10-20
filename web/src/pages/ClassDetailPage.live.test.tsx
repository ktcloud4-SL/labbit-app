import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { act, cleanup, render, screen, waitFor } from '@testing-library/react'
import { createMemoryRouter, RouterProvider } from 'react-router-dom'
import { afterEach, describe, expect, it, vi } from 'vitest'

import type { LiveSession } from '../shared/api/contracts'
import { HttpError } from '../shared/api/httpClient'
import { LabbitApiProvider } from '../shared/api/LabbitApiProvider'
import { labbitQueryKeys } from '../shared/api/labbitApi'
import { mockLabbitApi } from '../shared/api/mockLabbitApi'
import { ClassDetailPage } from './ClassDetailPage'

const terminalWrite = vi.fn()
vi.mock('@xterm/xterm', () => ({
  Terminal: class {
    loadAddon() {}
    open() {}
    reset() {}
    write(bytes: Uint8Array) { terminalWrite(bytes) }
    dispose() {}
  },
}))
vi.mock('@xterm/addon-fit', () => ({ FitAddon: class { fit() {} } }))

class LiveSocket extends EventTarget {
  static readonly CLOSED = 3
  static instances: LiveSocket[] = []
  readyState = 0
  binaryType = 'blob'
  sent: string[] = []
  constructor() {
    super()
    LiveSocket.instances.push(this)
  }
  send(frame: string) { this.sent.push(frame) }
  close() {
    this.readyState = 3
    this.dispatchEvent(new CloseEvent('close', { code: 1000 }))
  }
  subscribe(liveSessionId: string) {
    this.readyState = 1
    this.dispatchEvent(new Event('open'))
    const request = JSON.parse(this.sent[0]) as { messageId: string }
    this.message(JSON.stringify({
      type: 'LIVE_SUBSCRIBED', liveSessionId, replyToMessageId: request.messageId,
      payload: { historyAvailable: false },
    }))
  }
  message(data: unknown) { this.dispatchEvent(new MessageEvent('message', { data })) }
}

afterEach(() => {
  cleanup()
  LiveSocket.instances = []
  terminalWrite.mockClear()
  vi.unstubAllGlobals()
})

describe('LBT-26 Live continuity when revisiting a cached Class', () => {
  it.each(['503', 'network'] as const)(
    'keeps Class B WSS and OUTPUT after B → A → cached B, then %s',
    async (failure) => {
      vi.stubGlobal('WebSocket', LiveSocket)
      let pendingB: Promise<LiveSession> | null = null
      let rejectB!: (error: Error) => void
      const api = {
        ...mockLabbitApi,
        getClass: async (id: string) => ({ id, name: id, myRole: 'STUDENT' as const }),
        getActiveLiveSession: async (classId: string): Promise<LiveSession> => {
          if (classId === 'class-b' && pendingB) return pendingB
          return {
            id: `live-${classId}`, classId, sourceTerminalSessionId: `terminal-${classId}`,
            createdAt: '2026-10-20T00:00:00Z',
          }
        },
      }
      // Preserve successful B data during navigation, as the application does.
      const query = new QueryClient({ defaultOptions: { queries: { retry: false } } })
      const router = createMemoryRouter([
        { path: '/classes/:classId', element: <ClassDetailPage /> },
      ], { initialEntries: ['/classes/class-b'] })
      render(<QueryClientProvider client={query}>
        <LabbitApiProvider api={api}><RouterProvider router={router} /></LabbitApiProvider>
      </QueryClientProvider>)

      await waitFor(() => expect(LiveSocket.instances).toHaveLength(1))
      await act(async () => { LiveSocket.instances[0].subscribe('live-class-b') })
      expect(await screen.findByText('읽기 전용 연결됨')).toBeInTheDocument()

      await act(async () => { await router.navigate('/classes/class-a') })
      await waitFor(() => expect(LiveSocket.instances).toHaveLength(2))
      const oldA = LiveSocket.instances[1]
      await act(async () => { oldA.subscribe('live-class-a') })
      expect(await screen.findByText('읽기 전용 연결됨')).toBeInTheDocument()

      pendingB = new Promise<LiveSession>((_resolve, reject) => { rejectB = reject })
      await act(async () => { await router.navigate('/classes/class-b') })
      await waitFor(() => expect(LiveSocket.instances).toHaveLength(3))
      const currentB = LiveSocket.instances[2]
      await act(async () => { currentB.subscribe('live-class-b') })
      expect(await screen.findByText('읽기 전용 연결됨')).toBeInTheDocument()
      const viewer = screen.getByLabelText('강사 화면 읽기 전용 출력')
      expect(oldA.readyState).toBe(3)

      await act(async () => {
        rejectB(failure === '503' ? new HttpError(503) : new TypeError('Network offline'))
      })
      await waitFor(() => expect(query.getQueryState(labbitQueryKeys.activeLiveSession('class-b'))?.status).toBe('error'))
      expect(await screen.findByRole('alert')).toHaveTextContent('기존 읽기 전용 연결을 유지합니다')
      expect(screen.getByLabelText('강사 화면 읽기 전용 출력')).toBe(viewer)
      expect(currentB.readyState).toBe(1)
      expect(LiveSocket.instances).toHaveLength(3)

      // Only the current B socket may still deliver read-only output.
      await act(async () => {
        oldA.message(new Uint8Array([65]).buffer)
        currentB.message(new Uint8Array([66]).buffer)
      })
      expect(terminalWrite).toHaveBeenCalledExactlyOnceWith(new Uint8Array([66]))
      expect(currentB.sent).toHaveLength(1)
    },
  )
})
