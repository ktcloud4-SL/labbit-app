import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { fireEvent, render, screen, waitFor } from '@testing-library/react'
import { createMemoryRouter, RouterProvider } from 'react-router-dom'
import type { ReactNode } from 'react'
import { describe, expect, it, vi } from 'vitest'

import type { LabSpec, LabSpecWrite } from '../shared/api/contracts'
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
  detailElement: ReactNode = <div>LabSpec 저장 완료</div>,
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
      { path: '/lab-specs/:labSpecId', element: detailElement },
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
  it('Mapping 후보가 없는 경우 저장 요청하지 않는다', async () => {
    const createLabSpec = vi.fn(async () => {
      throw new Error('Mapping이 없어 저장하면 안 됨')
    })
    mountLabSpecFlow(
      { listLabSpecMappingOptions: async () => ({ items: [] }), createLabSpec },
      '/lab-specs/new',
    )
    expect(await screen.findByText(/현재 Organization에 활성 Image\/Flavor Mapping 후보가 없습니다/)).toBeInTheDocument()
    expect(screen.getByRole('combobox', { name: 'Image 참조' })).toBeDisabled()
    expect(screen.getByRole('combobox', { name: 'Size 참조' })).toBeDisabled()
    fireEvent.change(screen.getByRole('textbox', { name: '이름' }), {
      target: { value: 'Unmapped Lab' },
    })
    fireEvent.click(screen.getByRole('button', { name: '실습 정의 저장' }))
    expect(await screen.findByRole('alert')).toHaveTextContent('각 VM의 Role, Image 참조, Size 참조와 Count')
    expect(createLabSpec).not.toHaveBeenCalled()
  })

  it('VM Role이 중복되면 유효한 Mapping이어도 저장하지 않는다', async () => {
    const createLabSpec = vi.fn(async () => {
      throw new Error('중복 Role은 저장하면 안 됨')
    })
    mountLabSpecFlow({ createLabSpec }, '/lab-specs/new')

    await screen.findByRole('heading', { level: 1, name: '새 실습 정의' })
    const candidate = mockLabSpecMappingOptions.items[0]
    fireEvent.change(screen.getByRole('textbox', { name: '이름' }), {
      target: { value: 'Duplicated Roles' },
    })
    fireEvent.change(screen.getByRole('combobox', { name: 'Provider 연결' }), {
      target: { value: candidate.providerConnectionId },
    })
    fireEvent.click(screen.getByRole('button', { name: 'VM 역할 추가' }))
    for (const roleInput of screen.getAllByRole('textbox', { name: 'Role' })) {
      fireEvent.change(roleInput, { target: { value: 'control' } })
    }
    for (const imageSelect of screen.getAllByRole('combobox', { name: 'Image 참조' })) {
      fireEvent.change(imageSelect, { target: { value: candidate.images[0].id } })
    }
    for (const sizeSelect of screen.getAllByRole('combobox', { name: 'Size 참조' })) {
      fireEvent.change(sizeSelect, { target: { value: candidate.flavors[0].id } })
    }
    fireEvent.change(screen.getByRole('combobox', { name: 'Workspace VM' }), {
      target: { value: 'control:0' },
    })
    fireEvent.click(screen.getByRole('button', { name: '실습 정의 저장' }))
    expect(await screen.findByRole('alert')).toHaveTextContent('VM Role은 LabSpec 안에서 중복될 수 없습니다.')
    expect(createLabSpec).not.toHaveBeenCalled()
  })

  it('수정 충돌 412에서는 기존 ETag로 덮어쓰지 않고 최신 LabSpec을 다시 불러온다', async () => {
    const candidate = mockLabSpecMappingOptions.items[0]
    const original: LabSpec = {
      id: 'lab-spec-existing',
      ownerUserId: mockMe.id,
      name: 'Original Spec',
      vms: [{
        role: 'control',
        imageRef: candidate.images[0].id,
        sizeRef: candidate.flavors[0].id,
        count: 1,
      }],
      workspaceVm: { role: 'control', instanceIndex: 0 },
      internetOutbound: true,
    }
    let revision = 1
    const getLabSpec = vi.fn(async () => ({
      labSpec: { ...original, name: revision === 1 ? 'Original Spec' : 'Changed elsewhere' },
      etag: revision === 1 ? '"revision-1"' : '"revision-2"',
    }))
    const updateLabSpec = vi.fn(async () => {
      revision = 2
      throw new HttpError(412)
    })
    mountLabSpecFlow(
      { getLabSpec, updateLabSpec },
      '/lab-specs/lab-spec-existing',
      <LabSpecEditorPage />,
    )
    expect(await screen.findByRole('heading', { level: 1, name: 'Original Spec' })).toBeInTheDocument()
    fireEvent.change(screen.getByRole('textbox', { name: '이름' }), {
      target: { value: 'My concurrent edit' },
    })
    fireEvent.click(screen.getByRole('button', { name: '실습 정의 저장' }))
    expect(await screen.findByRole('alert')).toHaveTextContent('다른 곳에서 LabSpec이 수정되었습니다')
    expect(updateLabSpec).toHaveBeenCalledWith(
      'lab-spec-existing',
      expect.objectContaining({ name: 'My concurrent edit' }),
      '"revision-1"',
    )
    fireEvent.click(screen.getByRole('button', { name: '최신 내용 다시 불러오기' }))
    expect(await screen.findByRole('heading', { level: 1, name: 'Changed elsewhere' })).toBeInTheDocument()
    expect(screen.getByRole('textbox', { name: '이름' })).toHaveValue('Changed elsewhere')
    expect(getLabSpec).toHaveBeenCalledTimes(2)
    expect(updateLabSpec).toHaveBeenCalledTimes(1)
  })

  it('LabSpec 조회 403이면 상세 내용을 렌더하지 않고 목록 복귀를 안내한다', async () => {
    const updateLabSpec = vi.fn(async () => {
      throw new Error('접근 거부 상태에서 쓰기를 호출하면 안 됨')
    })
    mountLabSpecFlow(
      {
        getLabSpec: async () => { throw new HttpError(403) },
        updateLabSpec,
      },
      '/lab-specs/private',
      <LabSpecEditorPage />,
    )

    expect(await screen.findByText('이 LabSpec을 볼 권한이 없습니다.')).toBeInTheDocument()
    expect(screen.getByRole('link', { name: '실습 정의 목록으로 돌아가기' })).toHaveAttribute('href', '/lab-specs')
    expect(screen.queryByRole('button', { name: '실습 정의 저장' })).not.toBeInTheDocument()
    expect(updateLabSpec).not.toHaveBeenCalled()
  })

  it('API가 타인 소유 LabSpec을 반환하더라도 내용을 화면에 노출하지 않는다', async () => {
    const getLabSpec = vi.fn(async () => ({
      labSpec: {
        id: 'private-spec',
        name: 'Do not show private data',
        ownerUserId: 'other-instructor',
        vms: [],
        workspaceVm: { role: '', instanceIndex: 0 },
        internetOutbound: true,
      },
      etag: '"revision-1"',
    }))
    mountLabSpecFlow(
      { getLabSpec },
      '/lab-specs/private-spec',
      <LabSpecEditorPage />,
    )

    expect(await screen.findByText('이 LabSpec을 볼 권한이 없습니다.')).toBeInTheDocument()
    expect(screen.queryByText('Do not show private data')).not.toBeInTheDocument()
    expect(screen.queryByRole('button', { name: '실습 정의 저장' })).not.toBeInTheDocument()
  })

  it('수정 대상 LabSpec의 ETag 누락 시 PUT을 차단하고 재조회 안내를 표시한다', async () => {
    const candidate = mockLabSpecMappingOptions.items[0]
    const updateLabSpec = vi.fn(async () => {
      throw new Error('ETag 없이 PUT하면 안 됨')
    })
    mountLabSpecFlow(
      {
        getLabSpec: async () => ({
          labSpec: {
            id: 'no-etag',
            name: 'Existing Spec',
            ownerUserId: mockMe.id,
            vms: [{
              role: 'control',
              imageRef: candidate.images[0].id,
              sizeRef: candidate.flavors[0].id,
              count: 1,
            }],
            workspaceVm: { role: 'control', instanceIndex: 0 },
            internetOutbound: true,
          },
        }),
        updateLabSpec,
      },
      '/lab-specs/no-etag',
      <LabSpecEditorPage />,
    )

    expect(await screen.findByRole('heading', { level: 1, name: 'Existing Spec' })).toBeInTheDocument()
    expect(screen.getByText(/최신 ETag를 확인하지 못해 안전한 수정 요청을 보낼 수 없습니다/)).toBeInTheDocument()
    expect(screen.getByRole('button', { name: '실습 정의 저장' })).toBeDisabled()
    expect(updateLabSpec).not.toHaveBeenCalled()
  })

})
