import { fireEvent, render, screen, waitFor } from '@testing-library/react'
import { afterEach, describe, expect, it, vi } from 'vitest'

import { ConnectorInstallGuide } from './ConnectorInstallGuide'

afterEach(() => {
  vi.unstubAllGlobals()
  vi.restoreAllMocks()
})

describe('LBT-183 Connector installer guidance (UI only)', () => {
  it('승인된 Installer 주소가 없으면 실행 가능한 명령이나 복사 버튼을 노출하지 않는다', () => {
    render(<ConnectorInstallGuide />)

    expect(screen.getByRole('status')).toHaveTextContent('아직 확정되지 않았습니다')
    expect(screen.queryByRole('button', { name: 'Installer 주소 복사' })).not.toBeInTheDocument()
    expect(screen.getByText(/Provider 준비\(READY\)/)).toBeInTheDocument()
  })

  it.each([
    'http://installer.example.test/install.sh',
    'https://user:password@installer.example.test/install.sh',
    '/install.sh',
    'not-a-url',
  ])('보안 기준에 맞지 않는 URL %s을 복사 대상으로 표시하지 않는다', (url) => {
    render(<ConnectorInstallGuide approvedInstallerUrl={url} />)

    expect(screen.queryByRole('button', { name: 'Installer 주소 복사' })).not.toBeInTheDocument()
    expect(screen.getByRole('status')).toHaveTextContent('아직 확정되지 않았습니다')
  })

  it('승인된 HTTPS 주소만 복사하고 shell pipeline이나 Token은 만들지 않는다', async () => {
    const writeText = vi.fn(async () => undefined)
    vi.stubGlobal('navigator', { clipboard: { writeText } })
    const url = 'https://installer.example.test/install.sh'
    render(<ConnectorInstallGuide approvedInstallerUrl={url} />)

    fireEvent.click(screen.getByRole('button', { name: 'Installer 주소 복사' }))
    await waitFor(() => expect(writeText).toHaveBeenCalledWith(url))
    expect(screen.getByRole('status')).toHaveTextContent('Installer 주소를 복사했습니다')
    expect(screen.queryByText(/curl -fsSL/)).not.toBeInTheDocument()
  })

  it('복사 실패를 보안 안전한 일반 오류로 표시한다', async () => {
    vi.stubGlobal('navigator', {
      clipboard: { writeText: vi.fn(async () => { throw new Error('credential-secret') }) },
    })
    render(<ConnectorInstallGuide approvedInstallerUrl="https://installer.example.test/install.sh" />)

    fireEvent.click(screen.getByRole('button', { name: 'Installer 주소 복사' }))
    const alert = await screen.findByRole('alert')
    expect(alert).toHaveTextContent('주소 복사에 실패했습니다')
    expect(alert).not.toHaveTextContent('credential-secret')
  })
})
