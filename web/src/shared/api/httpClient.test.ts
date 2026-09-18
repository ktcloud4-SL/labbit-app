import { afterEach, describe, expect, it, vi } from 'vitest'

import { HttpError, request } from './httpClient'

describe('httpClient', () => {
  afterEach(() => {
    vi.unstubAllGlobals()
  })

  it('application/problem+json 오류를 HttpError.problem으로 보존한다', async () => {
    vi.stubGlobal(
      'fetch',
      vi.fn().mockResolvedValue(
        new Response(
          JSON.stringify({
            type: 'https://labbit.example/problems/forbidden',
            title: 'Forbidden',
            status: 403,
            detail: '권한이 없습니다.',
            code: 'FORBIDDEN',
            requestId: 'req-test-1',
          }),
          {
            status: 403,
            headers: {
              'content-type': 'application/problem+json; charset=utf-8',
            },
          },
        ),
      ),
    )

    const error = await request('/classes').catch((caught: unknown) => caught)

    expect(error).toBeInstanceOf(HttpError)
    expect(error).toMatchObject({
      status: 403,
      problem: {
        type: 'https://labbit.example/problems/forbidden',
        title: 'Forbidden',
        status: 403,
        code: 'FORBIDDEN',
        requestId: 'req-test-1',
      },
    })
  })

  it('204 응답은 body parsing 없이 undefined를 반환한다', async () => {
    vi.stubGlobal(
      'fetch',
      vi.fn().mockResolvedValue(
        new Response(null, {
          status: 204,
        }),
      ),
    )

    await expect(request<void>('/auth/logout', { method: 'POST' })).resolves.toBeUndefined()
  })
})
