import { useMutation, useQueryClient } from '@tanstack/react-query'
import { type PropsWithChildren, useSyncExternalStore } from 'react'
import { Link, useNavigate } from 'react-router-dom'

import type { Me } from '../api/contracts'
import { HttpError } from '../api/httpClient'
import { useLabbitApi } from '../api/LabbitApiProvider'
import type { LoginLocationState } from '../routing/loginNavigation'
import { clearTerminalResumeState } from '../terminal/terminalResumeStorage'
import {
  getWorkspaceEditState,
  subscribeWorkspaceEditState,
} from '../workspace/workspaceEditState'

interface AppShellProps extends PropsWithChildren {
  me: Me
}

export function AppShell({ me, children }: AppShellProps) {
  const api = useLabbitApi()
  const queryClient = useQueryClient()
  const navigate = useNavigate()
  const workspaceEditState = useSyncExternalStore(
    subscribeWorkspaceEditState,
    getWorkspaceEditState,
    getWorkspaceEditState,
  )

  const logoutMutation = useMutation({
    mutationFn: async () => {
      clearTerminalResumeState()

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

  function requestLogout() {
    if (workspaceEditState.savePending) {
      window.alert('파일 저장이 끝난 뒤 로그아웃해 주세요.')
      return
    }

    if (
      workspaceEditState.dirty &&
      !window.confirm(
        '저장되지 않은 변경이 있습니다. 변경 내용을 버리고 로그아웃할까요?',
      )
    ) {
      return
    }

    logoutMutation.mutate()
  }

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
            onClick={requestLogout}
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
