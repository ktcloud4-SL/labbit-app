import { act, fireEvent, render, screen, waitFor, cleanup } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { createMemoryRouter, RouterProvider } from 'react-router-dom'
import { afterEach, describe, expect, it, vi } from 'vitest'

import { HttpError } from '../api/httpClient'
import type { ClassDetail } from '../api/contracts'
import { LabbitApiProvider } from '../api/LabbitApiProvider'
import { labbitQueryKeys } from '../api/labbitApi'
import { mockLabbitApi } from '../api/mockLabbitApi'
import { WorkspaceFilePanels } from './WorkspaceFilePanels'
import { LabPage } from '../../pages/LabPage'

// The integration test exercises the real LabPage file-panel lifetime without
// creating a Terminal/Preview socket or any shared VM resources.
vi.mock('../terminal/TerminalPanel', () => ({ TerminalPanel: () => null }))
vi.mock('../preview/PreviewPanel', () => ({ PreviewPanel: () => null }))
import { getWorkspaceEditState } from './workspaceEditState'

const labId = 'lab-instance-file-boundary'
const path = 'README.md'
const tree = { path: '', items: [{ name: path, path, kind: 'file' as const }] }
const workspaceDocument = { file: { path, content: '# authorized content\n' }, etag: '"revision-1"' }

function mount(overrides: Partial<typeof mockLabbitApi>) {
  const api = { ...mockLabbitApi, ...overrides }
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false, refetchOnWindowFocus: false } },
  })
  const router = createMemoryRouter([
    { path: '/workspace', element: <WorkspaceFilePanels labInstanceId={labId} generation={1} /> },
  ], { initialEntries: ['/workspace'] })
  render(
    <QueryClientProvider client={client}>
      <LabbitApiProvider api={api}><RouterProvider router={router} /></LabbitApiProvider>
    </QueryClientProvider>,
  )
  return { client }
}

afterEach(() => { cleanup(); vi.restoreAllMocks() })

