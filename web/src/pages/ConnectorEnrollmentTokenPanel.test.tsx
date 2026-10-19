import { fireEvent, render, screen, waitFor } from '@testing-library/react'
import { useState } from 'react'
import { afterEach, describe, expect, it, vi } from 'vitest'

import { ConnectorEnrollmentTokenPanel } from './ConnectorEnrollmentTokenPanel'

const TEST_TOKEN = 'fixture-token-for-ui-test-only'

function TestHost() {
  const [token, setToken] = useState<string | null>(TEST_TOKEN)
  return token ? (
    <ConnectorEnrollmentTokenPanel
      token={token}
      expiresAt="2026-10-19T12:00:00+09:00"
      onClose={() => setToken(null)}
    />
  ) : (
    <p>등록 토큰 화면 종료</p>
  )
}

afterEach(() => {
  vi.unstubAllGlobals()
  vi.restoreAllMocks()
})

describe('LBT-183 Connector one-time enrollment token presentation', () => {
  it('발급 직후에만 토큰을 표시하며 닫기 시 화면에서 제거한다', () => {
    const setItem = vi.spyOn(Storage.prototype, 'setItem')
    render(<TestHost />)

    expect(screen.getByText(TEST_TOKEN)).toBeInTheDocument()
    expect(screen.getByText(/만료 시각/)).toBeInTheDocument()
    expect(setItem).not.toHaveBeenCalled()

    fireEvent.click(screen.getByRole('button', { name: '토큰 화면 닫기' }))

    expect(screen.queryByText(TEST_TOKEN)).not.toBeInTheDocument()
    expect(screen.getByText('등록 토큰 화면 종료')).toBeInTheDocument()
    expect(setItem).not.toHaveBeenCalled()
  })

  it('복사 요청 시 Clipboard API에만 토큰을 전달하고 상태를 알려준다', async () => {
    const writeText = vi.fn(async () => undefined)
    vi.stubGlobal('navigator', { clipboard: { writeText } })
    const setItem = vi.spyOn(Storage.prototype, 'setItem')
    render(<TestHost />)

    fireEvent.click(screen.getByRole('button', { name: '토큰 복사' }))

    await waitFor(() => expect(writeText).toHaveBeenCalledWith(TEST_TOKEN))
    expect(await screen.findByRole('status', { name: '' })).toHaveTextContent('토큰을 복사했습니다.')
    expect(setItem).not.toHaveBeenCalled()
  })

  it('클립보드 접근 실패 시 토큰·원본 오류를 노출하지 않는 안내를 보여준다', async () => {
    vi.stubGlobal('navigator', {
      clipboard: { writeText: vi.fn(async () => { throw new Error('raw secret failure') }) },
    })
    render(<TestHost />)

    fireEvent.click(screen.getByRole('button', { name: '토큰 복사' }))

    const alert = await screen.findByRole('alert')
    expect(alert).toHaveTextContent('복사에 실패했습니다.')
    expect(alert).not.toHaveTextContent(TEST_TOKEN)
    expect(alert).not.toHaveTextContent('raw secret failure')
  })
})
