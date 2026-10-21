import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { act, fireEvent, render, screen, waitFor } from '@testing-library/react'
import { createMemoryRouter, RouterProvider } from 'react-router-dom'
import { describe, expect, it, vi } from 'vitest'

import type { Operation } from '../shared/api/contracts'
import { HttpError } from '../shared/api/httpClient'
import { LabbitApiProvider } from '../shared/api/LabbitApiProvider'
import { labbitQueryKeys } from '../shared/api/labbitApi'
import { mockLabbitApi } from '../shared/api/mockLabbitApi'
import { OperationPage } from './OperationPage'

const OPERATION: Operation = {
  id: 'operation-1',
  type: 'PROVISION',
  status: 'PENDING',
  stage: 'PROVISIONING',
  target: { type: 'LAB_EXECUTION', id: 'execution-1' },
  createdAt: '2026-10-20T00:00:00Z',
  updatedAt: '2026-10-20T00:00:00Z',
}

function mount(getOperation: (id: string) => Promise<Operation>) {
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false, refetchOnWindowFocus: false } },
  })
  const get = vi.fn(getOperation)
  const router = createMemoryRouter(
    [
      { path: '/operations/:operationId', element: <OperationPage /> },
      { path: '/login', element: <p>로그인 화면</p> },
    ],
    { initialEntries: ['/operations/operation-1'] },
  )
  render(
    <QueryClientProvider client={client}>
      <LabbitApiProvider api={{ ...mockLabbitApi, getOperation: get }}>
        <RouterProvider router={router} />
      </LabbitApiProvider>
    </QueryClientProvider>,
  )
  return { client, router, get }
}

describe('LBT-53 Operation status read-only recovery', () => {
  it('PENDING/DEFERRED는 재시도 대기와 원인 미확정·중복 요청 금지를 안내한다', async () => {
    const { get } = mount(async () => ({ ...OPERATION, stage: 'DEFERRED' }))
    expect(await screen.findByRole('heading', { name: '요청 대기 중' })).toBeInTheDocument()
    expect(screen.getByText('재시도 대기 (DEFERRED)')).toBeInTheDocument()
    const notice = screen.getByRole('status')
    expect(notice).toHaveTextContent('정확한 원인은 알 수 없습니다')
    expect(notice).toHaveTextContent('같은 Provision·Reset·Cleanup 요청을 반복하지 마세요')
    expect(get).toHaveBeenCalledWith('operation-1')
  })

  it('일반 PENDING은 DEFERRED 안내를 보이지 않는다', async () => {
    mount(async () => OPERATION)
    expect(await screen.findByRole('heading', { name: '요청 대기 중' })).toBeInTheDocument()
    expect(screen.getByText('환경 생성 중')).toBeInTheDocument()
    expect(screen.queryByText(/재시도 대기 상태입니다/)).not.toBeInTheDocument()
  })

  it('DEFERRED의 마지막 성공 응답 뒤 조회 503이면 대기를 확정하지 않고 stale 안내만 보인다', async () => {
    let calls = 0
    const { client } = mount(async () => {
      calls += 1
      if (calls > 1) throw new HttpError(503)
      return { ...OPERATION, stage: 'DEFERRED' }
    })
    expect(await screen.findByRole('heading', { name: '요청 대기 중' })).toBeInTheDocument()
    expect(screen.getByRole('status')).toHaveTextContent('재시도 대기 상태입니다')
    await act(async () => {
      await client.invalidateQueries({ queryKey: labbitQueryKeys.operation('operation-1') })
    })
    expect(await screen.findByRole('alert')).toHaveTextContent('마지막으로 확인한 정보')
    expect(screen.queryByRole('status')).not.toBeInTheDocument()
  })

  it('최초 503에서는 작업 재실행 없이 GET 상태 재조회로 복구할 수 있다', async () => {
    let count = 0
    const { get } = mount(async () => {
      count += 1
      if (count === 1) throw new HttpError(503)
      return { ...OPERATION, status: 'SUCCEEDED' }
    })
    expect(await screen.findByRole('alert')).toHaveTextContent('작업 상태를 불러오지 못했습니다')
    fireEvent.click(screen.getByRole('button', { name: '상태 다시 확인' }))
    expect(await screen.findByRole('heading', { name: '완료' })).toBeInTheDocument()
    expect(get).toHaveBeenCalledTimes(2)
    expect(get).toHaveBeenNthCalledWith(2, 'operation-1')
  })

  it('200 PENDING 이후 503은 마지막 검증 상태를 유지하며 200 복구 시 최신 결과를 표시한다', async () => {
    let count = 0
    const { client, get } = mount(async () => {
      count += 1
      if (count === 2) throw new HttpError(503)
      return count === 1 ? OPERATION : { ...OPERATION, status: 'SUCCEEDED' }
    })
    expect(await screen.findByRole('heading', { name: '요청 대기 중' })).toBeInTheDocument()

    await act(async () => {
      await client.invalidateQueries({ queryKey: labbitQueryKeys.operation('operation-1') })
    })
    expect(await screen.findByRole('alert')).toHaveTextContent('마지막으로 확인한 정보')
    expect(screen.getByRole('heading', { name: '요청 대기 중' })).toBeInTheDocument()
    expect(screen.getByText('실습 환경 생성')).toBeInTheDocument()

    fireEvent.click(screen.getByRole('button', { name: '상태 다시 확인' }))
    await waitFor(() => expect(screen.getByRole('heading', { name: '완료' })).toBeInTheDocument())
    expect(screen.queryByText('마지막으로 확인한 정보')).not.toBeInTheDocument()
    expect(get).toHaveBeenCalledTimes(3)
  })

  it('마지막 정상 응답을 갖고 있어도 401이 오면 이전 Operation을 숨기고 로그인으로 이동한다', async () => {
    let count = 0
    const { client, router } = mount(async () => {
      count += 1
      if (count > 1) throw new HttpError(401)
      return OPERATION
    })
    expect(await screen.findByRole('heading', { name: '요청 대기 중' })).toBeInTheDocument()
    await act(async () => {
      await client.invalidateQueries({ queryKey: labbitQueryKeys.operation('operation-1') })
    })
    await waitFor(() => expect(router.state.location.pathname).toBe('/login'))
    expect(screen.queryByRole('heading', { name: '요청 대기 중' })).not.toBeInTheDocument()
  })

  it.each([403, 404])('마지막 정상 응답 후 %s 권한/미존재 오류는 이전 작업 정보를 노출하지 않는다', async (status) => {
    let count = 0
    const { client } = mount(async () => {
      count += 1
      if (count > 1) throw new HttpError(status)
      return OPERATION
    })
    expect(await screen.findByRole('heading', { name: '요청 대기 중' })).toBeInTheDocument()
    await act(async () => {
      await client.invalidateQueries({ queryKey: labbitQueryKeys.operation('operation-1') })
    })
    expect(await screen.findByRole('alert')).toHaveTextContent(
      status === 403 ? '이 작업을 볼 권한이 없습니다' : '작업을 찾을 수 없습니다',
    )
    expect(screen.queryByRole('heading', { name: '요청 대기 중' })).not.toBeInTheDocument()
  })
})
