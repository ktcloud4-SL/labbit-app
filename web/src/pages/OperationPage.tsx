import { useQuery } from '@tanstack/react-query'
import { Link, useParams } from 'react-router-dom'

import type { Operation } from '../shared/api/contracts'
import { HttpError } from '../shared/api/httpClient'
import { useLabbitApi } from '../shared/api/LabbitApiProvider'
import { labbitQueryKeys } from '../shared/api/labbitApi'
import { LoginRedirect } from '../shared/ui/LoginRedirect'
import { ErrorState } from '../shared/ui/ErrorState'
import { LoadingState } from '../shared/ui/LoadingState'

const activeOperationStatuses = new Set(['PENDING', 'RUNNING', 'RECONCILING'])

// The public Operation view omits internal reconciliation error codes.
// Refresh RECONCILING conservatively without inferring an item-level HOLD.
export function operationPollingInterval(operation: Pick<Operation, 'status'> | undefined) {
  if (!operation || !activeOperationStatuses.has(operation.status)) return false
  return operation.status === 'RECONCILING' ? 15_000 : 2000
}

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

function statusPillClass(status: string) {
  if (status === 'SUCCEEDED') return 'status-pill status-pill-success'
  if (status === 'FAILED') return 'status-pill status-pill-error'
  if (status === 'RUNNING' || status === 'PENDING' || status === 'RECONCILING') {
    return 'status-pill status-pill-progress'
  }
  return 'status-pill status-pill-neutral'
}

function stageLabel(stage: string | undefined) {
  switch (stage) {
    case 'DEFERRED':
      return '재시도 대기 (DEFERRED)'
    case 'PROVISIONING':
      return '환경 생성 중'
    case 'RESETTING':
      return '환경 초기화 중'
    case 'CLEANING_UP':
      return '환경 정리 중'
    case 'VERIFY_PROVIDER_STATE':
      return '실제 상태 확인 중'
    case 'VERIFYING_READINESS':
      return 'Workspace 사용 가능 상태 확인 중'
    case 'RECONCILING':
      return 'Provider 상태 대조 중'
    case 'HOLD':
      return '운영 담당자 확인 필요 (HOLD)'
    default:
      return stage ?? '확인 중'
  }
}

function operationTypeLabel(type: string) {
  switch (type) {
    case 'PROVISION':
      return '실습 환경 생성'
    case 'RESET':
      return '실습 환경 초기화'
    case 'CLEANUP':
      return '실습 환경 정리'
    default:
      return type
  }
}

function targetTypeLabel(type: string) {
  if (type === 'LAB_EXECUTION') return '실습 실행'
  if (type === 'LAB_INSTANCE') return '개별 실습 환경'
  return type
}

function targetLink(operation: Operation) {
  if (operation.target.type === 'LAB_EXECUTION') {
    return `/lab-executions/${encodeURIComponent(operation.target.id)}`
  }

  return undefined
}

