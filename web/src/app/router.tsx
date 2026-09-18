import { createBrowserRouter, Navigate, type RouteObject } from 'react-router-dom'

import { ClassListPage } from '../pages/ClassListPage'
import { LabPage } from '../pages/LabPage'
import { LoginPage } from '../pages/LoginPage'
import { NotFoundPage } from '../pages/NotFoundPage'

export const appRoutes: RouteObject[] = [
  { path: '/', element: <Navigate to="/classes" replace /> },
  { path: '/login', element: <LoginPage /> },
  { path: '/classes', element: <ClassListPage /> },
  { path: '/classes/:classId/lab', element: <LabPage /> },
  { path: '*', element: <NotFoundPage /> },
]

export function createAppRouter() {
  return createBrowserRouter(appRoutes)
}
