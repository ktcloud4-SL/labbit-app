import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { act, fireEvent, render, screen, waitFor } from '@testing-library/react'
import { createMemoryRouter, RouterProvider } from 'react-router-dom'
import { afterEach, describe, expect, it, vi } from 'vitest'

import type { PreviewSession } from '../api/contracts'
import { HttpError } from '../api/httpClient'
import { LabbitApiProvider } from '../api/LabbitApiProvider'
import type { LabbitApi } from '../api/labbitApi'
import { mockLabbitApi } from '../api/mockLabbitApi'
import { PreviewPanel } from './PreviewPanel'

const URL_WITH_FIXTURE = 'https://preview-fixture.example.test/__labbit/bootstrap#fixture-only-not-a-secret'

function session(): PreviewSession {
  return {
    id: 'fixture-preview-1',
    targetPort: 3000,
    previewUrl: URL_WITH_FIXTURE,
    expiresAt: new Date(Date.now() + 300_000).toISOString(),
  }
}

function mount(overrides: Partial<LabbitApi> = {}) {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  })
  const createPreviewSession = vi.fn(async () => session())
  const closePreviewSession = vi.fn(async () => undefined)
  const api: LabbitApi = {
    ...mockLabbitApi,
    createPreviewSession,
    closePreviewSession,
    ...overrides,
  }
  const router = createMemoryRouter([
    { path: '/workspace', element: <PreviewPanel labInstanceId="fixture-lab" generation={1} /> },
    { path: '/classes', element: <div>수업 목록</div> },
    { path: '/login', element: <div>로그인 화면</div> },
  ], { initialEntries: ['/workspace'] })
  const view = render(
    <QueryClientProvider client={client}>
      <LabbitApiProvider api={api}><RouterProvider router={router} /></LabbitApiProvider>
    </QueryClientProvider>,
  )
  return { client, api, router, ...view }
}

function submitPort(port = '3000') {
  fireEvent.change(screen.getByRole('spinbutton', { name: '애플리케이션 포트' }), {
    target: { value: port },
  })
  fireEvent.click(screen.getByRole('button', { name: '미리보기 시작' }))
}

afterEach(() => {
  vi.restoreAllMocks()
  vi.unstubAllGlobals()
})

