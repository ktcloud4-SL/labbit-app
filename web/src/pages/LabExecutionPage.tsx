import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { useState } from 'react'
import { Link, Navigate, useLocation, useNavigate, useParams } from 'react-router-dom'

import { HttpError } from '../shared/api/httpClient'
import { useLabbitApi } from '../shared/api/LabbitApiProvider'
import { labbitQueryKeys } from '../shared/api/labbitApi'
import { ErrorState } from '../shared/ui/ErrorState'
import { LoadingState } from '../shared/ui/LoadingState'

type PendingAction =
  | { type: 'RESET'; labInstanceId: string; username: string; idempotencyKey: string }
  | { type: 'CLEANUP'; idempotencyKey: string }
  | null

const knownExecutionStatuses = new Set([
  'PROVISIONING',
  'ACTIVE',
  'CLEANING_UP',
  'COMPLETED',
  'ERROR',
])

function createIdempotencyKey() {
  return crypto.randomUUID()
}

export function LabExecutionPage() {
  const api = useLabbitApi()
  const queryClient = useQueryClient()
  const location = useLocation()
  const navigate = useNavigate()
  const { labExecutionId } = useParams()
  const resolvedExecutionId = labExecutionId ?? ''
  const [pendingAction, setPendingAction] = useState<PendingAction>(null)

  const executionQuery = useQuery({
    queryKey: labbitQueryKeys.labExecution(resolvedExecutionId),
    queryFn: () => api.getLabExecution(resolvedExecutionId),
    enabled: Boolean(resolvedExecutionId),
    retry: false,
  })

  const classQuery = useQuery({
    queryKey: labbitQueryKeys.classDetail(executionQuery.data?.classId ?? ''),
    queryFn: () => api.getClass(executionQuery.data?.classId ?? ''),
    enabled: Boolean(executionQuery.data?.classId),
    retry: false,
  })

  const canLoadMemberships = classQuery.data?.myRole === 'INSTRUCTOR'

  const membershipsQuery = useQuery({
    queryKey: labbitQueryKeys.classMemberships(executionQuery.data?.classId ?? ''),
    queryFn: () => api.listClassMemberships(executionQuery.data?.classId ?? ''),
    enabled: Boolean(executionQuery.data?.classId && canLoadMemberships),
    retry: false,
  })

  const mutation = useMutation({
    mutationFn: async (action: Exclude<PendingAction, null>) => {
      if (action.type === 'RESET') {
        return api.resetLabInstance(action.labInstanceId, action.idempotencyKey)
      }
      return api.cleanupLabExecution(resolvedExecutionId, action.idempotencyKey)
    },
    onSuccess: (accepted) => {
      navigate(`/operations/${encodeURIComponent(accepted.operationId)}`)
    },
    onError: async (error) => {
      if (error instanceof HttpError && error.status === 401) {
        queryClient.removeQueries({ queryKey: labbitQueryKeys.me })
      }
      if (error instanceof HttpError && error.status === 409) {
        await queryClient.invalidateQueries({
          queryKey: labbitQueryKeys.labExecution(resolvedExecutionId),
        })
      }
    },
  })

  if (!resolvedExecutionId) {
    return (
      <main className="app-page">
        <ErrorState message="LabExecution ID가 없습니다." />
      </main>
    )
  }

  if (
    executionQuery.isPending ||
    (executionQuery.data && classQuery.isPending) ||
    (canLoadMemberships && membershipsQuery.isPending)
  ) {
    return (
      <main className="app-page">
        <LoadingState label="LabExecution 상태를 불러오는 중..." />
      </main>
    )
  }

  const primaryQueryErrors = [executionQuery.error, classQuery.error]
  if (
    primaryQueryErrors.some(
      (error) => error instanceof HttpError && error.status === 401,
    ) ||
    (mutation.error instanceof HttpError && mutation.error.status === 401)
  ) {
    return (
      <Navigate
        to="/login"
        replace
        state={{
          from: `${location.pathname}${location.search}`,
          reason: 'sessionExpired',
        }}
      />
    )
  }

  if (executionQuery.error instanceof HttpError && executionQuery.error.status === 403) {
    return (
      <main className="app-page">
        <ErrorState message="이 LabExecution을 볼 권한이 없습니다." />
      </main>
    )
  }

  if (executionQuery.error instanceof HttpError && executionQuery.error.status === 404) {
    return (
      <main className="app-page">
        <ErrorState message="LabExecution을 찾을 수 없습니다." />
      </main>
    )
  }

  if (classQuery.error instanceof HttpError && classQuery.error.status === 403) {
    return (
      <main className="app-page">
        <ErrorState message="이 Class를 운영할 권한이 없습니다." />
      </main>
    )
  }

  if (
    executionQuery.error ||
    classQuery.error ||
    !executionQuery.data ||
    !classQuery.data
  ) {
    return (
      <main className="app-page">
        <ErrorState message="LabExecution 정보를 불러오지 못했습니다." />
      </main>
    )
  }

  const execution = executionQuery.data
  const isInstructor = classQuery.data.myRole === 'INSTRUCTOR'

  if (!isInstructor) {
    return (
      <main className="app-page">
        <ErrorState message="INSTRUCTOR만 실습 운영 화면에 접근할 수 있습니다." />
        <Link
          className="secondary-link"
          to={`/classes/${encodeURIComponent(execution.classId)}`}
        >
          Class 상세로 돌아가기
        </Link>
      </main>
    )
  }

  if (
    membershipsQuery.error instanceof HttpError &&
    membershipsQuery.error.status === 401
  ) {
    return (
      <Navigate
        to="/login"
        replace
        state={{
          from: `${location.pathname}${location.search}`,
          reason: 'sessionExpired',
        }}
      />
    )
  }

  if (membershipsQuery.error || !membershipsQuery.data) {
    return (
      <main className="app-page">
        <ErrorState message="Class Membership 정보를 불러오지 못했습니다." />
      </main>
    )
  }

  const usernameById = new Map(
    membershipsQuery.data.items.map((membership) => [
      membership.userId,
      membership.username,
    ]),
  )
  const hasError = execution.labInstances.some(
    (labInstance) => labInstance.status === 'ERROR',
  )
  const isUnknownExecutionStatus = !knownExecutionStatuses.has(execution.status)
  const canCleanup =
    execution.status === 'ACTIVE' || execution.status === 'ERROR'

  const mutationError = mutation.error
  const conflictError =
    mutationError instanceof HttpError && mutationError.status === 409
  const mutationErrorMessage =
    mutationError instanceof HttpError && mutationError.status === 403
      ? '현재 계정에는 이 변경 작업을 실행할 권한이 없습니다. 권한이 변경되었을 수 있습니다.'
      : mutationError instanceof HttpError && mutationError.status === 409
        ? '다른 변경 작업이 진행 중입니다. 현재 Operation 상태를 확인해 주세요.'
      : mutationError instanceof HttpError && mutationError.status === 422
        ? pendingAction?.type === 'RESET'
          ? 'Reset 재현 조건 또는 제품 규칙을 만족하지 못했습니다. 재현 불가로 거절된 경우 기존 환경은 먼저 삭제되지 않습니다.'
          : '현재 LabExecution 상태에서는 Cleanup을 시작할 수 없습니다. 상태와 진행 중인 작업을 확인해 주세요.'
        : mutationError instanceof HttpError && mutationError.status === 503
          ? 'Connector 또는 Provider가 일시적으로 사용할 수 없습니다.'
          : mutationError
            ? '요청을 접수하지 못했습니다. 잠시 후 다시 시도해 주세요.'
            : null

  return (
    <main className="app-page">
      <header className="page-header">
        <div>
          <Link
            className="back-link"
            to={`/classes/${encodeURIComponent(execution.classId)}`}
          >
            ← Class 상세
          </Link>
          <p className="eyebrow">LabExecution</p>
          <h1>실습 운영 상태</h1>
          <p className="muted">{execution.id}</p>
        </div>
        <span className="operation-status">{execution.status}</span>
      </header>

      <section className="detail-grid operation-grid">
        <article className="detail-card">
          <h2>LabSpec</h2>
          <strong>{execution.labSpecId}</strong>
        </article>
        <article className="detail-card">
          <h2>대상 학생</h2>
          <strong>{execution.targetUserIds.length}명</strong>
        </article>
        <article className="detail-card">
          <h2>LabInstance</h2>
          <strong>{execution.labInstances.length}개</strong>
        </article>
      </section>

      {hasError && (
        <section className="notice-card notice-warning">
          <strong>일부 LabInstance에 오류가 있습니다.</strong>
          <p className="muted">
            성공한 환경은 유지하고 실패한 대상은 개별 상태로 추적합니다.
          </p>
        </section>
      )}

      {isUnknownExecutionStatus && (
        <section className="notice-card notice-warning">
          <strong>알 수 없는 LabExecution 상태입니다.</strong>
          <p className="muted">
            새 상태가 추가되었을 수 있으므로 destructive action을 임의로 활성화하지 않습니다.
          </p>
        </section>
      )}

      {canCleanup && (
        <div className="action-row">
          <button
            className="secondary-button danger-text"
            type="button"
            onClick={() => {
              mutation.reset()
              setPendingAction({
                type: 'CLEANUP',
                idempotencyKey: createIdempotencyKey(),
              })
            }}
          >
            Class Cleanup
          </button>
        </div>
      )}

      <section className="status-table" aria-label="LabInstance 상태">
        <div className="status-row status-header">
          <span>사용자</span>
          <span>구분</span>
          <span>상태</span>
          <span>Generation / 작업</span>
        </div>
        {execution.labInstances.map((labInstance) => {
          const rowIsInstructor =
            labInstance.userId === execution.instructorUserId
          const username =
            usernameById.get(labInstance.userId) ?? labInstance.userId

          return (
            <div className="status-row" key={labInstance.id}>
              <span>{username}</span>
              <span>{rowIsInstructor ? 'INSTRUCTOR' : 'STUDENT'}</span>
              <strong>{labInstance.status}</strong>
              <span>
                {labInstance.generation}
                {!rowIsInstructor &&
                  execution.status === 'ACTIVE' &&
                  (labInstance.status === 'READY' || labInstance.status === 'ERROR') && (
                    <>
                      {' · '}
                      <button
                        className="text-button danger-text"
                        type="button"
                        onClick={() => {
                          mutation.reset()
                          setPendingAction({
                            type: 'RESET',
                            labInstanceId: labInstance.id,
                            username,
                            idempotencyKey: createIdempotencyKey(),
                          })
                        }}
                      >
                        Reset
                      </button>
                    </>
                  )}

              </span>
            </div>
          )
        })}
      </section>

      {pendingAction && (
        <section className="confirmation-card">
          <p className="eyebrow">Confirm</p>
          <h2>
            {pendingAction.type === 'RESET'
              ? `${pendingAction.username} 환경을 Reset할까요?`
              : '현재 LabExecution을 Cleanup할까요?'}
          </h2>
          <p className="muted">
            {pendingAction.type === 'RESET'
              ? '현재 환경 데이터는 제거되고 생성 당시 immutable CreationSnapshot 기준으로 다시 생성됩니다. 재현 조건을 만족하지 못하면 기존 환경을 먼저 삭제하지 않고 요청이 실패합니다.'
              : '현재 실행에 연결된 Terminal/Live와 Provider 리소스를 정리합니다. Class와 LabSpec, 과거 실행 기록 자체는 삭제하지 않습니다.'}
          </p>

          {mutationErrorMessage && (
            <p className="form-error" role="alert">
              {mutationErrorMessage}
            </p>
          )}

          <div className="form-actions">
            <button
              className="secondary-button"
              type="button"
              disabled={mutation.isPending}
              onClick={() => {
                mutation.reset()
                setPendingAction(null)
              }}
            >
              취소
            </button>
            <button
              className="primary-button"
              type="button"
              disabled={mutation.isPending || conflictError}
              onClick={() => {
                if (pendingAction) {
                  mutation.mutate(pendingAction)
                }
              }}
            >
              {mutation.isPending
                ? '요청 중...'
                : pendingAction.type === 'RESET'
                  ? 'Reset 시작'
                  : 'Cleanup 시작'}
            </button>
          </div>
        </section>
      )}
    </main>
  )
}
