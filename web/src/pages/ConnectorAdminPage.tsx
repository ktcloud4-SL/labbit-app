import { useQuery, useQueryClient } from '@tanstack/react-query'
import { useEffect, useRef, useState, type FormEvent } from 'react'
import { Link } from 'react-router-dom'

import { ConnectorEnrollmentTokenPanel } from './ConnectorEnrollmentTokenPanel'
import { ConnectorInstallGuide } from './ConnectorInstallGuide'
import type { ConnectorEnrollmentTokenIssued } from '../shared/api/contracts'
import { HttpError } from '../shared/api/httpClient'
import { useLabbitApi } from '../shared/api/LabbitApiProvider'
import { labbitQueryKeys } from '../shared/api/labbitApi'
import { ErrorState } from '../shared/ui/ErrorState'
import { LoadingState } from '../shared/ui/LoadingState'
import { LoginRedirect } from '../shared/ui/LoginRedirect'

function statusLabel(status: string) {
  switch (status) {
    case 'NEVER_SEEN': return '등록됨 · Heartbeat 대기'
    case 'ONLINE': return 'Control ONLINE'
    case 'OFFLINE': return 'Control OFFLINE'
    case 'REVOKED': return '폐기됨'
    default: return '알 수 없는 상태'
  }
}

function errorMessage(error: unknown, action: string) {
  if (error instanceof HttpError) {
    if (error.status === 403) return '관리자 권한이 없거나 요청이 거부됐습니다.'
    if (error.status === 404) return '대상을 찾지 못했습니다. 목록을 새로고침해 주세요.'
    if (error.status === 409) return '이미 사용된 등록 토큰입니다. Connector 목록을 확인하고 필요한 경우 폐기한 뒤 새로 발급하세요.'
    if (error.status === 400 || error.status === 422) return '요청 값을 확인해 주세요.'
  }
  return `${action}에 실패했습니다. 연결 상태를 확인한 뒤 다시 시도해 주세요.`
}

/**
 * LBT-183 admin consumer for LBT-182 OpenAPI.
 * Enrollment secret must never enter a Query cache, URL, persistent storage,
 * telemetry, or an error message. The one-time secret lives only in page state.
 */
