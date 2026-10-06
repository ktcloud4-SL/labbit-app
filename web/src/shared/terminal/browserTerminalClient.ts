export interface TerminalAttachResult {
  resumed: boolean
  historyAvailable: boolean
}

export interface TerminalEndedEvent {
  reason: string
  exitCode?: number
}

export interface TerminalProtocolError {
  code: string
  message?: string
  fatal?: boolean
}

export interface BrowserTerminalClientHandlers {
  onAttached(result: TerminalAttachResult): void
  onOutput(data: Uint8Array): void
  onEnded(event: TerminalEndedEvent): void
  onProtocolError(error: TerminalProtocolError): void
  onClose(event: CloseEvent): void
}

export interface BrowserTerminalConnectInput {
  terminalSessionId: string
  sessionToken: string
  cols: number
  rows: number
}

const TERMINAL_PATH = '/realtime/v1/terminal'
const TERMINAL_SUBPROTOCOL = 'labbit.terminal.v1'

function terminalWebSocketUrl() {
  const url = new URL(TERMINAL_PATH, window.location.href)
  url.protocol = window.location.protocol === 'https:' ? 'wss:' : 'ws:'
  return url.toString()
}

function controlEnvelope(type: string, terminalSessionId: string, payload: object) {
  return {
    type,
    messageId: crypto.randomUUID(),
    sentAt: new Date().toISOString(),
    terminalSessionId,
    payload,
  }
}

export class BrowserTerminalClient {
  private socket: WebSocket | null = null
  private attached = false
  private closing = false
  private input: BrowserTerminalConnectInput | null = null

  constructor(private readonly handlers: BrowserTerminalClientHandlers) {}

  connect(input: BrowserTerminalConnectInput) {
    this.disconnect()
    this.input = input
    this.attached = false
    this.closing = false

    const socket = new WebSocket(terminalWebSocketUrl(), TERMINAL_SUBPROTOCOL)
    socket.binaryType = 'arraybuffer'
    this.socket = socket

    socket.addEventListener('open', () => {
      if (this.socket !== socket || this.closing) return

      socket.send(
        JSON.stringify(
          controlEnvelope('TERMINAL_ATTACH', input.terminalSessionId, {
            sessionToken: input.sessionToken,
            cols: input.cols,
            rows: input.rows,
          }),
        ),
      )
    })

    socket.addEventListener('message', (event) => {
      if (this.socket !== socket) return
      this.handleMessage(event.data, socket)
    })

    socket.addEventListener('close', (event) => {
      if (this.socket !== socket) return
      this.socket = null
      this.attached = false
      if (!this.closing) this.handlers.onClose(event)
    })
  }

  private handleMessage(data: unknown, sourceSocket: WebSocket) {
    if (typeof data === 'string') {
      let message: Record<string, unknown>
      try {
        message = JSON.parse(data) as Record<string, unknown>
      } catch {
        this.handlers.onProtocolError({
          code: 'PROTOCOL_ERROR',
          message: 'Terminal control message를 해석하지 못했습니다.',
          fatal: true,
        })
        return
      }

      const type = message.type
      const payload =
        message.payload && typeof message.payload === 'object'
          ? (message.payload as Record<string, unknown>)
          : {}

      if (type === 'TERMINAL_ATTACHED') {
        this.attached = true
        this.handlers.onAttached({
          resumed: payload.resumed === true,
          historyAvailable: payload.historyAvailable === true,
        })
        return
      }

      if (type === 'TERMINAL_SESSION_ENDED') {
        // TERMINAL_SESSION_ENDED가 authoritative lifecycle 종료다.
        // 뒤따르는 WebSocket close는 transport cleanup일 뿐이므로 onClose가
        // 종료 사유/exitCode UX를 다시 덮어쓰거나 reconnect를 시작하지 않게 한다.
        this.attached = false
        this.closing = true
        this.handlers.onEnded({
          reason: typeof payload.reason === 'string' ? payload.reason : 'UNKNOWN',
          exitCode:
            typeof payload.exitCode === 'number' ? payload.exitCode : undefined,
        })
        return
      }

      if (type === 'ERROR') {
        this.handlers.onProtocolError({
          code: typeof payload.code === 'string' ? payload.code : 'UNKNOWN',
          message:
            typeof payload.message === 'string' ? payload.message : undefined,
          fatal: payload.fatal === true,
        })
      }
      return
    }

    if (
      data instanceof ArrayBuffer ||
      Object.prototype.toString.call(data) === '[object ArrayBuffer]'
    ) {
      this.handlers.onOutput(new Uint8Array(data as ArrayBuffer))
      return
    }

    if (ArrayBuffer.isView(data)) {
      this.handlers.onOutput(
        new Uint8Array(data.buffer, data.byteOffset, data.byteLength),
      )
      return
    }

    if (data instanceof Blob) {
      void data.arrayBuffer().then((buffer) => {
        if (this.socket !== sourceSocket || this.closing) return
        this.handlers.onOutput(new Uint8Array(buffer))
      })
    }
  }

  sendInput(value: string) {
    if (!this.socket || this.socket.readyState !== WebSocket.OPEN || !this.attached) {
      return false
    }

    this.socket.send(new TextEncoder().encode(value))
    return true
  }

  resize(cols: number, rows: number) {
    if (
      !this.socket ||
      this.socket.readyState !== WebSocket.OPEN ||
      !this.attached ||
      !this.input
    ) {
      return false
    }

    this.socket.send(
      JSON.stringify(
        controlEnvelope('TERMINAL_RESIZE', this.input.terminalSessionId, {
          cols,
          rows,
        }),
      ),
    )
    return true
  }

  disconnect() {
    this.closing = true
    const socket = this.socket
    this.socket = null
    this.attached = false

    if (
      socket &&
      (socket.readyState === WebSocket.OPEN ||
        socket.readyState === WebSocket.CONNECTING)
    ) {
      socket.close(1000)
    }
  }
}

export const terminalTransport = {
  path: TERMINAL_PATH,
  subprotocol: TERMINAL_SUBPROTOCOL,
}