describe('LBT-26 PreviewSession Frontend consumer', () => {
  it('클릭 전 요청하지 않고 유효한 port만 생성 API에 보낸다', async () => {
    const { api } = mount()
    expect(api.createPreviewSession).not.toHaveBeenCalled()
    submitPort('65536')
    expect(await screen.findByRole('alert')).toHaveTextContent('1부터 65535')
    expect(api.createPreviewSession).not.toHaveBeenCalled()
    submitPort()
    await screen.findByText(/미리보기 세션이 준비되었습니다/)
    expect(api.createPreviewSession).toHaveBeenCalledWith('fixture-lab', { targetPort: 3000 })
  })

  it('201 성공 후 credential fragment를 DOM·React Query cache에 넣지 않고 한번만 새 탭으로 전달한다', async () => {
    const open = vi.spyOn(window, 'open').mockImplementation(() => null)
    const { client, container } = mount()
    submitPort()
    expect(await screen.findByText(/미리보기 세션이 준비되었습니다/)).toBeInTheDocument()
    expect(container.innerHTML).not.toContain(URL_WITH_FIXTURE)
    expect(container.innerHTML).not.toContain('#fixture-only-not-a-secret')
    expect(JSON.stringify(client.getQueryCache().getAll().map((query) => query.state.data))).not.toContain(URL_WITH_FIXTURE)
    expect(JSON.stringify(client.getMutationCache().getAll().map((mutation) => mutation.state.data))).not.toContain(URL_WITH_FIXTURE)
    fireEvent.click(screen.getByRole('button', { name: '미리보기 새 탭 열기' }))
    expect(open).toHaveBeenCalledWith(URL_WITH_FIXTURE, '_blank', 'noopener,noreferrer')
    expect(screen.getByRole('button', { name: '미리보기 새 탭 열기' })).toBeDisabled()
    expect(container.innerHTML).not.toContain(URL_WITH_FIXTURE)
  })

  it('로컬 HTTP Preview Origin은 Runtime Contract에 따라 허용하고 외부 HTTP Origin은 거부한다', async () => {
    expect(window.location.hostname).toBe('localhost')
    expect(window.location.protocol).toBe('http:')

    const { api } = mount({
      createPreviewSession: async () => ({
        ...session(),
        previewUrl: 'http://fixture-preview.localhost:8080/__labbit/bootstrap#fixture-local-only',
      }),
    })
    submitPort()
    expect(await screen.findByText(/미리보기 세션이 준비되었습니다/)).toBeInTheDocument()
    expect(api.closePreviewSession).not.toHaveBeenCalled()
    expect(screen.getByRole('button', { name: '미리보기 새 탭 열기' })).toBeEnabled()
  })

  it('명시 종료는 session ID만 전달하고 UI의 재생성 버튼을 복원한다', async () => {
    const { api } = mount()
    submitPort()
    await screen.findByText(/미리보기 세션이 준비되었습니다/)
    fireEvent.click(screen.getByRole('button', { name: '미리보기 종료' }))
    await waitFor(() => expect(api.closePreviewSession).toHaveBeenCalledWith('fixture-preview-1'))
    expect(screen.queryByText(/미리보기 세션이 준비되었습니다/)).not.toBeInTheDocument()
    expect(screen.getByRole('button', { name: '미리보기 시작' })).toBeEnabled()
  })

  it('명시 종료 404는 로컬 세션을 정리하고 새 미리보기를 생성할 수 있다', async () => {
    const createPreviewSession = vi.fn()
      .mockResolvedValueOnce(session())
      .mockResolvedValueOnce({ ...session(), id: 'fixture-preview-2' })
    const closePreviewSession = vi.fn(async () => { throw new HttpError(404) })
    mount({ createPreviewSession, closePreviewSession })

    submitPort()
    await screen.findByText(/미리보기 세션이 준비되었습니다/)
    fireEvent.click(screen.getByRole('button', { name: '미리보기 종료' }))

    await waitFor(() => expect(screen.getByRole('button', { name: '미리보기 시작' })).toBeEnabled())
    expect(closePreviewSession).toHaveBeenCalledWith('fixture-preview-1')
    expect(screen.queryByText(/미리보기 세션이 준비되었습니다/)).not.toBeInTheDocument()
    expect(screen.queryByRole('button', { name: '미리보기 새 탭 열기' })).not.toBeInTheDocument()
    expect(screen.queryByRole('alert')).not.toBeInTheDocument()
    expect(screen.getByRole('spinbutton', { name: '애플리케이션 포트' })).toBeEnabled()

    submitPort()
    await screen.findByText(/미리보기 세션이 준비되었습니다/)
    expect(createPreviewSession).toHaveBeenCalledTimes(2)
    expect(createPreviewSession).toHaveBeenLastCalledWith('fixture-lab', { targetPort: 3000 })
    expect(screen.getByRole('button', { name: '미리보기 새 탭 열기' })).toBeEnabled()
  })

  it('실행 중인 앱이 없는 502를 내부 메시지 노출 없이 설명한다', async () => {
    mount({
      createPreviewSession: async () => {
        throw new HttpError(502, {
          type: 'about:blank',
          title: 'private-runtime-output',
          status: 502,
          code: 'preview_app_not_running',
          requestId: 'fixture-request',
        })
      },
    })
    submitPort()
    expect(await screen.findByRole('alert')).toHaveTextContent('실행 중인 웹 애플리케이션이 없습니다')
    expect(screen.getByRole('alert')).not.toHaveTextContent('private-runtime-output')
  })

  it.each([
    {
      code: 'preview_unavailable',
      message: '현재 미리보기 기능을 사용할 수 없습니다. 계속 발생하면 운영 담당자에게 문의해 주세요.',
    },
    {
      code: 'preview_transport_unavailable',
      message: '실습 환경 Connector가 미리보기를 지원하지 않습니다. 운영 담당자에게 문의해 주세요.',
    },
    {
      code: 'connector_unavailable',
      message: '미리보기 연결 경로를 사용할 수 없습니다. 잠시 후 다시 시도해 주세요.',
    },
    {
      code: 'preview_open_failed',
      message: '미리보기 연결 경로를 사용할 수 없습니다. 잠시 후 다시 시도해 주세요.',
    },
    {
      code: 'unexpected_code',
      message: '미리보기 연결이 준비되지 않았습니다. 잠시 후 다시 시도해 주세요.',
    },
  ])('503 / $code는 계약에 맞게 안내하며 실패 응답에서 Preview URL·탭을 만들지 않는다', async ({ code, message }) => {
    const open = vi.spyOn(window, 'open').mockImplementation(() => null)
    const createPreviewSession = vi.fn(async () => {
      throw new HttpError(503, {
        type: 'about:blank',
        title: 'private-upstream-details',
        status: 503,
        code,
        requestId: 'fixture-request',
      })
    })
    const { api, container } = mount({ createPreviewSession })
    submitPort()
    const alert = await screen.findByRole('alert')
    expect(alert).toHaveTextContent(message)
    expect(alert).not.toHaveTextContent('private-upstream-details')
    expect(createPreviewSession).toHaveBeenCalledOnce()
    expect(api.closePreviewSession).not.toHaveBeenCalled()
    expect(screen.queryByRole('button', { name: '미리보기 새 탭 열기' })).not.toBeInTheDocument()
    expect(container.innerHTML).not.toContain(URL_WITH_FIXTURE)
    expect(open).not.toHaveBeenCalled()
  })

  it.each([
    { status: 403, code: 'preview_unavailable', expected: '미리보기를 사용할 권한이 없거나 포트가 거부됐습니다.' },
    { status: 409, code: 'preview_transport_unavailable', expected: '실습 환경이 준비되지 않았습니다. 상태를 확인해 주세요.' },
    { status: 404, code: 'connector_unavailable', expected: '실습 환경 또는 미리보기 세션을 찾을 수 없습니다.' },
  ])('503 전용 코드가 잘못 포함된 HTTP $status는 상태별 오류를 우선한다', async ({ status, code, expected }) => {
    const open = vi.spyOn(window, 'open').mockImplementation(() => null)
    const createPreviewSession = vi.fn(async () => {
      throw new HttpError(status, {
        type: 'about:blank', title: 'private-error-details', status, code, requestId: 'fixture-request',
      })
    })
    const { api, container } = mount({ createPreviewSession })
    submitPort()
    const alert = await screen.findByRole('alert')
    expect(alert).toHaveTextContent(expected)
    expect(alert).not.toHaveTextContent('private-error-details')
    expect(createPreviewSession).toHaveBeenCalledOnce()
    expect(api.closePreviewSession).not.toHaveBeenCalled()
    expect(open).not.toHaveBeenCalled()
    expect(container.innerHTML).not.toContain(URL_WITH_FIXTURE)
    expect(screen.queryByRole('button', { name: '미리보기 새 탭 열기' })).not.toBeInTheDocument()
  })

  it.each([
    { status: 403, code: 'preview_port_not_allowed', expected: '해당 포트는 미리보기에 사용할 수 없습니다.' },
    { status: 409, code: 'lab_instance_not_ready', expected: '실습 환경이 변경되었거나 준비되지 않았습니다.' },
    { status: 502, code: 'preview_app_not_running', expected: '해당 포트에서 실행 중인 웹 애플리케이션이 없습니다.' },
    { status: 504, code: 'preview_target_unreachable', expected: '실습 VM에 연결할 수 없습니다.' },
    { status: 504, code: 'preview_open_timeout', expected: '미리보기 연결 시간이 초과되었습니다.' },
    { status: 503, code: 'preview_port_not_allowed', expected: '미리보기 연결이 준비되지 않았습니다.' },
    { status: 503, code: 'lab_instance_not_ready', expected: '미리보기 연결이 준비되지 않았습니다.' },
    { status: 503, code: 'preview_app_not_running', expected: '미리보기 연결이 준비되지 않았습니다.' },
    { status: 503, code: 'preview_open_timeout', expected: '미리보기 연결이 준비되지 않았습니다.' },
    { status: 409, code: 'preview_app_not_running', expected: '실습 환경이 준비되지 않았습니다.' },
    { status: 502, code: 'preview_port_rejected', expected: '해당 포트의 애플리케이션이 응답하지 않습니다.' },
    { status: 403, code: 'preview_target_unreachable', expected: '미리보기를 사용할 권한이 없거나 포트가 거부됐습니다.' },
  ])('Problem Code / $code는 HTTP $status와 일치할 때만 원인별 메시지를 표시한다', async ({ status, code, expected }) => {
    const open = vi.spyOn(window, 'open').mockImplementation(() => null)
    const createPreviewSession = vi.fn(async () => {
      throw new HttpError(status, {
        type: 'about:blank',
        title: 'private-diagnostic-from-upstream',
        status,
        code,
        requestId: 'fixture-request',
      })
    })
    const { api, container } = mount({ createPreviewSession })
    submitPort()

    const alert = await screen.findByRole('alert')
    expect(alert).toHaveTextContent(expected)
    expect(alert).not.toHaveTextContent('private-diagnostic-from-upstream')
    expect(api.createPreviewSession).toHaveBeenCalledOnce()
    expect(api.closePreviewSession).not.toHaveBeenCalled()
    expect(open).not.toHaveBeenCalled()
    expect(container.innerHTML).not.toContain(URL_WITH_FIXTURE)
    expect(screen.queryByRole('button', { name: '미리보기 새 탭 열기' })).not.toBeInTheDocument()
  })

  it('401 인증 만료에서 저장된 URL 없이 로그인으로 이동한다', async () => {
    const { router, client } = mount({
      createPreviewSession: async () => { throw new HttpError(401) },
    })
    submitPort()
    await waitFor(() => expect(router.state.location.pathname).toBe('/login'))
    expect(screen.getByText('로그인 화면')).toBeInTheDocument()
    expect(JSON.stringify(client.getMutationCache().getAll().map((mutation) => mutation.state.data))).not.toContain(URL_WITH_FIXTURE)
  })

  it('유효하지 않은 previewUrl 응답은 열지 않고 서버 세션을 정리한다', async () => {
    const open = vi.spyOn(window, 'open').mockImplementation(() => null)
    const { api } = mount({
      createPreviewSession: async () => ({ ...session(), previewUrl: 'http://unsafe.example.test/' }),
    })
    submitPort()
    expect(await screen.findByRole('alert')).toHaveTextContent('안전하게 사용할 수 없습니다')
    expect(api.closePreviewSession).toHaveBeenCalledWith('fixture-preview-1')
    expect(open).not.toHaveBeenCalled()
    expect(screen.queryByRole('button', { name: '미리보기 새 탭 열기' })).not.toBeInTheDocument()
  })

  it('화면 이탈 후 늦게 온 생성 성공은 credential을 버리고 세션을 정리한다', async () => {
    let resolve!: (created: PreviewSession) => void
    const pending = new Promise<PreviewSession>((complete) => { resolve = complete })
    const close = vi.fn(async () => undefined)
    const { router, client } = mount({
      createPreviewSession: () => pending,
      closePreviewSession: close,
    })
    submitPort()
    expect(screen.getByRole('button', { name: '미리보기 연결 중...' })).toBeDisabled()
    await act(async () => { await router.navigate('/classes') })
    await act(async () => { resolve(session()); await pending })
    await waitFor(() => expect(close).toHaveBeenCalledWith('fixture-preview-1'))
    expect(screen.queryByText(/미리보기 세션이 준비되었습니다/)).not.toBeInTheDocument()
    expect(JSON.stringify(client.getQueryCache().getAll().map((query) => query.state.data))).not.toContain(URL_WITH_FIXTURE)
  })

  it('종료 403은 종료 성공으로 오인하지 않고 오류를 보여준다', async () => {
    mount({
      closePreviewSession: async () => { throw new HttpError(403) },
    })
    submitPort()
    await screen.findByText(/미리보기 세션이 준비되었습니다/)
    fireEvent.click(screen.getByRole('button', { name: '미리보기 종료' }))
    expect(await screen.findByRole('alert')).toHaveTextContent('권한이 없거나 포트가 거부')
    expect(screen.getByRole('button', { name: '미리보기 종료' })).toBeInTheDocument()
  })
})
