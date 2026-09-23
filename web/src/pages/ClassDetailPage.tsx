import { useQuery } from '@tanstack/react-query'
import { Link, useParams } from 'react-router-dom'

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

const labExecutionStatusLabel = (status?: string) => {
  if (!status) return '진행 중인 실습 없음'
  if (status === 'ACTIVE' || status === 'RUNNING') return '진행 중'
  if (status === 'PROVISIONING') return '생성 중'
  if (status === 'ERROR') return '오류'
  return status
}

const labInstanceStatusLabel = (status?: string) => {
  if (!status) return '환경 없음'
  if (status === 'READY') return '사용 가능'
  if (status === 'PENDING' || status === 'PROVISIONING') return '준비 중'
  if (status === 'DELETING') return '정리 중'
  if (status === 'ERROR') return '오류'
  return status
}

export function ClassDetailPage() {
  const api = useLabbitApi()
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
    return <LoginRedirect reason="sessionExpired" />
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
  const isInstructor = classDetail.myRole === 'INSTRUCTOR'
  const hasActiveExecution = Boolean(classDetail.activeLabExecution)

  const overviewTitle = hasActiveExecution
    ? '현재 실습이 진행 중입니다.'
    : '현재 진행 중인 실습이 없습니다.'

  const overviewDescription = hasActiveExecution
    ? isWorkspaceReady
      ? '내 실습 환경을 사용할 수 있습니다. 필요한 작업으로 바로 이동하세요.'
      : classDetail.myLabInstance
        ? '실습은 진행 중이지만 내 환경은 아직 사용할 수 없습니다.'
        : '실습은 진행 중입니다. 내 실습 환경 할당 상태를 확인해 주세요.'
    : isInstructor
      ? 'LabSpec을 선택해 새로운 실습 환경을 시작할 수 있습니다.'
      : '강사가 실습을 시작하면 이곳에서 내 실습 환경에 입장할 수 있습니다.'

  return (
    <main className="app-page">
      <header className="page-header page-header-spacious">
        <div>
          <Link className="back-link" to="/classes">
            ← 수업 목록
          </Link>
          <p className="eyebrow">Class detail</p>
          <h1>{classDetail.name}</h1>
          <p className="muted">
            수업과 실습 환경 상태를 확인하고 필요한 작업으로 이동합니다.
          </p>
        </div>
        {isInstructor && (
          <Link className="secondary-link header-action" to="/lab-specs">
            실습 정의 관리
          </Link>
        )}
      </header>

      <section
        className={'class-overview-card' + (hasActiveExecution ? ' class-overview-active' : '')}
      >
        <div className="class-overview-heading">
          <div>
            <p className="section-kicker">현재 실습</p>
            <h2>{overviewTitle}</h2>
            <p className="muted">{overviewDescription}</p>
          </div>
          <span
            className={
              'status-pill ' +
              (hasActiveExecution ? 'status-pill-success' : 'status-pill-neutral')
            }
          >
            {labExecutionStatusLabel(classDetail.activeLabExecution?.status)}
          </span>
        </div>

        <div className="class-overview-meta">
          <div>
            <span>내 역할</span>
            <strong>{roleLabel(classDetail.myRole)}</strong>
          </div>
          <div>
            <span>내 환경</span>
            <strong>{labInstanceStatusLabel(labInstanceStatus)}</strong>
          </div>
          <div>
            <span>Workspace</span>
            <strong>{isWorkspaceReady ? '입장 가능' : '입장 대기'}</strong>
          </div>
        </div>

        <div className="class-overview-actions">
          {isInstructor && !classDetail.activeLabExecution && (
            <Link
              className="primary-link inline-link"
              to={'/classes/' + encodeURIComponent(classDetail.id) + '/provision'}
            >
              새 환경 생성
            </Link>
          )}

          {isInstructor && classDetail.activeLabExecution && (
            <Link
              className="secondary-link action-link"
              to={'/lab-executions/' + encodeURIComponent(classDetail.activeLabExecution.id)}
            >
              현재 실습 운영 보기
            </Link>
          )}

          {isWorkspaceReady && (
            <Link
              className="primary-link inline-link"
              to={'/classes/' + encodeURIComponent(classDetail.id) + '/lab'}
            >
              Lab Workspace 열기
            </Link>
          )}
        </div>
      </section>

      {classDetail.myLabInstance && !isWorkspaceReady && (
        <section
          className={
            'notice-card compact-notice ' +
            (labInstanceStatus === 'ERROR' ? 'notice-error' : 'notice-warning')
          }
        >
          <strong>
            {labInstanceStatus === 'ERROR'
              ? '내 실습 환경에 오류가 있습니다.'
              : labInstanceStatus === 'DELETING'
                ? '내 실습 환경을 정리하고 있습니다.'
                : labInstanceStatus === 'PENDING' || labInstanceStatus === 'PROVISIONING'
                  ? '내 실습 환경을 준비하고 있습니다.'
                  : '현재 실습 환경 상태를 확인해 주세요.'}
          </strong>
          <p className="muted">
            {labInstanceStatus === 'ERROR'
              ? 'Workspace를 열 수 없습니다. 강사는 실습 운영 화면에서 상태를 확인할 수 있습니다.'
              : labInstanceStatus === 'DELETING'
                ? '리소스 정리가 완료되면 다음 실습 상태를 확인할 수 있습니다.'
                : labInstanceStatus === 'PENDING' || labInstanceStatus === 'PROVISIONING'
                  ? '환경이 사용 가능 상태가 되면 Workspace 버튼이 표시됩니다.'
                  : '새로운 상태가 추가되었을 수 있습니다. 현재 실습 운영 상태를 확인해 주세요.'}
          </p>
        </section>
      )}
    </main>
  )
}
