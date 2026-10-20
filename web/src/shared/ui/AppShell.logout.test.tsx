import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { fireEvent, render, screen } from '@testing-library/react'
import { createMemoryRouter, RouterProvider } from 'react-router-dom'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import { LabbitApiProvider } from '../api/LabbitApiProvider'
import { mockLabbitApi, mockMe } from '../api/mockLabbitApi'
import { HttpError } from '../api/httpClient'
import { saveTerminalResumeState } from '../terminal/terminalResumeStorage'
import { clearWorkspaceEditState } from '../workspace/workspaceEditState'
import { AppShell } from './AppShell'

const storageKey = 'labbit.terminal.resume.v1'

function setup(logout: () => Promise<void>) {
  const api = { ...mockLabbitApi, logout: vi.fn(logout) }
  const queryClient = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  })
  queryClient.setQueryData(['classes'], { items: [{ id: 'class-a' }] })

  const router = createMemoryRouter(
    [
      {
        path: '/classes',
        element: <AppShell me={mockMe}><p>로그인된 수업 화면</p></AppShell>,
      },
      { path: '/login', element: <p>로그인 화면</p> },
    ],
    { initialEntries: ['/classes'] },
  )

  render(
    <QueryClientProvider client={queryClient}>
      <LabbitApiProvider api={api}>
        <RouterProvider router={router} />
      </LabbitApiProvider>
    </QueryClientProvider>,
  )

  return { api, queryClient, router }
}

describe('AppShell logout and terminal resume', () => {
  beforeEach(() => {
    window.sessionStorage.clear()
    clearWorkspaceEditState()
    saveTerminalResumeState({
      labInstanceId: 'lab-instance-test',
      generation: 1,
      terminalSessionId: 'terminal-session-test',
      sessionToken: 'synthetic-test-token',
      tokenExpiresAt: '2099-01-01T00:00:00Z',
    })
  })

  afterEach(() => {
    clearWorkspaceEditState()
    window.sessionStorage.clear()
    vi.restoreAllMocks()
  })

  it('503 등 일시적 로그아웃 실패 시 로그인된 화면과 Terminal resume state를 유지한다', async () => {
    const { api, queryClient, router } = setup(async () => {
      throw new HttpError(503)
    })

    fireEvent.click(screen.getByRole('button', { name: '로그아웃' }))

    expect(await screen.findByRole('alert')).toHaveTextContent(
      '로그아웃 요청을 처리하지 못했습니다.',
    )
    expect(api.logout).toHaveBeenCalledTimes(1)
    expect(router.state.location.pathname).toBe('/classes')
    expect(screen.getByText('로그인된 수업 화면')).toBeInTheDocument()
    expect(window.sessionStorage.getItem(storageKey)).not.toBeNull()
    expect(queryClient.getQueryData(['classes'])).toBeDefined()
  })

  it('정상 로그아웃 성공 시 Terminal resume state와 query cache를 지우고 로그인으로 이동한다', async () => {
    const { queryClient, router } = setup(async () => {})

    fireEvent.click(screen.getByRole('button', { name: '로그아웃' }))

    expect(await screen.findByText('로그인 화면')).toBeInTheDocument()
    expect(router.state.location.pathname).toBe('/login')
    expect(router.state.historyAction).toBe('REPLACE')
    expect(window.sessionStorage.getItem(storageKey)).toBeNull()
    expect(queryClient.getQueryData(['classes'])).toBeUndefined()
  })

  it('서버가 401로 이미 만료된 세션을 알리면 안전하게 로컬 정보 제거 후 로그인으로 이동한다', async () => {
    const { router, queryClient } = setup(async () => {
      throw new HttpError(401)
    })

    fireEvent.click(screen.getByRole('button', { name: '로그아웃' }))

    expect(await screen.findByText('로그인 화면')).toBeInTheDocument()
    expect(router.state.location.pathname).toBe('/login')
    expect(window.sessionStorage.getItem(storageKey)).toBeNull()
    expect(queryClient.getQueryData(['classes'])).toBeUndefined()
  })
})
