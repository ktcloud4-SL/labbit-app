import { useQuery } from '@tanstack/react-query'
import { Link, Navigate, useLocation, useParams } from 'react-router-dom'

import { HttpError } from '../shared/api/httpClient'
import { useLabbitApi } from '../shared/api/LabbitApiProvider'
import { labbitQueryKeys } from '../shared/api/labbitApi'
import { ErrorState } from '../shared/ui/ErrorState'
import { LoadingState } from '../shared/ui/LoadingState'

export function LabExecutionPage() {
  const api = useLabbitApi()
  const location = useLocation()
  const { labExecutionId } = useParams()
  const resolvedExecutionId = labExecutionId ?? ''

  const executionQuery = useQuery({
    queryKey: labbitQueryKeys.labExecution(resolvedExecutionId),
    queryFn: () => api.getLabExecution(resolvedExecutionId),
    enabled: Boolean(resolvedExecutionId),
    retry: false,
  })

  const membershipsQuery = useQuery({
    queryKey: labbitQueryKeys.classMemberships(
      executionQuery.data?.classId ?? '',
    ),
    queryFn: () => api.listClassMemberships(executionQuery.data?.classId ?? ''),
    enabled: Boolean(executionQuery.data?.classId),
    retry: false,
  })

  if (!resolvedExecutionId) {
    return (
      <main className="app-page">
        <ErrorState message="LabExecution ID가 없습니다." />
      </main>
    )
  }

  if (executionQuery.isPending) {
    return (
      <main className="app-page">
        <LoadingState label="LabExecution 상태를 불러오는 중..." />
      </main>
    )
  }

  if (executionQuery.error instanceof HttpError && executionQuery.error.status === 401) {
    return <Navigate to="/login" replace state={{ from: location.pathname }} />
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

  if (executionQuery.error || !executionQuery.data) {
    return (
      <main className="app-page">
        <ErrorState message="LabExecution 정보를 불러오지 못했습니다." />
      </main>
    )
  }

  const execution = executionQuery.data
  const usernameById = new Map(
    (membershipsQuery.data?.items ?? []).map((membership) => [
      membership.userId,
      membership.username,
    ]),
  )
  const hasError = execution.labInstances.some(
    (labInstance) => labInstance.status === 'ERROR',
  )

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

      <section className="status-table" aria-label="LabInstance 상태">
        <div className="status-row status-header">
          <span>사용자</span>
          <span>구분</span>
          <span>상태</span>
          <span>Generation</span>
        </div>
        {execution.labInstances.map((labInstance) => {
          const isInstructor = labInstance.userId === execution.instructorUserId
          return (
            <div className="status-row" key={labInstance.id}>
              <span>{usernameById.get(labInstance.userId) ?? labInstance.userId}</span>
              <span>{isInstructor ? 'INSTRUCTOR' : 'STUDENT'}</span>
              <strong>{labInstance.status}</strong>
              <span>{labInstance.generation}</span>
            </div>
          )
        })}
      </section>
    </main>
  )
}
