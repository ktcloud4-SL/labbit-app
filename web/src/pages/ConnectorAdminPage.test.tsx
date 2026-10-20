import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { act, fireEvent, render, screen, waitFor } from '@testing-library/react'
import { createMemoryRouter, RouterProvider } from 'react-router-dom'
import { afterEach, describe, expect, it, vi } from 'vitest'

import type { ConnectorEnrollmentTokenIssued } from '../shared/api/contracts'
import { HttpError } from '../shared/api/httpClient'
import { LabbitApiProvider } from '../shared/api/LabbitApiProvider'
import { labbitQueryKeys, type LabbitApi } from '../shared/api/labbitApi'
import { mockLabbitApi, mockMe } from '../shared/api/mockLabbitApi'
import { ConnectorAdminPage } from './ConnectorAdminPage'

const ADMIN = { ...mockMe, organizationRole: 'ADMIN' as const }
const TOKEN = 'fixture-enrollment-token-not-secret'
const FUTURE_EXPIRY = new Date(Date.now() + 15 * 60_000).toISOString()

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
  vi.useRealTimers()
  vi.unstubAllGlobals()
  vi.unstubAllEnvs()
  vi.restoreAllMocks()
})

describe('LBT-183 Connector admin screen contract-only UI', () => {
  it('MEMBER는 관리자 메뉴/등록 API 대신 권한 없음 상태만 본다', async () => {
    const list = vi.fn()
    const url = 'https://raw.githubusercontent.com/ktcloud4-SL/labbit-app/e0caea4f3191e9109171c02581d498f48e4c9887/scripts/installer/install.sh'
    vi.stubEnv('VITE_LABBIT_APPROVED_INSTALLER_URL', url)
    mount({ listConnectors: list }, false)
    expect(await screen.findByRole('alert')).toHaveTextContent('Organization ADMIN')
    expect(list).not.toHaveBeenCalled()
    expect(screen.queryByRole('button', { name: '일회용 토큰 발급' })).not.toBeInTheDocument()
    expect(screen.queryByText(url)).not.toBeInTheDocument()
    expect(screen.queryByRole('button', { name: 'Installer 주소 복사' })).not.toBeInTheDocument()
  })

  it('화면에 서버 상태 갱신 주기를 안내하고 수동 새로고침도 동작한다', async () => {
    const { api } = mount()
    await screen.findByText('등록된 Connector가 없습니다.')
    expect(screen.getByText(/15초 간격으로 서버 상태를 확인합니다/)).toBeInTheDocument()
    fireEvent.click(screen.getByRole('button', { name: '상태 새로고침' }))
    await waitFor(() => expect(api.listConnectors).toHaveBeenCalledTimes(2))
  })

  it('이미 만료된 토큰 응답은 화면·Query cache에 노출하지 않는다', async () => {
    const { client } = mount({
      createConnectorEnrollmentToken: async () => ({
        tokenId: 'expired-1', enrollmentToken: TOKEN,
        expiresAt: new Date(Date.now() - 1000).toISOString(),
      }),
    })
    await screen.findByText('등록된 Connector가 없습니다.')
    fireEvent.change(screen.getByRole('textbox', { name: 'Connector 이름' }), { target: { value: 'VM' } })
    fireEvent.click(screen.getByRole('button', { name: '일회용 토큰 발급' }))
    expect(await screen.findByRole('alert')).toHaveTextContent('만료 시각이 유효하지 않습니다')
    expect(screen.queryByText(TOKEN)).not.toBeInTheDocument()
    expect(JSON.stringify(client.getQueryCache().getAll().map((query) => query.state.data))).not.toContain(TOKEN)
  })

  it('발급 후 만료되면 화면의 토큰 원문을 자동 제거하고 재발급을 안내한다', async () => {
    const { client } = mount({
      createConnectorEnrollmentToken: async () => ({
        tokenId: 'short-ttl', enrollmentToken: TOKEN,
        expiresAt: new Date(Date.now() + 650).toISOString(),
      }),
    })
    await screen.findByText('등록된 Connector가 없습니다.')
    // The expiry contract is time-based; a virtual clock avoids runner load
    // causing an unrelated 2-second waitFor timeout on the CI host.
    vi.useFakeTimers()
    try {
      fireEvent.change(screen.getByRole('textbox', { name: 'Connector 이름' }), { target: { value: 'VM' } })
      await act(async () => {
        fireEvent.click(screen.getByRole('button', { name: '일회용 토큰 발급' }))
        await Promise.resolve()
      })
      expect(screen.getByText(TOKEN)).toBeInTheDocument()
      await act(async () => { vi.advanceTimersByTime(651) })
      expect(screen.queryByText(TOKEN)).not.toBeInTheDocument()
      expect(screen.getByText('등록 토큰이 만료되었습니다. 필요한 경우 새 토큰을 발급해 주세요.')).toBeInTheDocument()
      expect(JSON.stringify(client.getMutationCache().getAll().map((mutation) => mutation.state.data))).not.toContain(TOKEN)
    } finally {
      vi.useRealTimers()
    }
  })

  it('ADMIN은 빈 목록과 Installer URL 대기 상태를 본다', async () => {
    mount()
    expect(await screen.findByText('등록된 Connector가 없습니다.')).toBeInTheDocument()
    expect(screen.getByText(/Installer 배포 주소가 아직 확정되지 않았습니다/)).toBeInTheDocument()
    expect(screen.getByText(/발급된 토큰은 실제 Installer 등록이 완료되기 전까지 목록에 포함되지 않습니다/)).toBeInTheDocument()
  })

  it('승인된 커밋 고정 Installer URL을 빌드 설정에서 ADMIN 안내로 전달하고 그대로 복사한다', async () => {
    const url = 'https://raw.githubusercontent.com/ktcloud4-SL/labbit-app/e0caea4f3191e9109171c02581d498f48e4c9887/scripts/installer/install.sh'
    const writeText = vi.fn(async () => undefined)
    vi.stubGlobal('navigator', { clipboard: { writeText } })
    vi.stubEnv('VITE_LABBIT_APPROVED_INSTALLER_URL', url)
    mount()
    expect(await screen.findByText('등록된 Connector가 없습니다.')).toBeInTheDocument()
    expect(screen.getByText(url)).toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Installer 주소 복사' })).toBeEnabled()
    fireEvent.click(screen.getByRole('button', { name: 'Installer 주소 복사' }))
    await waitFor(() => expect(writeText).toHaveBeenCalledWith(url))
    expect(screen.getByRole('status')).toHaveTextContent('Installer 주소를 복사했습니다')
    expect(screen.queryByText(/Installer 배포 주소가 아직 확정되지 않았습니다/)).not.toBeInTheDocument()
    expect(screen.queryByText(/curl -fsSL/)).not.toBeInTheDocument()
  })

  it('공개 설정값이 비승인 형식 또는 토큰 query를 포함하면 복사 UI를 노출하지 않는다', async () => {
    vi.stubEnv('VITE_LABBIT_APPROVED_INSTALLER_URL', 'https://installer.example.test/install.sh?token=fixture')
    mount()
    expect(await screen.findByText('등록된 Connector가 없습니다.')).toBeInTheDocument()
    expect(screen.getByText(/Installer 배포 주소가 아직 확정되지 않았습니다/)).toBeInTheDocument()
    expect(screen.queryByRole('button', { name: 'Installer 주소 복사' })).not.toBeInTheDocument()
    expect(document.body.innerHTML).not.toContain('token=fixture')
  })

  it('일회용 발급 Token은 React Query cache에 넣지 않고 화면 종료 뒤 제거한다', async () => {
    const create = vi.fn(async () => ({ tokenId: 'issued-1', enrollmentToken: TOKEN, expiresAt: FUTURE_EXPIRY }))
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
      createConnectorEnrollmentToken: async () => ({ tokenId: 'issued-1', enrollmentToken: TOKEN, expiresAt: FUTURE_EXPIRY }),
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
  it('Backend forbidden이 stale /me ADMIN 캐시와 겹쳐도 이전 발급 토큰을 숨긴다', async () => {
    let denied = false
    const listConnectors = vi.fn(async () => {
      if (denied) {
        throw new HttpError(403, {
          type: 'about:blank', title: 'Forbidden', status: 403,
          code: 'forbidden', requestId: 'role-changed-mock',
        })
      }
      return { observedAt: '2026-10-19T00:00:00Z', items: [] }
    })
    const { client } = mount({
      listConnectors,
      createConnectorEnrollmentToken: async () => ({
        tokenId: 'role-change-fixture',
        enrollmentToken: TOKEN,
        expiresAt: FUTURE_EXPIRY,
      }),
    })
    await screen.findByText('등록된 Connector가 없습니다.')
    fireEvent.change(screen.getByRole('textbox', { name: 'Connector 이름' }), {
      target: { value: 'Fixture only' },
    })
    fireEvent.click(screen.getByRole('button', { name: '일회용 토큰 발급' }))
    expect(await screen.findByText(TOKEN)).toBeInTheDocument()

    denied = true
    fireEvent.click(screen.getByRole('button', { name: '상태 새로고침' }))
    expect(await screen.findByText('Connector 목록에 접근할 권한이 없습니다.')).toBeInTheDocument()
    expect(client.getQueryData(labbitQueryKeys.me)).toMatchObject({ organizationRole: 'ADMIN' })
    expect(screen.queryByText(TOKEN)).not.toBeInTheDocument()
    expect(screen.queryByRole('button', { name: '토큰 복사' })).not.toBeInTheDocument()
  })

  it('발급 후 503 일시 오류는 권한 회수로 취급하지 않아 이전 토큰을 보존한다', async () => {
    let transient = false
    const listConnectors = vi.fn(async () => {
      if (transient) throw new HttpError(503)
      return { observedAt: '2026-10-19T00:00:00Z', items: [] }
    })
    mount({
      listConnectors,
      createConnectorEnrollmentToken: async () => ({
        tokenId: 'transient-503-fixture',
        enrollmentToken: TOKEN,
        expiresAt: FUTURE_EXPIRY,
      }),
    })
    await screen.findByText('등록된 Connector가 없습니다.')
    fireEvent.change(screen.getByRole('textbox', { name: 'Connector 이름' }), {
      target: { value: 'Fixture only' },
    })
    fireEvent.click(screen.getByRole('button', { name: '일회용 토큰 발급' }))
    expect(await screen.findByText(TOKEN)).toBeInTheDocument()

    transient = true
    fireEvent.click(screen.getByRole('button', { name: '상태 새로고침' }))
    expect(await screen.findByText('Connector 상태를 불러오지 못했습니다.')).toBeInTheDocument()
    expect(screen.getByText(TOKEN)).toBeInTheDocument()
    expect(screen.getByRole('button', { name: '토큰 복사' })).toBeInTheDocument()
  })

  it('토큰 취소에서 Backend forbidden이면 기존 원문과 복사 UI를 지운다', async () => {
    const cancelConnectorEnrollmentToken = vi.fn(async () => {
      throw new HttpError(403, {
        type: 'about:blank', title: 'Forbidden', status: 403,
        code: 'forbidden', requestId: 'cancel-role-change-mock',
      })
    })
    mount({
      createConnectorEnrollmentToken: async () => ({
        tokenId: 'cancel-forbidden-fixture',
        enrollmentToken: TOKEN,
        expiresAt: FUTURE_EXPIRY,
      }),
      cancelConnectorEnrollmentToken,
    })
    await screen.findByText('등록된 Connector가 없습니다.')
    fireEvent.change(screen.getByRole('textbox', { name: 'Connector 이름' }), {
      target: { value: 'Fixture only' },
    })
    fireEvent.click(screen.getByRole('button', { name: '일회용 토큰 발급' }))
    expect(await screen.findByText(TOKEN)).toBeInTheDocument()

    fireEvent.click(screen.getByRole('button', { name: '등록 토큰 취소' }))
    expect(await screen.findByText('Connector 목록에 접근할 권한이 없습니다.')).toBeInTheDocument()
    expect(cancelConnectorEnrollmentToken).toHaveBeenCalledWith('cancel-forbidden-fixture')
    expect(screen.queryByText(TOKEN)).not.toBeInTheDocument()
    expect(screen.queryByRole('button', { name: '토큰 복사' })).not.toBeInTheDocument()
    expect(screen.queryByText('등록 토큰을 취소했습니다.')).not.toBeInTheDocument()
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

  it('화면 이탈 뒤 늦게 도착한 발급 응답의 Token은 다시 표시되거나 Query cache에 남지 않는다', async () => {
    let completeIssue: ((result: ConnectorEnrollmentTokenIssued) => void) | undefined
    const issue = vi.fn(() => new Promise<ConnectorEnrollmentTokenIssued>((resolve) => {
      completeIssue = resolve
    }))
    const { router, client } = mount({ createConnectorEnrollmentToken: issue })
    await screen.findByText('등록된 Connector가 없습니다.')
    fireEvent.change(screen.getByRole('textbox', { name: 'Connector 이름' }), {
      target: { value: 'Only fixture VM' },
    })
    fireEvent.click(screen.getByRole('button', { name: '일회용 토큰 발급' }))
    expect(issue).toHaveBeenCalledTimes(1)

    fireEvent.click(screen.getByRole('link', { name: '← 수업 목록' }))
    await waitFor(() => expect(router.state.location.pathname).toBe('/classes'))
    await act(async () => {
      completeIssue?.({
        tokenId: 'late-response-id',
        enrollmentToken: TOKEN,
        expiresAt: new Date(Date.now() + 15 * 60_000).toISOString(),
      })
      await Promise.resolve()
    })

    expect(screen.queryByText(TOKEN)).not.toBeInTheDocument()
    expect(JSON.stringify(client.getQueryCache().getAll().map((query) => query.state.data))).not.toContain(TOKEN)
    expect(JSON.stringify(client.getMutationCache().getAll().map((mutation) => mutation.state.data))).not.toContain(TOKEN)
  })


  it('ADMIN이 다른 Organization으로 바뀌면 이전 발급 Token을 새 화면에서 재노출하지 않는다', async () => {
    const { client } = mount({
      createConnectorEnrollmentToken: async () => ({
        tokenId: 'org-a-issue',
        enrollmentToken: TOKEN,
        expiresAt: new Date(Date.now() + 15 * 60_000).toISOString(),
      }),
    })
    await screen.findByText('등록된 Connector가 없습니다.')
    fireEvent.change(screen.getByRole('textbox', { name: 'Connector 이름' }), {
      target: { value: 'Org A fixture' },
    })
    fireEvent.click(screen.getByRole('button', { name: '일회용 토큰 발급' }))
    expect(await screen.findByText(TOKEN)).toBeInTheDocument()

    act(() => {
      client.setQueryData(labbitQueryKeys.me, {
        ...ADMIN,
        organization: { id: 'org-b-fixture', name: 'Organization B' },
      })
    })
    await waitFor(() => expect(screen.queryByText(TOKEN)).not.toBeInTheDocument())
    expect(screen.queryByRole('button', { name: '토큰 복사' })).not.toBeInTheDocument()
    expect(screen.getByRole('textbox', { name: 'Connector 이름' })).toHaveValue('')
    expect(screen.getByRole('button', { name: '일회용 토큰 발급' })).toBeDisabled()
  })

  it('발급 요청 중 ADMIN Principal이 바뀌면 이전 조직의 늦은 Secret 응답을 버린다', async () => {
    let resolveIssue: ((value: ConnectorEnrollmentTokenIssued) => void) | undefined
    const issue = vi.fn(() => new Promise<ConnectorEnrollmentTokenIssued>((resolve) => {
      resolveIssue = resolve
    }))
    const { client } = mount({ createConnectorEnrollmentToken: issue })
    await screen.findByText('등록된 Connector가 없습니다.')
    fireEvent.change(screen.getByRole('textbox', { name: 'Connector 이름' }), {
      target: { value: 'Org A pending fixture' },
    })
    fireEvent.click(screen.getByRole('button', { name: '일회용 토큰 발급' }))
    expect(issue).toHaveBeenCalledTimes(1)

    act(() => {
      client.setQueryData(labbitQueryKeys.me, {
        ...ADMIN,
        id: 'other-admin-user',
        organization: { id: 'other-org', name: 'Other Organization' },
      })
    })
    // Query observer notifications are async: wait for the new scope to mount
    // before completing the previous scope's in-flight request.
    await waitFor(() => expect(screen.getByRole('button', { name: '일회용 토큰 발급' })).toBeInTheDocument())
    await act(async () => {
      resolveIssue?.({
        tokenId: 'old-principal-response',
        enrollmentToken: TOKEN,
        expiresAt: new Date(Date.now() + 15 * 60_000).toISOString(),
      })
      await Promise.resolve()
    })
    expect(screen.queryByText(TOKEN)).not.toBeInTheDocument()
    expect(screen.queryByRole('button', { name: '토큰 복사' })).not.toBeInTheDocument()
    expect(screen.getByRole('textbox', { name: 'Connector 이름' })).toHaveValue('')
    expect(screen.getByRole('button', { name: '일회용 토큰 발급' })).toBeDisabled()
    expect(JSON.stringify(client.getQueryCache().getAll().map((q) => q.state.data))).not.toContain(TOKEN)
    expect(JSON.stringify(client.getMutationCache().getAll().map((q) => q.state.data))).not.toContain(TOKEN)
  })

  it('ADMIN 권한이 내려갔다가 복구되어도 이전에 발급된 Token 원문을 복원하지 않는다', async () => {
    const { client } = mount({
      createConnectorEnrollmentToken: async () => ({
        tokenId: 'before-downgrade',
        enrollmentToken: TOKEN,
        expiresAt: new Date(Date.now() + 15 * 60_000).toISOString(),
      }),
    })
    await screen.findByText('등록된 Connector가 없습니다.')
    fireEvent.change(screen.getByRole('textbox', { name: 'Connector 이름' }), {
      target: { value: 'Privilege fixture' },
    })
    fireEvent.click(screen.getByRole('button', { name: '일회용 토큰 발급' }))
    expect(await screen.findByText(TOKEN)).toBeInTheDocument()

    act(() => {
      client.setQueryData(labbitQueryKeys.me, { ...ADMIN, organizationRole: 'MEMBER' })
    })
    expect(await screen.findByRole('alert')).toHaveTextContent('Organization ADMIN')
    expect(screen.queryByText(TOKEN)).not.toBeInTheDocument()

    act(() => { client.setQueryData(labbitQueryKeys.me, ADMIN) })
    await waitFor(() => expect(screen.getByRole('button', { name: '일회용 토큰 발급' })).toBeInTheDocument())
    expect(screen.queryByText(TOKEN)).not.toBeInTheDocument()
    expect(screen.getByRole('textbox', { name: 'Connector 이름' })).toHaveValue('')
    expect(screen.getByRole('button', { name: '일회용 토큰 발급' })).toBeDisabled()
  })

  it('서버 목록 401은 빈 Connector 목록 대신 로그인 화면으로 보낸다', async () => {
    const { router } = mount({
      listConnectors: async () => { throw new HttpError(401) },
    })
    await waitFor(() => expect(router.state.location.pathname).toBe('/login'))
    expect(screen.queryByText('등록된 Connector가 없습니다.')).not.toBeInTheDocument()
    expect(screen.queryByRole('button', { name: '일회용 토큰 발급' })).not.toBeInTheDocument()
  })

  it('토큰 취소 CSRF 403은 ADMIN 권한 회수로 오판하지 않고 기존 토큰을 유지한다', async () => {
    const cancel = vi.fn(async () => {
      throw new HttpError(403, {
        type: 'about:blank', title: 'CSRF Rejected', status: 403,
        code: 'csrf_rejected', requestId: 'csrf-fixture',
      })
    })
    const { client } = mount({
      createConnectorEnrollmentToken: async () => ({
        tokenId: 'csrf-fixture', enrollmentToken: TOKEN, expiresAt: FUTURE_EXPIRY,
      }),
      cancelConnectorEnrollmentToken: cancel,
    })
    await screen.findByText('등록된 Connector가 없습니다.')
    fireEvent.change(screen.getByRole('textbox', { name: 'Connector 이름' }), {
      target: { value: 'Fixture VM' },
    })
    fireEvent.click(screen.getByRole('button', { name: '일회용 토큰 발급' }))
    expect(await screen.findByText(TOKEN)).toBeInTheDocument()

    fireEvent.click(screen.getByRole('button', { name: '등록 토큰 취소' }))
    expect(await screen.findByRole('alert')).toHaveTextContent('관리자 권한이 없거나 요청이 거부됐습니다.')
    expect(cancel).toHaveBeenCalledWith('csrf-fixture')
    expect(screen.getByText(TOKEN)).toBeInTheDocument()
    expect(screen.getByRole('button', { name: '토큰 복사' })).toBeInTheDocument()
    expect(screen.queryByText('등록 토큰을 취소했습니다.')).not.toBeInTheDocument()
    expect(client.getQueryData(labbitQueryKeys.me)).toMatchObject({ organizationRole: 'ADMIN' })
  })

  it('토큰 취소 403은 성공으로 위장하지 않고 안전한 안내를 표시한다', async () => {
    const cancel = vi.fn(async () => { throw new HttpError(403) })
    mount({
      createConnectorEnrollmentToken: async () => ({
        tokenId: 'issue-403', enrollmentToken: TOKEN,
        expiresAt: new Date(Date.now() + 15 * 60_000).toISOString(),
      }),
      cancelConnectorEnrollmentToken: cancel,
    })
    await screen.findByText('등록된 Connector가 없습니다.')
    fireEvent.change(screen.getByRole('textbox', { name: 'Connector 이름' }), {
      target: { value: 'Test-only VM' },
    })
    fireEvent.click(screen.getByRole('button', { name: '일회용 토큰 발급' }))
    await screen.findByText(TOKEN)
    fireEvent.click(screen.getByRole('button', { name: '등록 토큰 취소' }))

    expect(await screen.findByRole('alert')).toHaveTextContent('관리자 권한이 없거나 요청이 거부됐습니다.')
    expect(cancel).toHaveBeenCalledWith('issue-403')
    expect(screen.queryByText('등록 토큰을 취소했습니다.')).not.toBeInTheDocument()
  })

  it('Connector Revoke 403은 폐기 완료로 표시하지 않고 기존 목록을 유지한다', async () => {
    const revokeConnector = vi.fn(async () => { throw new HttpError(403) })
    vi.spyOn(window, 'confirm').mockReturnValue(true)
    mount({
      listConnectors: async () => ({
        observedAt: '2026-10-19T00:00:00Z',
        items: [{
          id: 'connector-403', name: 'Fixture connector',
          registrationStatus: 'REGISTERED' as const,
          connectionStatus: 'OFFLINE' as const,
          createdAt: '2026-10-19T00:00:00Z',
          revokedAt: null, lastSeenAt: null, lastConnectorVersion: null, lastRuntimeId: null,
        }],
      }),
      revokeConnector,
    })
    await screen.findByText('Control OFFLINE')
    fireEvent.click(screen.getByRole('button', { name: 'Connector 폐기' }))

    expect(await screen.findByRole('alert')).toHaveTextContent('관리자 권한이 없거나 요청이 거부됐습니다.')
    expect(revokeConnector).toHaveBeenCalledWith('connector-403')
    expect(screen.getByText('Fixture connector')).toBeInTheDocument()
    expect(screen.queryByText('Connector를 폐기했습니다. 상태를 다시 조회합니다.')).not.toBeInTheDocument()
  })

  it('토큰 취소 요청에서 세션 401이 나면 비밀값 화면을 닫고 로그인으로 이동한다', async () => {
    const cancel = vi.fn(async () => { throw new HttpError(401) })
    const { router, client } = mount({
      createConnectorEnrollmentToken: async () => ({
        tokenId: 'cancel-401',
        enrollmentToken: TOKEN,
        expiresAt: new Date(Date.now() + 15 * 60_000).toISOString(),
      }),
      cancelConnectorEnrollmentToken: cancel,
    })
    await screen.findByText('등록된 Connector가 없습니다.')
    fireEvent.change(screen.getByRole('textbox', { name: 'Connector 이름' }), {
      target: { value: 'Mock-only VM' },
    })
    fireEvent.click(screen.getByRole('button', { name: '일회용 토큰 발급' }))
    await screen.findByText(TOKEN)
    fireEvent.click(screen.getByRole('button', { name: '등록 토큰 취소' }))
    await waitFor(() => expect(router.state.location.pathname).toBe('/login'))
    expect(cancel).toHaveBeenCalledWith('cancel-401')
    expect(screen.queryByText(TOKEN)).not.toBeInTheDocument()
    expect(screen.queryByText('등록 토큰을 취소했습니다.')).not.toBeInTheDocument()
    expect(JSON.stringify(client.getMutationCache().getAll().map((mutation) => mutation.state.data))).not.toContain(TOKEN)
  })

  it('Connector Revoke 요청에서 세션 401이 나면 성공 표시 없이 로그인으로 이동한다', async () => {
    const revoke = vi.fn(async () => { throw new HttpError(401) })
    vi.spyOn(window, 'confirm').mockReturnValue(true)
    const { router } = mount({
      listConnectors: async () => ({
        observedAt: '2026-10-19T00:00:00Z',
        items: [{
          id: 'connector-session-expired',
          name: 'Fixture connector',
          registrationStatus: 'REGISTERED' as const,
          connectionStatus: 'OFFLINE' as const,
          createdAt: '2026-10-19T00:00:00Z',
          revokedAt: null, lastSeenAt: null, lastConnectorVersion: null, lastRuntimeId: null,
        }],
      }),
      revokeConnector: revoke,
    })
    await screen.findByText('Control OFFLINE')
    fireEvent.click(screen.getByRole('button', { name: 'Connector 폐기' }))
    await waitFor(() => expect(router.state.location.pathname).toBe('/login'))
    expect(revoke).toHaveBeenCalledWith('connector-session-expired')
    expect(screen.queryByText('Connector를 폐기했습니다. 상태를 다시 조회합니다.')).not.toBeInTheDocument()
  })

})
