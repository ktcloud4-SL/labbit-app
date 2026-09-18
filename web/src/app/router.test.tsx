import { render, screen } from '@testing-library/react'
import { createMemoryRouter, RouterProvider } from 'react-router-dom'
import { describe, expect, it } from 'vitest'

import { AppProviders } from './providers/AppProviders'
import { appRoutes } from './router'

function renderRoute(path: string) {
  const router = createMemoryRouter(appRoutes, {
    initialEntries: [path],
  })

  render(
    <AppProviders>
      <RouterProvider router={router} />
    </AppProviders>,
  )
}

describe('Frontend foundation routing', () => {
  it('Login placeholder route를 렌더링한다', () => {
    renderRoute('/login')

    expect(screen.getByRole('heading', { name: '로그인' })).toBeInTheDocument()
  })

  it('Class 목록 placeholder route를 렌더링한다', () => {
    renderRoute('/classes')

    expect(screen.getByRole('heading', { name: 'Class 목록' })).toBeInTheDocument()
  })

  it('Lab placeholder route와 classId를 렌더링한다', () => {
    renderRoute('/classes/demo/lab')

    expect(screen.getByRole('heading', { name: 'Lab Workspace' })).toBeInTheDocument()
    expect(screen.getByText('Class: demo')).toBeInTheDocument()
  })

  it('정의되지 않은 경로는 Not Found 화면을 렌더링한다', () => {
    renderRoute('/not-found')

    expect(screen.getByRole('heading', { name: '페이지를 찾을 수 없습니다.' })).toBeInTheDocument()
  })
})
