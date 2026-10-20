import { useMutation, useQuery } from '@tanstack/react-query'
import { useEffect, useRef, useState } from 'react'
import type { Dispatch, SetStateAction } from 'react'
import { useBlocker } from 'react-router-dom'

import type {
  VersionedWorkspaceFileContent,
  WorkspaceFileEntry,
} from '../api/contracts'
import { HttpError } from '../api/httpClient'
import { useLabbitApi } from '../api/LabbitApiProvider'
import { labbitQueryKeys } from '../api/labbitApi'
import { LoginRedirect } from '../ui/LoginRedirect'
import {
  clearWorkspaceEditState,
  setWorkspaceEditState,
} from './workspaceEditState'

interface WorkspaceFilePanelsProps {
  labInstanceId: string
  generation: number
  retainedDraft?: WorkspaceDraftBackup | null
  onRetainedDraftChange?: Dispatch<SetStateAction<WorkspaceDraftBackup | null>>
  retainedSavePending?: boolean
  onRetainedSavePendingChange?: Dispatch<SetStateAction<boolean>>
  isCurrentWorkspace?: (id: string, generation: number) => boolean
}

export interface WorkspaceDraftBackup {
  labInstanceId: string
  generation: number
  path: string
  content: string
  sourceEtag: string
}

interface WorkspaceFileEditorProps {
  labInstanceId: string
  generation: number
  versionedFile: VersionedWorkspaceFileContent
  onReload(): Promise<unknown>
  onDirtyChange(dirty: boolean): void
  onSavePendingChange(pending: boolean): void
  savePending: boolean
  onDraftChange(content: string | null, sourceEtag: string): void
  onSaveAccepted(content: string, sourceEtag: string): void
  restoredDraft?: string
}

function workspaceWideAccessChange(error: unknown) {
  return error instanceof HttpError &&
    (error.status === 409 ||
      (error.status === 403 && error.problem?.code !== 'file_permission_denied'))
}

function parentDirectory(path: string) {
  const parts = path.split('/').filter(Boolean)
  parts.pop()
  return parts.join('/')
}

function fileErrorMessage(error: unknown, action: 'list' | 'read' | 'save') {
  if (!(error instanceof HttpError)) {
    return action === 'save'
      ? '파일 저장 중 예상하지 못한 오류가 발생했습니다.'
      : 'Workspace 파일을 불러오지 못했습니다.'
  }

  const code = error.problem?.code

  if (error.status === 403) {
    return code === 'file_permission_denied'
      ? 'Workspace VM의 파일 권한 때문에 이 파일에 접근할 수 없습니다.'
      : '현재 계정에는 이 Workspace 파일을 사용할 권한이 없습니다.'
  }

  if (error.status === 404) {
    return action === 'list'
      ? '이 디렉터리를 찾을 수 없습니다.'
      : '선택한 파일을 찾을 수 없습니다.'
  }

  if (error.status === 409) {
    if (code === 'workspace_target_changed') {
      return '실습 환경이 변경되었습니다. Workspace 상태를 다시 확인해 주세요.'
    }
    return '실습 환경이 아직 파일 기능을 사용할 수 있는 상태가 아닙니다.'
  }

  if (error.status === 412) {
    return '다른 변경이 먼저 저장되었습니다. 파일을 다시 읽고 변경 내용을 확인해 주세요.'
  }

  if (error.status === 413) {
    return code === 'directory_too_large'
      ? '이 디렉터리의 항목이 너무 많아 한 번에 표시할 수 없습니다.'
      : '파일이 현재 허용된 크기보다 큽니다.'
  }

  if (error.status === 422) {
    if (code === 'binary_content') {
      return 'Binary 파일은 현재 Editor에서 열 수 없습니다.'
    }
    if (code === 'unsupported_encoding') {
      return '현재 Editor는 UTF-8 text 파일만 지원합니다.'
    }
    if (code === 'path_not_directory') {
      return '선택한 경로는 디렉터리가 아닙니다.'
    }
    if (code === 'path_not_file') {
      return '선택한 경로는 일반 파일이 아닙니다.'
    }
    return '선택한 파일 경로를 사용할 수 없습니다.'
  }

  if (error.status === 503) {
    if (code === 'file_save_outcome_unknown') {
      return '저장 결과를 확인할 수 없습니다. 자동으로 다시 저장하지 말고 파일을 다시 읽어 확인해 주세요.'
    }
    if (code === 'file_transport_unavailable') {
      return 'Workspace File 전송 경로가 아직 준비되지 않았습니다.'
    }
    return 'Workspace VM 연결을 지금 사용할 수 없습니다. 잠시 후 다시 시도해 주세요.'
  }

  return action === 'save'
    ? '파일을 저장하지 못했습니다.'
    : 'Workspace 파일을 불러오지 못했습니다.'
}

