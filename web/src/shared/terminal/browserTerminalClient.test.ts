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

    expect([...onOutput.mock.calls[0][0]]).toEqual([
      104, 101, 108, 108, 111, 32,
    ])
    expect([...onOutput.mock.calls[1][0]]).toEqual([119, 111, 114, 108, 100])

    expect(client.sendInput('ls\r')).toBe(true)
    const binaryInput = socket.sent.at(-1) as Uint8Array
    expect(ArrayBuffer.isView(binaryInput)).toBe(true)
    expect([...binaryInput]).toEqual([108, 115, 13])

    expect(client.sendBinaryInput(new Uint8Array([0x80, 0xff]))).toBe(true)
    expect([...(socket.sent.at(-1) as Uint8Array)]).toEqual([0x80, 0xff])

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

  it('reconnect 뒤 이전 socket의 늦은 Blob OUTPUT을 버린다', async () => {
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

    const first = FakeWebSocket.instances[0]
    first.open()
    first.message(
      JSON.stringify({
        type: 'TERMINAL_ATTACHED',
        terminalSessionId: 'terminal-session-1',
        payload: {
          resumed: false,
          historyAvailable: false,
        },
      }),
    )

    let resolveBlob!: (buffer: ArrayBuffer) => void
    const delayedBlob = new Blob(['stale'])
    vi.spyOn(delayedBlob, 'arrayBuffer').mockReturnValue(
      new Promise<ArrayBuffer>((resolve) => {
        resolveBlob = resolve
      }),
    )
    first.message(delayedBlob)

    client.connect({
      terminalSessionId: 'terminal-session-1',
      sessionToken: 'opaque-token',
      cols: 80,
      rows: 20,
    })

    resolveBlob(new TextEncoder().encode('stale').buffer as ArrayBuffer)
    await Promise.resolve()
    await Promise.resolve()

    expect(onOutput).not.toHaveBeenCalled()
  })

  it('TERMINAL_SESSION_ENDED 뒤 transport close는 authoritative 종료 상태를 다시 덮어쓰지 않는다', () => {
    vi.stubGlobal('WebSocket', FakeWebSocket)

    const onEnded = vi.fn()
    const onClose = vi.fn()
    const client = new BrowserTerminalClient({
      onAttached: vi.fn(),
      onOutput: vi.fn(),
      onEnded,
      onProtocolError: vi.fn(),
      onClose,
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
          resumed: false,
          historyAvailable: false,
        },
      }),
    )

    socket.message(
      JSON.stringify({
        type: 'TERMINAL_SESSION_ENDED',
        terminalSessionId: 'terminal-session-1',
        payload: {
          reason: 'PTY_EXITED',
          exitCode: 17,
        },
      }),
    )
    socket.remoteClose(1000)

    expect(onEnded).toHaveBeenCalledWith({
      reason: 'PTY_EXITED',
      exitCode: 17,
    })
    expect(onClose).not.toHaveBeenCalled()
    expect(client.sendInput('echo should-not-send\r')).toBe(false)
  })

  it('non-fatal server ERROR 뒤에는 기존 PTY input을 유지한다', () => {
    vi.stubGlobal('WebSocket', FakeWebSocket)

    const onProtocolError = vi.fn()
    const client = new BrowserTerminalClient({
      onAttached: vi.fn(),
      onOutput: vi.fn(),
      onEnded: vi.fn(),
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
        type: 'TERMINAL_ATTACHED',
        terminalSessionId: 'terminal-session-1',
        payload: { resumed: false, historyAvailable: false },
      }),
    )

    socket.message(
      JSON.stringify({
        type: 'ERROR',
        payload: {
          code: 'CONNECTOR_UNAVAILABLE',
          message: 'retrying',
          fatal: false,
        },
      }),
    )
    expect(onProtocolError).toHaveBeenCalledWith({
      code: 'CONNECTOR_UNAVAILABLE',
      message: 'retrying',
      fatal: false,
    })
    expect(client.sendInput('still-attached')).toBe(true)
  })

  it('unknown fatal ERROR 뒤 socket을 닫고 이전 PTY input을 거부한다', () => {
    vi.stubGlobal('WebSocket', FakeWebSocket)

    const onProtocolError = vi.fn()
    const client = new BrowserTerminalClient({
      onAttached: vi.fn(),
      onOutput: vi.fn(),
      onEnded: vi.fn(),
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
        type: 'TERMINAL_ATTACHED',
        terminalSessionId: 'terminal-session-1',
        payload: { resumed: false, historyAvailable: false },
      }),
    )

    socket.message(
      JSON.stringify({
        type: 'ERROR',
        payload: {
          code: 'UNKNOWN_FATAL',
          message: 'stop',
          fatal: true,
        },
      }),
    )

    expect(onProtocolError).toHaveBeenCalledWith({
      code: 'UNKNOWN_FATAL',
      message: 'stop',
      fatal: true,
    })
    expect(socket.readyState).toBe(FakeWebSocket.CLOSED)
    expect(client.sendInput('must-not-send')).toBe(false)
  })

  it.each(['null', '[]', '"text"', '0', 'true', 'false'])(
    'object가 아닌 control %s를 예외 없이 차단하고 이전 PTY input을 거부한다',
    (frame) => {
      vi.stubGlobal('WebSocket', FakeWebSocket)

      const onProtocolError = vi.fn()
      const onOutput = vi.fn()
      const client = new BrowserTerminalClient({
        onAttached: vi.fn(),
        onOutput,
        onEnded: vi.fn(),
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
          type: 'TERMINAL_ATTACHED',
          terminalSessionId: 'terminal-session-1',
          payload: { resumed: false, historyAvailable: false },
        }),
      )

      expect(() => socket.message(frame)).not.toThrow()
      expect(onProtocolError).toHaveBeenCalledExactlyOnceWith({
        code: 'PROTOCOL_ERROR',
        message: 'Terminal control message를 해석하지 못했습니다.',
        fatal: true,
      })
      expect(socket.readyState).toBe(FakeWebSocket.CLOSED)
      expect(client.sendInput('must-not-send')).toBe(false)
      expect(client.resize(120, 32)).toBe(false)

      socket.message(new Uint8Array([1, 2, 3]).buffer)
      expect(onOutput).not.toHaveBeenCalled()
    },
  )

  it('malformed control 뒤 socket을 닫고 이전 PTY input을 거부한다', () => {
    vi.stubGlobal('WebSocket', FakeWebSocket)

    const onProtocolError = vi.fn()
    const client = new BrowserTerminalClient({
      onAttached: vi.fn(),
      onOutput: vi.fn(),
      onEnded: vi.fn(),
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
        type: 'TERMINAL_ATTACHED',
        terminalSessionId: 'terminal-session-1',
        payload: { resumed: false, historyAvailable: false },
      }),
    )
    socket.message('{bad json')

    expect(onProtocolError).toHaveBeenCalledWith({
      code: 'PROTOCOL_ERROR',
      message: 'Terminal control message를 해석하지 못했습니다.',
      fatal: true,
    })
    expect(socket.readyState).toBe(FakeWebSocket.CLOSED)
    expect(client.sendInput('must-not-send')).toBe(false)
  })
  it.each([
    { type: 'TERMINAL_ATTACHED', terminalSessionId: 'different-session',
      payload: { resumed: false, historyAvailable: false } },
    { type: 'TERMINAL_ATTACHED',
      payload: { resumed: false, historyAvailable: false } },
    { type: 'TERMINAL_SESSION_ENDED', terminalSessionId: 'different-session',
      payload: { reason: 'PTY_EXITED', exitCode: 0 } },
    { type: 'TERMINAL_SESSION_ENDED',
      payload: { reason: 'PTY_EXITED', exitCode: 0 } },
  ])('세션 스코프가 일치하지 않는 $type 프레임을 거절하며 PTY 상태를 바꾸지 않는다', (message) => {
    vi.stubGlobal('WebSocket', FakeWebSocket)
    const onAttached = vi.fn()
    const onEnded = vi.fn()
    const onProtocolError = vi.fn()
    const onClose = vi.fn()
    const client = new BrowserTerminalClient({
      onAttached, onEnded, onProtocolError, onClose, onOutput: vi.fn(),
    })
    client.connect({
      terminalSessionId: 'terminal-session-1',
      sessionToken: 'fixture-attach-token',
      cols: 80,
      rows: 24,
    })
    const socket = FakeWebSocket.instances[0]
    socket.open()
    socket.message(JSON.stringify(message))

    expect(onAttached).not.toHaveBeenCalled()
    expect(onEnded).not.toHaveBeenCalled()
    expect(onClose).not.toHaveBeenCalled()
    expect(onProtocolError).toHaveBeenCalledExactlyOnceWith({
      code: 'PROTOCOL_ERROR',
      message: 'Terminal 응답의 세션 식별자가 올바르지 않습니다.',
      fatal: true,
    })
    expect(socket.readyState).toBe(FakeWebSocket.CLOSED)
    expect(client.sendInput('do-not-send')).toBe(false)
    expect(client.resize(120, 32)).toBe(false)
  })

  it('정상 attach 뒤 다른 세션의 종료 프레임도 현재 PTY를 종료시키지 않고 차단한다', () => {
    vi.stubGlobal('WebSocket', FakeWebSocket)
    const onAttached = vi.fn()
    const onEnded = vi.fn()
    const onProtocolError = vi.fn()
    const client = new BrowserTerminalClient({
      onAttached, onEnded, onProtocolError, onClose: vi.fn(), onOutput: vi.fn(),
    })
    client.connect({
      terminalSessionId: 'terminal-session-1',
      sessionToken: 'fixture-attach-token',
      cols: 80,
      rows: 24,
    })
    const socket = FakeWebSocket.instances[0]
    socket.open()
    socket.message(JSON.stringify({
      type: 'TERMINAL_ATTACHED',
      terminalSessionId: 'terminal-session-1',
      payload: { resumed: false, historyAvailable: false },
    }))
    expect(onAttached).toHaveBeenCalledOnce()
    socket.message(JSON.stringify({
      type: 'TERMINAL_SESSION_ENDED',
      terminalSessionId: 'different-session',
      payload: { reason: 'LAB_CLEANUP' },
    }))
    expect(onEnded).not.toHaveBeenCalled()
    expect(onProtocolError).toHaveBeenCalledOnce()
    expect(socket.readyState).toBe(FakeWebSocket.CLOSED)
    expect(client.sendInput('do-not-send')).toBe(false)
  })

  it('정상 TERMINAL_SESSION_ENDED 이후 늦은 중복 종료/이진 출력은 authoritative UI를 바꾸지 않는다', () => {
    vi.stubGlobal('WebSocket', FakeWebSocket)
    const onAttached = vi.fn()
    const onEnded = vi.fn()
    const onOutput = vi.fn()
    const onProtocolError = vi.fn()
    const onClose = vi.fn()
    const client = new BrowserTerminalClient({
      onAttached, onEnded, onOutput, onProtocolError, onClose,
    })
    client.connect({
      terminalSessionId: 'terminal-session-1',
      sessionToken: 'fixture-attach-token',
      cols: 80,
      rows: 24,
    })
    const socket = FakeWebSocket.instances[0]
    socket.open()
    socket.message(JSON.stringify({
      type: 'TERMINAL_ATTACHED',
      terminalSessionId: 'terminal-session-1',
      payload: { resumed: false, historyAvailable: false },
    }))
    expect(onAttached).toHaveBeenCalledOnce()
    socket.message(JSON.stringify({
      type: 'TERMINAL_SESSION_ENDED',
      terminalSessionId: 'terminal-session-1',
      payload: { reason: 'PTY_EXITED', exitCode: 17 },
    }))
    expect(onEnded).toHaveBeenCalledExactlyOnceWith({ reason: 'PTY_EXITED', exitCode: 17 })

    socket.message(JSON.stringify({
      type: 'TERMINAL_SESSION_ENDED',
      terminalSessionId: 'terminal-session-1',
      payload: { reason: 'PTY_EXITED' },
    }))
    socket.message(JSON.stringify({
      type: 'TERMINAL_SESSION_ENDED',
      terminalSessionId: 'different-session',
      payload: { reason: 'LAB_CLEANUP' },
    }))
    socket.message(new Uint8Array([1, 2, 3]).buffer)
    socket.remoteClose(1000)

    expect(onEnded).toHaveBeenCalledTimes(1)
    expect(onProtocolError).not.toHaveBeenCalled()
    expect(onOutput).not.toHaveBeenCalled()
    expect(onClose).not.toHaveBeenCalled()
    expect(client.sendInput('should-not-send')).toBe(false)
  })

})
