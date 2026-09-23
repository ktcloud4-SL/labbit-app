import { useQuery } from '@tanstack/react-query'
import { Link } from 'react-router-dom'

import { HttpError } from '../shared/api/httpClient'
import { useLabbitApi } from '../shared/api/LabbitApiProvider'
import { labbitQueryKeys } from '../shared/api/labbitApi'
import { LoginRedirect } from '../shared/ui/LoginRedirect'
import { ErrorState } from '../shared/ui/ErrorState'
import { LoadingState } from '../shared/ui/LoadingState'

const roleLabel = (role: string) => {
  if (role === 'INSTRUCTOR') return '강사'
  if (role === 'STUDENT') return '수강생'
  return role
}

const executionStatusLabel = (status?: string) => {
  if (!status) return '진행 중인 실습 없음'
  if (status === 'ACTIVE' || status === 'RUNNING') return '진행 중'
  if (status === 'PROVISIONING') return '생성 중'
  if (status === 'ERROR') return '오류'
  return status
}

export function ClassListPage() {
  const api = useLabbitApi()
  const classesQuery = useQuery({
    queryKey: labbitQueryKeys.classes,
    queryFn: () => api.listClasses(),
    retry: false,
  })

  if (classesQuery.isPending) {
    return (
      <main className="app-page">
        <LoadingState label="수업 목록을 불러오는 중..." />
      </main>
    )
  }

  if (classesQuery.error instanceof HttpError && classesQuery.error.status === 401) {
    return <LoginRedirect reason="sessionExpired" />
  }

  if (classesQuery.error) {
    return (
      <main className="app-page">
        <ErrorState message="수업 목록을 불러오지 못했습니다." />
      </main>
    )
  }

  return (
    <main className="app-page">
      <header className="page-header page-header-spacious">
        <div>
          <p className="eyebrow">Class</p>
          <h1>수업</h1>
          <p className="muted">참여 중인 수업과 현재 실습 상태를 한눈에 확인합니다.</p>
        </div>
      </header>

      {classesQuery.data.items.length === 0 ? (
        <section className="empty-state">
          <h2>참여 중인 수업이 없습니다.</h2>
          <p>접근 가능한 수업이 생기면 이곳에 표시됩니다.</p>
        </section>
      ) : (
        <section className="class-grid" aria-label="접근 가능한 수업">
          {classesQuery.data.items.map((classItem) => {
            const hasActiveExecution = Boolean(classItem.activeLabExecution)
            return (
              <article
                className={'class-card' + (hasActiveExecution ? ' class-card-active' : '')}
                key={classItem.id}
              >
                <div className="class-card-topline">
                  <span className="role-badge">{roleLabel(classItem.myRole)}</span>
                  <span
                    className={
                      'status-pill ' +
                      (hasActiveExecution ? 'status-pill-success' : 'status-pill-neutral')
                    }
                  >
                    {executionStatusLabel(classItem.activeLabExecution?.status)}
                  </span>
                </div>

                <div className="class-card-body">
                  <h2>{classItem.name}</h2>
                  <p className="class-card-description">
                    {hasActiveExecution
                      ? '현재 실습이 진행 중입니다. 수업을 열어 내 환경과 운영 상태를 확인하세요.'
                      : '현재 진행 중인 실습이 없습니다. 수업 상세에서 다음 작업을 확인할 수 있습니다.'}
                  </p>
                </div>

                <div className="class-card-footer">
                  <span className="class-card-id">수업 ID · {classItem.id}</span>
                  <Link
                    className="primary-link"
                    to={'/classes/' + encodeURIComponent(classItem.id)}
                  >
                    수업 열기
                  </Link>
                </div>
              </article>
            )
          })}
        </section>
      )}
    </main>
  )
}