export function ConnectorAdminPage() {
  const api = useLabbitApi()
  const queryClient = useQueryClient()
  const meQuery = useQuery({
    queryKey: labbitQueryKeys.me,
    queryFn: () => api.getMe(),
    retry: false,
  })
  const me = meQuery.data
  const isAdmin = me?.organizationRole === 'ADMIN'
  const connectorsKey = ['connectors', me?.organization.id, me?.id] as const
  const connectorsQuery = useQuery({
    queryKey: connectorsKey,
    queryFn: () => api.listConnectors(),
    enabled: isAdmin,
    retry: false,
    // Server computes ONLINE/OFFLINE from real heartbeat; never infer from HELLO.
    // Poll only while the page is active, preserving the observedAt timestamp.
    refetchInterval: 15_000,
  })

  const [name, setName] = useState('')
  const [issued, setIssued] = useState<ConnectorEnrollmentTokenIssued | null>(null)
  const [pending, setPending] = useState<'issue' | 'cancel' | string | null>(null)
  const [actionError, setActionError] = useState<string | null>(null)
  const [actionNotice, setActionNotice] = useState<string | null>(null)
  const [authExpired, setAuthExpired] = useState(false)
  const requestEpoch = useRef(0)
  const busy = pending !== null

  // Each unmount invalidates late issuance: it cannot re-expose a secret in
  // a different screen after logout or navigation.
  useEffect(() => () => { requestEpoch.current += 1 }, [])

  useEffect(() => {
    if (!issued) return
    const expiresAt = Date.parse(issued.expiresAt)
    // Invalid/expired response timestamps were already rejected in issueToken.
    if (!Number.isFinite(expiresAt)) return
    const expireToken = () => {
      if (Date.now() >= expiresAt) {
        // Drop the secret from React state. Server-side expiry is authoritative.
        // The browser must never attempt token renewal or consumption.
        setIssued(null)
        setActionNotice('등록 토큰이 만료되었습니다. 필요한 경우 새 토큰을 발급해 주세요.')
      }
    }
    const timeout = window.setTimeout(expireToken, Math.max(0, expiresAt - Date.now()))
    window.addEventListener('focus', expireToken)
    document.addEventListener('visibilitychange', expireToken)
    return () => {
      window.clearTimeout(timeout)
      window.removeEventListener('focus', expireToken)
      document.removeEventListener('visibilitychange', expireToken)
    }
  }, [issued])

  async function issueToken(event: FormEvent<HTMLFormElement>) {
    event.preventDefault()
    if (busy || issued) return
    if (!name.trim() || name.length > 200) {
      setActionError('Connector 이름을 1~200자로 입력해 주세요.')
      return
    }
    const epoch = ++requestEpoch.current
    setPending('issue')
    setActionError(null)
    setActionNotice(null)
    try {
      // Do not use useMutation here: Query's mutation cache could retain the raw token.
      const result = await api.createConnectorEnrollmentToken(name)
      if (requestEpoch.current !== epoch) return
      if (!Number.isFinite(Date.parse(result.expiresAt)) || Date.parse(result.expiresAt) <= Date.now()) {
        setActionError('발급된 토큰의 만료 시각이 유효하지 않습니다. 새 토큰을 발급해 주세요.')
        return
      }
      setIssued(result)
      setName('')
    } catch (error) {
      if (requestEpoch.current !== epoch) return
      if (error instanceof HttpError && error.status === 401) setAuthExpired(true)
      else setActionError(errorMessage(error, '등록 토큰 발급'))
    } finally {
      if (requestEpoch.current === epoch) setPending(null)
    }
  }

  async function cancelToken() {
    if (busy || !issued) return
    const epoch = ++requestEpoch.current
    setPending('cancel')
    setActionError(null)
    setActionNotice(null)
    try {
      await api.cancelConnectorEnrollmentToken(issued.tokenId)
      if (requestEpoch.current !== epoch) return
      setIssued(null)
      setActionNotice('등록 토큰을 취소했습니다.')
    } catch (error) {
      if (requestEpoch.current !== epoch) return
      if (error instanceof HttpError && error.status === 401) setAuthExpired(true)
      else setActionError(errorMessage(error, '등록 토큰 취소'))
    } finally {
      if (requestEpoch.current === epoch) setPending(null)
    }
  }

  async function revoke(connectorId: string, connectorName: string) {
    if (busy || !window.confirm(`"${connectorName}" Connector를 폐기하시겠습니까? 이 작업은 되돌릴 수 없습니다.`)) return
    const epoch = ++requestEpoch.current
    setPending(connectorId)
    setActionError(null)
    setActionNotice(null)
    try {
      await api.revokeConnector(connectorId)
      if (requestEpoch.current !== epoch) return
      setActionNotice('Connector를 폐기했습니다. 상태를 다시 조회합니다.')
      await queryClient.invalidateQueries({ queryKey: connectorsKey })
    } catch (error) {
      if (requestEpoch.current !== epoch) return
      if (error instanceof HttpError && error.status === 401) setAuthExpired(true)
      else setActionError(errorMessage(error, 'Connector 폐기'))
    } finally {
      if (requestEpoch.current === epoch) setPending(null)
    }
  }

  if (authExpired ||
    (meQuery.error instanceof HttpError && meQuery.error.status === 401) ||
    (connectorsQuery.error instanceof HttpError && connectorsQuery.error.status === 401)) {
    return <LoginRedirect reason="sessionExpired" />
  }

  if (meQuery.isPending) {
    return <main className="app-page"><LoadingState label="관리자 권한을 확인하는 중..." /></main>
  }

  if (meQuery.error || !me) {
    return <main className="app-page"><ErrorState message="로그인 상태를 확인하지 못했습니다." /></main>
  }

  if (!isAdmin) {
    return <main className="app-page">
      <h1>접근 권한 없음</h1>
      <p role="alert">Connector 관리는 Organization ADMIN만 이용할 수 있습니다.</p>
      <Link to="/classes">수업 목록으로 돌아가기</Link>
    </main>
  }

  return (
    <main className="app-page">
      <header className="page-header">
        <div>
          <Link className="back-link" to="/classes">← 수업 목록</Link>
          <p className="eyebrow">Organization ADMIN</p>
          <h1>Connector 관리</h1>
          <p className="muted">SaaS Control 연결과 Provider 준비는 별개입니다. 등록 토큰 발급만으로 Connector가 등록되지는 않습니다.</p>
        </div>
      </header>

      <section className="class-card" aria-labelledby="connector-token-title" style={{ marginBottom: 16 }}>
        <h2 id="connector-token-title">Connector 등록 토큰 발급</h2>
        {issued ? (
          <>
            <ConnectorEnrollmentTokenPanel
              token={issued.enrollmentToken}
              expiresAt={issued.expiresAt}
              onClose={() => { if (!busy) setIssued(null) }}
            />
            <p className="muted">취소하지 않고 화면을 닫으면 서버에서 만료될 때까지 미사용 토큰이 유효할 수 있습니다.</p>
            <button type="button" className="secondary-button" disabled={busy} onClick={() => void cancelToken()}>
              {pending === 'cancel' ? '취소 중...' : '등록 토큰 취소'}
            </button>
          </>
        ) : (
          <form className="login-form" onSubmit={(event) => void issueToken(event)}>
            <label className="field">
              Connector 이름
              <input
                value={name}
                onChange={(event) => setName(event.target.value)}
                minLength={1}
                maxLength={200}
                required
                disabled={busy}
                placeholder="고객사 관리 VM 구분 이름"
              />
            </label>
            <div>
              <button type="submit" className="primary-button" disabled={busy || !name.trim()}>
                {pending === 'issue' ? '발급 중...' : '일회용 토큰 발급'}
              </button>
            </div>
          </form>
        )}
        {actionError && <p className="form-error" role="alert">{actionError}</p>}
        {actionNotice && <p role="status">{actionNotice}</p>}
      </section>

      <section aria-labelledby="connector-list-title" style={{ marginBottom: 20 }}>
        <div className="page-header" style={{ alignItems: 'center', marginBottom: 14 }}>
          <h2 id="connector-list-title">등록된 Connector</h2>
          <button type="button" className="secondary-button"
            onClick={() => void connectorsQuery.refetch()}
            disabled={connectorsQuery.isFetching}
          >{connectorsQuery.isFetching ? '조회 중...' : '상태 새로고침'}</button>
        </div>
        <p className="muted">화면이 활성화된 동안 15초 간격으로 서버 상태를 확인합니다. 표시된 ONLINE은 마지막 서버 관측값입니다.</p>
        {connectorsQuery.isPending ? (
          <LoadingState label="Connector 목록을 불러오는 중..." />
        ) : connectorsQuery.error ? (
          <ErrorState message={
            connectorsQuery.error instanceof HttpError && connectorsQuery.error.status === 403
              ? 'Connector 목록에 접근할 권한이 없습니다.'
              : 'Connector 상태를 불러오지 못했습니다.'
          } />
        ) : !connectorsQuery.data || connectorsQuery.data.items.length === 0 ? (
          <div className="empty-state">
            <h3>등록된 Connector가 없습니다.</h3>
            <p>발급된 토큰은 실제 Installer 등록이 완료되기 전까지 목록에 포함되지 않습니다.</p>
          </div>
        ) : (
          <>
            <p className="muted">서버 상태 관측 시각: {connectorsQuery.data.observedAt}</p>
            <div className="class-grid">
              {connectorsQuery.data.items.map((connector) => (
                <article className="class-card" key={connector.id}>
                  <h3 style={{ overflowWrap: 'anywhere' }}>{connector.name}</h3>
                  <p><strong>{statusLabel(connector.connectionStatus)}</strong></p>
                  <p className="muted">등록: {connector.registrationStatus}</p>
                  <p className="muted">마지막 Heartbeat: {connector.lastSeenAt ?? '없음'}</p>
                  <p className="muted">Connector Version: {connector.lastConnectorVersion ?? '미확인'}</p>
                  <p className="muted">Provider READY: 미확인 (별도 검증 필요)</p>
                  {connector.registrationStatus !== 'REVOKED' && (
                    <button className="secondary-button" type="button"
                      disabled={busy}
                      onClick={() => void revoke(connector.id, connector.name)}
                    >{pending === connector.id ? '폐기 중...' : 'Connector 폐기'}</button>
                  )}
                </article>
              ))}
            </div>
          </>
        )}
      </section>
      <ConnectorInstallGuide />
    </main>
  )
}
