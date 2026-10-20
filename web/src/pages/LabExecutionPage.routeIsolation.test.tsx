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
  return { router, cleanupLabExecution, resetLabInstance }
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
})