function FilePanelError({
  message,
  actionLabel,
  onAction,
  busy = false,
}: {
  message: string
  actionLabel: string
  onAction(): void
  busy?: boolean
}) {
  return (
    <div className="workspace-file-state workspace-file-state-error" role="alert">
      <p>{message}</p>
      <button
        className="workspace-file-action"
        type="button"
        disabled={busy}
        onClick={onAction}
      >
        {busy ? '다시 확인 중...' : actionLabel}
      </button>
    </div>
  )
}

function WorkspaceFileEditor({
  labInstanceId,
  generation,
  versionedFile,
  onReload,
  onDirtyChange,
  onSavePendingChange,
  savePending,
  onDraftChange,
  onSaveAccepted,
  restoredDraft,
}: WorkspaceFileEditorProps) {
  const api = useLabbitApi()
  const [draft, setDraft] = useState(restoredDraft ?? versionedFile.file.content)
  const [savedContent, setSavedContent] = useState(versionedFile.file.content)
  const [etag, setEtag] = useState(versionedFile.etag)
  const [saveError, setSaveError] = useState<unknown>(null)

  const saveMutation = useMutation({
    mutationFn: () =>
      api.saveWorkspaceFile(
        labInstanceId,
        versionedFile.file.path,
        draft,
        etag,
      ),
    onSuccess: (saved) => {
      setEtag(saved.etag)
      setSavedContent(draft)
      setSaveError(null)
      // A PUT can settle after the Editor has unmounted or after generation
      // changed. The parent must verify the saved scope before clearing backup.
      onSaveAccepted(draft, etag)
    },
    onError: (error) => {
      setSaveError(error)
    },
  })

  const dirty = draft !== savedContent

  async function submitSave() {
    if (savePending || saveMutation.isPending) return
    // Keep the parent navigation/dirty barrier until the actual HTTP result,
    // even if access revocation unmounts this Editor during the in-flight PUT.
    onSavePendingChange(true)
    try {
      await saveMutation.mutateAsync()
    } catch {
      // The mutation's onError already preserves the draft and safe UX.
    } finally {
      onSavePendingChange(false)
    }
  }

  // Parent dirty state is updated by user edits, explicit restoration and save.
  // Avoid cascading React state updates from a child effect.
  useEffect(() => () => onDirtyChange(false), [onDirtyChange])

  useEffect(() => {
    if (!dirty) return

    const handleBeforeUnload = (event: BeforeUnloadEvent) => {
      event.preventDefault()
      event.returnValue = ''
    }

    window.addEventListener('beforeunload', handleBeforeUnload)
    return () => window.removeEventListener('beforeunload', handleBeforeUnload)
  }, [dirty])

  const staleSave =
    saveError instanceof HttpError &&
    (saveError.status === 412 ||
      saveError.problem?.code === 'file_save_outcome_unknown' ||
      saveError.problem?.code === 'workspace_target_changed')

  if (saveError instanceof HttpError && saveError.status === 401) {
    return <LoginRedirect reason="sessionExpired" />
  }

  return (
    <div className="workspace-editor-body">
      <div className="workspace-editor-toolbar">
        <div>
          <strong>{versionedFile.file.path}</strong>
          <span>{dirty ? '저장되지 않은 변경' : '저장됨'}</span>
        </div>
        <button
          className="workspace-file-action workspace-file-action-primary"
          type="button"
          disabled={!dirty || savePending || saveMutation.isPending}
          onClick={() => void submitSave()}
        >
          {savePending || saveMutation.isPending ? '저장 중...' : '저장'}
        </button>
      </div>

      {saveError !== null && (
        <div className="workspace-editor-error" role="alert">
          <span>{fileErrorMessage(saveError, 'save')}</span>
          {staleSave && (
            <button
              className="workspace-file-action"
              type="button"
              onClick={() => {
                if (
                  dirty &&
                  !window.confirm(
                    '파일을 다시 읽으면 현재 수정본을 버립니다. 계속할까요?',
                  )
                ) {
                  return
                }
                onDraftChange(null, etag)
                void onReload()
              }}
            >
              파일 다시 읽기
            </button>
          )}
        </div>
      )}

      <textarea
        className="workspace-code-editor"
        aria-label="파일 편집기"
        spellCheck={false}
        disabled={savePending || saveMutation.isPending}
        value={draft}
        onChange={(event) => {
          const next = event.target.value
          setDraft(next)
          onDirtyChange(next !== savedContent)
          onDraftChange(next === savedContent ? null : next, etag)
          if (saveError) setSaveError(null)
        }}
      />

      <div className="workspace-editor-footer">
        <span>generation {generation}</span>
        <span>UTF-8 text · ETag/If-Match 보호</span>
      </div>
    </div>
  )
}

