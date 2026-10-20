import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { act, fireEvent, render, screen, waitFor } from '@testing-library/react'
import { createMemoryRouter, RouterProvider } from 'react-router-dom'
import { describe, expect, it, vi } from 'vitest'

import type { ClassDetail, OperationAccepted } from '../shared/api/contracts'
import { LabbitApiProvider } from '../shared/api/LabbitApiProvider'
import { mockLabbitApi, mockMe } from '../shared/api/mockLabbitApi'
import { ProvisionPage } from './ProvisionPage'

function mount(createImpl = mockLabbitApi.createLabExecution) {
  const createLabExecution = vi.fn(createImpl)
  const api = {
    ...mockLabbitApi,
    getMe: async () => mockMe,
    getClass: async (id: string): Promise<ClassDetail> => ({
      id,
      name: `Class ${id}`,
      myRole: 'INSTRUCTOR',
    }),
    listClassMemberships: async (classId: string) => ({
      items: [{ userId: `student-${classId}`, username: `Student ${classId}`, role: 'STUDENT' as const }],
    }),
    listLabSpecs: async () => ({
      items: [{ id: 'lab-spec-one', name: 'Lab one', ownerUserId: mockMe.id }],
    }),
    createLabExecution,
  }
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false, refetchOnWindowFocus: false } },
  })
  const router = createMemoryRouter(
    [
      { path: '/classes/:classId/provision', element: <ProvisionPage /> },
      { path: '/operations/:operationId', element: <p>Operation screen</p> },
    ],
    { initialEntries: ['/classes/class-a/provision'] },
  )
  render(
    <QueryClientProvider client={client}>
      <LabbitApiProvider api={api}><RouterProvider router={router} /></LabbitApiProvider>
    </QueryClientProvider>,
  )
  return { router, createLabExecution }
}

async function selectProvisionInputs() {
  await screen.findByRole('heading', { name: 'Class class-a · 새 환경 생성' })
  fireEvent.change(screen.getByRole('combobox', { name: 'LabSpec' }), {
    target: { value: 'lab-spec-one' },
  })
  fireEvent.click(screen.getByRole('checkbox', { name: /Student class-a/ }))
}

describe('ProvisionPage Class navigation isolation', () => {
  it('Class A 확인 화면에서 B로 이동하면 A의 제출 대상·확인 정보가 남지 않는다', async () => {
    const { router, createLabExecution } = mount()
    await selectProvisionInputs()
    fireEvent.click(screen.getByRole('button', { name: '생성 내용 확인' }))
    expect(screen.getByRole('heading', { name: '환경 생성 전 확인' })).toBeInTheDocument()
    expect(screen.getByText('Student class-a')).toBeInTheDocument()

    await act(async () => { await router.navigate('/classes/class-b/provision') })

    expect(await screen.findByRole('heading', { name: 'Class class-b · 새 환경 생성' })).toBeInTheDocument()
    expect(screen.queryByRole('button', { name: '환경 생성 시작' })).not.toBeInTheDocument()
    expect(screen.getByRole('combobox', { name: 'LabSpec' })).toHaveValue('')
    expect(screen.getByRole('checkbox', { name: /Student class-b/ })).not.toBeChecked()
    expect(screen.queryByText('Student class-a')).not.toBeInTheDocument()
    expect(createLabExecution).not.toHaveBeenCalled()
  })

  it('Class A의 미확정 학생/LabSpec 선택도 B 또는 다시 방문한 A로 넘어가지 않는다', async () => {
    const { router } = mount()
    await selectProvisionInputs()
    expect(screen.getByRole('combobox', { name: 'LabSpec' })).toHaveValue('lab-spec-one')

    await act(async () => { await router.navigate('/classes/class-b/provision') })

    expect(await screen.findByRole('heading', { name: 'Class class-b · 새 환경 생성' })).toBeInTheDocument()
    expect(screen.getByRole('combobox', { name: 'LabSpec' })).toHaveValue('')
    expect(screen.getByRole('checkbox', { name: /Student class-b/ })).not.toBeChecked()

    await act(async () => { await router.navigate('/classes/class-a/provision') })
    expect(await screen.findByRole('heading', { name: 'Class class-a · 새 환경 생성' })).toBeInTheDocument()
    expect(screen.getByRole('combobox', { name: 'LabSpec' })).toHaveValue('')
    expect(screen.getByRole('checkbox', { name: /Student class-a/ })).not.toBeChecked()
  })
  it('Class A 제출을 시작한 뒤 B로 이동하면 지연된 A 성공 응답이 B를 다른 Operation으로 이동시키지 않는다', async () => {
    let complete!: (value: OperationAccepted) => void
    const pending = new Promise<OperationAccepted>((resolve) => { complete = resolve })
    const { router, createLabExecution } = mount(async () => pending)
    await selectProvisionInputs()
    fireEvent.click(screen.getByRole('button', { name: '생성 내용 확인' }))
    fireEvent.click(screen.getByRole('button', { name: '환경 생성 시작' }))
    await waitFor(() => expect(createLabExecution).toHaveBeenCalledTimes(1))

    await act(async () => { await router.navigate('/classes/class-b/provision') })
    expect(await screen.findByRole('heading', { name: 'Class class-b · 새 환경 생성' })).toBeInTheDocument()

    await act(async () => {
      complete({ operationId: 'old-class-a-op', target: { type: 'LAB_EXECUTION', id: 'execution-a' } })
      await pending
    })

    expect(router.state.location.pathname).toBe('/classes/class-b/provision')
    expect(screen.queryByText('Operation screen')).not.toBeInTheDocument()
    expect(screen.getByRole('combobox', { name: 'LabSpec' })).toHaveValue('')
  })

})
