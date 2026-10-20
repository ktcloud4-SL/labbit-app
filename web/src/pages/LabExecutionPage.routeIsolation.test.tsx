import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { act, fireEvent, render, screen, waitFor } from '@testing-library/react'
import { createMemoryRouter, RouterProvider } from 'react-router-dom'
import { describe, expect, it, vi } from 'vitest'

import type {
  ClassDetail,
  LabExecution,
  OperationAccepted,
} from '../shared/api/contracts'
import { LabbitApiProvider } from '../shared/api/LabbitApiProvider'
import { labbitQueryKeys } from '../shared/api/labbitApi'
import { mockLabbitApi, mockMe } from '../shared/api/mockLabbitApi'
import { LabExecutionPage } from './LabExecutionPage'

function executionFor(id: string): LabExecution {
  return {
    id,
    classId: `class-${id}`,
    labSpecId: 'lab-spec-test',
    instructorUserId: mockMe.id,
    targetUserIds: [`student-${id}`],
    status: 'ACTIVE',
    labInstances: [
      {
        id: `instance-${id}`,
        userId: `student-${id}`,
        status: 'READY',
        generation: 1,
      },
    ],
  }
}

function mount(
  cleanupImpl: (id: string, key: string) => Promise<OperationAccepted> = async () => ({
    operationId: 'cleanup-operation',
    target: { type: 'LAB_EXECUTION', id: 'execution-a' },
  }),
  overrides: Partial<typeof mockLabbitApi> = {},
) {
  const cleanupLabExecution = vi.fn(cleanupImpl)
  const resetLabInstance = vi.fn(async (id: string) => ({
    operationId: `reset-${id}`,
    target: { type: 'LAB_INSTANCE', id },
  }))
  const api = {
    ...mockLabbitApi,
    getLabExecution: async (id: string) => executionFor(id),
    getClass: async (id: string): Promise<ClassDetail> => ({
      id,
      name: `Class ${id}`,
      myRole: 'INSTRUCTOR',
    }),
    listClassMemberships: async (id: string) => ({
      items: [{
        userId: id.replace(/^class-/, 'student-'),
        username: `Student ${id}`,
        role: 'STUDENT' as const,
      }],
    }),
    cleanupLabExecution,
    resetLabInstance,
    ...overrides,
  }
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  })
  const router = createMemoryRouter(
    [
      { path: '/lab-executions/:labExecutionId', element: <LabExecutionPage /> },
      { path: '/operations/:operationId', element: <p>Operation destination</p> },
      { path: '/login', element: <p>Login destination</p> },
    ],
    { initialEntries: ['/lab-executions/execution-a'] },
  )

  render(
    <QueryClientProvider client={client}>
      <LabbitApiProvider api={api}>
        <RouterProvider router={router} />
      </LabbitApiProvider>
    </QueryClientProvider>,
  )
  return { router, client, cleanupLabExecution, resetLabInstance }
}

async function goToExecutionB(router: ReturnType<typeof mount>['router']) {
  await act(async () => { await router.navigate('/lab-executions/execution-b') })
  expect(await screen.findByText('execution-b')).toBeInTheDocument()
}

