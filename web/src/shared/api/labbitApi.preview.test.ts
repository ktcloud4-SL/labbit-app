import { afterEach, describe, expect, it, vi } from 'vitest'

import { httpLabbitApi } from './labbitApi'

afterEach(() => vi.unstubAllGlobals())

describe('LBT-26 PreviewSession HTTP contract', () => {
  it('create uses POST JSON targetPort only, Cookie, encoded lab ID and no URL credential in request', async () => {
    const created = {
      id: 'preview-session-fixture',
      targetPort: 3000,
      previewUrl: 'https://preview-fixture.example.test/__labbit/bootstrap#fixture-not-secret',
      expiresAt: '2026-12-01T00:00:00Z',
    }
    const fetch = vi.fn().mockResolvedValue(new Response(JSON.stringify(created), {
      status: 201,
      headers: { 'content-type': 'application/json', 'cache-control': 'no-store' },
    }))
    vi.stubGlobal('fetch', fetch)

    await expect(httpLabbitApi.createPreviewSession('lab/demo', { targetPort: 3000 }))
      .resolves.toEqual(created)

    const [url, init] = fetch.mock.calls[0] as [string, RequestInit]
    expect(url).toBe('/api/v1/lab-instances/lab%2Fdemo/preview-sessions')
    expect(init.method).toBe('POST')
    expect(init.credentials).toBe('include')
    expect(JSON.parse(init.body as string)).toEqual({ targetPort: 3000 })
    expect(new Headers(init.headers).get('Content-Type')).toBe('application/json')
    expect(url).not.toContain('#')
  })

  it('close uses DELETE opaque ID only and does not transmit bootstrap URL', async () => {
    const fetch = vi.fn().mockResolvedValue(new Response(null, { status: 204 }))
    vi.stubGlobal('fetch', fetch)

    await httpLabbitApi.closePreviewSession('preview/session')
    const [url, init] = fetch.mock.calls[0] as [string, RequestInit]
    expect(url).toBe('/api/v1/preview-sessions/preview%2Fsession')
    expect(init.method).toBe('DELETE')
    expect(init.credentials).toBe('include')
    expect(init.body).toBeUndefined()
  })

  it('HTTP 403/problem code is surfaced instead of silently accepting Preview creation', async () => {
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue(new Response(JSON.stringify({
      type: 'about:blank',
      title: 'Forbidden',
      status: 403,
      code: 'preview_port_not_allowed',
      requestId: 'fixture-req',
    }), { status: 403, headers: { 'content-type': 'application/problem+json' } })))

    await expect(httpLabbitApi.createPreviewSession('lab', { targetPort: 22 })).rejects.toMatchObject({
      status: 403,
      problem: { code: 'preview_port_not_allowed' },
    })
  })
})
