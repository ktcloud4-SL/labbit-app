import { useMutation, useQuery } from '@tanstack/react-query'
import { useEffect, useState } from 'react'

import type {
  VersionedWorkspaceFileContent,
  WorkspaceFileEntry,
} from '../api/contracts'
import { HttpError } from '../api/httpClient'
import { useLabbitApi } from '../api/LabbitApiProvider'
import { labbitQueryKeys } from '../api/labbitApi'
import { LoginRedirect } from '../ui/LoginRedirect'

interface WorkspaceFilePanelsProps {
  labInstanceId: string
  generation: number
}

interface WorkspaceFileEditorProps {
  labInstanceId: string
  generation: number
  versionedFile: VersionedWorkspaceFileContent
  onReload(): Promise<unknown>
  onDirtyChange(dirty: boolean): void
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
}: WorkspaceFileEditorProps) {
  const api = useLabbitApi()
  const [draft, setDraft] = useState(versionedFile.file.content)
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
      onDirtyChange(false)
    },
    onError: (error) => {
      setSaveError(error)
    },
  })

  const dirty = draft !== savedContent

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
          disabled={!dirty || saveMutation.isPending}
          onClick={() => saveMutation.mutate()}
        >
          {saveMutation.isPending ? '저장 중...' : '저장'}
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
                onDirtyChange(false)
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
        value={draft}
        onChange={(event) => {
          const next = event.target.value
          setDraft(next)
          onDirtyChange(next !== savedContent)
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

export function WorkspaceFilePanels({
  labInstanceId,
  generation,
}: WorkspaceFilePanelsProps) {
  const api = useLabbitApi()
  const [directoryPath, setDirectoryPath] = useState('')
  const [selectedFilePath, setSelectedFilePath] = useState<string | null>(null)
  const [editorDirty, setEditorDirty] = useState(false)

  const treeQuery = useQuery({
    queryKey: labbitQueryKeys.workspaceFileTree(
      labInstanceId,
      generation,
      directoryPath,
    ),
    queryFn: () => api.listWorkspaceFiles(labInstanceId, directoryPath),
    retry: false,
  })

  const selectedPath = selectedFilePath ?? ''
  const fileQuery = useQuery({
    queryKey: labbitQueryKeys.workspaceFile(
      labInstanceId,
      generation,
      selectedPath,
    ),
    queryFn: () => api.readWorkspaceFile(labInstanceId, selectedPath),
    enabled: Boolean(selectedFilePath),
    retry: false,
  })

  const authExpired =
    (treeQuery.error instanceof HttpError && treeQuery.error.status === 401) ||
    (fileQuery.error instanceof HttpError && fileQuery.error.status === 401)

  if (authExpired) {
    return <LoginRedirect reason="sessionExpired" />
  }

  function canLeaveEditor() {
    if (!editorDirty) return true
    return window.confirm(
      '저장되지 않은 변경이 있습니다. 변경 내용을 버리고 다른 파일로 이동할까요?',
    )
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
      <aside className="workspace-panel workspace-files">
        <div className="workspace-panel-heading">
          <strong>파일</strong>
          <span>{directoryPath || 'Workspace root'}</span>
        </div>

        <div className="workspace-file-browser">
          {directoryPath && (
            <button
              className="workspace-file-up"
              type="button"
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

          {treeQuery.data && treeQuery.data.items.length === 0 && (
            <div className="workspace-file-state">
              <p>이 폴더에는 표시할 파일이 없습니다.</p>
            </div>
          )}

          {treeQuery.data && treeQuery.data.items.length > 0 && (
            <div className="workspace-file-list" role="list" aria-label="Workspace 파일 목록">
              {treeQuery.data.items.map((entry) => (
                <button
                  key={entry.path}
                  className={
                    'workspace-file-entry' +
                    (entry.path === selectedFilePath ? ' workspace-file-entry-selected' : '')
                  }
                  type="button"
                  role="listitem"
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
          <span>{selectedFilePath || '파일을 선택하세요'}</span>
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

        {selectedFilePath && fileQuery.data && (
          <WorkspaceFileEditor
            key={`${fileQuery.data.file.path}:${fileQuery.data.etag}:${fileQuery.dataUpdatedAt}`}
            labInstanceId={labInstanceId}
            generation={generation}
            versionedFile={fileQuery.data}
            onDirtyChange={setEditorDirty}
            onReload={async () => {
              setEditorDirty(false)
              return fileQuery.refetch()
            }}
          />
        )}
      </section>
    </>
  )
}