describe('LBT-53 execution destructive action route isolation', () => {
  it('Execution A의 Cleanup 확인창이 B로 이어지지 않고 B만 실행한다', async () => {
    const { router, cleanupLabExecution } = mount()
    expect(await screen.findByText('execution-a')).toBeInTheDocument()
    fireEvent.click(screen.getByRole('button', { name: '전체 실습 정리' }))
    expect(screen.getByRole('dialog')).toHaveTextContent('현재 실습을 정리할까요?')

    await goToExecutionB(router)
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument()
    expect(cleanupLabExecution).not.toHaveBeenCalled()

    fireEvent.click(screen.getByRole('button', { name: '전체 실습 정리' }))
    fireEvent.click(screen.getByRole('button', { name: '정리 시작' }))
    await waitFor(() => expect(cleanupLabExecution).toHaveBeenCalledTimes(1))
    expect(cleanupLabExecution).toHaveBeenCalledWith('execution-b', expect.any(String))
  })

  it('Execution A의 학생 Reset 확인창은 B에 남지 않으며 B 학생만 초기화한다', async () => {
    const { router, resetLabInstance } = mount()
    expect(await screen.findByText('execution-a')).toBeInTheDocument()
    fireEvent.click(screen.getByRole('button', { name: '초기화' }))
    expect(screen.getByRole('dialog')).toHaveTextContent('Student class-execution-a 환경을 초기화할까요?')

    await goToExecutionB(router)
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument()
    expect(resetLabInstance).not.toHaveBeenCalled()
    fireEvent.click(screen.getByRole('button', { name: '초기화' }))
    fireEvent.click(screen.getByRole('button', { name: '초기화 시작' }))
    await waitFor(() => expect(resetLabInstance).toHaveBeenCalledTimes(1))
    expect(resetLabInstance).toHaveBeenCalledWith('instance-execution-b', expect.any(String))
  })

  it('A Cleanup 전송 후 B로 이동하면 A의 늦은 성공 응답이 B 화면을 이동시키지 않는다', async () => {
    let finish!: (v: OperationAccepted) => void
    const pending = new Promise<OperationAccepted>((resolve) => { finish = resolve })
    const { router, cleanupLabExecution } = mount(async () => pending)
    expect(await screen.findByText('execution-a')).toBeInTheDocument()
    fireEvent.click(screen.getByRole('button', { name: '전체 실습 정리' }))
    fireEvent.click(screen.getByRole('button', { name: '정리 시작' }))
    await waitFor(() => expect(cleanupLabExecution).toHaveBeenCalledTimes(1))

    await goToExecutionB(router)
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument()
    await act(async () => {
      finish({
        operationId: 'late-cleanup-a',
        target: { type: 'LAB_EXECUTION', id: 'execution-a' },
      })
      await pending
    })
    expect(router.state.location.pathname).toBe('/lab-executions/execution-b')
    expect(screen.queryByText('Operation destination')).not.toBeInTheDocument()
    expect(cleanupLabExecution).toHaveBeenCalledTimes(1)
    expect(cleanupLabExecution).toHaveBeenCalledWith('execution-a', expect.any(String))
  })


  it('Cleanup 확인 후 사전 GET 지연 중 B로 이동하면 A의 파괴적 POST를 새로 시작하지 않는다', async () => {
    let finishGet!: (value: LabExecution) => void
    const pendingGet = new Promise<LabExecution>((resolve) => { finishGet = resolve })
    let readsA = 0
    const getLabExecution = vi.fn(async (id: string) => {
      if (id !== 'execution-a') return executionFor(id)
      readsA += 1
      return readsA === 1 ? executionFor(id) : pendingGet
    })
    const { router, cleanupLabExecution } = mount(undefined, { getLabExecution })
    expect(await screen.findByText('execution-a')).toBeInTheDocument()
    fireEvent.click(screen.getByRole('button', { name: '전체 실습 정리' }))
    fireEvent.click(screen.getByRole('button', { name: '정리 시작' }))
    await waitFor(() => expect(getLabExecution).toHaveBeenCalledTimes(2))
    expect(cleanupLabExecution).not.toHaveBeenCalled()

    await goToExecutionB(router)
    await act(async () => {
      finishGet(executionFor('execution-a'))
      await pendingGet
      await Promise.resolve()
      await Promise.resolve()
    })

    expect(router.state.location.pathname).toBe('/lab-executions/execution-b')
    expect(cleanupLabExecution).not.toHaveBeenCalled()
    expect(screen.queryByText('Operation destination')).not.toBeInTheDocument()
  })

  it('Cleanup 확인 후 서버가 CLEANING_UP으로 바뀌었으면 POST하지 않고 상태를 다시 읽는다', async () => {
    let isCleaning = false
    const { cleanupLabExecution } = mount(undefined, {
      getLabExecution: async (id) => ({
        ...executionFor(id),
        status: isCleaning ? 'CLEANING_UP' : 'ACTIVE',
      }),
    })
    expect(await screen.findByText('execution-a')).toBeInTheDocument()
    fireEvent.click(screen.getByRole('button', { name: '전체 실습 정리' }))
    expect(screen.getByRole('dialog')).toBeInTheDocument()
    isCleaning = true
    fireEvent.click(screen.getByRole('button', { name: '정리 시작' }))
    await waitFor(() => {
      expect(cleanupLabExecution).not.toHaveBeenCalled()
      expect(screen.queryByRole('dialog')).not.toBeInTheDocument()
    })
    expect(screen.getByText('정리 중')).toBeInTheDocument()
  })

  it('Cleanup 확인 당시 강사였어도 POST 전 서버 역할이 STUDENT라면 403 차단한다', async () => {
    let role: ClassDetail['myRole'] = 'INSTRUCTOR'
    const { cleanupLabExecution } = mount(undefined, {
      getClass: async (id) => ({ id, name: `Class ${id}`, myRole: role }),
    })
    expect(await screen.findByText('execution-a')).toBeInTheDocument()
    fireEvent.click(screen.getByRole('button', { name: '전체 실습 정리' }))
    role = 'STUDENT'
    fireEvent.click(screen.getByRole('button', { name: '정리 시작' }))
    expect(await screen.findByRole('alert')).toHaveTextContent('권한이 없습니다')
    expect(cleanupLabExecution).not.toHaveBeenCalled()
  })

  it('Reset 확인 후 LabInstance generation이 변경됐으면 이전 대상으로 POST하지 않는다', async () => {
    let generation = 1
    const { resetLabInstance } = mount(undefined, {
      getLabExecution: async (id) => ({
        ...executionFor(id),
        labInstances: executionFor(id).labInstances.map((instance) => ({
          ...instance,
          generation,
        })),
      }),
    })
    expect(await screen.findByText('execution-a')).toBeInTheDocument()
    fireEvent.click(screen.getByRole('button', { name: '초기화' }))
    generation = 2
    fireEvent.click(screen.getByRole('button', { name: '초기화 시작' }))
    await waitFor(() => expect(screen.queryByRole('dialog')).not.toBeInTheDocument())
    expect(resetLabInstance).not.toHaveBeenCalled()
  })

  it('이미 연 Cleanup 확인창은 강사 권한 회수 후 복구되어도 다시 나타나지 않는다', async () => {
    const { client } = mount()
    expect(await screen.findByText('execution-a')).toBeInTheDocument()
    fireEvent.click(screen.getByRole('button', { name: '전체 실습 정리' }))
    expect(screen.getByRole('dialog')).toBeInTheDocument()

    act(() => {
      client.setQueryData(labbitQueryKeys.classDetail('class-execution-a'), {
        id: 'class-execution-a',
        name: 'Class class-execution-a',
        myRole: 'STUDENT',
      } satisfies ClassDetail)
    })
    expect(await screen.findByText('INSTRUCTOR만 실습 운영 화면에 접근할 수 있습니다.')).toBeInTheDocument()
    act(() => {
      client.setQueryData(labbitQueryKeys.classDetail('class-execution-a'), {
        id: 'class-execution-a',
        name: 'Class class-execution-a',
        myRole: 'INSTRUCTOR',
      } satisfies ClassDetail)
    })
    await waitFor(() => expect(screen.getByText('execution-a')).toBeInTheDocument())
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument()
  })

})
