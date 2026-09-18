import { fireEvent, render, screen } from '@testing-library/react'
import { createMemoryRouter, RouterProvider } from 'react-router-dom'
import { describe, expect, it, vi } from 'vitest'

import type { ClassDetail, Me } from '../shared/api/contracts'
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

describe('Auth·Class routing', () => {
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

  it('Class 상세 route에서 현재 사용자 컨텍스트를 렌더링한다', async () => {
    renderRoute('/classes/class-kubernetes-basic')

    expect(
      await screen.findByRole('heading', { name: 'Kubernetes Basic' }),
    ).toBeInTheDocument()
    expect(screen.getByText('INSTRUCTOR')).toBeInTheDocument()
    expect(screen.getByText('READY')).toBeInTheDocument()
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
