import { afterEach, describe, expect, it, vi } from 'vitest'

import { ApiResponseFormatError, HttpError, request, requestWithMetadata } from './httpClient'

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

  it('응답 metadata가 필요한 consumer에 status와 header를 함께 반환한다', async () => {
    vi.stubGlobal(
      'fetch',
      vi.fn().mockResolvedValue(
        new Response(JSON.stringify({ id: 'lab-spec-1' }), {
          status: 200,
          headers: {
            'content-type': 'application/json',
            etag: '"v3"',
          },
        }),
      ),
    )

    const response = await requestWithMetadata<{ id: string }>('/lab-specs/lab-spec-1')

    expect(response.data).toEqual({ id: 'lab-spec-1' })
    expect(response.status).toBe(200)
    expect(response.headers.get('etag')).toBe('"v3"')
  })

  it('200 text/html이 JSON 문자열을 담더라도 API 응답으로 수용하지 않는다', async () => {
    vi.stubGlobal(
      'fetch',
      vi.fn().mockResolvedValue(
        new Response(JSON.stringify({ items: [{ id: 'fake-class' }] }), {
          status: 200,
          headers: { 'content-type': 'text/html; charset=utf-8' },
        }),
      ),
    )

    await expect(request<{ items: { id: string }[] }>('/classes')).rejects.toMatchObject({
      name: 'ApiResponseFormatError',
      status: 200,
      contentType: 'text/html; charset=utf-8',
    })
  })

  it('200 HTML SPA fallback을 JSON SyntaxError가 아닌 응답 형식 오류로 구분한다', async () => {
    vi.stubGlobal(
      'fetch',
      vi.fn().mockResolvedValue(
        new Response('<!doctype html><html><body>SPA</body></html>', {
          status: 200,
          headers: { 'content-type': 'text/html' },
        }),
      ),
    )

    await expect(request('/me')).rejects.toBeInstanceOf(ApiResponseFormatError)
  })

  it('200 JSON 응답의 Content-Type 누락도 fail closed로 처리한다', async () => {
    vi.stubGlobal(
      'fetch',
      vi.fn().mockResolvedValue(new Response(null, { status: 200 })),
    )

    await expect(request('/classes')).rejects.toMatchObject({
      name: 'ApiResponseFormatError',
      status: 200,
      contentType: null,
    })
  })

  it('application/*+json 및 charset 지정 응답은 기존처럼 파싱한다', async () => {
    vi.stubGlobal(
      'fetch',
      vi.fn().mockResolvedValue(
        new Response(JSON.stringify({ items: [] }), {
          status: 200,
          headers: { 'content-type': 'application/vnd.labbit+json; charset=UTF-8' },
        }),
      ),
    )

    await expect(request<{ items: unknown[] }>('/classes')).resolves.toEqual({ items: [] })
  })
})
