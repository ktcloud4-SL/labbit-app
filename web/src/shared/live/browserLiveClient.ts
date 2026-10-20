/** Student-only read-only subscription. Never accepts TerminalSession credentials or INPUT. */
export const liveTransport = {
  path: '/realtime/v1/live',
  subprotocol: 'labbit.live.v1',
} as const

export interface LiveClientHandlers {
  onSubscribed(): void
  onOutput(data: Uint8Array): void
  onEnded(reason: string): void
  onError(code: string): void
  onClose(code: number): void
}

function liveUrl() {
  const url = new URL(liveTransport.path, window.location.href)
  url.protocol = url.protocol === 'https:' ? 'wss:' : 'ws:'
  return url.toString()
}

export class BrowserLiveClient {
  private socket: WebSocket | null = null
  private liveId: string | null = null
  private subscribeId: string | null = null
  private subscribed = false
  private closing = false

  constructor(private readonly handlers: LiveClientHandlers) {}

  connect(liveSessionId: string) {
    this.disconnect()
    this.liveId = liveSessionId
    this.subscribed = false
    this.closing = false
    this.subscribeId = null

    const socket = new WebSocket(liveUrl(), liveTransport.subprotocol)
    socket.binaryType = 'arraybuffer'
    this.socket = socket

    socket.addEventListener('open', () => {
      if (this.socket !== socket || this.closing) return
      const messageId = crypto.randomUUID()
      this.subscribeId = messageId
      socket.send(JSON.stringify({
        type: 'LIVE_SUBSCRIBE',
        messageId,
        liveSessionId,
        sentAt: new Date().toISOString(),
        payload: {},
      }))
    })

    socket.addEventListener('message', (event) => {
      if (this.socket !== socket || this.closing) return
      this.receive(event.data, socket)
    })

    socket.addEventListener('close', (event) => {
      if (this.socket !== socket) return
      this.socket = null
      this.subscribed = false
      this.subscribeId = null
      if (!this.closing) this.handlers.onClose(event.code)
    })
  }

  private failClosed(socket: WebSocket) {
    if (this.socket !== socket || this.closing) return
    this.closing = true
    this.subscribed = false
    this.handlers.onError('PROTOCOL_ERROR')
    socket.close(1000)
  }

  private receive(data: unknown, socket: WebSocket) {
    if (typeof data === 'string') {
      let frame: Record<string, unknown>
      try {
        const parsed: unknown = JSON.parse(data)
        if (!parsed || typeof parsed !== 'object' || Array.isArray(parsed)) throw new Error()
        frame = parsed as Record<string, unknown>
      } catch {
        this.failClosed(socket)
        return
      }

      const payload = frame.payload && typeof frame.payload === 'object' && !Array.isArray(frame.payload)
        ? frame.payload as Record<string, unknown>
        : {}
      if (frame.type === 'LIVE_SUBSCRIBED') {
        if (this.subscribed || !this.subscribeId ||
          frame.liveSessionId !== this.liveId ||
          frame.replyToMessageId !== this.subscribeId ||
          payload.historyAvailable !== false) {
          this.failClosed(socket)
          return
        }
        this.subscribeId = null
        this.subscribed = true
        this.handlers.onSubscribed()
        return
      }
      if (frame.type === 'LIVE_ENDED') {
        if (!this.subscribed || frame.liveSessionId !== this.liveId) {
          this.failClosed(socket)
          return
        }
        this.closing = true
        this.subscribed = false
        this.handlers.onEnded(typeof payload.reason === 'string' ? payload.reason : 'UNKNOWN')
        socket.close(1000)
        return
      }
      if (frame.type === 'ERROR') {
        if (frame.liveSessionId !== undefined && frame.liveSessionId !== this.liveId) {
          this.failClosed(socket)
          return
        }
        const code = typeof payload.code === 'string' ? payload.code : 'UNKNOWN'
        this.handlers.onError(code)
        if (payload.fatal === true) {
          this.closing = true
          this.subscribed = false
          socket.close(1000)
        }
        return
      }
      this.failClosed(socket)
      return
    }

    // Control ACK must precede all OUTPUT. Each new subscription starts blank.
    if (!this.subscribed) return
    if (data instanceof ArrayBuffer) {
      this.handlers.onOutput(new Uint8Array(data))
    } else if (ArrayBuffer.isView(data)) {
      this.handlers.onOutput(new Uint8Array(data.buffer, data.byteOffset, data.byteLength))
    } else if (data instanceof Blob) {
      void data.arrayBuffer().then(buffer => {
        if (this.socket !== socket || this.closing || !this.subscribed) return
        this.handlers.onOutput(new Uint8Array(buffer))
      }).catch(() => {
        if (this.socket === socket && !this.closing) this.handlers.onError('PROTOCOL_ERROR')
      })
    }
  }

  disconnect() {
    const socket = this.socket
    this.socket = null
    this.liveId = null
    this.subscribeId = null
    this.subscribed = false
    this.closing = true
    if (socket && socket.readyState !== WebSocket.CLOSED) socket.close(1000)
  }
}
