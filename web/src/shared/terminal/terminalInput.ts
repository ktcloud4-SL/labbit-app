import type { BrowserTerminalClient } from './browserTerminalClient'

interface Disposable {
  dispose(): void
}

interface TerminalInputSource {
  onData(listener: (data: string) => void): Disposable
  onBinary(listener: (data: string) => void): Disposable
}

export function xtermBinaryStringToBytes(value: string) {
  const bytes = new Uint8Array(value.length)
  for (let index = 0; index < value.length; index += 1) {
    bytes[index] = value.charCodeAt(index) & 0xff
  }
  return bytes
}

export function subscribeTerminalInput(
  terminal: TerminalInputSource,
  getClient: () => BrowserTerminalClient | null,
): Disposable {
  const dataSubscription = terminal.onData((data) => {
    getClient()?.sendInput(data)
  })
  const binarySubscription = terminal.onBinary((data) => {
    getClient()?.sendBinaryInput(xtermBinaryStringToBytes(data))
  })

  return {
    dispose() {
      dataSubscription.dispose()
      binarySubscription.dispose()
    },
  }
}
