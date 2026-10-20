import { afterEach, describe, expect, it, vi } from 'vitest'
import { BrowserLiveClient, liveTransport } from './browserLiveClient'

type Listener = (event: { data?: unknown; code?: number }) => void
class FakeWebSocket {
  static readonly OPEN = 1
  static readonly CLOSED = 3
  static instances: FakeWebSocket[] = []
  readonly url: string
  readonly protocol: string
  binaryType = 'blob'
  readyState = 0
  sent: unknown[] = []
  listeners = new Map<string, Listener[]>()
  constructor(url: string | URL, protocol: string) {
    this.url = String(url)
    this.protocol = protocol
    FakeWebSocket.instances.push(this)
  }
  addEventListener(name: string, listener: Listener) {
    this.listeners.set(name, [...(this.listeners.get(name) ?? []), listener])
  }
  send(frame: unknown) { this.sent.push(frame) }
  close(code = 1000) {
    this.readyState = FakeWebSocket.CLOSED
    this.emit('close', { code })
  }
  open() { this.readyState = FakeWebSocket.OPEN; this.emit('open', {}) }
  message(data: unknown) { this.emit('message', { data }) }
  remoteClose(code: number) { this.readyState = 3; this.emit('close', { code }) }
  emit(name: string, event: { data?: unknown; code?: number }) {
    for (const listener of this.listeners.get(name) ?? []) listener(event)
  }
}

function setup() {
  vi.stubGlobal('WebSocket', FakeWebSocket)
  const handlers = {
    onSubscribed: vi.fn(),
    onOutput: vi.fn(),
    onEnded: vi.fn(),
    onError: vi.fn(),
    onClose: vi.fn(),
  }
  const client = new BrowserLiveClient(handlers)
  client.connect('live-1')
  const ws = FakeWebSocket.instances.at(-1)!
  ws.open()
  const frame = JSON.parse(String(ws.sent[0])) as { messageId: string; type: string; liveSessionId: string; payload: unknown }
  const ack = (overrides: Record<string, unknown> = {}) => ws.message(JSON.stringify({
    type: 'LIVE_SUBSCRIBED',
    liveSessionId: 'live-1',
    replyToMessageId: frame.messageId,
    payload: { historyAvailable: false },
    ...overrides,
  }))
  return { ws, frame, ack, client, handlers }
}

afterEach(() => { vi.unstubAllGlobals(); FakeWebSocket.instances = [] })

describe('student read-only Live WSS contract', () => {
  it('connects on same-site WSS, sends only LIVE_SUBSCRIBE with no Terminal token', () => {
    const { ws, frame } = setup()
    expect(ws.url).toContain(liveTransport.path)
    expect(ws.url).not.toContain('token')
    expect(ws.protocol).toBe('labbit.live.v1')
    expect(ws.binaryType).toBe('arraybuffer')
    expect(frame).toMatchObject({ type: 'LIVE_SUBSCRIBE', liveSessionId: 'live-1', payload: {} })
    expect(typeof frame.messageId).toBe('string')
    expect(ws.sent).toHaveLength(1)
    expect(Object.keys(frame)).not.toContain('sessionToken')
  })

  it('does not render Binary until exact LIVE_SUBSCRIBED ACK; no replay or INPUT', () => {
    const { ws, ack, handlers, client } = setup()
    ws.message(new Uint8Array([65]).buffer)
    expect(handlers.onOutput).not.toHaveBeenCalled()
    ack()
    expect(handlers.onSubscribed).toHaveBeenCalledOnce()
    ws.message(new Uint8Array([66, 67]).buffer)
    expect([...handlers.onOutput.mock.calls[0][0]]).toEqual([66, 67])
    expect(ws.sent).toHaveLength(1)
    expect('sendInput' in client).toBe(false)
    expect('resize' in client).toBe(false)
  })

  it.each([
    { replyToMessageId: 'wrong' },
    { liveSessionId: 'other-class-live' },
    { payload: { historyAvailable: true } },
  ])('rejects incorrect ACK correlation, class scope or replay declaration', (bad) => {
    const { ws, ack, handlers } = setup()
    ack(bad)
    expect(handlers.onError).toHaveBeenCalledWith('PROTOCOL_ERROR')
    expect(handlers.onSubscribed).not.toHaveBeenCalled()
    expect(ws.readyState).toBe(FakeWebSocket.CLOSED)
  })

  it('rejects malformed messages and unexpected frames after subscription', () => {
    const { ws, handlers, ack } = setup()
    ack()
    ws.message('{bad')
    expect(handlers.onError).toHaveBeenCalledWith('PROTOCOL_ERROR')
    expect(handlers.onOutput).not.toHaveBeenCalled()
  })

  it('LIVE_ENDED is final and cannot be replaced by pending OUTPUT/close', () => {
    const { ws, ack, handlers } = setup()
    ack()
    ws.message(JSON.stringify({ type: 'LIVE_ENDED', liveSessionId: 'live-1', payload: { reason: 'SOURCE_TERMINAL_ENDED' } }))
    ws.message(new Uint8Array([68]).buffer)
    expect(handlers.onEnded).toHaveBeenCalledWith('SOURCE_TERMINAL_ENDED')
    expect(handlers.onOutput).not.toHaveBeenCalled()
    expect(handlers.onClose).not.toHaveBeenCalled()
  })

  it('ignores stale socket after changing LiveSession or explicit disconnect', () => {
    const { ws, client, handlers, ack } = setup()
    ack()
    client.connect('live-2')
    ws.message(new Uint8Array([69]).buffer)
    expect(handlers.onOutput).not.toHaveBeenCalled()
    client.disconnect()
    expect(handlers.onClose).not.toHaveBeenCalled()
  })

  it('maps ERROR by stable code and never forwards message/OUTPUT to logs', () => {
    const { ws, handlers } = setup()
    ws.message(JSON.stringify({ type: 'ERROR', liveSessionId: 'live-1', payload: { code: 'FORBIDDEN', message: 'synthetic secret', fatal: true } }))
    expect(handlers.onError).toHaveBeenCalledWith('FORBIDDEN')
    expect(ws.readyState).toBe(FakeWebSocket.CLOSED)
  })

  it('reports abnormal server close for controlled UI reconnect handling', () => {
    const { ws, handlers } = setup()
    ws.remoteClose(1012)
    expect(handlers.onClose).toHaveBeenCalledWith(1012)
  })
})
