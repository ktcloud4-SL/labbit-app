import { useQuery } from '@tanstack/react-query'
import { Link } from 'react-router-dom'

import { HttpError } from '../shared/api/httpClient'
import { useLabbitApi } from '../shared/api/LabbitApiProvider'
import { labbitQueryKeys } from '../shared/api/labbitApi'
import { LoginRedirect } from '../shared/ui/LoginRedirect'
import { ErrorState } from '../shared/ui/ErrorState'
import { LoadingState } from '../shared/ui/LoadingState'

export function LabSpecListPage() {
  const api = useLabbitApi()
  const meQuery = useQuery({
    queryKey: labbitQueryKeys.me,
    queryFn: () => api.getMe(),
    retry: false,
  })
  const labSpecsQuery = useQuery({
    queryKey: labbitQueryKeys.labSpecs,
    queryFn: () => api.listLabSpecs(),
    retry: false,
  })

  if (meQuery.isPending || labSpecsQuery.isPending) {
    return (
      <main className="app-page">
        <LoadingState label="실습 정의를 불러오는 중..." />
      </main>
    )
  }

  const authError =
    (meQuery.error instanceof HttpError && meQuery.error.status === 401) ||
    (labSpecsQuery.error instanceof HttpError && labSpecsQuery.error.status === 401)

  if (authError) {
    return <LoginRedirect reason="sessionExpired" />
  }

  if (meQuery.error || labSpecsQuery.error || !meQuery.data || !labSpecsQuery.data) {
    return (
      <main className="app-page">
        <ErrorState message="실습 정의를 불러오지 못했습니다." />
      </main>
    )
  }

  return (
    <main className="app-page">
      <header className="page-header page-header-spacious">
        <div>
          <Link className="back-link" to="/classes">
            ← 수업 목록
          </Link>
          <p className="eyebrow">실습 정의</p>
          <h1>실습 정의</h1>
          <p className="muted">
            실습 환경 구성을 미리 정의하고 관리합니다. 저장만으로 실제 실습 환경이 생성되지는 않습니다.
          </p>
        </div>
        <Link className="primary-link header-action" to="/lab-specs/new">
          새 실습 정의
        </Link>
      </header>

      {labSpecsQuery.data.items.length === 0 ? (
        <section className="empty-state">
          <h2>저장된 LabSpec이 없습니다.</h2>
          <p>새 LabSpec을 만들고 이후 Class Provision에서 사용할 수 있습니다.</p>
        </section>
      ) : (
        <section className="class-grid" aria-label="조회 가능한 LabSpec">
          {labSpecsQuery.data.items.map((labSpec) => {
            const isOwner = labSpec.ownerUserId === meQuery.data.id

            return (
              <article className="class-card labspec-card" key={labSpec.id}>
                <div className="class-card-topline">
                  <span className="role-badge">{isOwner ? '내 실습 정의' : '읽기 전용'}</span>
                  <span className="status-pill status-pill-neutral">정의만 저장됨</span>
                </div>
                <div className="class-card-body">
                  <h2>{labSpec.name}</h2>
                  <p className="class-card-description">{labSpec.description || '설명이 없습니다.'}</p>
                </div>
                <div className="class-card-footer">
                  <span className="class-card-id">LabSpec · {labSpec.id}</span>
                  <Link
                  className={isOwner ? 'primary-link' : 'secondary-link card-link'}
                  to={`/lab-specs/${encodeURIComponent(labSpec.id)}`}
                  >
                    {isOwner ? '편집' : '상세'}
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
