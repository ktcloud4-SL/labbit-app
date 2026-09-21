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

  it('opaque classId를 URL encoding해 Class 상세를 요청한다', async () => {
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
          name: labSpecWrite.name,
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
          name: labSpecWrite.name,
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
