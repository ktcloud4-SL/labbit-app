import { afterEach, describe, expect, it, vi } from 'vitest'

import { httpLabbitApi } from './labbitApi'

describe('httpLabbitApi Class consumer', () => {
  afterEach(() => {
    vi.unstubAllGlobals()
  })

  it('Class 상세 403 응답을 HttpError로 전달한다', async () => {
    vi.stubGlobal(
      'fetch',
      vi.fn().mockResolvedValue(
        new Response(
          JSON.stringify({
            type: 'about:blank',
            title: 'Forbidden',
            status: 403,
            code: 'FORBIDDEN',
            requestId: 'req-class-403',
          }),
          {
            status: 403,
            headers: {
              'content-type': 'application/problem+json',
            },
          },
        ),
      ),
    )

    await expect(httpLabbitApi.getClass('class-alpha')).rejects.toMatchObject({
      status: 403,
      problem: {
        code: 'FORBIDDEN',
        requestId: 'req-class-403',
      },
    })
  })

  it('Class 상세 404 응답을 HttpError로 전달한다', async () => {
    vi.stubGlobal(
      'fetch',
      vi.fn().mockResolvedValue(
        new Response(
          JSON.stringify({
            type: 'about:blank',
            title: 'Not Found',
            status: 404,
            code: 'NOT_FOUND',
            requestId: 'req-class-404',
          }),
          {
            status: 404,
            headers: {
              'content-type': 'application/problem+json',
            },
          },
        ),
      ),
    )

    await expect(httpLabbitApi.getClass('missing-class')).rejects.toMatchObject({
      status: 404,
      problem: {
        code: 'NOT_FOUND',
        requestId: 'req-class-404',
      },
    })
  })
})