// Only standalone usage registers this blocker. A real LabPage owns the sole
// router blocker for both READY and non-READY states; registering two blockers
// would let the later (inactive) one override its navigation policy.
function StandaloneWorkspaceNavigationGuard({
  hasUnsavedChanges,
  savePending,
}: {
  hasUnsavedChanges: boolean
  savePending: boolean
}) {
  const blocker = useBlocker(
    ({ nextLocation }) =>
      (hasUnsavedChanges || savePending) && nextLocation.pathname !== '/login',
  )

  useEffect(() => {
    if (blocker.state !== 'blocked') return
    if (savePending) {
      blocker.reset()
      return
    }
    if (window.confirm(
      '저장되지 않은 변경이 있습니다. 변경 내용을 버리고 페이지를 이동할까요?',
    )) {
      blocker.proceed()
      return
    }
    blocker.reset()
  }, [blocker, savePending])

  return null
}

export function WorkspaceFilePanels({
  labInstanceId,
  generation,
  retainedDraft,
  onRetainedDraftChange,
  retainedSavePending,
  onRetainedSavePendingChange,
  isCurrentWorkspace,
}: WorkspaceFilePanelsProps) {
  const api = useLabbitApi()
  const [directoryPath, setDirectoryPath] = useState('')
  const [selectedFilePath, setSelectedFilePath] = useState<string | null>(null)
  const activeEditorScope = useRef({ labInstanceId, generation, path: selectedFilePath })
  useEffect(() => {
    activeEditorScope.current = { labInstanceId, generation, path: selectedFilePath }
  }, [labInstanceId, generation, selectedFilePath])
  const [editorDirty, setEditorDirty] = useState(false)
  const [internalSavePending, setInternalSavePending] = useState(false)
  const savePending = onRetainedSavePendingChange ? Boolean(retainedSavePending) : internalSavePending
  const setSavePending = onRetainedSavePendingChange ?? setInternalSavePending
  // In-memory only; never render, persist or log denied Workspace file bytes.
  const [internalDraftBackup, setInternalDraftBackup] = useState<WorkspaceDraftBackup | null>(null)
  // LabPage owns this volatile draft across generation changes and READY/RESET
  // branch transitions. Isolated component consumers use the internal fallback.
  const draftBackup = onRetainedDraftChange ? retainedDraft ?? null : internalDraftBackup
  const setDraftBackup = onRetainedDraftChange ?? setInternalDraftBackup
  const [restoredDraft, setRestoredDraft] = useState<{
    scope: string
    path: string
    content: string
  } | null>(null)
  const [restoreEpoch, setRestoreEpoch] = useState(0)
  // Request-start epochs prevent pre-revocation GET responses from
  // authorizing another cached path after the workspace target has changed.
  const workspaceRequestEpoch = useRef(0)
  const [workspaceGate, setWorkspaceGate] = useState<{
    scope: string
    verifiedTrees: string[]
    verifiedFiles: string[]
  } | null>(null)
  const [requiresFreshRead, setRequiresFreshRead] = useState<{
    labInstanceId: string
    generation: number
    path: string
  } | null>(null)
  const scope = `${labInstanceId}:${generation}`
  const hasUnsavedChanges = editorDirty || draftBackup !== null
  const ownedByLabPage = Boolean(onRetainedDraftChange && onRetainedSavePendingChange)

  useEffect(() => {
    if (!ownedByLabPage) setWorkspaceEditState({ dirty: hasUnsavedChanges, savePending })
  }, [hasUnsavedChanges, savePending, ownedByLabPage])

  useEffect(
    () => () => {
      if (!ownedByLabPage) clearWorkspaceEditState()
    },
    [ownedByLabPage],
  )

  const treeQuery = useQuery({
    queryKey: labbitQueryKeys.workspaceFileTree(
      labInstanceId,
      generation,
      directoryPath,
    ),
    queryFn: async ({ signal }) => {
      const startedAtEpoch = workspaceRequestEpoch.current
      try {
        const result = await api.listWorkspaceFiles(labInstanceId, directoryPath)
        if (!signal.aborted && startedAtEpoch === workspaceRequestEpoch.current) setWorkspaceGate((previous) => previous?.scope === scope
          ? { ...previous, verifiedTrees: [...new Set([...previous.verifiedTrees, directoryPath])] }
          : previous)
        return result
      } catch (error) {
        if (signal.aborted) throw error
        if (workspaceWideAccessChange(error)) {
          workspaceRequestEpoch.current += 1
          setWorkspaceGate({ scope, verifiedTrees: [], verifiedFiles: [] })
        }
        if (error instanceof HttpError && [403, 404, 409].includes(error.status) && selectedFilePath) {
          setRequiresFreshRead({ labInstanceId, generation, path: selectedFilePath })
        }
        throw error
      }
    },
    retry: false,
  })

  const selectedPath = selectedFilePath ?? ''
  const fileQuery = useQuery({
    queryKey: labbitQueryKeys.workspaceFile(
      labInstanceId,
      generation,
      selectedPath,
    ),
    queryFn: async ({ signal }) => {
      const startedAtEpoch = workspaceRequestEpoch.current
      try {
        const result = await api.readWorkspaceFile(labInstanceId, selectedPath)
        const validRead = !signal.aborted && startedAtEpoch === workspaceRequestEpoch.current
        if (validRead) setWorkspaceGate((previous) => previous?.scope === scope
          ? { ...previous, verifiedFiles: [...new Set([...previous.verifiedFiles, selectedPath])] }
          : previous)
        if (validRead) setRequiresFreshRead((previous) =>
          previous?.labInstanceId === labInstanceId &&
          previous?.generation === generation &&
          previous?.path === selectedPath ? null : previous,
        )
        return result
      } catch (error) {
        if (signal.aborted) throw error
        if (workspaceWideAccessChange(error)) {
          workspaceRequestEpoch.current += 1
          setWorkspaceGate({ scope, verifiedTrees: [], verifiedFiles: [] })
        }
        if (error instanceof HttpError && [403, 404, 409].includes(error.status)) {
          setRequiresFreshRead({ labInstanceId, generation, path: selectedPath })
        }
        throw error
      }
    },
    enabled: Boolean(selectedFilePath),
    retry: false,
    // 편집 중 자동 refetch가 Editor를 새 revision으로 remount해
    // 저장되지 않은 draft를 조용히 잃지 않도록 수동 reload만 허용한다.
    refetchOnWindowFocus: false,
    refetchOnReconnect: false,
  })

  // The editor may unmount on access loss, but drafts still need unload protection.
  useEffect(() => {
    if (ownedByLabPage || !draftBackup) return
    const warn = (event: BeforeUnloadEvent) => {
      event.preventDefault()
      event.returnValue = ''
    }
    window.addEventListener('beforeunload', warn)
    return () => window.removeEventListener('beforeunload', warn)
  }, [draftBackup, ownedByLabPage])

  const authExpired =
    (treeQuery.error instanceof HttpError && treeQuery.error.status === 401) ||
    (fileQuery.error instanceof HttpError && fileQuery.error.status === 401)

  // React Query retains the last successful result after a failed refetch.
  // 403/404/409 are authoritative permission, identity or generation changes:
  // never render cached file names/content alongside those failures.
  // For transient transport errors, preserve unsaved editor drafts.
  const treeAccessChanged =
    treeQuery.error instanceof HttpError &&
    [403, 404, 409].includes(treeQuery.error.status)
  const fileAccessChanged =
    fileQuery.error instanceof HttpError &&
    [403, 404, 409].includes(fileQuery.error.status)
  const gatedWorkspace = workspaceGate?.scope === scope
  const needsFreshTreeRead = gatedWorkspace && !workspaceGate.verifiedTrees.includes(directoryPath)
  const currentPathUnverified = gatedWorkspace && selectedFilePath !== null &&
    !workspaceGate.verifiedFiles.includes(selectedFilePath)
  const selectedAccessChanged = treeAccessChanged || fileAccessChanged
  // An authoritative 403/404/409 latches a fresh-read gate until the selected
  // File GET really succeeds. Transient 503 failures do not set this latch.
  const needsFreshFileRead = Boolean(currentPathUnverified) || (Boolean(selectedFilePath) &&
    requiresFreshRead?.labInstanceId === labInstanceId &&
    requiresFreshRead?.generation === generation &&
    requiresFreshRead?.path === selectedFilePath)
  const currentWorkspaceDraft = draftBackup?.labInstanceId === labInstanceId
  const previousGenerationDraft = currentWorkspaceDraft && draftBackup.generation !== generation
  const backupForSelected = currentWorkspaceDraft && draftBackup.path === selectedFilePath
  const restoredForSelected = restoredDraft?.scope === scope && restoredDraft?.path === selectedFilePath
    ? restoredDraft.content : null

  if (authExpired) {
    return <LoginRedirect reason="sessionExpired" />
  }

  function canLeaveEditor() {
    if (savePending) return false
    if (!hasUnsavedChanges) return true
    if (!window.confirm(
      '저장되지 않은 변경이 있습니다. 변경 내용을 버리고 다른 파일로 이동할까요?',
    )) return false
    setDraftBackup(null)
    setRestoredDraft(null)
    return true
  }

  function openEntry(entry: WorkspaceFileEntry) {
    if (entry.path === selectedFilePath) return
    if (!canLeaveEditor()) return

    setEditorDirty(false)

    if (entry.kind === 'directory') {
      setDirectoryPath(entry.path)
      setSelectedFilePath(null)
      return
    }

    setSelectedFilePath(entry.path)
  }

  function goUp() {
    if (!canLeaveEditor()) return

    setEditorDirty(false)
    setDirectoryPath(parentDirectory(directoryPath))
    setSelectedFilePath(null)
  }

  return (
    <>
      {!ownedByLabPage && (
        <StandaloneWorkspaceNavigationGuard
          hasUnsavedChanges={hasUnsavedChanges}
          savePending={savePending}
        />
      )}
      <aside className="workspace-panel workspace-files">
        <div className="workspace-panel-heading">
          <strong>파일</strong>
          <span>{(treeAccessChanged || needsFreshTreeRead) ? '접근 확인 필요' : directoryPath || 'Workspace root'}</span>
        </div>

        <div className="workspace-file-browser">
          {directoryPath && (
            <button
              className="workspace-file-up"
              type="button"
              disabled={savePending}
              onClick={goUp}
            >
              ← 상위 폴더
            </button>
          )}

          {treeQuery.isPending && (
            <div className="workspace-file-state">
              <p>파일 목록을 불러오는 중...</p>
            </div>
          )}

          {treeQuery.error && (
            <FilePanelError
              message={fileErrorMessage(treeQuery.error, 'list')}
              actionLabel="파일 목록 다시 불러오기"
              busy={treeQuery.isFetching}
              onAction={() => void treeQuery.refetch()}
            />
          )}

          {needsFreshTreeRead && !treeAccessChanged && (
            <div className="workspace-file-state" role="status">
              <p>Workspace 대상 변경 이후 폴더 내용을 다시 확인해야 합니다.</p>
              <button className="workspace-file-action" type="button"
                disabled={treeQuery.isFetching}
                onClick={() => void treeQuery.refetch()}>파일 목록 접근 다시 확인</button>
            </div>
          )}

          {treeQuery.data && !treeAccessChanged && !needsFreshTreeRead && treeQuery.data.items.length === 0 && (
            <div className="workspace-file-state">
              <p>이 폴더에는 표시할 파일이 없습니다.</p>
            </div>
          )}

          {treeQuery.data && !treeAccessChanged && !needsFreshTreeRead && treeQuery.data.items.length > 0 && (
            <div className="workspace-file-list" role="list" aria-label="Workspace 파일 목록">
              {treeQuery.data.items.map((entry) => (
                <button
                  key={entry.path}
                  className={
                    'workspace-file-entry' +
                    (entry.path === selectedFilePath ? ' workspace-file-entry-selected' : '')
                  }
                  type="button"
                  disabled={savePending}
                  onClick={() => openEntry(entry)}
                >
                  <span aria-hidden="true">
                    {entry.kind === 'directory' ? '▸' : '·'}
                  </span>
                  <span>{entry.name}</span>
                </button>
              ))}
            </div>
          )}
        </div>
      </aside>

      <section className="workspace-panel workspace-editor">
        <div className="workspace-panel-heading">
          <strong>Editor</strong>
          <span>{(selectedAccessChanged || needsFreshFileRead) ? '접근 확인 필요' : selectedFilePath || '파일을 선택하세요'}</span>
        </div>

        {!selectedFilePath && (
          <div className="workspace-placeholder">
            <span className="workspace-placeholder-mark" aria-hidden="true">
              &lt;/&gt;
            </span>
            <p>편집할 파일을 선택하세요.</p>
            <small>
              왼쪽 File Tree에서 UTF-8 text 파일을 선택하면 내용을 읽고 저장할 수 있습니다.
            </small>
          </div>
        )}

        {selectedFilePath && fileQuery.isPending && (
          <div className="workspace-file-state workspace-file-state-editor">
            <p>파일 내용을 불러오는 중...</p>
          </div>
        )}

        {selectedFilePath && fileQuery.error && (
          <FilePanelError
            message={fileErrorMessage(fileQuery.error, 'read')}
            actionLabel="파일 다시 읽기"
            busy={fileQuery.isFetching}
            onAction={() => void fileQuery.refetch()}
          />
        )}

        {previousGenerationDraft && (
          <div className="workspace-file-state" role="status">
            <p>이전 generation {draftBackup.generation}의 미저장 수정본이 메모리에 보관되어 있습니다.
              현재 generation {generation}에는 자동으로 적용되지 않습니다.</p>
            <button className="workspace-file-action" type="button" disabled={savePending}
              onClick={() => {
                setRestoredDraft(null)
                setEditorDirty(false)
                setDirectoryPath(parentDirectory(draftBackup.path))
                setSelectedFilePath(draftBackup.path)
              }}>현재 generation에서 해당 파일 확인</button>
            <button className="workspace-file-action" type="button" disabled={savePending}
              onClick={() => {
                if (window.confirm('이전 generation의 미저장 수정본을 버릴까요?')) {
                  setDraftBackup(null)
                  setRestoredDraft(null)
                  setEditorDirty(false)
                }
              }}>이전 수정본 버리기</button>
          </div>
        )}

        {selectedFilePath && selectedAccessChanged && backupForSelected && (
          <p className="workspace-file-state" role="status">
            미저장 수정본을 이 탭의 메모리에 임시 보관했습니다.
            접근 권한을 다시 확인한 후 복원할 수 있습니다. 새로고침하면 사라집니다.
          </p>
        )}

        {selectedFilePath && !selectedAccessChanged && needsFreshFileRead && (
          <div className="workspace-file-state" role="status">
            <p>이전 접근 오류 이후 파일 내용을 다시 확인해야 합니다.</p>
            <button className="workspace-file-action" type="button"
              disabled={fileQuery.isFetching}
              onClick={() => void fileQuery.refetch()}>파일 접근 다시 확인</button>
          </div>
        )}

        {selectedFilePath && fileQuery.data && !selectedAccessChanged && !needsFreshFileRead && backupForSelected && restoredForSelected === null && !editorDirty && !savePending && (
          <div className="workspace-file-state" role="status">
            <p>이전에 저장하지 못한 수정본이 있습니다. 최신 파일과 비교한 뒤 복원하거나 버려 주세요.</p>
            <button className="workspace-file-action" type="button"
              onClick={() => {
                if (window.confirm(
                  '미저장 수정본을 다시 편집기에 불러옵니다. 서버 파일의 버전이 달라졌을 수 있으니 저장 전에 반드시 내용을 비교해 주세요.',
                )) {
                  setRestoredDraft({ scope, path: selectedFilePath, content: draftBackup.content })
                  setEditorDirty(draftBackup.content !== fileQuery.data.file.content)
                  setRestoreEpoch((epoch) => epoch + 1)
                }
              }}>미저장 수정본 복원</button>
            <button className="workspace-file-action" type="button"
              onClick={() => {
                if (window.confirm('복원하지 않은 미저장 수정본을 버릴까요?')) {
                  setDraftBackup(null)
                  setRestoredDraft(null)
                }
              }}>수정본 버리기</button>
          </div>
        )}

        {selectedFilePath && fileQuery.data && !selectedAccessChanged && !needsFreshFileRead &&
          (!previousGenerationDraft || restoredForSelected !== null) && (
          <WorkspaceFileEditor
            key={`${labInstanceId}:${generation}:${fileQuery.data.file.path}:${fileQuery.data.etag}:${fileQuery.dataUpdatedAt}:${restoreEpoch}`}
            labInstanceId={labInstanceId}
            generation={generation}
            versionedFile={fileQuery.data}
            onDirtyChange={setEditorDirty}
            onSavePendingChange={setSavePending}
            savePending={savePending}
            restoredDraft={restoredForSelected ?? undefined}
            onDraftChange={(content, sourceEtag) => {
              if (content === null) setRestoredDraft(null)
              setDraftBackup(content === null ? null : {
                labInstanceId,
                generation,
                path: fileQuery.data.file.path,
                content,
                sourceEtag,
              })
            }}
            onSaveAccepted={(savedContent, sourceEtag) => {
              const active = activeEditorScope.current
              // The Editor may already be unmounted during LabPage PROVISIONING.
              // Its local ref alone cannot tell whether the page changed
              // generations; defer to the current parent Workspace identity.
              if (isCurrentWorkspace && !isCurrentWorkspace(labInstanceId, generation)) return
              if (active.labInstanceId !== labInstanceId ||
                  active.generation !== generation ||
                  active.path !== fileQuery.data.file.path) return
              setDraftBackup((previous) =>
                previous?.labInstanceId === labInstanceId &&
                previous.generation === generation &&
                previous.path === fileQuery.data.file.path &&
                previous.sourceEtag === sourceEtag &&
                previous.content === savedContent ? null : previous)
              setEditorDirty(false)
              setRestoredDraft(null)
            }}
            onReload={() => fileQuery.refetch()}
          />
        )}
      </section>
    </>
  )
}
