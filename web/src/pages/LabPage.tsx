import { useQuery } from '@tanstack/react-query'
import { Link, Navigate, useLocation, useParams } from 'react-router-dom'

import { HttpError } from '../shared/api/httpClient'
import { useLabbitApi } from '../shared/api/LabbitApiProvider'
import { labbitQueryKeys } from '../shared/api/labbitApi'
import { ErrorState } from '../shared/ui/ErrorState'
import { LoadingState } from '../shared/ui/LoadingState'

function blockedWorkspaceCopy(status: string) {
  switch (status) {
    case 'PENDING':
    case 'PROVISIONING':
      return {
        tone: 'warning' as const,
        title: '실습 환경을 준비하고 있습니다.',
        detail:
          'LabInstance가 READY가 되면 Editor, Terminal, Preview Workspace를 사용할 수 있습니다.',
      }
    case 'ERROR':
      return {
        tone: 'error' as const,
        title: '실습 환경에 오류가 있어 Workspace를 열 수 없습니다.',
        detail: 'Class 운영 화면에서 현재 LabInstance 상태와 후속 조치를 확인해 주세요.',
      }
    case 'DELETING':
      return {
        tone: 'warning' as const,
        title: '실습 환경을 정리하고 있습니다.',
        detail: 'Cleanup이 끝날 때까지 Workspace를 사용할 수 없습니다.',
      }
    default:
      return {
        tone: 'warning' as const,
        title: '현재 LabInstance 상태에서는 Workspace를 열 수 없습니다.',
        detail: `알 수 없는 상태(${status})를 임의로 해석하지 않고 최신 상태를 확인합니다.`,
      }
  }
}

export function LabPage() {
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
        <LoadingState label="Workspace 진입 조건을 확인하는 중..." />
      </main>
    )
  }

  if (classQuery.error instanceof HttpError && classQuery.error.status === 401) {
    return <Navigate to="/login" replace state={{ from: location.pathname }} />
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
        <header className="page-header">
          <div>
            <Link
              className="back-link"
              to={`/classes/${encodeURIComponent(classDetail.id)}`}
            >
              ← Class 상세
            </Link>
            <p className="eyebrow">Workspace</p>
            <h1>{classDetail.name}</h1>
          </div>
        </header>
        <section className="notice-card">
          <strong>현재 사용자에게 할당된 LabInstance가 없습니다.</strong>
          <p className="muted">
            활성 LabExecution과 대상 학생 여부를 확인해 주세요.
          </p>
        </section>
      </main>
    )
  }

  if (labInstance.status !== 'READY') {
    const blockedCopy = blockedWorkspaceCopy(labInstance.status)

    return (
      <main className="app-page">
        <header className="page-header">
          <div>
            <Link
              className="back-link"
              to={`/classes/${encodeURIComponent(classDetail.id)}`}
            >
              ← Class 상세
            </Link>
            <p className="eyebrow">Workspace</p>
            <h1>{classDetail.name}</h1>
            <p className="muted">
              LabInstance · {labInstance.id} · generation {labInstance.generation}
            </p>
          </div>
          <span className="operation-status">{labInstance.status}</span>
        </header>

        <section
          className={`notice-card ${blockedCopy.tone === 'error' ? 'notice-error' : 'notice-warning'}`}
        >
          <strong>{blockedCopy.title}</strong>
          <p className="muted">{blockedCopy.detail}</p>
          {classDetail.activeLabExecution && (
            <Link
              className="secondary-link"
              to={`/lab-executions/${encodeURIComponent(classDetail.activeLabExecution.id)}`}
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
      <header className="workspace-header">
        <div>
          <Link
            className="back-link"
            to={`/classes/${encodeURIComponent(classDetail.id)}`}
          >
            ← Class 상세
          </Link>
          <p className="eyebrow">Lab Workspace</p>
          <h1>{classDetail.name}</h1>
          <p className="muted">
            {classDetail.myRole} · LabInstance {labInstance.id} · generation{' '}
            {labInstance.generation}
          </p>
        </div>
        <span className="operation-status">READY</span>
      </header>

      <section className="workspace-shell" aria-label="Lab Workspace Shell">
        <aside className="workspace-panel workspace-files">
          <div className="workspace-panel-heading">
            <strong>File Tree</strong>
            <span>Workspace VM</span>
          </div>
          <div className="workspace-placeholder">
            <p>File API 연결 대기</p>
            <small>
              Workspace VM 파일시스템은 후속 File API 계약을 통해 연결합니다.
            </small>
          </div>
        </aside>

        <section className="workspace-panel workspace-editor">
          <div className="workspace-panel-heading">
            <strong>Editor</strong>
            <span>Monaco 예정</span>
          </div>
          <div className="workspace-placeholder">
            <p>Code Editor 영역</p>
            <small>
              현재 PR은 READY Guard와 Shell까지만 구성하고 파일 읽기·저장은 후속 작업으로 남깁니다.
            </small>
          </div>
        </section>

        <section className="workspace-panel workspace-preview">
          <div className="workspace-panel-heading">
            <strong>Preview</strong>
            <span>별도 Origin 예정</span>
          </div>
          <div className="workspace-placeholder">
            <p>Web Preview 영역</p>
            <small>
              PreviewSession/URL 계약이 확정되면 이 Panel에 연결합니다.
            </small>
          </div>
        </section>

        <section className="workspace-panel workspace-terminal">
          <div className="workspace-panel-heading">
            <strong>Terminal / Live</strong>
            <span>xterm.js 예정</span>
          </div>
          <div className="workspace-placeholder workspace-terminal-placeholder">
            <p>Terminal Dock 영역</p>
            <small>
              TerminalSession/LiveSession HTTP Control 계약 이후 WSS v0.1을 연결합니다.
            </small>
          </div>
        </section>
      </section>
    </main>
  )
}
