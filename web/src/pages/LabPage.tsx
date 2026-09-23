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

export function LabPage() {
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
        <LoadingState label="Workspace 진입 조건을 확인하는 중..." />
      </main>
    )
  }

  if (classQuery.error instanceof HttpError && classQuery.error.status === 401) {
    return <LoginRedirect reason="sessionExpired" />
  }

  if (classQuery.error instanceof HttpError && classQuery.error.status === 403) {
    return (
      <main className="app-page">
        <ErrorState message="이 Class의 Workspace에 접근할 권한이 없습니다." />
        <Link className="secondary-link" to="/classes">
          수업 목록으로 돌아가기
        </Link>
      </main>
    )
  }

  if (classQuery.error instanceof HttpError && classQuery.error.status === 404) {
    return (
      <main className="app-page">
        <ErrorState message="Class를 찾을 수 없습니다." />
        <Link className="secondary-link" to="/classes">
          수업 목록으로 돌아가기
        </Link>
      </main>
    )
  }

  if (classQuery.error || !classQuery.data) {
    return (
      <main className="app-page">
        <ErrorState message="Workspace 진입 조건을 확인하지 못했습니다." />
      </main>
    )
  }

  const classDetail = classQuery.data
  const labInstance = classDetail.myLabInstance

  if (!labInstance) {
    return (
      <main className="app-page">
        <header className="page-header page-header-spacious">
          <div>
            <Link className="back-link" to={'/classes/' + encodeURIComponent(classDetail.id)}>
              ← 수업 상세
            </Link>
            <p className="eyebrow">Lab Workspace</p>
            <h1>{classDetail.name}</h1>
          </div>
        </header>
        <section className="notice-card">
          <strong>현재 사용할 수 있는 실습 환경이 없습니다.</strong>
          <p className="muted">활성 실습과 내 실습 환경 할당 상태를 확인해 주세요.</p>
        </section>
      </main>
    )
  }

  if (labInstance.status !== 'READY') {
    const isError = labInstance.status === 'ERROR'
    const isPreparing =
      labInstance.status === 'PENDING' || labInstance.status === 'PROVISIONING'
    const isDeleting = labInstance.status === 'DELETING'

    const title = isError
      ? '실습 환경에 오류가 있어 Workspace를 열 수 없습니다.'
      : isPreparing
        ? '실습 환경을 준비하고 있습니다.'
        : isDeleting
          ? '실습 환경을 정리하고 있습니다.'
          : '알 수 없는 LabInstance 상태입니다.'

    const description = isError
      ? '강사는 실습 운영 화면에서 현재 환경 상태와 후속 조치를 확인할 수 있습니다.'
      : isPreparing
        ? '환경 준비가 끝나면 파일, 편집기, 미리보기, 터미널 영역을 사용할 수 있습니다.'
        : isDeleting
          ? '리소스 정리가 완료되기 전에는 Workspace를 사용할 수 없습니다.'
          : '새 상태가 추가되었을 수 있습니다. 현재 실습 운영 상태를 확인해 주세요.'

    return (
      <main className="app-page">
        <header className="page-header page-header-spacious">
          <div>
            <Link className="back-link" to={'/classes/' + encodeURIComponent(classDetail.id)}>
              ← 수업 상세
            </Link>
            <p className="eyebrow">Lab Workspace</p>
            <h1>{classDetail.name}</h1>
            <p className="muted">
              실습 환경 · {labInstance.id} · generation {labInstance.generation}
            </p>
          </div>
          <span className="operation-status">{labInstance.status}</span>
        </header>

        <section className={'notice-card ' + (isError ? 'notice-error' : 'notice-warning')}>
          <strong>{title}</strong>
          <p className="muted">{description}</p>
          {classDetail.activeLabExecution && (
            <Link
              className="secondary-link"
              to={'/lab-executions/' + encodeURIComponent(classDetail.activeLabExecution.id)}
            >
              실습 운영 상태 보기
            </Link>
          )}
        </section>
      </main>
    )
  }

  return (
    <main className="workspace-page">
      <header className="workspace-header workspace-context-header">
        <div>
          <Link className="back-link" to={'/classes/' + encodeURIComponent(classDetail.id)}>
            ← 수업 상세
          </Link>
          <p className="eyebrow">Lab Workspace</p>
          <h1>{classDetail.name}</h1>
          <p className="muted workspace-meta">
            실습 환경 {labInstance.id} · generation {labInstance.generation}
          </p>
        </div>

        <div className="workspace-header-badges" aria-label="Workspace 상태">
          <span className="role-badge">{roleLabel(classDetail.myRole)}</span>
          <span className="workspace-ready-badge">
            <span className="workspace-ready-dot" />
            사용 가능
          </span>
        </div>
      </header>

      <section className="workspace-shell" aria-label="Lab Workspace Shell">
        <aside className="workspace-panel workspace-files">
          <div className="workspace-panel-heading">
            <strong>파일</strong>
            <span>Workspace VM</span>
          </div>
          <div className="workspace-placeholder">
            <span className="workspace-placeholder-mark" aria-hidden="true">F</span>
            <p>파일 탐색기 준비 중</p>
            <small>
              파일 연결 기능이 제공되면 Workspace VM의 파일을 이곳에서 확인할 수 있습니다.
            </small>
          </div>
        </aside>

        <section className="workspace-panel workspace-editor">
          <div className="workspace-panel-heading">
            <strong>Editor</strong>
            <span>Workspace</span>
          </div>
          <div className="workspace-placeholder">
            <span className="workspace-placeholder-mark" aria-hidden="true">&lt;/&gt;</span>
            <p>편집기 준비 중</p>
            <small>
              파일을 선택하면 이 영역에서 내용을 확인하고 편집할 수 있도록 연결할 예정입니다.
            </small>
          </div>
        </section>

        <section className="workspace-panel workspace-preview">
          <div className="workspace-panel-heading">
            <strong>미리보기</strong>
            <span>Web Preview</span>
          </div>
          <div className="workspace-placeholder">
            <span className="workspace-placeholder-mark" aria-hidden="true">↗</span>
            <p>실행 중인 미리보기가 없습니다.</p>
            <small>
              실습 애플리케이션의 미리보기가 준비되면 이 영역에서 바로 확인할 수 있습니다.
            </small>
          </div>
        </section>

        <section className="workspace-panel workspace-terminal">
          <div className="workspace-panel-heading">
            <strong>Terminal / Live</strong>
            <span>Shell</span>
          </div>
          <div className="workspace-placeholder workspace-terminal-placeholder">
            <span
              className="workspace-placeholder-mark workspace-placeholder-mark-dark"
              aria-hidden="true"
            >
              &gt;_
            </span>
            <p>터미널 연결 준비 중</p>
            <small>
              실습 환경과 터미널 연결이 준비되면 이곳에서 명령을 실행하고 Live 화면을 확인할 수 있습니다.
            </small>
          </div>
        </section>
      </section>
    </main>
  )
}
