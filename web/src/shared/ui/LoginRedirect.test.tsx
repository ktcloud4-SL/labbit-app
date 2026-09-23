import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { render, screen } from '@testing-library/react'
import { createMemoryRouter, RouterProvider, useLocation } from 'react-router-dom'
import { describe, expect, it } from 'vitest'

import type {
  LoginLocationState,
  LoginReason,
} from '../routing/loginNavigation'
import { LoginRedirect } from './LoginRedirect'

function LoginStateProbe() {
  const location = useLocation()
  const state = location.state as LoginLocationState | null

  return (
    <div>
      <span data-testid="pathname">{location.pathname}</span>
      <span data-testid="from">{state?.from}</span>
      <span data-testid="reason">{state?.reason}</span>
    </div>
  )
}

describe('LoginRedirect', () => {
  it.each<LoginReason>(['authRequired', 'sessionExpired'])(
    '%s 상태에서 사용자 query cache를 제거하고 기존 경로를 보존한 채 Login으로 replace 이동한다',
    async (reason) => {
      const queryClient = new QueryClient()
      queryClient.setQueryData(['previous-user-sensitive-data'], {
        className: 'previous-user-class',
      })

      const router = createMemoryRouter(
        [
          {
            path: '/classes/:classId',
            element: <LoginRedirect reason={reason} />,
          },
          {
            path: '/login',
            element: <LoginStateProbe />,
          },
        ],
        {
          initialEntries: ['/classes/class-a?tab=workspace'],
        },
      )

      render(
        <QueryClientProvider client={queryClient}>
          <RouterProvider router={router} />
        </QueryClientProvider>,
      )

      expect(await screen.findByTestId('pathname')).toHaveTextContent('/login')
      expect(screen.getByTestId('from')).toHaveTextContent(
        '/classes/class-a?tab=workspace',
      )
      expect(screen.getByTestId('reason')).toHaveTextContent(reason)
      expect(router.state.historyAction).toBe('REPLACE')
      expect(
        queryClient.getQueryData(['previous-user-sensitive-data']),
      ).toBeUndefined()
    },
  )
})
