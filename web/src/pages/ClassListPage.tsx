import { useQuery } from '@tanstack/react-query'
import { Link, Navigate, useLocation } from 'react-router-dom'

import { HttpError } from '../shared/api/httpClient'
import { useLabbitApi } from '../shared/api/LabbitApiProvider'
import { labbitQueryKeys } from '../shared/api/labbitApi'
import { ErrorState } from '../shared/ui/ErrorState'
import { LoadingState } from '../shared/ui/LoadingState'

export function ClassListPage() {
  const api = useLabbitApi()
  const location = useLocation()
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
    return <Navigate
      to="/login"
      replace
      state={{ from: `${location.pathname}${location.search}`, reason: 'sessionExpired' }}
    />
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
      <header className="page-header">
        <div>
          <p className="eyebrow">Class</p>
          <h1>수업</h1>
          <p className="muted">내가 접근할 수 있는 Class와 현재 실습 상태를 확인합니다.</p>
        </div>
      </header>

      {classesQuery.data.items.length === 0 ? (
        <section className="empty-state">
          <h2>참여 중인 수업이 없습니다.</h2>
          <p>접근 가능한 Class가 생기면 이곳에 표시됩니다.</p>
        </section>
      ) : (
        <section className="class-grid" aria-label="접근 가능한 수업">
          {classesQuery.data.items.map((classItem) => (
            <article className="class-card" key={classItem.id}>
              <div className="class-card-topline">
                <span className="role-badge">{classItem.myRole}</span>
                <span className="status-text">
                  {classItem.activeLabExecution?.status ?? '활성 실습 없음'}
                </span>
              </div>
              <h2>{classItem.name}</h2>
              <p className="muted">Class ID · {classItem.id}</p>
              <Link className="primary-link" to={`/classes/${encodeURIComponent(classItem.id)}`}>
                수업 열기
              </Link>
            </article>
          ))}
        </section>
      )}
    </main>
  )
}
