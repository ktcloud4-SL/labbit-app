import { describe, expect, it, vi } from 'vitest'

import type { BrowserTerminalClient } from './browserTerminalClient'
import {
  subscribeTerminalInput,
  xtermBinaryStringToBytes,
} from './terminalInput'

describe('terminal input', () => {
  it('xterm binary code unit의 low 8-bit를 그대로 보낸다', () => {
    expect([...xtermBinaryStringToBytes('\u0080\u00ff\u1234')]).toEqual([
      0x80, 0xff, 0x34,
    ])
  })

  it('onData/onBinary를 각각 전송하고 두 구독을 함께 정리한다', () => {
    let onData!: (value: string) => void
    let onBinary!: (value: string) => void
    const disposeData = vi.fn()
    const disposeBinary = vi.fn()
    const terminal = {
      onData(listener: (value: string) => void) {
        onData = listener
        return { dispose: disposeData }
      },
      onBinary(listener: (value: string) => void) {
        onBinary = listener
        return { dispose: disposeBinary }
      },
    }
    const client = {
      sendInput: vi.fn(),
      sendBinaryInput: vi.fn(),
    } as unknown as BrowserTerminalClient

    const subscription = subscribeTerminalInput(terminal, () => client)
    onData('한글')
    onBinary('\u0080\u00ff')

    expect(client.sendInput).toHaveBeenCalledWith('한글')
    expect(client.sendBinaryInput).toHaveBeenCalledTimes(1)
    expect([
      ...(client.sendBinaryInput as ReturnType<typeof vi.fn>).mock.calls[0][0],
    ]).toEqual([0x80, 0xff])

    subscription.dispose()
    expect(disposeData).toHaveBeenCalledTimes(1)
    expect(disposeBinary).toHaveBeenCalledTimes(1)
  })
})
