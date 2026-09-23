import { useMutation, useQueryClient } from '@tanstack/react-query'
import type { PropsWithChildren } from 'react'
import { Link, useNavigate } from 'react-router-dom'

import type { Me } from '../api/contracts'
import { HttpError } from '../api/httpClient'
import { useLabbitApi } from '../api/LabbitApiProvider'
import type { LoginLocationState } from '../routing/loginNavigation'

interface AppShellProps extends PropsWithChildren {
  me: Me
}

export function AppShell({ me, children }: AppShellProps) {
  const api = useLabbitApi()
  const queryClient = useQueryClient()
  const navigate = useNavigate()

  const logoutMutation = useMutation({
    mutationFn: async () => {
      try {
        await api.logout()
      } catch (error) {
        if (error instanceof HttpError && error.status === 401) {
          return
        }
        throw error
      }
    },
    onSuccess: () => {
      queryClient.clear()
      const state: LoginLocationState = { signedOut: true }
      navigate('/login', { replace: true, state })
    },
  })

  return (
    <div className="app-shell">
      <header className="app-topbar">
        <div className="app-topbar-main">
          <Link className="app-brand" to="/classes">
            Labbit
          </Link>
          <nav className="app-nav" aria-label="주요 메뉴">
            <Link to="/classes">수업</Link>
          </nav>
        </div>

        <div className="app-user">
          <div className="app-user-context">
            <strong>{me.username}</strong>
            <span>{me.organization.name}</span>
          </div>
          <button
            className="secondary-button"
            type="button"
            disabled={logoutMutation.isPending}
            onClick={() => logoutMutation.mutate()}
          >
            {logoutMutation.isPending ? '로그아웃 중...' : '로그아웃'}
          </button>
        </div>
      </header>

      {logoutMutation.error && (
        <div className="app-shell-alert form-error" role="alert">
          로그아웃 요청을 처리하지 못했습니다. 잠시 후 다시 시도해 주세요.
        </div>
      )}

      {children}
    </div>
  )
}
