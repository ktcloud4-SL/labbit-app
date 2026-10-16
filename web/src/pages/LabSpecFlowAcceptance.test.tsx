import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { fireEvent, render, screen, waitFor } from '@testing-library/react'
import { createMemoryRouter, RouterProvider } from 'react-router-dom'
import { describe, expect, it, vi } from 'vitest'

import type { LabSpecWrite } from '../shared/api/contracts'
import { LabbitApiProvider } from '../shared/api/LabbitApiProvider'
import { HttpError } from '../shared/api/httpClient'
import {
  mockLabbitApi,
  mockLabSpecMappingOptions,
  mockMe,
} from '../shared/api/mockLabbitApi'
import { LabSpecEditorPage } from './LabSpecEditorPage'
import { LabSpecListPage } from './LabSpecListPage'

function mountLabSpecFlow(
  overrides: Partial<typeof mockLabbitApi> = {},
  initialPath = '/lab-specs',
) {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  })
  const api = {
    ...mockLabbitApi,
    getMe: async () => mockMe,
    listLabSpecs: async () => ({ items: [] }),
    listLabSpecMappingOptions: async () => mockLabSpecMappingOptions,
    ...overrides,
  }
  const router = createMemoryRouter(
    [
      { path: '/lab-specs', element: <LabSpecListPage /> },
      { path: '/lab-specs/new', element: <LabSpecEditorPage /> },
      { path: '/lab-specs/:labSpecId', element: <div>LabSpec 저장 완료</div> },
    ],
    { initialEntries: [initialPath] },
  )
  render(
    <QueryClientProvider client={client}>
      <LabbitApiProvider api={api}>
        <RouterProvider router={router} />
      </LabbitApiProvider>
    </QueryClientProvider>,
  )
  return { router }
}

describe('FE-05 LabSpec navigation and validation (mock-only)', () => {
  it('목록의 새 실습 정의 링크로 이동하고 빈 이름은 저장 요청 전에 차단한다', async () => {
    const createLabSpec = vi.fn(async () => {
      throw new Error('빈 입력에서는 호출하지 않아야 함')
    })
    const { router } = mountLabSpecFlow({ createLabSpec })

    const addLink = await screen.findByRole('link', { name: '새 실습 정의' })
    expect(addLink).toHaveAttribute('href', '/lab-specs/new')
    fireEvent.click(addLink)

    expect(await screen.findByRole('heading', { level: 1, name: '새 실습 정의' })).toBeInTheDocument()
    expect(router.state.location.pathname).toBe('/lab-specs/new')
    fireEvent.click(screen.getByRole('button', { name: '실습 정의 저장' }))

    expect(await screen.findByRole('alert')).toHaveTextContent('LabSpec 이름을 입력해 주세요.')
    expect(createLabSpec).not.toHaveBeenCalled()
  })

  it('VM 필수 정보 누락 시 저장을 막고 정상 입력은 create API로 전달한다', async () => {
    const createLabSpec = vi.fn(async (input: LabSpecWrite) => ({
      ...input,
      id: 'labspec-created',
      ownerUserId: mockMe.id,
    }))
    const { router } = mountLabSpecFlow({ createLabSpec }, '/lab-specs/new')

    await screen.findByRole('heading', { level: 1, name: '새 실습 정의' })
    fireEvent.change(screen.getByRole('textbox', { name: '이름' }), {
      target: { value: '  Network Lab  ' },
    })
    fireEvent.click(screen.getByRole('button', { name: '실습 정의 저장' }))

    expect(await screen.findByRole('alert')).toHaveTextContent('각 VM의 Role, Image 참조, Size 참조와 Count')
    expect(createLabSpec).not.toHaveBeenCalled()

    const candidate = mockLabSpecMappingOptions.items[0]
    fireEvent.change(screen.getByRole('combobox', { name: 'Provider 연결' }), {
      target: { value: candidate.providerConnectionId },
    })
    fireEvent.change(screen.getByRole('textbox', { name: 'Role' }), {
      target: { value: 'control' },
    })
    fireEvent.change(screen.getByRole('combobox', { name: 'Image 참조' }), {
      target: { value: candidate.images[0].id },
    })
    fireEvent.change(screen.getByRole('combobox', { name: 'Size 참조' }), {
      target: { value: candidate.flavors[0].id },
    })
    fireEvent.change(screen.getByRole('combobox', { name: 'Workspace VM' }), {
      target: { value: 'control:0' },
    })

    fireEvent.click(screen.getByRole('button', { name: '실습 정의 저장' }))
    await waitFor(() => expect(createLabSpec).toHaveBeenCalledTimes(1))
    expect(createLabSpec).toHaveBeenCalledWith(
      expect.objectContaining({
        name: 'Network Lab',
        vms: [expect.objectContaining({
          role: 'control',
          imageRef: candidate.images[0].id,
          sizeRef: candidate.flavors[0].id,
        })],
        workspaceVm: { role: 'control', instanceIndex: 0 },
      }),
    )
    expect(await screen.findByText('LabSpec 저장 완료')).toBeInTheDocument()
    expect(router.state.location.pathname).toBe('/lab-specs/labspec-created')
  })

  it('Mapping 권한 403이면 편집기를 차단하고 저장을 호출하지 않는다', async () => {
    const createLabSpec = vi.fn(async () => {
      throw new Error('403에서는 호출하지 않아야 함')
    })
    mountLabSpecFlow(
      {
        listLabSpecMappingOptions: async () => { throw new HttpError(403) },
        createLabSpec,
      },
      '/lab-specs/new',
    )

    expect(
      await screen.findByText('Mapping 후보를 조회할 강사 권한이 없습니다.'),
    ).toBeInTheDocument()
    expect(screen.queryByRole('button', { name: '실습 정의 저장' })).not.toBeInTheDocument()
    expect(createLabSpec).not.toHaveBeenCalled()
  })
})
