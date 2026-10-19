import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { fireEvent, render, screen, waitFor } from '@testing-library/react'
import { createMemoryRouter, RouterProvider } from 'react-router-dom'
import { afterEach, describe, expect, it, vi } from 'vitest'

import { HttpError } from '../shared/api/httpClient'
import { LabbitApiProvider } from '../shared/api/LabbitApiProvider'
import type { LabbitApi } from '../shared/api/labbitApi'
import { mockLabbitApi, mockMe } from '../shared/api/mockLabbitApi'
import { ConnectorAdminPage } from './ConnectorAdminPage'

const ADMIN = { ...mockMe, organizationRole: 'ADMIN' as const }
const TOKEN = 'fixture-enrollment-token-not-secret'

function mount(overrides: Partial<LabbitApi> = {}, isAdmin = true) {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } })
  const listConnectors = vi.fn(async () => ({
    observedAt: '2026-10-19T00:00:00Z',
    items: [],
  }))
  const api = {
    ...mockLabbitApi,
    getMe: async () => isAdmin ? ADMIN : mockMe,
    listConnectors,
    ...overrides,
  }
  const router = createMemoryRouter([
    { path: '/connectors', element: <ConnectorAdminPage /> },
    { path: '/classes', element: <div>수업 목록</div> },
    { path: '/login', element: <div>로그인 화면</div> },
  ], { initialEntries: ['/connectors'] })
  const view = render(<QueryClientProvider client={client}>
    <LabbitApiProvider api={api}><RouterProvider router={router} /></LabbitApiProvider>
  </QueryClientProvider>)
  return { api, client, router, ...view }
}

afterEach(() => {
  vi.unstubAllGlobals()
  vi.restoreAllMocks()
})

