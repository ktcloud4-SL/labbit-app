import { fireEvent, render, screen } from '@testing-library/react'
import { createMemoryRouter, RouterProvider } from 'react-router-dom'
import { describe, expect, it, vi } from 'vitest'

import type {
  ClassDetail,
  LabExecution,
  LabSpec,
  Me,
  Operation,
} from '../shared/api/contracts'
import { HttpError } from '../shared/api/httpClient'
import type { LabbitApi } from '../shared/api/labbitApi'
import { AppProviders } from './providers/AppProviders'
import { appRoutes } from './router'

const meFixture: Me = {
  id: 'user-heechul',
  username: 'heechul',
  organization: {
    id: 'org-samsunglions',
    name: 'SamsungLions Org',
  },
  organizationRole: 'MEMBER',
}

const classDetailFixture: ClassDetail = {
  id: 'class-kubernetes-basic',
  name: 'Kubernetes Basic',
  myRole: 'INSTRUCTOR',
  activeLabExecution: {
    id: 'execution-kubernetes-basic',
    status: 'ACTIVE',
  },
  myLabInstance: {
    id: 'lab-instance-heechul',
    userId: meFixture.id,
    status: 'READY',
    generation: 1,
  },
}

const labExecutionFixture: LabExecution = {
  id: 'execution-kubernetes-basic',
  classId: 'class-kubernetes-basic',
  labSpecId: 'lab-spec-kubernetes-basic',
  instructorUserId: meFixture.id,
  targetUserIds: ['user-student-a'],
  status: 'ACTIVE',
  labInstances: [
    {
      id: 'lab-instance-heechul',
      userId: meFixture.id,
      status: 'READY',
      generation: 1,
    },
    {
      id: 'lab-instance-student-a',
      userId: 'user-student-a',
      status: 'ERROR',
      generation: 1,
    },
  ],
}

const operationFixture: Operation = {
  id: 'operation-provision-1',
  type: 'PROVISION',
  status: 'SUCCEEDED',
  stage: 'READY',
  target: {
    type: 'LAB_EXECUTION',
    id: labExecutionFixture.id,
  },
  createdAt: '2026-09-21T00:00:00Z',
  updatedAt: '2026-09-21T00:01:00Z',
  finishedAt: '2026-09-21T00:01:00Z',
}

const labSpecFixture: LabSpec = {  id: 'lab-spec-kubernetes-basic',
  name: 'Kubernetes Basic Lab',
  description: 'Multi-VM LabSpec',
  ownerUserId: meFixture.id,
  vms: [
    {
      role: 'control',
      imageRef: 'ubuntu-24.04',
      sizeRef: 'medium',
      count: 1,
    },
  ],
  workspaceVm: {
    role: 'control',
    instanceIndex: 0,
  },
  internetOutbound: true,
}

function createApi(overrides: Partial<LabbitApi> = {}): LabbitApi {
  return {
    login: async () => {},
    logout: async () => {},
    getMe: async () => meFixture,
    listClasses: async () => ({
      items: [
        {
          id: classDetailFixture.id,
          name: classDetailFixture.name,
          myRole: classDetailFixture.myRole,
          activeLabExecution: classDetailFixture.activeLabExecution,
        },
      ],
    }),
    getClass: async () => classDetailFixture,
    listClassMemberships: async () => ({ items: [] }),
    listLabSpecs: async () => ({
      items: [
        {
          id: labSpecFixture.id,
          name: labSpecFixture.name,
          description: labSpecFixture.description,
          ownerUserId: labSpecFixture.ownerUserId,
        },
      ],
    }),
    getLabSpec: async () => ({
      labSpec: labSpecFixture,
      etag: '"lab-spec-v1"',
    }),
    createLabSpec: async (input) => ({
      id: 'lab-spec-created',
      ownerUserId: meFixture.id,
      ...input,
    }),
    updateLabSpec: async (_labSpecId, input) => ({
      labSpec: {
        id: labSpecFixture.id,
        ownerUserId: meFixture.id,
        ...input,
      },
      etag: '"lab-spec-v2"',
    }),
    createLabExecution: async () => ({
      operationId: operationFixture.id,
      target: operationFixture.target,
    }),
    getLabExecution: async () => labExecutionFixture,
    cleanupLabExecution: async () => ({
      operationId: 'operation-cleanup-1',
      target: {
        type: 'LAB_EXECUTION',
        id: labExecutionFixture.id,
      },
    }),
    resetLabInstance: async (labInstanceId) => ({
      operationId: 'operation-reset-1',
      target: {
        type: 'LAB_INSTANCE',
        id: labInstanceId,
      },
    }),
    getOperation: async () => operationFixture,
    ...overrides,
  }
}

