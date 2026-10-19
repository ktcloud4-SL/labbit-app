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
    expect(client.getQueryCache().getAll()).toHaveLength(0)
  })

})
