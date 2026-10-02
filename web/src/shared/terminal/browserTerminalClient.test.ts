import { afterEach, describe, expect, it, vi } from 'vitest'

import {
  BrowserTerminalClient,
  terminalTransport,
} from './browserTerminalClient'

type Listener = (event: unknown) => void

class FakeWebSocket {
  static readonly CONNECTING = 0
  static readonly OPEN = 1
  static readonly CLOSING = 2
  static readonly CLOSED = 3

  static instances: FakeWebSocket[] = []

  readonly url: string
  readonly protocol: string
  readyState = FakeWebSocket.CONNECTING
  binaryType = 'blob'
  sent: unknown[] = []
  private readonly listeners = new Map<string, Listener[]>()

  constructor(url: string | URL, protocols?: string | string[]) {
    this.url = String(url)
    this.protocol = Array.isArray(protocols) ? protocols[0] ?? '' : protocols ?? ''
    FakeWebSocket.instances.push(this)
  }

  addEventListener(type: string, listener: Listener) {
    this.listeners.set(type, [...(this.listeners.get(type) ?? []), listener])
  }

  send(data: unknown) {
    this.sent.push(data)
  }

  close(code = 1000) {
    this.readyState = FakeWebSocket.CLOSED
    this.emit('close', { code })
  }

  open() {
    this.readyState = FakeWebSocket.OPEN
    this.emit('open', {})
  }

  message(data: unknown) {
    this.emit('message', { data })
  }

  remoteClose(code: number) {
    this.readyState = FakeWebSocket.CLOSED
    this.emit('close', { code })
  }

  private emit(type: string, event: unknown) {
    for (const listener of this.listeners.get(type) ?? []) {
      listener(event)
    }
  }
}

describe('BrowserTerminalClient', () => {
  afterEach(() => {
    FakeWebSocket.instances = []
    vi.unstubAllGlobals()
  })

  it('sessionToken을 URL에 넣지 않고 첫 JSON frame의 TERMINAL_ATTACH에만 전달한다', () => {
    vi.stubGlobal('WebSocket', FakeWebSocket)

    const onAttached = vi.fn()
    const client = new BrowserTerminalClient({
      onAttached,
      onOutput: vi.fn(),
      onEnded: vi.fn(),
      onProtocolError: vi.fn(),
      onClose: vi.fn(),
    })

    client.connect({
      terminalSessionId: 'terminal-session-1',
      sessionToken: 'very-secret-attach-token',
      cols: 100,
      rows: 24,
    })

    const socket = FakeWebSocket.instances[0]
    expect(socket.url).toContain(terminalTransport.path)
    expect(socket.url).not.toContain('very-secret-attach-token')
    expect(socket.protocol).toBe(terminalTransport.subprotocol)

    socket.open()

    expect(socket.sent).toHaveLength(1)
    const attach = JSON.parse(String(socket.sent[0]))
    expect(attach).toMatchObject({
      type: 'TERMINAL_ATTACH',
      terminalSessionId: 'terminal-session-1',
      payload: {
        sessionToken: 'very-secret-attach-token',
        cols: 100,
        rows: 24,
      },
    })

    socket.message(
      JSON.stringify({
        type: 'TERMINAL_ATTACHED',
        terminalSessionId: 'terminal-session-1',
        payload: {
          resumed: false,
          historyAvailable: false,
        },
      }),
    )

    expect(onAttached).toHaveBeenCalledWith({
      resumed: false,
      historyAvailable: false,
    })
  })

  it('attach 이후 Binary INPUT/OUTPUT과 TERMINAL_RESIZE를 사용한다', () => {
    vi.stubGlobal('WebSocket', FakeWebSocket)

    const onOutput = vi.fn()
    const client = new BrowserTerminalClient({
      onAttached: vi.fn(),
      onOutput,
      onEnded: vi.fn(),
      onProtocolError: vi.fn(),
      onClose: vi.fn(),
    })

    client.connect({
      terminalSessionId: 'terminal-session-1',
      sessionToken: 'opaque-token',
      cols: 80,
      rows: 20,
    })

    const socket = FakeWebSocket.instances[0]
    socket.open()
    socket.message(
      JSON.stringify({
        type: 'TERMINAL_ATTACHED',
        terminalSessionId: 'terminal-session-1',
        payload: {
          resumed: true,
          historyAvailable: false,
        },
      }),
    )

    socket.message(new TextEncoder().encode('hello ').buffer)
    socket.message(new TextEncoder().encode('world').buffer)

    expect(onOutput).toHaveBeenNthCalledWith(1, 'hello ')
    expect(onOutput).toHaveBeenNthCalledWith(2, 'world')

    expect(client.sendInput('ls\r')).toBe(true)
    const binaryInput = socket.sent.at(-1) as Uint8Array
    expect(ArrayBuffer.isView(binaryInput)).toBe(true)
    expect([...binaryInput]).toEqual([108, 115, 13])

    expect(client.resize(120, 32)).toBe(true)
    const resize = JSON.parse(String(socket.sent.at(-1)))
    expect(resize).toMatchObject({
      type: 'TERMINAL_RESIZE',
      terminalSessionId: 'terminal-session-1',
      payload: {
        cols: 120,
        rows: 32,
      },
    })
  })

  it('server ERROR와 TERMINAL_SESSION_ENDED를 handler로 전달한다', () => {
    vi.stubGlobal('WebSocket', FakeWebSocket)

    const onProtocolError = vi.fn()
    const onEnded = vi.fn()
    const client = new BrowserTerminalClient({
      onAttached: vi.fn(),
      onOutput: vi.fn(),
      onEnded,
      onProtocolError,
      onClose: vi.fn(),
    })

    client.connect({
      terminalSessionId: 'terminal-session-1',
      sessionToken: 'opaque-token',
      cols: 80,
      rows: 20,
    })

    const socket = FakeWebSocket.instances[0]
    socket.open()

    socket.message(
      JSON.stringify({
        type: 'ERROR',
        payload: {
          code: 'SESSION_EXPIRED',
          message: 'expired',
          fatal: true,
        },
      }),
    )
    expect(onProtocolError).toHaveBeenCalledWith({
      code: 'SESSION_EXPIRED',
      message: 'expired',
      fatal: true,
    })

    socket.message(
      JSON.stringify({
        type: 'TERMINAL_SESSION_ENDED',
        payload: {
          reason: 'PTY_EXITED',
          exitCode: 0,
        },
      }),
    )
    expect(onEnded).toHaveBeenCalledWith({
      reason: 'PTY_EXITED',
      exitCode: 0,
    })
  })
})
