import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { fireEvent, render, screen } from '@testing-library/react'
import { createMemoryRouter, RouterProvider } from 'react-router-dom'
import { describe, expect, it, vi } from 'vitest'

import { HttpError } from '../shared/api/httpClient'
import { LabbitApiProvider } from '../shared/api/LabbitApiProvider'
import { labbitQueryKeys } from '../shared/api/labbitApi'
import { mockLabbitApi, mockMe } from '../shared/api/mockLabbitApi'
import { LoginPage } from './LoginPage'

describe('LoginPage', () => {
  it('실제 HTTP consumer가 401을 반환하면 자격 증명 오류로 안내한다', async () => {
    const queryClient = new QueryClient()
    const api = {
      ...mockLabbitApi,
      login: async () => {
        throw new HttpError(401)
      },
    }

    const router = createMemoryRouter(
      [
        {
          path: '/login',
          element: <LoginPage />,
        },
      ],
      {
        initialEntries: ['/login'],
      },
    )

    render(
      <QueryClientProvider client={queryClient}>
        <LabbitApiProvider api={api}>
          <RouterProvider router={router} />
        </LabbitApiProvider>
      </QueryClientProvider>,
    )

    fireEvent.change(screen.getByLabelText('사용자 이름'), {
      target: { value: 'unknown-user' },
    })
    fireEvent.change(screen.getByLabelText('비밀번호'), {
      target: { value: 'wrong-password' },
    })
    fireEvent.click(screen.getByRole('button', { name: '로그인' }))

    expect(
      await screen.findByText('사용자 이름 또는 비밀번호를 확인해 주세요.'),
    ).toBeInTheDocument()
  })

  it('직접 재로그인 성공 시 이전 사용자 query cache를 제거하고 새 me만 저장한다', async () => {
    const queryClient = new QueryClient()
    queryClient.setQueryData(labbitQueryKeys.me, {
      ...mockMe,
      id: 'user-previous',
      username: 'previous-user',
    })
    queryClient.setQueryData(labbitQueryKeys.classes, {
      items: [{ id: 'class-previous-user' }],
    })
    queryClient.setQueryData(labbitQueryKeys.labSpecs, {
      items: [{ id: 'lab-spec-previous-user' }],
    })

    const nextMe = {
      ...mockMe,
      id: 'user-next',
      username: 'next-user',
    }
    const login = vi.fn(async () => {})
    const api = {
      ...mockLabbitApi,
      login,
      getMe: async () => nextMe,
    }

    const router = createMemoryRouter(
      [
        {
          path: '/login',
          element: <LoginPage />,
        },
        {
          path: '/classes',
          element: <div>Class 목록</div>,
        },
      ],
      {
        initialEntries: ['/login'],
      },
    )

    render(
      <QueryClientProvider client={queryClient}>
        <LabbitApiProvider api={api}>
          <RouterProvider router={router} />
        </LabbitApiProvider>
      </QueryClientProvider>,
    )

    fireEvent.change(screen.getByLabelText('사용자 이름'), {
      target: { value: 'next-user' },
    })
    fireEvent.change(screen.getByLabelText('비밀번호'), {
      target: { value: 'password' },
    })
    fireEvent.click(screen.getByRole('button', { name: '로그인' }))

    expect(await screen.findByText('Class 목록')).toBeInTheDocument()
    expect(login).toHaveBeenCalledWith({
      username: 'next-user',
      password: 'password',
    })
    expect(queryClient.getQueryData(labbitQueryKeys.me)).toEqual(nextMe)
    expect(queryClient.getQueryData(labbitQueryKeys.classes)).toBeUndefined()
    expect(queryClient.getQueryData(labbitQueryKeys.labSpecs)).toBeUndefined()
  })
})
