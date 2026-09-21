import { fireEvent, render, screen } from '@testing-library/react'
import { createMemoryRouter, RouterProvider } from 'react-router-dom'
import { describe, expect, it, vi } from 'vitest'

import type { ClassDetail, LabSpec, Me } from '../shared/api/contracts'
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

const labSpecFixture: LabSpec = {
  id: 'lab-spec-kubernetes-basic',
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

  it('Lab placeholder route와 classId를 보호 route 안에서 렌더링한다', async () => {
    renderRoute('/classes/demo/lab')

    expect(
      await screen.findByRole('heading', { name: 'Lab Workspace' }),
    ).toBeInTheDocument()
    expect(screen.getByText('Class: demo')).toBeInTheDocument()
  })

  it('정의되지 않은 경로는 Not Found 화면을 렌더링한다', () => {
    renderRoute('/not-found')

    expect(
      screen.getByRole('heading', { name: '페이지를 찾을 수 없습니다.' }),
    ).toBeInTheDocument()
  })
})