function renderRoute(path: string, api: LabbitApi = createApi()) {
  const router = createMemoryRouter(appRoutes, {
    initialEntries: [path],
  })

  render(
    <AppProviders api={api}>
      <RouterProvider router={router} />
    </AppProviders>,
  )

  return router
}

describe('Auth·Class·LabSpec routing', () => {
  it('미인증 사용자가 보호 route에 진입하면 Login으로 이동한다', async () => {
    renderRoute(
      '/classes',
      createApi({
        getMe: async () => {
          throw new HttpError(401)
        },
      }),
    )

    expect(
      await screen.findByRole('heading', { name: 'Labbit에 로그인' }),
    ).toBeInTheDocument()
  })

  it('Login → /me → Class 목록 Flow를 수행한다', async () => {
    const login = vi.fn(async () => {})
    const getMe = vi.fn(async () => meFixture)

    renderRoute(
      '/login',
      createApi({
        login,
        getMe,
      }),
    )

    fireEvent.change(screen.getByLabelText('사용자 이름'), {
      target: { value: 'heechul' },
    })
    fireEvent.change(screen.getByLabelText('비밀번호'), {
      target: { value: 'password' },
    })
    fireEvent.click(screen.getByRole('button', { name: '로그인' }))

    expect(await screen.findByRole('heading', { name: '수업' })).toBeInTheDocument()
    expect(login).toHaveBeenCalledWith({
      username: 'heechul',
      password: 'password',
    })
    expect(getMe).toHaveBeenCalled()
    expect(screen.getByText('Kubernetes Basic')).toBeInTheDocument()
  })

  it('Class 목록이 비어 있으면 Empty 상태를 렌더링한다', async () => {
    renderRoute(
      '/classes',
      createApi({
        listClasses: async () => ({ items: [] }),
      }),
    )

    expect(
      await screen.findByRole('heading', { name: '참여 중인 수업이 없습니다.' }),
    ).toBeInTheDocument()
  })

  it('Class 상세 route에서 현재 사용자 컨텍스트를 렌더링한다', async () => {
    renderRoute('/classes/class-kubernetes-basic')

    expect(
      await screen.findByRole('heading', { name: 'Kubernetes Basic' }),
    ).toBeInTheDocument()
    expect(screen.getByText('INSTRUCTOR')).toBeInTheDocument()
    expect(screen.getByText('READY')).toBeInTheDocument()
    expect(screen.getByRole('link', { name: 'Lab Workspace 열기' })).toBeInTheDocument()
    expect(screen.getByRole('link', { name: '실습 정의 관리' })).toBeInTheDocument()
  })

  it('LabInstance가 READY가 아니면 Workspace 진입 링크를 노출하지 않는다', async () => {
    renderRoute(
      '/classes/class-kubernetes-basic',
      createApi({
        getClass: async () => ({
          ...classDetailFixture,
          myLabInstance: {
            ...classDetailFixture.myLabInstance!,
            status: 'PROVISIONING',
          },
        }),
      }),
    )

    expect(
      await screen.findByRole('heading', { name: 'Kubernetes Basic' }),
    ).toBeInTheDocument()
    expect(screen.getByText('PROVISIONING')).toBeInTheDocument()
    expect(
      screen.queryByRole('link', { name: 'Lab Workspace 열기' }),
    ).not.toBeInTheDocument()
    expect(
      screen.getByText('실습 환경이 READY 상태가 되면 Workspace를 열 수 있습니다.'),
    ).toBeInTheDocument()
  })

  it('LabInstance ERROR에서는 Workspace 진입을 막고 오류 안내를 표시한다', async () => {
    renderRoute(
      '/classes/class-kubernetes-basic',
      createApi({
        getClass: async () => ({
          ...classDetailFixture,
          myLabInstance: {
            ...classDetailFixture.myLabInstance!,
            status: 'ERROR',
          },
        }),
      }),
    )

    expect(
      await screen.findByText(
        '실습 환경에 오류가 있어 Workspace를 열 수 없습니다. 상태를 확인해 주세요.',
      ),
    ).toBeInTheDocument()
    expect(
      screen.queryByRole('link', { name: 'Lab Workspace 열기' }),
    ).not.toBeInTheDocument()
  })

  it('Class 상세 403은 권한 없음 상태로 표시한다', async () => {
    renderRoute(
      '/classes/forbidden-class',
      createApi({
        getClass: async () => {
          throw new HttpError(403)
        },
      }),
    )

    expect(await screen.findByText('이 수업을 볼 권한이 없습니다.')).toBeInTheDocument()
  })

  it('Class 상세 404는 찾을 수 없음 상태로 표시한다', async () => {
    renderRoute(
      '/classes/missing-class',
      createApi({
        getClass: async () => {
          throw new HttpError(404)
        },
      }),
    )

    expect(await screen.findByText('수업을 찾을 수 없습니다.')).toBeInTheDocument()
  })

  it('LabSpec 목록에서 소유 여부와 편집 진입을 표시한다', async () => {
    renderRoute('/lab-specs')

    expect(
      await screen.findByRole('heading', { name: '실습 정의' }),
    ).toBeInTheDocument()
    expect(screen.getByText('Kubernetes Basic Lab')).toBeInTheDocument()
    expect(screen.getByText('내 LabSpec')).toBeInTheDocument()
    expect(screen.getByRole('link', { name: '편집' })).toBeInTheDocument()
  })

  it('owner는 LabSpec 상세를 편집할 수 있다', async () => {
    renderRoute('/lab-specs/lab-spec-kubernetes-basic')

    expect(
      await screen.findByRole('heading', { name: 'Kubernetes Basic Lab' }),
    ).toBeInTheDocument()
    expect(screen.getByLabelText('이름')).toHaveValue('Kubernetes Basic Lab')
    expect(screen.getByLabelText('Role')).toHaveValue('control')
    expect(screen.getByRole('button', { name: 'LabSpec 저장' })).toBeInTheDocument()
  })

  it('LabSpec stale update 412를 덮어쓰지 않고 안내한다', async () => {
    const updateLabSpec = vi.fn(async () => {
      throw new HttpError(412)
    })

    renderRoute(
      '/lab-specs/lab-spec-kubernetes-basic',
      createApi({ updateLabSpec }),
    )

    await screen.findByRole('heading', { name: 'Kubernetes Basic Lab' })
    fireEvent.click(screen.getByRole('button', { name: 'LabSpec 저장' }))

    expect(
      await screen.findByText(
        '다른 곳에서 LabSpec이 수정되었습니다. 최신 내용을 다시 불러온 뒤 다시 저장해 주세요.',
      ),
    ).toBeInTheDocument()
    expect(updateLabSpec).toHaveBeenCalled()
  })

  it('다른 owner의 LabSpec은 읽기 전용으로 표시한다', async () => {
    renderRoute(
      '/lab-specs/lab-spec-other',
      createApi({
        getLabSpec: async () => ({
          labSpec: {
            ...labSpecFixture,
            id: 'lab-spec-other',
            ownerUserId: 'user-other',
          },
          etag: '"other-v1"',
        }),
      }),
    )

    expect(
      await screen.findByText(
        '이 LabSpec은 다른 Instructor가 소유하고 있어 현재 계정에서는 읽기만 할 수 있습니다.',
      ),
    ).toBeInTheDocument()
    expect(
      screen.queryByRole('button', { name: 'LabSpec 저장' }),
    ).not.toBeInTheDocument()
  })

  it('활성 LabExecution이 있으면 중복 Provision을 막고 현재 실행을 안내한다', async () => {
    renderRoute(
      '/classes/class-kubernetes-basic/provision',
      createApi({
        listClassMemberships: async () => ({
          items: [
            {
              userId: 'user-student-a',
              username: 'student-a',
              role: 'STUDENT',
            },
          ],
        }),
      }),
    )

    expect(
      await screen.findByText('이미 활성 LabExecution이 있습니다.'),
    ).toBeInTheDocument()
    expect(
      screen.getByRole('link', { name: '현재 실습 운영 보기' }),
    ).toBeInTheDocument()
  })

  it('LabSpec과 학생을 확인한 뒤 Provision Operation을 시작한다', async () => {
    const createLabExecution = vi.fn(async () => ({
      operationId: 'operation-new',
      target: {
        type: 'LAB_EXECUTION',
        id: 'execution-new',
      },
    }))

    renderRoute(
      '/classes/class-kubernetes-basic/provision',
      createApi({
        getClass: async () => ({
          ...classDetailFixture,
          activeLabExecution: undefined,
          myLabInstance: undefined,
        }),
        listClassMemberships: async () => ({
          items: [
            {
              userId: 'user-student-a',
              username: 'student-a',
              role: 'STUDENT',
            },
          ],
        }),
        createLabExecution,
        getOperation: async () => ({
          ...operationFixture,
          id: 'operation-new',
          target: {
            type: 'LAB_EXECUTION',
            id: 'execution-new',
          },
        }),
      }),
    )

    await screen.findByRole('heading', {
      name: 'Kubernetes Basic · 새 환경 생성',
    })

    fireEvent.change(screen.getByLabelText('LabSpec'), {
      target: { value: labSpecFixture.id },
    })
    fireEvent.click(screen.getByRole('checkbox'))
    fireEvent.click(screen.getByRole('button', { name: '생성 내용 확인' }))
    expect(
      await screen.findByRole('heading', { name: 'Provision 시작 전 확인' }),
    ).toBeInTheDocument()

    fireEvent.click(screen.getByRole('button', { name: 'Provision 시작' }))

    expect(
      await screen.findByRole('heading', { name: '완료' }),
    ).toBeInTheDocument()
    expect(createLabExecution).toHaveBeenCalledWith(
      classDetailFixture.id,
      {
        labSpecId: labSpecFixture.id,
        targetStudentIds: ['user-student-a'],
      },
      expect.any(String),
    )
  })

  it('Operation RECONCILING을 중복 재실행이 아닌 Provider 확인 상태로 표시한다', async () => {
    renderRoute(
      '/operations/operation-reconciling',
      createApi({
        getOperation: async () => ({
          ...operationFixture,
          id: 'operation-reconciling',
          status: 'RECONCILING',
          stage: 'VERIFY_PROVIDER_STATE',
        }),
      }),
    )

    expect(
      await screen.findByRole('heading', {
        name: '실제 Provider 상태 확인 중',
      }),
    ).toBeInTheDocument()
    expect(
      screen.getByText(
        '같은 작업을 다시 실행하지 않고 Provider의 실제 상태를 확인하고 있습니다.',
      ),
    ).toBeInTheDocument()
  })

  it('LabExecution의 학생별 부분 실패를 개별 상태로 표시한다', async () => {
    renderRoute('/lab-executions/execution-kubernetes-basic')

    expect(
      await screen.findByRole('heading', { name: '실습 운영 상태' }),
    ).toBeInTheDocument()
    expect(
      screen.getByText('일부 LabInstance에 오류가 있습니다.'),
    ).toBeInTheDocument()
    expect(screen.getByText('ERROR')).toBeInTheDocument()
  })

  it('강사는 학생 LabInstance Reset을 확인한 뒤 Operation을 시작한다', async () => {
    const resetLabInstance = vi.fn(async (labInstanceId: string) => ({
      operationId: 'operation-reset-1',
      target: {
        type: 'LAB_INSTANCE',
        id: labInstanceId,
      },
    }))

    renderRoute(
      '/lab-executions/execution-kubernetes-basic',
      createApi({
        getLabExecution: async () => ({
          ...labExecutionFixture,
          labInstances: labExecutionFixture.labInstances.map((instance) =>
            instance.id === 'lab-instance-student-a'
              ? { ...instance, status: 'READY' }
              : instance,
          ),
        }),
        listClassMemberships: async () => ({
          items: [
            {
              userId: 'user-student-a',
              username: 'student-a',
              role: 'STUDENT',
            },
          ],
        }),
        resetLabInstance,
        getOperation: async () => ({
          ...operationFixture,
          id: 'operation-reset-1',
          type: 'RESET',
          target: {
            type: 'LAB_INSTANCE',
            id: 'lab-instance-student-a',
          },
        }),
      }),
    )

    await screen.findByRole('heading', { name: '실습 운영 상태' })
    fireEvent.click(screen.getByRole('button', { name: 'Reset' }))
    expect(
      await screen.findByRole('heading', { name: 'student-a 환경을 Reset할까요?' }),
    ).toBeInTheDocument()

    fireEvent.click(screen.getByRole('button', { name: 'Reset 시작' }))
    expect(await screen.findByRole('heading', { name: '완료' })).toBeInTheDocument()
    expect(resetLabInstance).toHaveBeenCalledWith(
      'lab-instance-student-a',
      expect.any(String),
    )
  })

  it('ERROR 학생 LabInstance에는 Reset 대신 Cleanup 필요를 안내한다', async () => {
    renderRoute('/lab-executions/execution-kubernetes-basic')

    await screen.findByRole('heading', { name: '실습 운영 상태' })
    expect(screen.queryByRole('button', { name: 'Reset' })).not.toBeInTheDocument()
    expect(screen.getByText('Cleanup 필요')).toBeInTheDocument()
  })

  it('강사는 LabExecution Cleanup을 확인한 뒤 Operation을 시작한다', async () => {
    const cleanupLabExecution = vi.fn(async () => ({
      operationId: 'operation-cleanup-1',
      target: {
        type: 'LAB_EXECUTION',
        id: labExecutionFixture.id,
      },
    }))

    renderRoute(
      '/lab-executions/execution-kubernetes-basic',
      createApi({
        cleanupLabExecution,
        getOperation: async () => ({
          ...operationFixture,
          id: 'operation-cleanup-1',
          type: 'CLEANUP',
        }),
      }),
    )

    await screen.findByRole('heading', { name: '실습 운영 상태' })
    fireEvent.click(screen.getByRole('button', { name: 'Class Cleanup' }))
    expect(
      await screen.findByRole('heading', {
        name: '현재 LabExecution을 Cleanup할까요?',
      }),
    ).toBeInTheDocument()

    fireEvent.click(screen.getByRole('button', { name: 'Cleanup 시작' }))
    expect(await screen.findByRole('heading', { name: '완료' })).toBeInTheDocument()
    expect(cleanupLabExecution).toHaveBeenCalledWith(
      labExecutionFixture.id,
      expect.any(String),
    )
  })

  it('READY LabInstance만 직접 Workspace URL 진입을 허용한다', async () => {
    renderRoute('/classes/class-kubernetes-basic/lab')

    expect(
      await screen.findByRole('heading', { name: 'Kubernetes Basic' }),
    ).toBeInTheDocument()
    expect(
      screen.getByRole('region', { name: 'Lab Workspace Shell' }),
    ).toBeInTheDocument()
    expect(screen.getByText('File Tree')).toBeInTheDocument()
    expect(screen.getByText('Editor')).toBeInTheDocument()
    expect(screen.getByText('Preview')).toBeInTheDocument()
    expect(screen.getByText('Terminal / Live')).toBeInTheDocument()
  })

  it('직접 Workspace URL에서도 PROVISIONING 상태는 진입을 막는다', async () => {
    renderRoute(
      '/classes/class-kubernetes-basic/lab',
      createApi({
        getClass: async () => ({
          ...classDetailFixture,
          myLabInstance: {
            ...classDetailFixture.myLabInstance!,
            status: 'PROVISIONING',
          },
        }),
      }),
    )

    expect(
      await screen.findByText('실습 환경을 준비하고 있습니다.'),
    ).toBeInTheDocument()
    expect(
      screen.queryByRole('region', { name: 'Lab Workspace Shell' }),
    ).not.toBeInTheDocument()
  })

  it('직접 Workspace URL에서도 ERROR 상태는 진입을 막는다', async () => {
    renderRoute(
      '/classes/class-kubernetes-basic/lab',
      createApi({
        getClass: async () => ({
          ...classDetailFixture,
          myLabInstance: {
            ...classDetailFixture.myLabInstance!,
            status: 'ERROR',
          },
        }),
      }),
    )

    expect(
      await screen.findByText(
        '실습 환경에 오류가 있어 Workspace를 열 수 없습니다.',
      ),
    ).toBeInTheDocument()
    expect(
      screen.queryByRole('region', { name: 'Lab Workspace Shell' }),
    ).not.toBeInTheDocument()
  })

  it('직접 Workspace URL의 Class 403을 권한 없음으로 닫는다', async () => {
    renderRoute(
      '/classes/forbidden/lab',
      createApi({
        getClass: async () => {
          throw new HttpError(403)
        },
      }),
    )

    expect(
      await screen.findByText(
        '이 Class의 Workspace에 접근할 권한이 없습니다.',
      ),
    ).toBeInTheDocument()
  })

  it('정의되지 않은 경로는 Not Found 화면을 렌더링한다', () => {
    renderRoute('/not-found')

    expect(
      screen.getByRole('heading', { name: '페이지를 찾을 수 없습니다.' }),
    ).toBeInTheDocument()
  })
})