export function OperationPage() {
  const api = useLabbitApi()
  const { operationId } = useParams()
  const resolvedOperationId = operationId ?? ''

  const operationQuery = useQuery({
    queryKey: labbitQueryKeys.operation(resolvedOperationId),
    queryFn: () => api.getOperation(resolvedOperationId),
    enabled: Boolean(resolvedOperationId),
    retry: false,
    refetchInterval: (query) => operationPollingInterval(query.state.data),
  })

  if (!resolvedOperationId) {
    return (
      <main className="app-page">
        <ErrorState message="작업 ID가 없습니다." />
      </main>
    )
  }

  if (operationQuery.isPending) {
    return (
      <main className="app-page">
        <LoadingState label="작업 상태를 확인하는 중..." />
      </main>
    )
  }

  if (operationQuery.error instanceof HttpError && operationQuery.error.status === 401) {
    return <LoginRedirect reason="sessionExpired" />
  }

  if (operationQuery.error instanceof HttpError && operationQuery.error.status === 403) {
    return (
      <main className="app-page">
        <ErrorState message="이 작업을 볼 권한이 없습니다." />
      </main>
    )
  }

  if (operationQuery.error instanceof HttpError && operationQuery.error.status === 404) {
    return (
      <main className="app-page">
        <ErrorState message="작업을 찾을 수 없습니다." />
      </main>
    )
  }

  if (!operationQuery.data) {
    return (
      <main className="app-page">
        <ErrorState message="작업 상태를 불러오지 못했습니다. 서버 작업을 재실행하지 말고 상태만 다시 확인해 주세요." />
        <button
          className="secondary-button"
          type="button"
          disabled={operationQuery.isFetching}
          onClick={() => void operationQuery.refetch()}
        >
          {operationQuery.isFetching ? '상태 확인 중...' : '상태 다시 확인'}
        </button>
      </main>
    )
  }

  const operation = operationQuery.data
  const isDeferredPending = operation.status === 'PENDING' && operation.stage === 'DEFERRED' && !operationQuery.error
  const isReconciling = operation.status === 'RECONCILING'
  const isUnknownStatus = !knownOperationStatuses.has(operation.status)
  const link = targetLink(operation)
  // Provider SUCCEEDED does not prove that the Workspace reached READY.
  // A legacy Connector can report success without readiness attestation.
  const awaitingReadiness =
    (operation.type === 'PROVISION' || operation.type === 'RESET') &&
    operation.status === 'SUCCEEDED' &&
    operation.stage === 'VERIFYING_READINESS'
  const visibleStatusLabel = awaitingReadiness
    ? 'Provider 작업 완료 · READY 확인 중'
    : statusLabel(operation.status)

  return (
    <main className="app-page">
      <header className="page-header page-header-spacious">
        <div>
          <Link className="back-link" to="/classes">
            ← 수업 목록
          </Link>
          <p className="eyebrow">작업 상태</p>
          <h1>{visibleStatusLabel}</h1>
          <p className="muted">
            {operationTypeLabel(operation.type)} · {operation.id}
          </p>
        </div>
        <span className={awaitingReadiness ? 'status-pill status-pill-progress' : statusPillClass(operation.status)}>
          {visibleStatusLabel}
        </span>
      </header>

      {operationQuery.error && (
        <section className="notice-card notice-warning" role="alert">
          <strong>작업 상태를 일시적으로 확인하지 못했습니다.</strong>
          <p className="muted">
            아래 상태는 마지막으로 확인한 정보이며 최신 결과가 아닐 수 있습니다.
            Provision·Reset·Cleanup 요청을 다시 실행하지 말고 상태 조회만 재시도해 주세요.
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

      {isDeferredPending && (
        <section className="notice-card notice-warning" role="status">
          <strong>요청이 접수됐지만 작업이 재시도 대기 상태입니다.</strong>
          <p className="muted">
            연결 또는 실행 환경 문제로 대기할 수 있지만, 이 화면만으로 정확한 원인은 알 수 없습니다.
            같은 Provision·Reset·Cleanup 요청을 반복하지 마세요.
            대기가 길어지면 작업 ID를 담당자에게 전달해 상태를 확인해 주세요.
          </p>
        </section>
      )}

      {isReconciling && (
        <section className="notice-card notice-warning" role="status">
          <strong>같은 작업을 다시 실행하지 않고 Provider의 실제 상태를 확인하고 있습니다.</strong>
          <p className="muted">
            RECONCILING은 실제 자원 결과를 대조하는 상태이며 자동 완료를 보장하지 않습니다.
            이 상태가 지속되면 위 작업 ID를 운영 담당자에게 전달해 확인을 요청하세요.
            Provision·Reset·Cleanup을 반복 요청하거나 Provider 자원을 직접 삭제하지 마세요.
          </p>
          {!operationQuery.error && (
            <button
              className="secondary-button"
              type="button"
              disabled={operationQuery.isFetching}
              onClick={() => void operationQuery.refetch()}
            >
              {operationQuery.isFetching ? '상태 확인 중...' : '상태 다시 확인'}
            </button>
          )}
        </section>
      )}

      {awaitingReadiness && (
        <section className="notice-card notice-warning" role="status">
          <strong>Provider 작업은 완료됐지만 실습 환경 READY는 아직 확인되지 않았습니다.</strong>
          <p className="muted">
            Workspace 입장 가능 여부는 실습 운영 화면의 LabInstance 상태에서 확인해 주세요.
            이 화면만 보고 생성이나 초기화 요청을 다시 실행하지 마세요.
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

      {isUnknownStatus && (
        <section className="notice-card notice-warning">
          <strong>알 수 없는 Operation 상태입니다.</strong>
          <p className="muted">
            새 상태가 추가되었을 수 있어 자동 판단을 멈췄습니다. 필요하면 상태를 다시 확인해 주세요.
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
          <h2>작업</h2>
          <strong>{operationTypeLabel(operation.type)}</strong>
        </article>
        <article className="detail-card">
          <h2>현재 단계</h2>
          <strong>{stageLabel(operation.stage)}</strong>
        </article>
        <article className="detail-card">
          <h2>대상</h2>
          <strong>{targetTypeLabel(operation.target.type)}</strong>
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
          실습 운영 상태 보기
        </Link>
      )}
    </main>
  )
}
