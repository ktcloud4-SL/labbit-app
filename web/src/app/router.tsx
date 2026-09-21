import { useQuery } from '@tanstack/react-query'
import { type ReactNode } from 'react'
import {
  createBrowserRouter,
  Navigate,
  useLocation,
  type RouteObject,
} from 'react-router-dom'

import { ClassDetailPage } from '../pages/ClassDetailPage'
import { ClassListPage } from '../pages/ClassListPage'
import { LabExecutionPage } from '../pages/LabExecutionPage'
import { LabPage } from '../pages/LabPage'
import { LabSpecEditorPage } from '../pages/LabSpecEditorPage'
import { LabSpecListPage } from '../pages/LabSpecListPage'
import { LoginPage } from '../pages/LoginPage'
import { NotFoundPage } from '../pages/NotFoundPage'
import { OperationPage } from '../pages/OperationPage'
import { ProvisionPage } from '../pages/ProvisionPage'
import { HttpError } from '../shared/api/httpClient'
import { useLabbitApi } from '../shared/api/LabbitApiProvider'
import { labbitQueryKeys } from '../shared/api/labbitApi'
import { AppShell } from '../shared/ui/AppShell'
import { ErrorState } from '../shared/ui/ErrorState'
import { LoadingState } from '../shared/ui/LoadingState'

function RequireAuth({ children }: { children: ReactNode }) {
  const api = useLabbitApi()
  const location = useLocation()
  const meQuery = useQuery({
    queryKey: labbitQueryKeys.me,
    queryFn: () => api.getMe(),
    retry: false,
  })

  if (meQuery.isPending) {
    return (
      <main className="app-page">
        <LoadingState label="로그인 상태를 확인하는 중..." />
      </main>
    )
  }

  if (meQuery.error instanceof HttpError && meQuery.error.status === 401) {
    return (
      <Navigate
        to="/login"
        replace
        state={{
          from: `${location.pathname}${location.search}`,
          authRequired: true,
        }}
      />
    )
  }

  if (meQuery.error || !meQuery.data) {
    return (
      <main className="app-page">
        <ErrorState message="로그인 상태를 확인하지 못했습니다." />
      </main>
    )
  }

  return <AppShell me={meQuery.data}>{children}</AppShell>
}

function protectedRoute(element: ReactNode) {
  return <RequireAuth>{element}</RequireAuth>
}

export const appRoutes: RouteObject[] = [
  { path: '/', element: <Navigate to="/classes" replace /> },
  { path: '/login', element: <LoginPage /> },
  { path: '/classes', element: protectedRoute(<ClassListPage />) },
  { path: '/classes/:classId', element: protectedRoute(<ClassDetailPage />) },
  { path: '/classes/:classId/provision', element: protectedRoute(<ProvisionPage />) },
  { path: '/classes/:classId/lab', element: protectedRoute(<LabPage />) },
  { path: '/lab-specs', element: protectedRoute(<LabSpecListPage />) },
  { path: '/lab-specs/new', element: protectedRoute(<LabSpecEditorPage />) },
  { path: '/lab-specs/:labSpecId', element: protectedRoute(<LabSpecEditorPage />) },
  {
    path: '/lab-executions/:labExecutionId',
    element: protectedRoute(<LabExecutionPage />),
  },
  { path: '/operations/:operationId', element: protectedRoute(<OperationPage />) },
  { path: '*', element: <NotFoundPage /> },
]

export function createAppRouter() {
  return createBrowserRouter(appRoutes)
}