describe('LBT-27 Workspace File authoritative access-change boundary', () => {
  it.each([403, 404, 409])('hides stale File Tree names when a later refetch returns %i', async (status) => {
    let denied = false
    const { client } = mount({
      listWorkspaceFiles: async () => {
        if (denied) throw new HttpError(status)
        return tree
      },
    })
    expect(await screen.findByRole('button', { name: /README.md/ })).toBeInTheDocument()
    denied = true
    await act(async () => {
      await client.invalidateQueries({ queryKey: labbitQueryKeys.workspaceFileTree(labId, 1, '') })
    })
    await waitFor(() => expect(screen.getByRole('alert')).toBeInTheDocument())
    expect(screen.queryByRole('button', { name: /README.md/ })).not.toBeInTheDocument()
    expect(screen.getByRole('button', { name: '파일 목록 다시 불러오기' })).toBeEnabled()
    expect(client.getQueryData(labbitQueryKeys.workspaceFileTree(labId, 1, ''))).toEqual(tree)
  })

  it.each([403, 404, 409])('removes previously displayed editor bytes after a later read %i', async (status) => {
    let denied = false
    const { client } = mount({
      listWorkspaceFiles: async () => tree,
      readWorkspaceFile: async () => {
        if (denied) throw new HttpError(status)
        return workspaceDocument
      },
    })
    fireEvent.click(await screen.findByRole('button', { name: /README.md/ }))
    expect(await screen.findByLabelText('파일 편집기')).toHaveValue(workspaceDocument.file.content)
    denied = true
    await act(async () => {
      await client.invalidateQueries({ queryKey: labbitQueryKeys.workspaceFile(labId, 1, path) })
    })
    await waitFor(() => expect(screen.getByRole('alert')).toBeInTheDocument())
    expect(screen.queryByLabelText('파일 편집기')).not.toBeInTheDocument()
    expect(screen.getByRole('button', { name: '파일 다시 읽기' })).toBeInTheDocument()
    expect(client.getQueryData(labbitQueryKeys.workspaceFile(labId, 1, path))).toEqual(workspaceDocument)
  })

  it('Tree 403 hides an already open Editor and retains draft until a fresh authorized File GET', async () => {
    let treeDenied = false
    const readWorkspaceFile = vi.fn(async () => workspaceDocument)
    const { client } = mount({
      listWorkspaceFiles: async () => {
        if (treeDenied) throw new HttpError(403)
        return tree
      },
      readWorkspaceFile,
    })
    fireEvent.click(await screen.findByRole('button', { name: /README.md/ }))
    fireEvent.change(await screen.findByLabelText('파일 편집기'), {
      target: { value: '# sensitive unsaved edit\n' },
    })
    treeDenied = true
    await act(async () => {
      await client.invalidateQueries({ queryKey: labbitQueryKeys.workspaceFileTree(labId, 1, '') })
    })
    expect(await screen.findByText(/미저장 수정본을 이 탭의 메모리에 임시 보관했습니다/)).toBeInTheDocument()
    expect(screen.queryByLabelText('파일 편집기')).not.toBeInTheDocument()
    expect(document.body.textContent).not.toContain('# sensitive unsaved edit')
    expect(getWorkspaceEditState().dirty).toBe(true)

    treeDenied = false
    await act(async () => {
      await client.invalidateQueries({ queryKey: labbitQueryKeys.workspaceFileTree(labId, 1, '') })
    })
    const verify = await screen.findByRole('button', { name: '파일 접근 다시 확인' })
    expect(screen.queryByLabelText('파일 편집기')).not.toBeInTheDocument()
    expect(readWorkspaceFile).toHaveBeenCalledTimes(1)
    fireEvent.click(verify)
    expect(await screen.findByRole('button', { name: '미저장 수정본 복원' })).toBeInTheDocument()
    expect((screen.getByLabelText('파일 편집기') as HTMLTextAreaElement).value.trimEnd()).toBe('# authorized content')

    vi.spyOn(window, 'confirm').mockReturnValue(true)
    fireEvent.click(screen.getByRole('button', { name: '미저장 수정본 복원' }))
    expect((await screen.findByLabelText('파일 편집기') as HTMLTextAreaElement).value.trimEnd()).toBe('# sensitive unsaved edit')
    expect(getWorkspaceEditState().dirty).toBe(true)
    expect(readWorkspaceFile).toHaveBeenCalledTimes(2)
  })

  it('File 409 hides edited draft and restores only after a new authorized read', async () => {
    let fileDenied = false
    const { client } = mount({
      listWorkspaceFiles: async () => tree,
      readWorkspaceFile: async () => {
        if (fileDenied) throw new HttpError(409)
        return workspaceDocument
      },
    })
    fireEvent.click(await screen.findByRole('button', { name: /README.md/ }))
    fireEvent.change(await screen.findByLabelText('파일 편집기'), {
      target: { value: '# private draft\n' },
    })
    fileDenied = true
    await act(async () => {
      await client.invalidateQueries({ queryKey: labbitQueryKeys.workspaceFile(labId, 1, path) })
    })
    expect(await screen.findByText(/미저장 수정본을 이 탭의 메모리에 임시 보관했습니다/)).toBeInTheDocument()
    expect(screen.queryByLabelText('파일 편집기')).not.toBeInTheDocument()
    expect(document.body.textContent).not.toContain('# private draft')
    expect(getWorkspaceEditState().dirty).toBe(true)

    fileDenied = false
    fireEvent.click(screen.getByRole('button', { name: '파일 다시 읽기' }))
    expect(await screen.findByRole('button', { name: '미저장 수정본 복원' })).toBeInTheDocument()
    expect((screen.getByLabelText('파일 편집기') as HTMLTextAreaElement).value.trimEnd()).toBe('# authorized content')
    vi.spyOn(window, 'confirm').mockReturnValue(true)
    fireEvent.click(screen.getByRole('button', { name: '수정본 버리기' }))
    expect(screen.queryByRole('button', { name: '미저장 수정본 복원' })).not.toBeInTheDocument()
    expect(getWorkspaceEditState().dirty).toBe(false)
  })

  it('does not release pending PUT barrier when a Tree 403 unmounts the saving Editor', async () => {
    let treeDenied = false
    let finishSave!: (result: { file: { path: string }; etag: string }) => void
    const saveWorkspaceFile = vi.fn(() => new Promise<{ file: { path: string }; etag: string }>((resolve) => {
      finishSave = resolve
    }))
    const { client } = mount({
      listWorkspaceFiles: async () => {
        if (treeDenied) throw new HttpError(403)
        return tree
      },
      readWorkspaceFile: async () => workspaceDocument,
      saveWorkspaceFile,
    })

    fireEvent.click(await screen.findByRole('button', { name: /README.md/ }))
    fireEvent.change(await screen.findByLabelText('파일 편집기'), {
      target: { value: '# pending save edit' },
    })
    fireEvent.click(screen.getByRole('button', { name: '저장' }))
    await waitFor(() => expect(getWorkspaceEditState().savePending).toBe(true))
    treeDenied = true
    await act(async () => {
      await client.invalidateQueries({ queryKey: labbitQueryKeys.workspaceFileTree(labId, 1, '') })
    })
    expect(screen.queryByLabelText('파일 편집기')).not.toBeInTheDocument()
    expect(getWorkspaceEditState().savePending).toBe(true)
    expect(saveWorkspaceFile).toHaveBeenCalledTimes(1)

    await act(async () => finishSave({ file: { path }, etag: '"revision-2"' }))
    await waitFor(() => expect(getWorkspaceEditState().savePending).toBe(false))
    expect(saveWorkspaceFile).toHaveBeenCalledTimes(1)
  })


  it('blocks cached file B after workspace-wide 409 on A until a fresh B GET succeeds', async () => {
    const entries = { path: '', items: [
      { name: 'A.md', path: 'A.md', kind: 'file' as const },
      { name: 'B.md', path: 'B.md', kind: 'file' as const },
    ] }
    let denyA = false
    let delayB = false
    let resumeB!: (result: typeof workspaceDocument) => void
    const b = { file: { path: 'B.md', content: '# cached B should be hidden' }, etag: '"B-1"' }
    const { client } = mount({
      listWorkspaceFiles: async () => entries,
      readWorkspaceFile: async (_id, filePath) => {
        if (filePath === 'A.md' && denyA) throw new HttpError(409, {
          type: 'about:blank', title: 'Conflict', status: 409,
          code: 'workspace_target_changed', requestId: 'fixture',
        })
        if (filePath === 'B.md') {
          if (delayB) return new Promise<typeof workspaceDocument>((resolve) => { resumeB = resolve })
          return b
        }
        return { file: { path: 'A.md', content: '# file A' }, etag: '"A-1"' }
      },
    })
    fireEvent.click(await screen.findByRole('button', { name: 'B.md' }))
    expect(await screen.findByLabelText('파일 편집기')).toHaveValue(b.file.content)
    fireEvent.click(screen.getByRole('button', { name: 'A.md' }))
    expect(await screen.findByLabelText('파일 편집기')).toHaveValue('# file A')
    denyA = true
    await act(async () => {
      await client.invalidateQueries({ queryKey: labbitQueryKeys.workspaceFile(labId, 1, 'A.md') })
    })
    expect(screen.queryByRole('button', { name: 'B.md' })).not.toBeInTheDocument()
    expect(screen.queryByLabelText('파일 편집기')).not.toBeInTheDocument()
    fireEvent.click(screen.getByRole('button', { name: '파일 목록 접근 다시 확인' }))
    expect(await screen.findByRole('button', { name: 'B.md' })).toBeInTheDocument()
    delayB = true
    fireEvent.click(screen.getByRole('button', { name: 'B.md' }))
    await waitFor(() => expect(resumeB).toBeDefined())
    expect(client.getQueryData(labbitQueryKeys.workspaceFile(labId, 1, 'B.md'))).toEqual(b)
    expect(screen.queryByLabelText('파일 편집기')).not.toBeInTheDocument()
    expect(document.body.textContent).not.toContain(b.file.content)
    await act(async () => { resumeB(b) })
    expect(await screen.findByLabelText('파일 편집기')).toHaveValue(b.file.content)
  })

  it('blocks cached parent directory after a nested workspace-wide 403 until a fresh Tree GET', async () => {
    let denyNested = false
    const readTree = vi.fn(async (_id: string, dir = '') => {
      if (dir === 'docs' && denyNested) throw new HttpError(403)
      return dir === 'docs'
        ? { path: 'docs', items: [{ name: 'private.md', path: 'docs/private.md', kind: 'file' as const }] }
        : { path: '', items: [{ name: 'docs', path: 'docs', kind: 'directory' as const }] }
    })
    const { client } = mount({ listWorkspaceFiles: readTree })
    fireEvent.click(await screen.findByRole('button', { name: 'docs' }))
    expect(await screen.findByRole('button', { name: 'private.md' })).toBeInTheDocument()
    denyNested = true
    await act(async () => {
      await client.invalidateQueries({ queryKey: labbitQueryKeys.workspaceFileTree(labId, 1, 'docs') })
    })
    expect(screen.queryByRole('button', { name: 'private.md' })).not.toBeInTheDocument()
    fireEvent.click(screen.getByRole('button', { name: /상위 폴더/ }))
    expect(screen.queryByRole('button', { name: 'docs' })).not.toBeInTheDocument()
    expect(screen.getByRole('button', { name: '파일 목록 접근 다시 확인' })).toBeInTheDocument()
    fireEvent.click(screen.getByRole('button', { name: '파일 목록 접근 다시 확인' }))
    expect(await screen.findByRole('button', { name: 'docs' })).toBeInTheDocument()
    expect(readTree.mock.calls.filter(([, dir]) => dir === '').length).toBeGreaterThan(1)
  })

  it('does not global-revoke unrelated files for specific file_permission_denied 403', async () => {
    const entries = { path: '', items: [
      { name: 'A.md', path: 'A.md', kind: 'file' as const },
      { name: 'B.md', path: 'B.md', kind: 'file' as const },
    ] }
    let forbidden = false
    const { client } = mount({
      listWorkspaceFiles: async () => entries,
      readWorkspaceFile: async (_id, filePath) => {
        if (forbidden && filePath === 'A.md') throw new HttpError(403, {
          type: 'about:blank', title: 'Forbidden', status: 403,
          code: 'file_permission_denied', requestId: 'fixture',
        })
        return { file: { path: filePath, content: filePath }, etag: '"ok"' }
      },
    })
    fireEvent.click(await screen.findByRole('button', { name: 'A.md' }))
    expect(await screen.findByLabelText('파일 편집기')).toHaveValue('A.md')
    forbidden = true
    await act(async () => {
      await client.invalidateQueries({ queryKey: labbitQueryKeys.workspaceFile(labId, 1, 'A.md') })
    })
    expect(screen.getByRole('button', { name: 'B.md' })).toBeInTheDocument()
    expect(screen.queryByLabelText('파일 편집기')).not.toBeInTheDocument()
    fireEvent.click(screen.getByRole('button', { name: 'B.md' }))
    expect(await screen.findByLabelText('파일 편집기')).toHaveValue('B.md')
  })

  it('retains dirty draft across LabPage READY generation change and explicit recovery only', async () => {
    let generation = 1
    let status: 'READY' | 'PROVISIONING' = 'READY'
    const classDetail = (): ClassDetail => ({
      id: 'class-a', name: 'Workspace Test', myRole: 'STUDENT',
      activeLabExecution: { id: 'execution-a', status: 'ACTIVE' },
      myLabInstance: { id: labId, userId: 'student-a', generation, status },
    })
    const api = { ...mockLabbitApi,
      getClass: async () => classDetail(),
      listWorkspaceFiles: async () => tree,
      readWorkspaceFile: async () => ({
        file: { path, content: generation === 1 ? '# old server content' : '# new generation content' },
        etag: generation === 1 ? '"g1"' : '"g2"',
      }),
    }
    const client = new QueryClient({ defaultOptions: { queries: { retry: false, refetchOnWindowFocus: false } } })
    const router = createMemoryRouter([
      { path: '/classes/:classId/lab', element: <LabPage /> },
    ], { initialEntries: ['/classes/class-a/lab'] })
    render(<QueryClientProvider client={client}>
      <LabbitApiProvider api={api}><RouterProvider router={router} /></LabbitApiProvider>
    </QueryClientProvider>)
    fireEvent.click(await screen.findByRole('button', { name: /README.md/ }))
    fireEvent.change(await screen.findByLabelText('파일 편집기'), {
      target: { value: '# unpublished generation one changes' },
    })
    // The real LabPage switches to a non-READY branch during Reset.
    status = 'PROVISIONING'
    await act(async () => { await client.invalidateQueries({ queryKey: labbitQueryKeys.classDetail('class-a') }) })
    expect(await screen.findByText(/실습 환경을 준비하고 있습니다/)).toBeInTheDocument()
    status = 'READY'
    generation = 2
    await act(async () => { await client.invalidateQueries({ queryKey: labbitQueryKeys.classDetail('class-a') }) })
    expect(await screen.findByText(/이전 generation 1의 미저장 수정본/)).toBeInTheDocument()
    expect(document.body.textContent).not.toContain('# unpublished generation one changes')
    expect(screen.queryByLabelText('파일 편집기')).not.toBeInTheDocument()
    fireEvent.click(screen.getByRole('button', { name: '현재 generation에서 해당 파일 확인' }))
    expect(await screen.findByRole('button', { name: '미저장 수정본 복원' })).toBeInTheDocument()
    vi.spyOn(window, 'confirm').mockReturnValue(true)
    fireEvent.click(screen.getByRole('button', { name: '미저장 수정본 복원' }))
    expect(await screen.findByLabelText('파일 편집기')).toHaveValue('# unpublished generation one changes')
    expect(getWorkspaceEditState().dirty).toBe(true)
  })

  it('blocks a remounted Editor while an earlier PUT is in flight after a Tree denial', async () => {
    let treeDenied = false
    let finishSave!: (result: { file: { path: string }; etag: string }) => void
    const saveWorkspaceFile = vi.fn(() =>
      new Promise<{ file: { path: string }; etag: string }>((resolve) => { finishSave = resolve }))
    const readWorkspaceFile = vi.fn(async () => workspaceDocument)
    const { client } = mount({
      listWorkspaceFiles: async () => {
        if (treeDenied) throw new HttpError(403)
        return tree
      },
      readWorkspaceFile,
      saveWorkspaceFile,
    })
    fireEvent.click(await screen.findByRole('button', { name: /README.md/ }))
    fireEvent.change(await screen.findByLabelText('파일 편집기'), {
      target: { value: '# unsaved during PUT' },
    })
    fireEvent.click(screen.getByRole('button', { name: '저장' }))
    await waitFor(() => expect(getWorkspaceEditState().savePending).toBe(true))
    treeDenied = true
    await act(async () => {
      await client.invalidateQueries({ queryKey: labbitQueryKeys.workspaceFileTree(labId, 1, '') })
    })
    treeDenied = false
    fireEvent.click(screen.getByRole('button', { name: '파일 목록 다시 불러오기' }))
    expect(await screen.findByRole('button', { name: '파일 접근 다시 확인' })).toBeInTheDocument()
    fireEvent.click(screen.getByRole('button', { name: '파일 접근 다시 확인' }))
    const save = await screen.findByRole('button', { name: '저장 중...' })
    expect(save).toBeDisabled()
    expect(screen.getByLabelText('파일 편집기')).toBeDisabled()
    expect(saveWorkspaceFile).toHaveBeenCalledTimes(1)
    await act(async () => finishSave({ file: { path }, etag: '"revision-2"' }))
    await waitFor(() => expect(getWorkspaceEditState().savePending).toBe(false))
    expect(saveWorkspaceFile).toHaveBeenCalledTimes(1)
  })

  it('keeps an unsaved editor draft during a transient 503 refetch failure', async () => {
    let unavailable = false
    const { client } = mount({
      listWorkspaceFiles: async () => tree,
      readWorkspaceFile: async () => {
        if (unavailable) throw new HttpError(503)
        return workspaceDocument
      },
    })
    fireEvent.click(await screen.findByRole('button', { name: /README.md/ }))
    const editor = await screen.findByLabelText('파일 편집기')
    fireEvent.change(editor, { target: { value: '# local unsaved draft\n' } })
    unavailable = true
    await act(async () => {
      await client.invalidateQueries({ queryKey: labbitQueryKeys.workspaceFile(labId, 1, path) })
    })
    await waitFor(() => expect(screen.getByRole('alert')).toBeInTheDocument())
    expect(screen.getByLabelText('파일 편집기')).toHaveValue('# local unsaved draft\n')
    expect(screen.getByText('저장되지 않은 변경')).toBeInTheDocument()
  })
})
