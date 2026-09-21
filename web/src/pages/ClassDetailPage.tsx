import { useQuery } from '@tanstack/react-query'
import { Link, Navigate, useLocation, useParams } from 'react-router-dom'

import { HttpError } from '../shared/api/httpClient'
import { useLabbitApi } from '../shared/api/LabbitApiProvider'
import { labbitQueryKeys } from '../shared/api/labbitApi'
import { ErrorState } from '../shared/ui/ErrorState'
import { LoadingState } from '../shared/ui/LoadingState'

export function ClassDetailPage() {
  const api = useLabbitApi()
  const location = useLocation()
  const { classId } = useParams()
  const resolvedClassId = classId ?? ''

  const classQuery = useQuery({
    queryKey: labbitQueryKeys.classDetail(resolvedClassId),
    queryFn: () => api.getClass(resolvedClassId),
    enabled: Boolean(resolvedClassId),
    retry: false,
  })

  if (!resolvedClassId) {
    return (
      <main className="app-page">
        <ErrorState message="Class ID가 없습니다." />
      </main>
    )
  }

  if (classQuery.isPending) {
    return (
      <main className="app-page">
        <LoadingState label="수업 정보를 불러오는 중..." />
      </main>
    )
  }

  if (classQuery.error instanceof HttpError && classQuery.error.status === 401) {
    return <Navigate to="/login" replace state={{ from: location.pathname }} />
  }

  if (classQuery.error instanceof HttpError && classQuery.error.status === 403) {
    return (
      <main className="app-page">
        <ErrorState message="이 수업을 볼 권한이 없습니다." />
        <Link className="secondary-link" to="/classes">
          수업 목록으로 돌아가기
        </Link>
      </main>
    )
  }

  if (classQuery.error instanceof HttpError && classQuery.error.status === 404) {
    return (
      <main className="app-page">
        <ErrorState message="수업을 찾을 수 없습니다." />
        <Link className="secondary-link" to="/classes">
          수업 목록으로 돌아가기
        </Link>
      </main>
    )
  }

  if (classQuery.error || !classQuery.data) {
    return (
      <main className="app-page">
        <ErrorState message="수업 정보를 불러오지 못했습니다." />
      </main>
    )
  }

  const classDetail = classQuery.data
  const labInstanceStatus = classDetail.myLabInstance?.status
  const isWorkspaceReady = labInstanceStatus === 'READY'

  return (
    <main className="app-page">
      <header className="page-header">
        <div>
          <Link className="back-link" to="/classes">
            ← 수업 목록
          </Link>
          <p className="eyebrow">Class detail</p>
          <h1>{classDetail.name}</h1>
          <p className="muted">현재 사용자 기준의 Class 컨텍스트입니다.</p>
        </div>
        {classDetail.myRole === 'INSTRUCTOR' && (
          <Link className="secondary-link header-action" to="/lab-specs">
            실습 정의 관리
          </Link>
        )}
      </header>

      <section className="detail-grid">
        <article className="detail-card">
          <h2>내 역할</h2>
          <strong>{classDetail.myRole}</strong>
        </article>
        <article className="detail-card">
          <h2>활성 실습</h2>
          <strong>{classDetail.activeLabExecution?.status ?? '없음'}</strong>
        </article>
        <article className="detail-card">
          <h2>내 환경</h2>
          <strong>{labInstanceStatus ?? '없음'}</strong>
        </article>
      </section>

      {isWorkspaceReady && (
        <Link className="primary-link inline-link" to={`/classes/${encodeURIComponent(classDetail.id)}/lab`}>
          Lab Workspace 열기
        </Link>
      )}

      {classDetail.myLabInstance && !isWorkspaceReady && (
        <p className="muted">
          {labInstanceStatus === 'ERROR'
            ? '실습 환경에 오류가 있어 Workspace를 열 수 없습니다. 상태를 확인해 주세요.'
            : '실습 환경이 READY 상태가 되면 Workspace를 열 수 있습니다.'}
        </p>
      )}
    </main>
  )
}