describe('LBT-183 Connector admin screen contract-only UI', () => {
  it('MEMBER는 관리자 메뉴/등록 API 대신 권한 없음 상태만 본다', async () => {
    const list = vi.fn()
    mount({ listConnectors: list }, false)
    expect(await screen.findByRole('alert')).toHaveTextContent('Organization ADMIN')
    expect(list).not.toHaveBeenCalled()
    expect(screen.queryByRole('button', { name: '일회용 토큰 발급' })).not.toBeInTheDocument()
  })

  it('ADMIN은 빈 목록과 Installer URL 대기 상태를 본다', async () => {
    mount()
    expect(await screen.findByText('등록된 Connector가 없습니다.')).toBeInTheDocument()
    expect(screen.getByText(/Installer 배포 주소가 아직 확정되지 않았습니다/)).toBeInTheDocument()
    expect(screen.getByText(/발급된 토큰은 실제 Installer 등록이 완료되기 전까지 목록에 포함되지 않습니다/)).toBeInTheDocument()
  })

  it('일회용 발급 Token은 React Query cache에 넣지 않고 화면 종료 뒤 제거한다', async () => {
    const create = vi.fn(async () => ({ tokenId: 'issued-1', enrollmentToken: TOKEN, expiresAt: '2026-10-19T01:00:00Z' }))
    const { client } = mount({ createConnectorEnrollmentToken: create })
    await screen.findByText('등록된 Connector가 없습니다.')
    fireEvent.change(screen.getByRole('textbox', { name: 'Connector 이름' }), { target: { value: 'KT D1 Admin VM' } })
    fireEvent.click(screen.getByRole('button', { name: '일회용 토큰 발급' }))
    expect(await screen.findByText(TOKEN)).toBeInTheDocument()
    expect(create).toHaveBeenCalledWith('KT D1 Admin VM')
    expect(JSON.stringify(client.getQueryCache().getAll().map(x => x.state.data))).not.toContain(TOKEN)
    expect(JSON.stringify(client.getMutationCache().getAll().map(x => x.state.data))).not.toContain(TOKEN)
    fireEvent.click(screen.getByRole('button', { name: '토큰 화면 닫기' }))
    expect(screen.queryByText(TOKEN)).not.toBeInTheDocument()
  })

  it('Token 취소는 원문이 아닌 tokenId로 요청하고 원문을 화면에서 지운다', async () => {
    const cancel = vi.fn(async () => undefined)
    mount({
      createConnectorEnrollmentToken: async () => ({ tokenId: 'issued-1', enrollmentToken: TOKEN, expiresAt: '2026-10-19T01:00:00Z' }),
      cancelConnectorEnrollmentToken: cancel,
    })
    await screen.findByText('등록된 Connector가 없습니다.')
    fireEvent.change(screen.getByRole('textbox', { name: 'Connector 이름' }), { target: { value: 'New VM' } })
    fireEvent.click(screen.getByRole('button', { name: '일회용 토큰 발급' }))
    await screen.findByText(TOKEN)
    fireEvent.click(screen.getByRole('button', { name: '등록 토큰 취소' }))
    await waitFor(() => expect(cancel).toHaveBeenCalledWith('issued-1'))
    await waitFor(() => expect(screen.queryByText(TOKEN)).not.toBeInTheDocument())
    expect(screen.getByText('등록 토큰을 취소했습니다.')).toBeInTheDocument()
  })

  it('서버 상태 NEVER_SEEN을 ONLINE으로 추정하지 않고 Provider READY는 미확인으로 표시한다', async () => {
    mount({ listConnectors: async () => ({
      observedAt: '2026-10-19T00:00:00Z',
      items: [{
        id: 'conn-1', name: 'Customer connector', registrationStatus: 'REGISTERED' as const,
        connectionStatus: 'NEVER_SEEN' as const, createdAt: '2026-10-19T00:00:00Z',
        revokedAt: null, lastSeenAt: null, lastConnectorVersion: null, lastRuntimeId: null,
      }],
    }) })
    expect(await screen.findByText('등록됨 · Heartbeat 대기')).toBeInTheDocument()
    expect(screen.getByText('Provider READY: 미확인 (별도 검증 필요)')).toBeInTheDocument()
    expect(screen.queryByText('Control ONLINE')).not.toBeInTheDocument()
  })

  it('서버 403은 정상 빈 목록으로 위장하지 않고 안전하게 표시한다', async () => {
    mount({ listConnectors: async () => { throw new HttpError(403) } })
    expect(await screen.findByText('Connector 목록에 접근할 권한이 없습니다.')).toBeInTheDocument()
    expect(screen.queryByText('등록된 Connector가 없습니다.')).not.toBeInTheDocument()
  })
  it('명시적 확인 없이 Connector 폐기를 요청하지 않으며 확인한 경우만 Revoke한다', async () => {
    const revokeConnector = vi.fn(async () => undefined)
    const connector = {
      id: 'conn-1', name: 'Customer connector',
      registrationStatus: 'REGISTERED' as const, connectionStatus: 'OFFLINE' as const,
      createdAt: '2026-10-19T00:00:00Z', revokedAt: null, lastSeenAt: null,
      lastConnectorVersion: null, lastRuntimeId: null,
    }
    const confirm = vi.spyOn(window, 'confirm').mockReturnValue(false)
    mount({
      listConnectors: async () => ({ observedAt: '2026-10-19T00:00:00Z', items: [connector] }),
      revokeConnector,
    })
    await screen.findByText('Control OFFLINE')
    fireEvent.click(screen.getByRole('button', { name: 'Connector 폐기' }))
    expect(revokeConnector).not.toHaveBeenCalled()
    confirm.mockReturnValue(true)
    fireEvent.click(screen.getByRole('button', { name: 'Connector 폐기' }))
    await waitFor(() => expect(revokeConnector).toHaveBeenCalledWith('conn-1'))
    expect(screen.getByText('Provider READY: 미확인 (별도 검증 필요)')).toBeInTheDocument()
  })

  it('발급 중 무효 세션 401은 원문 노출 없이 로그인 화면으로 이동한다', async () => {
    const createConnectorEnrollmentToken = vi.fn(async () => {
      throw new HttpError(401)
    })
    const { router, client } = mount({ createConnectorEnrollmentToken })
    await screen.findByText('등록된 Connector가 없습니다.')
    fireEvent.change(screen.getByRole('textbox', { name: 'Connector 이름' }), {
      target: { value: 'Failed session' },
    })
    fireEvent.click(screen.getByRole('button', { name: '일회용 토큰 발급' }))
    await waitFor(() => expect(router.state.location.pathname).toBe('/login'))
    expect(JSON.stringify(client.getQueryCache().getAll().map((query) => query.state.data))).not.toContain(TOKEN)
  })

})
