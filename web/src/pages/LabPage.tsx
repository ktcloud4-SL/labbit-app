import { useQuery, useQueryClient } from '@tanstack/react-query'
import { useEffect, useState } from 'react'
import { Link, useBlocker, useParams } from 'react-router-dom'

import type { ClassDetail } from '../shared/api/contracts'
import { HttpError } from '../shared/api/httpClient'
import { useLabbitApi } from '../shared/api/LabbitApiProvider'
import { labbitQueryKeys } from '../shared/api/labbitApi'
import {
  classReadinessPollingInterval,
  retryPendingClassReadiness,
} from '../shared/workspace/classReadinessPolling'
import { LoginRedirect } from '../shared/ui/LoginRedirect'
import { ErrorState } from '../shared/ui/ErrorState'
import { LoadingState } from '../shared/ui/LoadingState'
import { TerminalPanel } from '../shared/terminal/TerminalPanel'
import { PreviewPanel } from '../shared/preview/PreviewPanel'
import { WorkspaceFilePanels } from '../shared/workspace/WorkspaceFilePanels'
import type { WorkspaceDraftBackup } from '../shared/workspace/WorkspaceFilePanels'
import { clearWorkspaceEditState, setWorkspaceEditState } from '../shared/workspace/workspaceEditState'

const roleLabel = (role: string) => {
  if (role === 'INSTRUCTOR') return '강사'
  if (role === 'STUDENT') return '수강생'
  return role
}

export function LabPage() {
  const api = useLabbitApi()
  const queryClient = useQueryClient()
  const { classId } = useParams()
  const resolvedClassId = classId ?? ''
  // Keep only in volatile React memory, above the READY / RESET render split.
  // Generation changes must not silently discard dirty editor drafts or a PUT.
  const [retainedWorkspaceDraft, setRetainedWorkspaceDraft] = useState<WorkspaceDraftBackup | null>(null)
  const [retainedFileSavePending, setRetainedFileSavePending] = useState(false)
  // Late PUT callbacks from an unmounted Editor consult the live Class query,
  // not the generation that was current when the Editor first rendered.
  const dirty = retainedWorkspaceDraft !== null
  // Own the navigation/unload barrier above WorkspaceFilePanels. The panel
  // unmounts while a LabInstance is PROVISIONING or missing, but the user
  // must still be able to decide whether to discard their in-memory draft.
  const navigationBlocker = useBlocker(
    ({ nextLocation }) => (dirty || retainedFileSavePending) && nextLocation.pathname !== '/login',
  )

  useEffect(() => {
    setWorkspaceEditState({ dirty, savePending: retainedFileSavePending })
  }, [dirty, retainedFileSavePending])

  useEffect(() => () => clearWorkspaceEditState(), [])

  useEffect(() => {
    if (!dirty && !retainedFileSavePending) return
    const warn = (event: BeforeUnloadEvent) => {
      event.preventDefault()
      event.returnValue = ''
    }
    window.addEventListener('beforeunload', warn)
    return () => window.removeEventListener('beforeunload', warn)
  }, [dirty, retainedFileSavePending])

  useEffect(() => {
    if (navigationBlocker.state !== 'blocked') return
    if (retainedFileSavePending) {
      navigationBlocker.reset()
      return
    }
    if (window.confirm('저장되지 않은 변경이 있습니다. 변경 내용을 버리고 페이지를 이동할까요?')) {
      // The same LabPage instance can survive a /classes/:classId/lab route change.
      // Discard explicitly before proceeding so its old Class draft cannot linger.
      setRetainedWorkspaceDraft(null)
      navigationBlocker.proceed()
      return
    }
    navigationBlocker.reset()
  }, [navigationBlocker, retainedFileSavePending])

  const classQuery = useQuery({
    queryKey: labbitQueryKeys.classDetail(resolvedClassId),
    queryFn: () => api.getClass(resolvedClassId),
    enabled: Boolean(resolvedClassId),
    retry: (failureCount, error) =>
      retryPendingClassReadiness(
        queryClient.getQueryData<ClassDetail>(labbitQueryKeys.classDetail(resolvedClassId)),
        failureCount,
        error,
      ),
    refetchInterval: (query) =>
      classReadinessPollingInterval(query.state.data, query.state.error),
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
          {(dirty || retainedFileSavePending) && (
            <p role="status">Workspace에 저장되지 않은 수정본 또는 진행 중인 저장 요청이 있어
              이 화면을 벗어나기 전 확인이 필요합니다. 새로고침하면 메모리 수정본은 사라집니다.</p>
          )}
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
        <WorkspaceFilePanels
          labInstanceId={labInstance.id}
          generation={labInstance.generation}
          retainedDraft={retainedWorkspaceDraft}
          onRetainedDraftChange={setRetainedWorkspaceDraft}
          retainedSavePending={retainedFileSavePending}
          onRetainedSavePendingChange={setRetainedFileSavePending}
          isCurrentWorkspace={(id, expectedGeneration) => {
            const current = queryClient.getQueryState<ClassDetail>(
              labbitQueryKeys.classDetail(resolvedClassId),
            )
            return current?.status === 'success' &&
              current.data?.myLabInstance?.status === 'READY' &&
              current.data.myLabInstance.id === id &&
              current.data.myLabInstance.generation === expectedGeneration
          }}
        />

        <section className="workspace-panel workspace-preview">
          <div className="workspace-panel-heading">
            <strong>미리보기</strong>
            <span>Web Preview</span>
          </div>
          <PreviewPanel
            key={`${labInstance.id}:${labInstance.generation}`}
            labInstanceId={labInstance.id}
            generation={labInstance.generation}
          />
        </section>

        <section className="workspace-panel workspace-terminal">
          <div className="workspace-panel-heading">
            <strong>Terminal</strong>
            <span>Browser WSS · multi-VM</span>
          </div>
          <TerminalPanel
            key={`${labInstance.id}:${labInstance.generation}`}
            labInstanceId={labInstance.id}
            generation={labInstance.generation}
          />
        </section>
      </section>
    </main>
  )
}
