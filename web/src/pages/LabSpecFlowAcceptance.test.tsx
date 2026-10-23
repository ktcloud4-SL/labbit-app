import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { act, fireEvent, render, screen, waitFor } from '@testing-library/react'
import { createMemoryRouter, RouterProvider } from 'react-router-dom'
import type { ReactNode } from 'react'
import { describe, expect, it, vi } from 'vitest'

import type { LabSpec, LabSpecWrite } from '../shared/api/contracts'
import { LabbitApiProvider } from '../shared/api/LabbitApiProvider'
import { labbitQueryKeys } from '../shared/api/labbitApi'
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
  return { router, client }
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

  it('생성 성공 후 동일 Editor의 상세 수정은 목록 이탈 경고를 유지하고 취소 시 편집을 보존한다', async () => {
    const candidate = mockLabSpecMappingOptions.items[0]
    let savedLabSpec: LabSpec | null = null
    const createLabSpec = vi.fn(async (input: LabSpecWrite) => {
      savedLabSpec = { ...input, id: 'newly-created-spec', ownerUserId: mockMe.id }
      return savedLabSpec
    })
    const getLabSpec = vi.fn(async () => {
      if (!savedLabSpec) throw new Error('Read requested before create')
      return { labSpec: savedLabSpec, etag: '"created-v1"' }
    })
    const confirmation = vi.spyOn(window, 'confirm').mockReturnValue(false)
    const { router } = mountLabSpecFlow(
      { createLabSpec, getLabSpec },
      '/lab-specs/new',
      <LabSpecEditorPage />,
    )

    await screen.findByRole('heading', { level: 1, name: '새 실습 정의' })
    fireEvent.change(screen.getByRole('textbox', { name: '이름' }), {
      target: { value: 'New LabSpec' },
    })
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

    await waitFor(() =>
      expect(router.state.location.pathname).toBe('/lab-specs/newly-created-spec'),
    )
    expect(createLabSpec).toHaveBeenCalledTimes(1)
    expect(await screen.findByRole('textbox', { name: '이름' })).toHaveValue('New LabSpec')
    expect(await screen.findByRole('status')).toHaveTextContent('새 실습 정의가 저장되었습니다.')
    expect(confirmation).not.toHaveBeenCalled()

    fireEvent.change(screen.getByRole('textbox', { name: '이름' }), {
      target: { value: 'Unsaved detail changes' },
    })
    await waitFor(() => expect(screen.queryByRole('status')).not.toBeInTheDocument())
    fireEvent.click(screen.getByRole('link', { name: /실습 정의 목록/ }))
    await waitFor(() => expect(confirmation).toHaveBeenCalledTimes(1))
    expect(router.state.location.pathname).toBe('/lab-specs/newly-created-spec')
    expect(screen.getByRole('textbox', { name: '이름' })).toHaveValue('Unsaved detail changes')
    confirmation.mockRestore()
  })

  it('생성 성공 안내는 최초 상세에서만 보이고 목록→Back에서 재표시되지 않는다', async () => {
    const candidate = mockLabSpecMappingOptions.items[0]
    let savedLabSpec: LabSpec | null = null
    const createLabSpec = vi.fn(async (input: LabSpecWrite) => {
      savedLabSpec = { ...input, id: 'created-flash-spec', ownerUserId: mockMe.id }
      return savedLabSpec
    })
    const getLabSpec = vi.fn(async () => {
      if (!savedLabSpec) throw new Error('Read requested before create')
      return { labSpec: savedLabSpec, etag: '"created-flash-v1"' }
    })
    const { router } = mountLabSpecFlow(
      { createLabSpec, getLabSpec },
      '/lab-specs/new',
      <LabSpecEditorPage />,
    )

    await screen.findByRole('heading', { level: 1, name: '새 실습 정의' })
    fireEvent.change(screen.getByRole('textbox', { name: '이름' }), {
      target: { value: 'One-time notice' },
    })
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

    expect(await screen.findByRole('status')).toHaveTextContent('새 실습 정의가 저장되었습니다.')
    await waitFor(() => {
      expect(router.state.location.pathname).toBe('/lab-specs/created-flash-spec')
      expect(router.state.location.state).toBeNull()
    })

    fireEvent.click(screen.getByRole('link', { name: /실습 정의 목록/ }))
    await waitFor(() => expect(router.state.location.pathname).toBe('/lab-specs'))

    await act(async () => { await router.navigate(-1) })
    expect(await screen.findByRole('textbox', { name: '이름' })).toHaveValue('One-time notice')
    expect(screen.queryByRole('status')).not.toBeInTheDocument()
    expect(createLabSpec).toHaveBeenCalledTimes(1)
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
    expect(screen.queryByText('실습 정의 변경사항이 저장되었습니다.')).not.toBeInTheDocument()
    expect(updateLabSpec).toHaveBeenCalledWith(
      'lab-spec-existing',
      expect.objectContaining({ name: 'My concurrent edit' }),
      '"revision-1"',
    )
    fireEvent.click(screen.getByRole('button', { name: '최신 내용 다시 불러오기' }))
    expect(await screen.findByRole('heading', { level: 1, name: 'Changed elsewhere' })).toBeInTheDocument()
    expect(screen.getByRole('textbox', { name: '이름' })).toHaveValue('Changed elsewhere')
    expect(screen.getByRole('status')).toHaveTextContent('이전 미저장 수정본은 메모리에 보관했습니다')
    expect(screen.getByRole('button', { name: '실습 정의 저장' })).toBeDisabled()
    expect(getLabSpec).toHaveBeenCalledTimes(2)
    expect(updateLabSpec).toHaveBeenCalledTimes(1)
    // Do not silently re-submit the old draft. Reinstating it must be explicit.
    fireEvent.click(screen.getByRole('button', { name: '이전 수정본 복원' }))
    expect(screen.getByRole('textbox', { name: '이름' })).toHaveValue('My concurrent edit')
    expect(screen.getByRole('button', { name: '실습 정의 저장' })).toBeEnabled()
    expect(screen.queryByText(/이전 미저장 수정본은 메모리에 보관했습니다/)).not.toBeInTheDocument()
    expect(updateLabSpec).toHaveBeenCalledTimes(1)
  })


  it('백그라운드 ETag 변경 시에도 편집본을 유지하고 서버 최신본과 분리해 선택적으로 복원한다', async () => {
    const candidate = mockLabSpecMappingOptions.items[0]
    const original: LabSpec = {
      id: 'lab-spec-refresh',
      ownerUserId: mockMe.id,
      name: 'Initial remote',
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
    const updateLabSpec = vi.fn(async () => { throw new Error('Should not auto-save') })
    const { client } = mountLabSpecFlow(
      {
        getLabSpec: async () => ({
          labSpec: { ...original, name: revision === 1 ? 'Initial remote' : 'Updated remotely' },
          etag: revision === 1 ? '"etag-1"' : '"etag-2"',
        }),
        updateLabSpec,
      },
      '/lab-specs/lab-spec-refresh',
      <LabSpecEditorPage />,
    )
    expect(await screen.findByRole('textbox', { name: '이름' })).toHaveValue('Initial remote')
    fireEvent.change(screen.getByRole('textbox', { name: '이름' }), {
      target: { value: 'My unsaved draft' },
    })
    expect(screen.getByRole('textbox', { name: '이름' })).toHaveValue('My unsaved draft')

    revision = 2
    await act(async () => {
      await client.invalidateQueries({ queryKey: labbitQueryKeys.labSpec('lab-spec-refresh') })
    })
    await waitFor(() => expect(screen.getByRole('textbox', { name: '이름' })).toHaveValue('Updated remotely'))
    expect(screen.getByRole('status')).toHaveTextContent('이전 미저장 수정본은 메모리에 보관했습니다')
    expect(updateLabSpec).not.toHaveBeenCalled()

    fireEvent.click(screen.getByRole('button', { name: '이전 수정본 복원' }))
    expect(screen.getByRole('textbox', { name: '이름' })).toHaveValue('My unsaved draft')
    expect(updateLabSpec).not.toHaveBeenCalled()
  })

  it('ETag 변경 시 복원 선택 전 새 서버 폼 편집을 잠가 오래된 미저장 초안을 보호한다', async () => {
    const candidate = mockLabSpecMappingOptions.items[0]
    let revision = 1
    const { client } = mountLabSpecFlow(
      {
        getLabSpec: async () => ({
          labSpec: {
            id: 'spec-lock',
            ownerUserId: mockMe.id,
            name: revision === 1 ? 'Old original' : 'New remote version',
            vms: [{
              role: 'control', imageRef: candidate.images[0].id,
              sizeRef: candidate.flavors[0].id, count: 1,
            }],
            workspaceVm: { role: 'control', instanceIndex: 0 },
            internetOutbound: true,
          },
          etag: revision === 1 ? '"v1"' : '"v2"',
        }),
      },
      '/lab-specs/spec-lock',
      <LabSpecEditorPage />,
    )
    expect(await screen.findByRole('textbox', { name: '이름' })).toHaveValue('Old original')
    fireEvent.change(screen.getByRole('textbox', { name: '이름' }), {
      target: { value: 'My old unsaved draft' },
    })

    revision = 2
    await act(async () => {
      await client.invalidateQueries({ queryKey: labbitQueryKeys.labSpec('spec-lock') })
    })
    await waitFor(() =>
      expect(screen.getByRole('textbox', { name: '이름' })).toHaveValue('New remote version'),
    )
    expect(screen.getByRole('status')).toHaveTextContent('이전 미저장 수정본은 메모리에 보관했습니다')
    expect(screen.getByRole('textbox', { name: '이름' })).toBeDisabled()
    expect(screen.getByRole('textbox', { name: 'Role' })).toBeDisabled()
    expect(screen.getByRole('combobox', { name: 'Provider 연결' })).toBeDisabled()
    expect(screen.getByRole('button', { name: 'VM 역할 추가' })).toBeDisabled()
    expect(screen.getByRole('button', { name: '실습 정의 저장' })).toBeDisabled()
    // fireEvent.change can dispatch synthetic events even on a disabled input;
    // the disabled attribute is the browser-user interaction boundary.
    expect(screen.getByRole('status')).toHaveTextContent('이전 미저장 수정본은 메모리에 보관했습니다')

    fireEvent.click(screen.getByRole('button', { name: '이전 수정본 복원' }))
    expect(screen.getByRole('textbox', { name: '이름' })).toHaveValue('My old unsaved draft')
    expect(screen.getByRole('textbox', { name: '이름' })).toBeEnabled()
  })

  it('ETag 변경 후 다른 Provider의 이전 수정본을 복원하면 Provider와 VM 참조를 함께 복원한다', async () => {
    const [providerA, providerB] = mockLabSpecMappingOptions.items
    const original: LabSpec = {
      id: 'spec-cross-provider',
      ownerUserId: mockMe.id,
      name: 'Provider B original',
      vms: [{
        role: 'control', imageRef: providerB.images[0].id,
        sizeRef: providerB.flavors[0].id, count: 1,
      }],
      workspaceVm: { role: 'control', instanceIndex: 0 },
      internetOutbound: true,
    }
    let revision = 1
    const updateLabSpec = vi.fn(async (id: string, input: LabSpecWrite, etag: string) => {
      if (id !== 'spec-cross-provider' || etag !== '"v2"') {
        throw new Error('Expected update against latest LabSpec ETag')
      }
      return { labSpec: { ...original, ...input }, etag: '"v3"' }
    })
    const { client } = mountLabSpecFlow(
      {
        getLabSpec: async () => ({
          labSpec: revision === 1 ? original : {
            ...original,
            name: 'Provider A remote',
            vms: [{
              role: 'control', imageRef: providerA.images[0].id,
              sizeRef: providerA.flavors[0].id, count: 1,
            }],
          },
          etag: revision === 1 ? '"v1"' : '"v2"',
        }),
        updateLabSpec,
      },
      '/lab-specs/spec-cross-provider',
      <LabSpecEditorPage />,
    )
    expect(await screen.findByRole('textbox', { name: '이름' })).toHaveValue('Provider B original')
    expect(screen.getByRole('combobox', { name: 'Provider 연결' })).toHaveValue(providerB.providerConnectionId)
    fireEvent.change(screen.getByRole('textbox', { name: '이름' }), {
      target: { value: 'My Provider B draft' },
    })

    revision = 2
    await act(async () => {
      await client.invalidateQueries({ queryKey: labbitQueryKeys.labSpec('spec-cross-provider') })
    })
    await waitFor(() =>
      expect(screen.getByRole('textbox', { name: '이름' })).toHaveValue('Provider A remote'),
    )
    expect(screen.getByRole('combobox', { name: 'Provider 연결' })).toHaveValue(providerA.providerConnectionId)

    fireEvent.click(screen.getByRole('button', { name: '이전 수정본 복원' }))
    expect(screen.getByRole('textbox', { name: '이름' })).toHaveValue('My Provider B draft')
    expect(screen.getByRole('combobox', { name: 'Provider 연결' })).toHaveValue(providerB.providerConnectionId)
    expect(screen.getByRole('combobox', { name: 'Image 참조' })).toHaveValue(providerB.images[0].id)
    expect(screen.getByRole('combobox', { name: 'Size 참조' })).toHaveValue(providerB.flavors[0].id)

    fireEvent.click(screen.getByRole('button', { name: '실습 정의 저장' }))
    await waitFor(() => expect(updateLabSpec).toHaveBeenCalledTimes(1))
    expect(updateLabSpec).toHaveBeenCalledWith(
      'spec-cross-provider',
      expect.objectContaining({
        name: 'My Provider B draft',
        vms: [expect.objectContaining({
          imageRef: providerB.images[0].id, sizeRef: providerB.flavors[0].id,
        })],
      }),
      '"v2"',
    )
  })

  it('ETag 변경 후 이전 수정본 버리기를 선택하면 최신 서버 입력을 유지한다', async () => {
    const candidate = mockLabSpecMappingOptions.items[0]
    let revision = 1
    const { client } = mountLabSpecFlow(
      {
        getLabSpec: async () => ({
          labSpec: {
            id: 'spec-discard',
            ownerUserId: mockMe.id,
            name: revision === 1 ? 'Old server' : 'New server',
            vms: [{ role: 'control', imageRef: candidate.images[0].id, sizeRef: candidate.flavors[0].id, count: 1 }],
            workspaceVm: { role: 'control', instanceIndex: 0 },
            internetOutbound: true,
          },
          etag: revision === 1 ? '"v1"' : '"v2"',
        }),
      },
      '/lab-specs/spec-discard',
      <LabSpecEditorPage />,
    )
    expect(await screen.findByRole('textbox', { name: '이름' })).toHaveValue('Old server')
    fireEvent.change(screen.getByRole('textbox', { name: '이름' }), {
      target: { value: 'Draft to discard' },
    })
    revision = 2
    await act(async () => {
      await client.invalidateQueries({ queryKey: labbitQueryKeys.labSpec('spec-discard') })
    })
    await waitFor(() => expect(screen.getByRole('textbox', { name: '이름' })).toHaveValue('New server'))
    expect(screen.getByRole('button', { name: '실습 정의 저장' })).toBeDisabled()
    fireEvent.click(screen.getByRole('button', { name: '이전 수정본 버리기' }))
    expect(screen.getByRole('textbox', { name: '이름' })).toHaveValue('New server')
    expect(screen.getByRole('button', { name: '실습 정의 저장' })).toBeEnabled()
    expect(screen.queryByText(/이전 미저장 수정본은 메모리에 보관했습니다/)).not.toBeInTheDocument()
  })


  it('미저장 LabSpec을 가지고 뒤로 이동할 때 취소하면 편집 내용을 보존하고 확인해야 이동한다', async () => {
    const confirmation = vi.spyOn(window, 'confirm').mockReturnValue(false)
    const { router } = mountLabSpecFlow({}, '/lab-specs/new')
    await screen.findByRole('heading', { level: 1, name: '새 실습 정의' })

    fireEvent.change(screen.getByRole('textbox', { name: '이름' }), {
      target: { value: 'Draft that must not disappear' },
    })
    fireEvent.click(screen.getByRole('link', { name: /실습 정의 목록/ }))
    await waitFor(() => expect(confirmation).toHaveBeenCalledTimes(1))
    expect(router.state.location.pathname).toBe('/lab-specs/new')
    expect(screen.getByRole('textbox', { name: '이름' })).toHaveValue('Draft that must not disappear')

    confirmation.mockReturnValue(true)
    fireEvent.click(screen.getByRole('link', { name: /실습 정의 목록/ }))
    await waitFor(() => expect(router.state.location.pathname).toBe('/lab-specs'))
    confirmation.mockRestore()
  })

  it('LabSpec 저장 전 브라우저 페이지 종료 시 변경 내용 경고가 활성화된다', async () => {
    mountLabSpecFlow({}, '/lab-specs/new')
    await screen.findByRole('heading', { level: 1, name: '새 실습 정의' })
    fireEvent.change(screen.getByRole('textbox', { name: '이름' }), {
      target: { value: 'Not yet saved' },
    })
    const unload = new Event('beforeunload', { cancelable: true })
    await act(async () => { window.dispatchEvent(unload) })
    expect(unload.defaultPrevented).toBe(true)
  })


  it('LabSpec PUT 대기 중에는 추가 편집을 막아 뒤늦은 저장 완료로 입력이 유실되지 않게 한다', async () => {
    const candidate = mockLabSpecMappingOptions.items[0]
    const original: LabSpec = {
      id: 'lab-spec-saving',
      ownerUserId: mockMe.id,
      name: 'Before save',
      vms: [{
        role: 'control', imageRef: candidate.images[0].id,
        sizeRef: candidate.flavors[0].id, count: 1,
      }],
      workspaceVm: { role: 'control', instanceIndex: 0 },
      internetOutbound: true,
    }
    let finishSave!: (value: { labSpec: LabSpec; etag: string }) => void
    const pendingSave = new Promise<{ labSpec: LabSpec; etag: string }>((resolve) => {
      finishSave = resolve
    })
    const updateLabSpec = vi.fn(async () => pendingSave)
    mountLabSpecFlow(
      {
        getLabSpec: async () => ({ labSpec: original, etag: '"v1"' }),
        updateLabSpec,
      },
      '/lab-specs/lab-spec-saving',
      <LabSpecEditorPage />,
    )
    expect(await screen.findByRole('textbox', { name: '이름' })).toHaveValue('Before save')
    fireEvent.change(screen.getByRole('textbox', { name: '이름' }), {
      target: { value: 'Submitted edit' },
    })
    fireEvent.click(screen.getByRole('button', { name: '실습 정의 저장' }))
    await waitFor(() => expect(updateLabSpec).toHaveBeenCalledTimes(1))
    expect(screen.getByRole('textbox', { name: '이름' })).toBeDisabled()
    expect(screen.getByRole('textbox', { name: 'Role' })).toBeDisabled()
    expect(screen.getByRole('button', { name: 'VM 역할 추가' })).toBeDisabled()
    expect(screen.getByRole('button', { name: '저장 중...' })).toBeDisabled()
    await act(async () => {
      finishSave({ labSpec: { ...original, name: 'Submitted edit' }, etag: '"v2"' })
      await pendingSave
    })
    await waitFor(() => expect(screen.getByRole('textbox', { name: '이름' })).toHaveValue('Submitted edit'))
    expect(screen.getByRole('textbox', { name: '이름' })).toBeEnabled()
    expect(await screen.findByRole('status')).toHaveTextContent('실습 정의 변경사항이 저장되었습니다.')
    fireEvent.change(screen.getByRole('textbox', { name: '이름' }), {
      target: { value: 'Next unsaved edit' },
    })
    await waitFor(() => expect(screen.queryByRole('status')).not.toBeInTheDocument())
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
