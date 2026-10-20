import { act, fireEvent, render, screen, waitFor, cleanup } from '@testing-library/react'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { createMemoryRouter, RouterProvider } from 'react-router-dom'
import { afterEach, describe, expect, it } from 'vitest'

import { HttpError } from '../api/httpClient'
import { LabbitApiProvider } from '../api/LabbitApiProvider'
import { labbitQueryKeys } from '../api/labbitApi'
import { mockLabbitApi } from '../api/mockLabbitApi'
import { WorkspaceFilePanels } from './WorkspaceFilePanels'

const labId = 'lab-instance-file-boundary'
const path = 'README.md'
const tree = { path: '', items: [{ name: path, path, kind: 'file' as const }] }
const document = { file: { path, content: '# authorized content\n' }, etag: '"revision-1"' }

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

afterEach(() => cleanup())

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
        return document
      },
    })
    fireEvent.click(await screen.findByRole('button', { name: /README.md/ }))
    expect(await screen.findByLabelText('파일 편집기')).toHaveValue(document.file.content)
    denied = true
    await act(async () => {
      await client.invalidateQueries({ queryKey: labbitQueryKeys.workspaceFile(labId, 1, path) })
    })
    await waitFor(() => expect(screen.getByRole('alert')).toBeInTheDocument())
    expect(screen.queryByLabelText('파일 편집기')).not.toBeInTheDocument()
    expect(screen.getByRole('button', { name: '파일 다시 읽기' })).toBeInTheDocument()
    expect(client.getQueryData(labbitQueryKeys.workspaceFile(labId, 1, path))).toEqual(document)
  })

  it('keeps an unsaved editor draft during a transient 503 refetch failure', async () => {
    let unavailable = false
    const { client } = mount({
      listWorkspaceFiles: async () => tree,
      readWorkspaceFile: async () => {
        if (unavailable) throw new HttpError(503)
        return document
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
