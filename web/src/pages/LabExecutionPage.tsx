import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { useEffect, useRef, useState } from 'react'
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

function executionStatusLabel(status: string) {
  switch (status) {
    case 'PROVISIONING':
      return '환경 생성 중'
    case 'ACTIVE':
      return '진행 중'
    case 'CLEANING_UP':
      return '정리 중'
    case 'COMPLETED':
      return '종료'
    case 'ERROR':
      return '오류'
    default:
      return status
  }
}

function executionStatusClass(status: string) {
  if (status === 'ACTIVE' || status === 'COMPLETED') {
    return 'status-pill status-pill-success'
  }
  if (status === 'ERROR') return 'status-pill status-pill-error'
  if (status === 'PROVISIONING' || status === 'CLEANING_UP') {
    return 'status-pill status-pill-progress'
  }
  return 'status-pill status-pill-neutral'
}

function instanceStatusLabel(status: string) {
  switch (status) {
    case 'READY':
      return '사용 가능'
    case 'PENDING':
    case 'PROVISIONING':
      return '준비 중'
    case 'DELETING':
      return '정리 중'
    case 'ERROR':
      return '오류'
    default:
      return status
  }
}

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
  const previousFocusRef = useRef<HTMLElement | null>(null)
  const cancelButtonRef = useRef<HTMLButtonElement | null>(null)
  const mutationPendingRef = useRef(false)

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

  mutationPendingRef.current = mutation.isPending

  useEffect(() => {
    if (!pendingAction) return

    previousFocusRef.current =
      document.activeElement instanceof HTMLElement ? document.activeElement : null
    cancelButtonRef.current?.focus()

    const handleKeyDown = (event: KeyboardEvent) => {
      if (event.key !== 'Escape' || mutationPendingRef.current) return

      event.preventDefault()
      setPendingAction(null)
    }

    document.addEventListener('keydown', handleKeyDown)

    return () => {
      document.removeEventListener('keydown', handleKeyDown)
      previousFocusRef.current?.focus()
      previousFocusRef.current = null
    }
  }, [pendingAction])

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
          ? '초기화 재현 조건 또는 제품 규칙을 만족하지 못했습니다. 재현이 불가능한 경우 기존 환경은 먼저 삭제되지 않습니다.'
          : '현재 실습 상태에서는 정리를 시작할 수 없습니다. 상태와 진행 중인 작업을 확인해 주세요.'
        : mutationError instanceof HttpError && mutationError.status === 503
          ? '인프라 연결 구성요소를 일시적으로 사용할 수 없습니다.'
          : mutationError
            ? '요청을 접수하지 못했습니다. 잠시 후 다시 시도해 주세요.'
            : null

  return (
    <main className="app-page">
      <header className="page-header page-header-spacious">
        <div>
          <Link
            className="back-link"
            to={`/classes/${encodeURIComponent(execution.classId)}`}
          >
            ← 수업 상세
          </Link>
          <p className="eyebrow">실습 운영</p>
          <h1>실습 운영 상태</h1>
          <p className="muted">{execution.id}</p>
        </div>
        <span className={executionStatusClass(execution.status)}>{executionStatusLabel(execution.status)}</span>
      </header>

      <section className="detail-grid operation-grid">
        <article className="detail-card">
          <h2>실습 정의</h2>
          <strong>{execution.labSpecId}</strong>
        </article>
        <article className="detail-card">
          <h2>대상 학생</h2>
          <strong>{execution.targetUserIds.length}명</strong>
        </article>
        <article className="detail-card">
          <h2>실습 환경</h2>
          <strong>{execution.labInstances.length}개</strong>
        </article>
      </section>

      {hasError && (
        <section className="notice-card notice-warning">
          <strong>일부 실습 환경에 오류가 있습니다.</strong>
          <p className="muted">
            성공한 환경은 유지하며 실패한 대상만 개별 상태로 확인합니다.
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
        <div className="danger-action-card">
          <div>
            <strong>실습 종료 및 리소스 정리</strong>
            <p className="muted">모든 참여자의 실습 환경을 정리하기 전에 현재 상태를 확인하세요.</p>
          </div>
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
            전체 실습 정리
          </button>
        </div>
      )}

      <section className="status-table" aria-label="LabInstance 상태">
        <div className="status-row status-header">
          <span>사용자</span>
          <span>구분</span>
          <span>상태</span>
          <span>세대 / 작업</span>
        </div>
        {execution.labInstances.map((labInstance) => {
          const rowIsInstructor =
            labInstance.userId === execution.instructorUserId
          const username =
            usernameById.get(labInstance.userId) ?? labInstance.userId

          return (
            <div className="status-row" key={labInstance.id}>
              <span>{username}</span>
              <span className="status-identity">
                <strong>{rowIsInstructor ? '강사' : '수강생'}</strong>
                <small>{rowIsInstructor ? 'INSTRUCTOR' : 'STUDENT'}</small>
              </span>
              <span className={'instance-status instance-status-' + labInstance.status.toLowerCase()}>
                <strong>{instanceStatusLabel(labInstance.status)}</strong>
                <small>{labInstance.status}</small>
              </span>
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
                        초기화
                      </button>
                    </>
                  )}

              </span>
            </div>
          )
        })}
      </section>

      {pendingAction && (
        <div className="modal-backdrop">
          <section
            className={`modal-card ${pendingAction.type === 'CLEANUP' ? 'modal-card-danger' : ''}`}
            role="dialog"
            aria-modal="true"
            aria-labelledby="lab-action-confirm-title"
          >
          <p className="eyebrow">최종 확인</p>
          <h2 id="lab-action-confirm-title">
            {pendingAction.type === 'RESET'
              ? `${pendingAction.username} 환경을 초기화할까요?`
              : '현재 실습을 정리할까요?'}
          </h2>
          <p className="muted">
            {pendingAction.type === 'RESET'
              ? '현재 환경 데이터는 제거되고 생성 당시 기준으로 다시 만들어집니다. 재현 조건을 만족하지 못하면 기존 환경을 먼저 삭제하지 않고 요청이 실패합니다.'
              : '현재 실습에 연결된 터미널·Live와 인프라 리소스를 정리합니다. 수업, 실습 정의, 과거 실행 기록은 삭제하지 않습니다.'}
          </p>

          {mutationErrorMessage && (
            <p className="form-error" role="alert">
              {mutationErrorMessage}
            </p>
          )}

          <div className="form-actions">
            <button
              ref={cancelButtonRef}
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
                  ? '초기화 시작'
                  : '정리 시작'}
            </button>
          </div>
          </section>
        </div>
      )}
    </main>
  )
}
