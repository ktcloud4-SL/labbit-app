import { afterEach, describe, expect, it, vi } from 'vitest'

import { HttpError } from './httpClient'
import { httpLabbitApi } from './labbitApi'
import {
  mockCredentials,
  mockLabbitApi,
  mockMe,
  resetMockApiSession,
} from './mockLabbitApi'

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
})

describe('mockLabbitApi', () => {
  afterEach(() => {
    resetMockApiSession()
  })

  it('로그인 전 /me 요청은 401로 거절한다', async () => {
    await expect(mockLabbitApi.getMe()).rejects.toMatchObject<HttpError>({
      status: 401,
    })
  })

  it('Mock 계정 로그인 후 /me를 반환한다', async () => {
    await mockLabbitApi.login(mockCredentials)

    await expect(mockLabbitApi.getMe()).resolves.toEqual(mockMe)
  })

  it('존재하지 않는 Class는 404로 처리한다', async () => {
    await mockLabbitApi.login(mockCredentials)

    await expect(mockLabbitApi.getClass('missing-class')).rejects.toMatchObject<HttpError>({
      status: 404,
    })
  })
})
