import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { render, screen } from '@testing-library/react'
import { createMemoryRouter, RouterProvider } from 'react-router-dom'
import { describe, expect, it, vi } from 'vitest'

import type { ClassDetail } from '../shared/api/contracts'
import { HttpError } from '../shared/api/httpClient'
import { LabbitApiProvider } from '../shared/api/LabbitApiProvider'
import { mockLabbitApi } from '../shared/api/mockLabbitApi'
import { LabPage } from './LabPage'

const readyClassDetail: ClassDetail = {
  id: 'class-workspace',
  name: 'Workspace Class',
  myRole: 'STUDENT',
  activeLabExecution: {
    id: 'execution-workspace',
    status: 'ACTIVE',
  },
  myLabInstance: {
    id: 'lab-instance-workspace',
    userId: 'user-student',
    status: 'READY',
    generation: 2,
  },
}

function classWithLabStatus(status: string): ClassDetail {
  return {
    ...readyClassDetail,
    myLabInstance: {
      ...readyClassDetail.myLabInstance!,
      status,
    },
  }
}

function renderLabPage(result: ClassDetail | Error) {
  const queryClient = new QueryClient({
    defaultOptions: {
      queries: {
        retry: false,
      },
    },
  })

  const getClass = vi.fn(async () => {
    if (result instanceof Error) {
      throw result
    }

    return result
  })

  const api = {
    ...mockLabbitApi,
    getClass,
  }

  const router = createMemoryRouter(
    [
      {
        path: '/classes/:classId/lab',
        element: <LabPage />,
      },
      {
        path: '/login',
        element: <div>Login route</div>,
      },
      {
        path: '/classes',
        element: <div>Class list route</div>,
      },
      {
        path: '/classes/:classId',
        element: <div>Class detail route</div>,
      },
      {
        path: '/lab-executions/:labExecutionId',
        element: <div>Lab execution route</div>,
      },
    ],
    {
      initialEntries: ['/classes/class-workspace/lab'],
    },
  )

  render(
    <QueryClientProvider client={queryClient}>
      <LabbitApiProvider api={api}>
        <RouterProvider router={router} />
      </LabbitApiProvider>
    </QueryClientProvider>,
  )

  return { getClass, router }
}

describe('LabPage Workspace 진입 상태', () => {
  it('Class 조회가 401이면 sessionExpired로 Login에 이동한다', async () => {
    const { router } = renderLabPage(new HttpError(401))

    expect(await screen.findByText('Login route')).toBeInTheDocument()
    expect(router.state.location.pathname).toBe('/login')
    expect(router.state.location.state).toEqual({
      from: '/classes/class-workspace/lab',
      reason: 'sessionExpired',
    })
  })

  it('Class 조회가 403이면 Workspace 접근 권한 없음으로 안내한다', async () => {
    renderLabPage(new HttpError(403))

    expect(
      await screen.findByText('이 Class의 Workspace에 접근할 권한이 없습니다.'),
    ).toBeInTheDocument()
    expect(
      screen.getByRole('link', { name: '수업 목록으로 돌아가기' }),
    ).toHaveAttribute('href', '/classes')
  })

  it('Class 조회가 404이면 Class 없음으로 안내한다', async () => {
    renderLabPage(new HttpError(404))

    expect(await screen.findByText('Class를 찾을 수 없습니다.')).toBeInTheDocument()
    expect(
      screen.getByRole('link', { name: '수업 목록으로 돌아가기' }),
    ).toHaveAttribute('href', '/classes')
  })

  it('Class 조회가 일반 오류이면 Workspace 진입 조건 확인 실패로 안내한다', async () => {
    renderLabPage(new HttpError(503))

    expect(
      await screen.findByText('Workspace 진입 조건을 확인하지 못했습니다.'),
    ).toBeInTheDocument()
  })

  it('내 LabInstance가 없으면 실습 환경 없음으로 안내한다', async () => {
    renderLabPage({
      ...readyClassDetail,
      myLabInstance: undefined,
    })

    expect(
      await screen.findByText('현재 사용할 수 있는 실습 환경이 없습니다.'),
    ).toBeInTheDocument()
    expect(
      screen.getByText('활성 실습과 내 실습 환경 할당 상태를 확인해 주세요.'),
    ).toBeInTheDocument()
  })

  it.each(['PENDING', 'PROVISIONING'])(
    'LabInstance가 %s이면 준비 중 상태로 안내한다',
    async (status) => {
      renderLabPage(classWithLabStatus(status))

      expect(
        await screen.findByText('실습 환경을 준비하고 있습니다.'),
      ).toBeInTheDocument()
      expect(
        screen.getByText(
          '환경 준비가 끝나면 파일, 편집기, 미리보기, 터미널 영역을 사용할 수 있습니다.',
        ),
      ).toBeInTheDocument()
    },
  )

  it('LabInstance가 ERROR이면 Workspace 차단과 운영 상태 확인을 안내한다', async () => {
    renderLabPage(classWithLabStatus('ERROR'))

    expect(
      await screen.findByText('실습 환경에 오류가 있어 Workspace를 열 수 없습니다.'),
    ).toBeInTheDocument()
    expect(
      screen.getByRole('link', { name: '실습 운영 상태 보기' }),
    ).toHaveAttribute('href', '/lab-executions/execution-workspace')
  })

  it('LabInstance가 DELETING이면 정리 중 상태로 안내한다', async () => {
    renderLabPage(classWithLabStatus('DELETING'))

    expect(
      await screen.findByText('실습 환경을 정리하고 있습니다.'),
    ).toBeInTheDocument()
    expect(
      screen.getByText('리소스 정리가 완료되기 전에는 Workspace를 사용할 수 없습니다.'),
    ).toBeInTheDocument()
  })

  it('LabInstance가 READY이면 현재 Workspace Shell을 노출한다', async () => {
    const { getClass } = renderLabPage(readyClassDetail)

    expect(await screen.findByText('Workspace Class')).toBeInTheDocument()
    expect(screen.getByText('실습 환경 lab-instance-workspace · generation 2')).toBeInTheDocument()
    expect(screen.getByText('파일 탐색기 준비 중')).toBeInTheDocument()
    expect(screen.getByText('편집기 준비 중')).toBeInTheDocument()
    expect(screen.getByText('실행 중인 미리보기가 없습니다.')).toBeInTheDocument()
    expect(screen.getByText('터미널 연결 준비 중')).toBeInTheDocument()
    expect(getClass).toHaveBeenCalledWith('class-workspace')
  })
})
