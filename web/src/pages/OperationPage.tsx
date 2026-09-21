import { useQuery } from '@tanstack/react-query'
import { Link, Navigate, useLocation, useParams } from 'react-router-dom'

import type { Operation } from '../shared/api/contracts'
import { HttpError } from '../shared/api/httpClient'
import { useLabbitApi } from '../shared/api/LabbitApiProvider'
import { labbitQueryKeys } from '../shared/api/labbitApi'
import { ErrorState } from '../shared/ui/ErrorState'
import { LoadingState } from '../shared/ui/LoadingState'

const activeOperationStatuses = new Set(['PENDING', 'RUNNING', 'RECONCILING'])
const knownOperationStatuses = new Set([
  'PENDING',
  'RUNNING',
  'RECONCILING',
  'SUCCEEDED',
  'FAILED',
])

function statusLabel(status: string) {
  switch (status) {
    case 'PENDING':
      return '요청 대기 중'
    case 'RUNNING':
      return '작업 진행 중'
    case 'RECONCILING':
      return '실제 Provider 상태 확인 중'
    case 'SUCCEEDED':
      return '완료'
    case 'FAILED':
      return '실패'
    default:
      return `상태 확인 필요 · ${status}`
  }
}

function targetLink(operation: Operation) {
  if (operation.target.type === 'LAB_EXECUTION') {
    return `/lab-executions/${encodeURIComponent(operation.target.id)}`
  }

  return undefined
}

export function OperationPage() {
  const api = useLabbitApi()
  const location = useLocation()
  const { operationId } = useParams()
  const resolvedOperationId = operationId ?? ''

  const operationQuery = useQuery({
    queryKey: labbitQueryKeys.operation(resolvedOperationId),
    queryFn: () => api.getOperation(resolvedOperationId),
    enabled: Boolean(resolvedOperationId),
    retry: false,
    refetchInterval: (query) => {
      const status = query.state.data?.status
      return status && activeOperationStatuses.has(status) ? 2000 : false
    },
  })

  if (!resolvedOperationId) {
    return (
      <main className="app-page">
        <ErrorState message="Operation ID가 없습니다." />
      </main>
    )
  }

  if (operationQuery.isPending) {
    return (
      <main className="app-page">
        <LoadingState label="Operation 상태를 확인하는 중..." />
      </main>
    )
  }

  if (operationQuery.error instanceof HttpError && operationQuery.error.status === 401) {
    return <Navigate
      to="/login"
      replace
      state={{ from: `${location.pathname}${location.search}`, reason: 'sessionExpired' }}
    />
  }

  if (operationQuery.error instanceof HttpError && operationQuery.error.status === 403) {
    return (
      <main className="app-page">
        <ErrorState message="이 Operation을 볼 권한이 없습니다." />
      </main>
    )
  }

  if (operationQuery.error instanceof HttpError && operationQuery.error.status === 404) {
    return (
      <main className="app-page">
        <ErrorState message="Operation을 찾을 수 없습니다." />
      </main>
    )
  }

  if (operationQuery.error || !operationQuery.data) {
    return (
      <main className="app-page">
        <ErrorState message="Operation 상태를 불러오지 못했습니다." />
      </main>
    )
  }

  const operation = operationQuery.data
  const isReconciling = operation.status === 'RECONCILING'
  const isUnknownStatus = !knownOperationStatuses.has(operation.status)
  const link = targetLink(operation)

  return (
    <main className="app-page">
      <header className="page-header">
        <div>
          <Link className="back-link" to="/classes">
            ← 수업 목록
          </Link>
          <p className="eyebrow">Operation</p>
          <h1>{statusLabel(operation.status)}</h1>
          <p className="muted">
            {operation.type} · {operation.id}
          </p>
        </div>
        <span className="operation-status">{operation.status}</span>
      </header>

      {isReconciling && (
        <section className="notice-card notice-warning">
          <strong>같은 작업을 다시 실행하지 않고 Provider의 실제 상태를 확인하고 있습니다.</strong>
          <p className="muted">
            결과가 불명확한 동안 중복 Create/Delete를 보내지 않습니다.
          </p>
        </section>
      )}

      {isUnknownStatus && (
        <section className="notice-card notice-warning">
          <strong>알 수 없는 Operation 상태입니다.</strong>
          <p className="muted">
            새 상태가 추가되었을 수 있으므로 성공·실패를 임의로 판단하지 않으며 자동 polling도 중단합니다.
          </p>
          <button
            className="secondary-button"
            type="button"
            disabled={operationQuery.isFetching}
            onClick={() => void operationQuery.refetch()}
          >
            {operationQuery.isFetching ? '상태 확인 중...' : '상태 다시 확인'}
          </button>
        </section>
      )}

      <section className="detail-grid operation-grid">
        <article className="detail-card">
          <h2>작업 종류</h2>
          <strong>{operation.type}</strong>
        </article>
        <article className="detail-card">
          <h2>진행 단계</h2>
          <strong>{operation.stage ?? '확인 중'}</strong>
        </article>
        <article className="detail-card">
          <h2>대상</h2>
          <strong>{operation.target.type}</strong>
          <p className="muted">{operation.target.id}</p>
        </article>
      </section>

      {operation.error && (
        <section className="form-error">
          <strong>{operation.error.code}</strong>
          {operation.error.message && <p>{operation.error.message}</p>}
        </section>
      )}

      {operation.status === 'FAILED' && (
        <p className="muted">
          실패한 Operation은 대상 리소스의 실제 상태와 안전한 후속 조치를 함께 확인해야 합니다.
        </p>
      )}

      {link && (
        <Link className="primary-link inline-link" to={link}>
          대상 LabExecution 보기
        </Link>
      )}
    </main>
  )
}
