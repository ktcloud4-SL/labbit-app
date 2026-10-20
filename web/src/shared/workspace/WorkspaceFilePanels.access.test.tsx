import { act, fireEvent, render, screen, waitFor, cleanup } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { createMemoryRouter, RouterProvider } from 'react-router-dom'
import { afterEach, describe, expect, it, vi } from 'vitest'

import { HttpError } from '../api/httpClient'
import { LabbitApiProvider } from '../api/LabbitApiProvider'
import { labbitQueryKeys } from '../api/labbitApi'
import { mockLabbitApi } from '../api/mockLabbitApi'
import { WorkspaceFilePanels } from './WorkspaceFilePanels'
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
    expect(screen.getByLabelText('파일 편집기')).toHaveValue('# authorized content')

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
    expect(screen.getByLabelText('파일 편집기')).toHaveValue('# authorized content')
    vi.spyOn(window, 'confirm').mockReturnValue(true)
    fireEvent.click(screen.getByRole('button', { name: '수정본 버리기' }))
    expect(screen.queryByRole('button', { name: '미저장 수정본 복원' })).not.toBeInTheDocument()
    expect(getWorkspaceEditState().dirty).toBe(false)
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
