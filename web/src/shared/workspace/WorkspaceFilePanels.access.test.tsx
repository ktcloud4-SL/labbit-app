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
    fireEvent.click(await screen.findByRole('button', { name: '파일 목록 다시 불러오기' }))
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

  it('preserves the previous-generation draft when its in-flight PUT finishes after Reset', async () => {
    let generation = 1
    let status: 'READY' | 'PROVISIONING' = 'READY'
    let completePut!: (result: { file: { path: string }; etag: string }) => void
    const saveWorkspaceFile = vi.fn(() => new Promise<{ file: { path: string }; etag: string }>(
      (resolve) => { completePut = resolve },
    ))
    const api = {
      ...mockLabbitApi,
      getClass: async (): Promise<ClassDetail> => ({
        id: 'class-a', name: 'Reset test', myRole: 'STUDENT',
        activeLabExecution: { id: 'execution-a', status: 'ACTIVE' },
        myLabInstance: { id: labId, userId: 'student-a', generation, status },
      }),
      listWorkspaceFiles: async () => tree,
      readWorkspaceFile: async () => ({
        file: { path, content: generation === 1 ? '# gen1' : '# gen2' },
        etag: generation === 1 ? '"g1"' : '"g2"',
      }),
      saveWorkspaceFile,
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
      target: { value: '# gen1 pending put' },
    })
    fireEvent.click(screen.getByRole('button', { name: '저장' }))
    await waitFor(() => expect(saveWorkspaceFile).toHaveBeenCalledTimes(1))
    expect(getWorkspaceEditState().savePending).toBe(true)

    status = 'PROVISIONING'
    await act(async () => { await client.invalidateQueries({ queryKey: labbitQueryKeys.classDetail('class-a') }) })
    expect(await screen.findByText(/실습 환경을 준비하고 있습니다/)).toBeInTheDocument()
    expect(getWorkspaceEditState().savePending).toBe(true)
    expect(getWorkspaceEditState().dirty).toBe(true)

    generation = 2
    status = 'READY'
    await act(async () => { await client.invalidateQueries({ queryKey: labbitQueryKeys.classDetail('class-a') }) })
    expect(await screen.findByText(/이전 generation 1의 미저장 수정본/)).toBeInTheDocument()

    await act(async () => completePut({ file: { path }, etag: '"gen1-saved"' }))
    await waitFor(() => expect(getWorkspaceEditState().savePending).toBe(false))
    expect(getWorkspaceEditState().dirty).toBe(true)
    expect(screen.getByText(/이전 generation 1의 미저장 수정본/)).toBeInTheDocument()
    expect(screen.queryByLabelText('파일 편집기')).not.toBeInTheDocument()

    fireEvent.click(screen.getByRole('button', { name: '현재 generation에서 해당 파일 확인' }))
    expect(await screen.findByRole('button', { name: '미저장 수정본 복원' })).toBeInTheDocument()
    vi.spyOn(window, 'confirm').mockReturnValue(true)
    fireEvent.click(screen.getByRole('button', { name: '미저장 수정본 복원' }))
    expect(await screen.findByLabelText('파일 편집기')).toHaveValue('# gen1 pending put')
    expect(saveWorkspaceFile).toHaveBeenCalledTimes(1)
  })

  it('protects a dirty draft from route exit and browser reload during non-READY LabPage', async () => {
    let status: 'READY' | 'PROVISIONING' = 'READY'
    const api = {
      ...mockLabbitApi,
      getClass: async (): Promise<ClassDetail> => ({
        id: 'class-a', name: 'Navigation test', myRole: 'STUDENT',
        activeLabExecution: { id: 'execution-a', status: 'ACTIVE' },
        myLabInstance: { id: labId, userId: 'student-a', generation: 1, status },
      }),
      listWorkspaceFiles: async () => tree,
      readWorkspaceFile: async () => workspaceDocument,
    }
    const client = new QueryClient({ defaultOptions: { queries: { retry: false, refetchOnWindowFocus: false } } })
    const router = createMemoryRouter([
      { path: '/classes/:classId/lab', element: <LabPage /> },
      { path: '/classes/:classId', element: <div>Class detail destination</div> },
    ], { initialEntries: ['/classes/class-a/lab'] })
    render(<QueryClientProvider client={client}>
      <LabbitApiProvider api={api}><RouterProvider router={router} /></LabbitApiProvider>
    </QueryClientProvider>)

    fireEvent.click(await screen.findByRole('button', { name: /README.md/ }))
    fireEvent.change(await screen.findByLabelText('파일 편집기'), {
      target: { value: '# unsaved during provisioning' },
    })
    status = 'PROVISIONING'
    await act(async () => { await client.invalidateQueries({ queryKey: labbitQueryKeys.classDetail('class-a') }) })
    expect(await screen.findByText(/실습 환경을 준비하고 있습니다/)).toBeInTheDocument()
    expect(getWorkspaceEditState().dirty).toBe(true)

    const beforeUnload = new Event('beforeunload', { cancelable: true })
    window.dispatchEvent(beforeUnload)
    expect(beforeUnload.defaultPrevented).toBe(true)

    const confirm = vi.spyOn(window, 'confirm').mockReturnValueOnce(false).mockReturnValueOnce(true)
    fireEvent.click(screen.getByRole('link', { name: /수업 상세/ }))
    await waitFor(() => expect(confirm).toHaveBeenCalledTimes(1))
    expect(screen.getByText(/실습 환경을 준비하고 있습니다/)).toBeInTheDocument()

    fireEvent.click(screen.getByRole('link', { name: /수업 상세/ }))
    expect(await screen.findByText('Class detail destination')).toBeInTheDocument()
    expect(confirm).toHaveBeenCalledTimes(2)
  })

  it('does not treat a pre-revocation delayed File GET as a fresh authorized reread', async () => {
    let readCount = 0
    let denyTree = false
    let finishStaleRead!: (value: typeof workspaceDocument) => void
    const { client } = mount({
      listWorkspaceFiles: async () => {
        if (denyTree) throw new HttpError(409)
        return tree
      },
      readWorkspaceFile: async () => {
        readCount += 1
        if (readCount === 2) {
          return new Promise<typeof workspaceDocument>((resolve) => { finishStaleRead = resolve })
        }
        return workspaceDocument
      },
    })
    fireEvent.click(await screen.findByRole('button', { name: /README.md/ }))
    expect(await screen.findByLabelText('파일 편집기')).toHaveValue(workspaceDocument.file.content)

    act(() => {
      void client.invalidateQueries({ queryKey: labbitQueryKeys.workspaceFile(labId, 1, path) })
    })
    await waitFor(() => expect(finishStaleRead).toBeDefined())

    denyTree = true
    await act(async () => {
      await client.invalidateQueries({ queryKey: labbitQueryKeys.workspaceFileTree(labId, 1, '') })
    })
    expect(screen.queryByLabelText('파일 편집기')).not.toBeInTheDocument()
    await act(async () => { finishStaleRead(workspaceDocument) })
    expect(screen.queryByLabelText('파일 편집기')).not.toBeInTheDocument()

    denyTree = false
    await act(async () => {
      await client.invalidateQueries({ queryKey: labbitQueryKeys.workspaceFileTree(labId, 1, '') })
    })
    expect(await screen.findByRole('button', { name: '파일 접근 다시 확인' })).toBeInTheDocument()
    expect(screen.queryByLabelText('파일 편집기')).not.toBeInTheDocument()
    fireEvent.click(screen.getByRole('button', { name: '파일 접근 다시 확인' }))
    expect(await screen.findByLabelText('파일 편집기')).toHaveValue(workspaceDocument.file.content)
    expect(readCount).toBe(3)
  })

  it('retains an old LabInstance draft on instance replacement until explicit discard', async () => {
    let activeLabId = labId
    const api = {
      ...mockLabbitApi,
      getClass: async (): Promise<ClassDetail> => ({
        id: 'class-a', name: 'Instance swap test', myRole: 'STUDENT',
        activeLabExecution: { id: 'execution-a', status: 'ACTIVE' },
        myLabInstance: { id: activeLabId, userId: 'student-a', generation: 1, status: 'READY' },
      }),
      listWorkspaceFiles: async () => tree,
      readWorkspaceFile: async () => workspaceDocument,
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
      target: { value: '# unsaved previous instance draft' },
    })
    expect(getWorkspaceEditState().dirty).toBe(true)

    activeLabId = 'replacement-lab-instance'
    await act(async () => { await client.invalidateQueries({ queryKey: labbitQueryKeys.classDetail('class-a') }) })
    const discard = await screen.findByRole('button', { name: '이전 LabInstance 수정본 버리기' })
    expect(screen.queryByLabelText('파일 편집기')).not.toBeInTheDocument()
    expect(document.body.textContent).not.toContain('# unsaved previous instance draft')
    expect(getWorkspaceEditState().dirty).toBe(true)

    const confirm = vi.spyOn(window, 'confirm').mockReturnValueOnce(false).mockReturnValueOnce(true)
    fireEvent.click(discard)
    expect(screen.getByRole('button', { name: '이전 LabInstance 수정본 버리기' })).toBeInTheDocument()
    expect(getWorkspaceEditState().dirty).toBe(true)
    fireEvent.click(discard)
    expect(confirm).toHaveBeenCalledTimes(2)
    expect(await screen.findByLabelText('파일 편집기')).toHaveValue(workspaceDocument.file.content)
    expect(getWorkspaceEditState().dirty).toBe(false)
    fireEvent.change(screen.getByLabelText('파일 편집기'), { target: { value: '# new instance edit' } })
    expect(getWorkspaceEditState().dirty).toBe(true)
  })

  it('discards a confirmed SPA exit draft when the same LabPage serves another Class', async () => {
    const api = {
      ...mockLabbitApi,
      getClass: async (id: string): Promise<ClassDetail> => ({
        id, name: 'Class ' + id, myRole: 'STUDENT',
        activeLabExecution: { id: 'execution-' + id, status: 'ACTIVE' },
        myLabInstance: { id: 'lab-' + id, userId: 'student-a', generation: 1, status: 'READY' },
      }),
      listWorkspaceFiles: async () => tree,
      readWorkspaceFile: async () => workspaceDocument,
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
      target: { value: '# discarded old class edit' },
    })
    expect(getWorkspaceEditState().dirty).toBe(true)
    const confirm = vi.spyOn(window, 'confirm').mockReturnValue(true)
    await act(async () => { await router.navigate('/classes/class-b/lab') })
    expect(await screen.findByRole('heading', { name: 'Class class-b' })).toBeInTheDocument()
    expect(confirm).toHaveBeenCalledTimes(1)
    expect(getWorkspaceEditState().dirty).toBe(false)
    expect(screen.queryByRole('button', { name: '이전 LabInstance 수정본 버리기' })).not.toBeInTheDocument()
    expect(document.body.textContent).not.toContain('# discarded old class edit')
  })

  it.each([
    [403, 'tree'], [403, 'editor'], [409, 'tree'], [409, 'editor'],
  ] as const)('retains workspace-wide %i across LabPage route remount until fresh %s GET', async (status, boundary) => {
    const entries = { path: '', items: ['A.md', 'B.md'].map((filePath) => ({
      path: filePath, name: filePath, kind: 'file' as const,
    })) }
    const cachedB = { file: { path: 'B.md', content: '# synthetic cached B' }, etag: '"B-old"' }
    const freshB = { file: { path: 'B.md', content: '# freshly authorized B' }, etag: '"B-new"' }
    let denied = false
    let delay = false
    let finishTree!: (value: typeof entries) => void
    let finishFile!: (value: typeof freshB) => void
    const api = {
      ...mockLabbitApi,
      getClass: async (): Promise<ClassDetail> => ({
        id: 'class-remount', name: 'Route remount boundary', myRole: 'STUDENT',
        activeLabExecution: { id: 'execution-remount', status: 'ACTIVE' },
        myLabInstance: { id: labId, userId: 'student-a', status: 'READY', generation: 1 },
      }),
      listWorkspaceFiles: async () => {
        if (delay) return new Promise<typeof entries>((resolve) => { finishTree = resolve })
        return entries
      },
      readWorkspaceFile: async (_id: string, filePath: string) => {
        if (delay) return new Promise<typeof freshB>((resolve) => { finishFile = resolve })
        if (denied && filePath === 'A.md') throw new HttpError(status, {
          type: 'about:blank', title: 'Workspace access changed', status,
          code: status === 409 ? 'workspace_target_changed' : 'forbidden', requestId: 'fixture-remount',
        })
        return filePath === 'B.md' ? cachedB : { file: { path: filePath, content: '# file A' }, etag: '"A-1"' }
      },
    }
    const client = new QueryClient({ defaultOptions: { queries: { retry: false, refetchOnWindowFocus: false } } })
    const router = createMemoryRouter([
      { path: '/classes/:classId/lab', element: <LabPage /> },
      { path: '/classes/:classId', element: <div>Remount class detail</div> },
    ], { initialEntries: ['/classes/class-remount/lab'] })
    render(<QueryClientProvider client={client}>
      <LabbitApiProvider api={api}><RouterProvider router={router} /></LabbitApiProvider>
    </QueryClientProvider>)
    try {
      fireEvent.click(await screen.findByRole('button', { name: 'B.md' }))
      expect(await screen.findByLabelText('파일 편집기')).toHaveValue(cachedB.file.content)
      fireEvent.click(screen.getByRole('button', { name: 'A.md' }))
      await waitFor(() => expect(screen.getByLabelText('파일 편집기')).toHaveValue('# file A'))
      denied = true
      await act(async () => { await client.invalidateQueries({ queryKey: labbitQueryKeys.workspaceFile(labId, 1, 'A.md') }) })
      await waitFor(() => expect(screen.queryByRole('button', { name: 'B.md' })).not.toBeInTheDocument())
      expect(screen.queryByLabelText('파일 편집기')).not.toBeInTheDocument()

      await act(async () => { await router.navigate('/classes/class-remount') })
      expect(await screen.findByText('Remount class detail')).toBeInTheDocument()
      delay = true
      await act(async () => { await router.navigate('/classes/class-remount/lab') })
      await waitFor(() => expect(finishTree).toBeDefined())
      if (boundary === 'tree') {
        expect(screen.queryByRole('button', { name: 'B.md' })).not.toBeInTheDocument()
      }
      // Authorizing the root directory must not authorize the cached B bytes.
      await act(async () => { finishTree(entries) })
      fireEvent.click(await screen.findByRole('button', { name: 'B.md' }))
      await waitFor(() => expect(finishFile).toBeDefined())
      expect(client.getQueryData(labbitQueryKeys.workspaceFile(labId, 1, 'B.md'))).toEqual(cachedB)
      expect(screen.queryByLabelText('파일 편집기')).not.toBeInTheDocument()
      await act(async () => { finishFile(freshB) })
      expect(await screen.findByLabelText('파일 편집기')).toHaveValue(freshB.file.content)
    } finally {
      cleanup()
      client.clear()
    }
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
