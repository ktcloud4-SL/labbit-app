import { afterEach, describe, expect, it, vi } from 'vitest'

import type { LabSpecWrite } from './contracts'
import { httpLabbitApi } from './labbitApi'
import {
  mockCredentials,
  mockLabbitApi,
  mockMe,
  resetMockApiSession,
} from './mockLabbitApi'

const labSpecWrite: LabSpecWrite = {
  name: 'Kubernetes Basic',
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

describe('httpLabbitApi', () => {
  afterEach(() => {
    vi.unstubAllGlobals()
  })

  it('login 요청을 OpenAPI 경로와 JSON body로 전송한다', async () => {
    const fetchMock = vi.fn().mockResolvedValue(new Response(null, { status: 204 }))
    vi.stubGlobal('fetch', fetchMock)

    await httpLabbitApi.login({
      username: 'heechul',
      password: 'password',
    })

    expect(fetchMock).toHaveBeenCalledWith(
      '/api/v1/auth/login',
      expect.objectContaining({
        method: 'POST',
        credentials: 'include',
        body: JSON.stringify({
          username: 'heechul',
          password: 'password',
        }),
      }),
    )
  })

  it('logout 요청을 Session Cookie 포함 POST로 전송한다', async () => {
    const fetchMock = vi.fn().mockResolvedValue(new Response(null, { status: 204 }))
    vi.stubGlobal('fetch', fetchMock)

    await httpLabbitApi.logout()

    expect(fetchMock).toHaveBeenCalledWith(
      '/api/v1/auth/logout',
      expect.objectContaining({
        method: 'POST',
        credentials: 'include',
      }),
    )
  })

  it('/me 요청을 Session Cookie 포함 GET으로 전송한다', async () => {
    const fetchMock = vi.fn().mockResolvedValue(
      new Response(JSON.stringify({ id: 'user-1' }), {
        status: 200,
        headers: { 'content-type': 'application/json' },
      }),
    )
    vi.stubGlobal('fetch', fetchMock)

    await httpLabbitApi.getMe()

    expect(fetchMock).toHaveBeenCalledWith(
      '/api/v1/me',
      expect.objectContaining({
        credentials: 'include',
      }),
    )
  })

  it('Class 목록 요청을 Session Cookie 포함 GET으로 전송한다', async () => {
    const fetchMock = vi.fn().mockResolvedValue(
      new Response(JSON.stringify({ items: [] }), {
        status: 200,
        headers: { 'content-type': 'application/json' },
      }),
    )
    vi.stubGlobal('fetch', fetchMock)

    await httpLabbitApi.listClasses()

    expect(fetchMock).toHaveBeenCalledWith(
      '/api/v1/classes',
      expect.objectContaining({
        credentials: 'include',
      }),
    )
  })

  it('Class 상세 요청은 opaque classId를 encoding하고 Session Cookie를 포함한다', async () => {
    const fetchMock = vi.fn().mockResolvedValue(
      new Response(
        JSON.stringify({
          id: 'class/demo',
          name: 'Demo',
          myRole: 'STUDENT',
        }),
        {
          status: 200,
          headers: {
            'content-type': 'application/json',
          },
        },
      ),
    )
    vi.stubGlobal('fetch', fetchMock)

    await httpLabbitApi.getClass('class/demo')

    expect(fetchMock).toHaveBeenCalledWith(
      '/api/v1/classes/class%2Fdemo',
      expect.objectContaining({
        credentials: 'include',
      }),
    )
  })

  it('LabSpec 상세의 ETag를 consumer metadata로 보존한다', async () => {
    const fetchMock = vi.fn().mockResolvedValue(
      new Response(
        JSON.stringify({
          id: 'lab-spec-1',
          ownerUserId: 'user-heechul',
          ...labSpecWrite,
        }),
        {
          status: 200,
          headers: {
            'content-type': 'application/json',
            etag: '"lab-spec-v2"',
          },
        },
      ),
    )
    vi.stubGlobal('fetch', fetchMock)

    const result = await httpLabbitApi.getLabSpec('lab/spec')

    expect(result.etag).toBe('"lab-spec-v2"')
    expect(fetchMock).toHaveBeenCalledWith(
      '/api/v1/lab-specs/lab%2Fspec',
      expect.objectContaining({
        credentials: 'include',
      }),
    )
  })

  it('LabSpec 수정 시 최신 ETag를 If-Match로 전달한다', async () => {
    const fetchMock = vi.fn().mockResolvedValue(
      new Response(
        JSON.stringify({
          id: 'lab-spec-1',
          ownerUserId: 'user-heechul',
          ...labSpecWrite,
        }),
        {
          status: 200,
          headers: {
            'content-type': 'application/json',
            etag: '"lab-spec-v3"',
          },
        },
      ),
    )
    vi.stubGlobal('fetch', fetchMock)

    await httpLabbitApi.updateLabSpec('lab-spec-1', labSpecWrite, '"lab-spec-v2"')

    const init = fetchMock.mock.calls[0][1] as RequestInit
    expect(init.method).toBe('PUT')
    expect(new Headers(init.headers).get('If-Match')).toBe('"lab-spec-v2"')
    expect(init.body).toBe(JSON.stringify(labSpecWrite))
  })

  it('Reset 요청에 Idempotency-Key를 전달한다', async () => {
    const fetchMock = vi.fn().mockResolvedValue(
      new Response(
        JSON.stringify({
          operationId: 'operation-reset-1',
          target: {
            type: 'LAB_INSTANCE',
            id: 'lab-instance/student',
          },
        }),
        {
          status: 202,
          headers: {
            'content-type': 'application/json',
          },
        },
      ),
    )
    vi.stubGlobal('fetch', fetchMock)

    await httpLabbitApi.resetLabInstance('lab-instance/student', 'idem-reset-1')

    expect(fetchMock).toHaveBeenCalledWith(
      '/api/v1/lab-instances/lab-instance%2Fstudent/reset',
      expect.objectContaining({
        method: 'POST',
        credentials: 'include',
      }),
    )
    const init = fetchMock.mock.calls[0][1] as RequestInit
    expect(new Headers(init.headers).get('Idempotency-Key')).toBe('idem-reset-1')
  })

  it('Cleanup 요청에 Idempotency-Key를 전달한다', async () => {
    const fetchMock = vi.fn().mockResolvedValue(
      new Response(
        JSON.stringify({
          operationId: 'operation-cleanup-1',
          target: {
            type: 'LAB_EXECUTION',
            id: 'execution/demo',
          },
        }),
        {
          status: 202,
          headers: {
            'content-type': 'application/json',
          },
        },
      ),
    )
    vi.stubGlobal('fetch', fetchMock)

    await httpLabbitApi.cleanupLabExecution('execution/demo', 'idem-cleanup-1')

    expect(fetchMock).toHaveBeenCalledWith(
      '/api/v1/lab-executions/execution%2Fdemo/cleanup',
      expect.objectContaining({
        method: 'POST',
        credentials: 'include',
      }),
    )
    const init = fetchMock.mock.calls[0][1] as RequestInit
    expect(new Headers(init.headers).get('Idempotency-Key')).toBe('idem-cleanup-1')
  })

  it('Provision 요청에 Idempotency-Key와 선택 학생을 전달한다', async () => {
    const fetchMock = vi.fn().mockResolvedValue(
      new Response(
        JSON.stringify({
          operationId: 'operation-1',
          target: {
            type: 'LAB_EXECUTION',
            id: 'execution-1',
          },
        }),
        {
          status: 202,
          headers: {
            'content-type': 'application/json',
          },
        },
      ),
    )
    vi.stubGlobal('fetch', fetchMock)

    await httpLabbitApi.createLabExecution(
      'class/demo',
      {
        labSpecId: 'lab-spec-1',
        targetStudentIds: ['user-a', 'user-b'],
      },
      'idem-test-1',
    )

    expect(fetchMock).toHaveBeenCalledWith(
      '/api/v1/classes/class%2Fdemo/lab-executions',
      expect.objectContaining({
        method: 'POST',
        credentials: 'include',
        body: JSON.stringify({
          labSpecId: 'lab-spec-1',
          targetStudentIds: ['user-a', 'user-b'],
        }),
      }),
    )

    const init = fetchMock.mock.calls[0][1] as RequestInit
    expect(new Headers(init.headers).get('Idempotency-Key')).toBe('idem-test-1')
  })
})

describe('mockLabbitApi', () => {
  afterEach(() => {
    resetMockApiSession()
  })

  it('로그인 전 /me 요청은 401로 거절한다', async () => {
    await expect(mockLabbitApi.getMe()).rejects.toMatchObject({
      status: 401,
    })
  })

  it('잘못된 Mock 로그인 정보는 401로 거절한다', async () => {
    await expect(
      mockLabbitApi.login({
        username: mockCredentials.username,
        password: 'wrong-password',
      }),
    ).rejects.toMatchObject({
      status: 401,
    })
  })

  it('Mock 계정 로그인 후 /me를 반환한다', async () => {
    await mockLabbitApi.login(mockCredentials)

    await expect(mockLabbitApi.getMe()).resolves.toEqual(mockMe)
  })

  it('존재하지 않는 Class는 404로 처리한다', async () => {
    await mockLabbitApi.login(mockCredentials)

    await expect(mockLabbitApi.getClass('missing-class')).rejects.toMatchObject({
      status: 404,
    })
  })

  it('Mock Reset 완료 후 대상 LabInstance generation이 증가한다', async () => {
    await mockLabbitApi.login(mockCredentials)

    const accepted = await mockLabbitApi.resetLabInstance(
      'lab-instance-student-a',
      'idem-reset-mock',
    )

    await mockLabbitApi.getOperation(accepted.operationId)
    await mockLabbitApi.getOperation(accepted.operationId)

    const execution = await mockLabbitApi.getLabExecution(
      'execution-kubernetes-basic',
    )
    const target = execution.labInstances.find(
      (instance) => instance.id === 'lab-instance-student-a',
    )

    expect(target).toMatchObject({
      status: 'READY',
      generation: 2,
    })
  })

  it('Mock Cleanup 완료 후 Class의 활성 Execution을 해제한다', async () => {
    await mockLabbitApi.login(mockCredentials)

    const accepted = await mockLabbitApi.cleanupLabExecution(
      'execution-kubernetes-basic',
      'idem-cleanup-mock',
    )

    await mockLabbitApi.getOperation(accepted.operationId)
    await mockLabbitApi.getOperation(accepted.operationId)

    const detail = await mockLabbitApi.getClass('class-kubernetes-basic')
    expect(detail.activeLabExecution).toBeUndefined()
    expect(detail.myLabInstance).toBeUndefined()
  })

  it('Mock Cleanup 후 Class 목록과 상세의 active execution 상태가 함께 갱신된다', async () => {
    await mockLabbitApi.login(mockCredentials)

    const accepted = await mockLabbitApi.cleanupLabExecution(
      'execution-kubernetes-basic',
      'idem-cleanup-list-sync',
    )

    await mockLabbitApi.getOperation(accepted.operationId)
    await mockLabbitApi.getOperation(accepted.operationId)

    const list = await mockLabbitApi.listClasses()
    const summary = list.items.find(
      (classItem) => classItem.id === 'class-kubernetes-basic',
    )
    const detail = await mockLabbitApi.getClass('class-kubernetes-basic')

    expect(summary?.activeLabExecution).toBeUndefined()
    expect(detail.activeLabExecution).toBeUndefined()
  })

  it('Mock session reset은 변경된 Class 상태를 초기 fixture로 복원한다', async () => {
    await mockLabbitApi.login(mockCredentials)

    const accepted = await mockLabbitApi.cleanupLabExecution(
      'execution-kubernetes-basic',
      'idem-cleanup-reset-state',
    )
    await mockLabbitApi.getOperation(accepted.operationId)
    await mockLabbitApi.getOperation(accepted.operationId)

    resetMockApiSession()
    await mockLabbitApi.login(mockCredentials)

    const detail = await mockLabbitApi.getClass('class-kubernetes-basic')
    expect(detail.activeLabExecution).toMatchObject({
      id: 'execution-kubernetes-basic',
      status: 'ACTIVE',
    })
    expect(detail.myLabInstance).toMatchObject({
      id: 'lab-instance-heechul',
      status: 'READY',
      generation: 1,
    })
  })

  it('Mock LabSpec 수정은 stale ETag를 412로 거절한다', async () => {
    await mockLabbitApi.login(mockCredentials)

    await expect(
      mockLabbitApi.updateLabSpec(
        'lab-spec-kubernetes-basic',
        labSpecWrite,
        '"stale"',
      ),
    ).rejects.toMatchObject({
      status: 412,
    })
  })
})
