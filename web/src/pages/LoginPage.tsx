import { useMutation, useQueryClient } from '@tanstack/react-query'
import { useState, type FormEvent } from 'react'
import { useLocation, useNavigate } from 'react-router-dom'

import { HttpError } from '../shared/api/httpClient'
import { useLabbitApi } from '../shared/api/LabbitApiProvider'
import { labbitQueryKeys } from '../shared/api/labbitApi'

interface LoginLocationState {
  from?: string
  authRequired?: boolean
  signedOut?: boolean
}

export function LoginPage() {
  const api = useLabbitApi()
  const queryClient = useQueryClient()
  const navigate = useNavigate()
  const location = useLocation()
  const [username, setUsername] = useState('')
  const [password, setPassword] = useState('')
  const [validationError, setValidationError] = useState<string | null>(null)

  const loginMutation = useMutation({
    mutationFn: async () => {
      await api.login({ username, password })
      return api.getMe()
    },
    onSuccess: (me) => {
      queryClient.setQueryData(labbitQueryKeys.me, me)

      const state = location.state as LoginLocationState | null
      const destination =
        state?.from && state.from.startsWith('/') ? state.from : '/classes'

      navigate(destination, { replace: true })
    },
  })

  function handleSubmit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault()
    setValidationError(null)
    loginMutation.reset()

    if (!username || !password) {
      setValidationError('사용자 이름과 비밀번호를 모두 입력해 주세요.')
      return
    }

    loginMutation.mutate()
  }

  const loginError =
    loginMutation.error instanceof HttpError && loginMutation.error.status === 401
      ? '사용자 이름 또는 비밀번호를 확인해 주세요.'
      : loginMutation.error
        ? '로그인 요청을 처리하지 못했습니다. 잠시 후 다시 시도해 주세요.'
        : null

  const locationState = location.state as LoginLocationState | null
  const sessionNotice = locationState?.authRequired
    ? '세션이 만료되었거나 로그인이 필요합니다. 다시 로그인해 주세요.'
    : locationState?.signedOut
      ? '로그아웃했습니다.'
      : null

  return (
    <main className="login-shell">
      <section className="login-card" aria-labelledby="login-title">
        <div className="brand-mark" aria-hidden="true">
          L
        </div>
        <h1 id="login-title">Labbit에 로그인</h1>
        <p className="muted">사전 생성된 Local Account로 수업과 실습 환경에 접속합니다.</p>

        {sessionNotice && (
          <p className="login-notice" role="status">
            {sessionNotice}
          </p>
        )}

        <form className="login-form" onSubmit={handleSubmit}>
          <label className="field">
            <span>사용자 이름</span>
            <input
              name="username"
              autoComplete="username"
              value={username}
              onChange={(event) => setUsername(event.target.value)}
            />
          </label>

          <label className="field">
            <span>비밀번호</span>
            <input
              name="password"
              type="password"
              autoComplete="current-password"
              value={password}
              onChange={(event) => setPassword(event.target.value)}
            />
          </label>

          {(validationError || loginError) && (
            <p className="form-error" role="alert">
              {validationError ?? loginError}
            </p>
          )}

          <button className="primary-button" type="submit" disabled={loginMutation.isPending}>
            {loginMutation.isPending ? '로그인 중...' : '로그인'}
          </button>
        </form>

        {import.meta.env.DEV && (
          <p className="dev-hint">개발 Mock 계정: heechul / password</p>
        )}
      </section>
    </main>
  )
}
