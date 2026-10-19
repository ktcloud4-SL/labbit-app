import { afterEach, describe, expect, it, vi } from 'vitest'

import { httpLabbitApi } from './labbitApi'

const json = (data: unknown, status = 200) => new Response(JSON.stringify(data), {
  status,
  headers: { 'content-type': 'application/json' },
})

afterEach(() => vi.unstubAllGlobals())

describe('LBT-182 / LBT-183 Connector ADMIN HTTP consumer contract', () => {
  it('조직 목록은 인증 Cookie를 포함한 GET /connectors이며 빈 응답을 그대로 유지한다', async () => {
    const fetch = vi.fn().mockResolvedValue(json({ items: [], observedAt: '2026-10-19T00:00:00Z' }))
    vi.stubGlobal('fetch', fetch)

    await expect(httpLabbitApi.listConnectors()).resolves.toMatchObject({ items: [] })
    const [url, init] = fetch.mock.calls[0] as [string, RequestInit]
    expect(url).toBe('/api/v1/connectors')
    expect(init.credentials).toBe('include')
    expect(init.method).toBeUndefined()
  })

  it('Token은 세션 Cookie를 사용한 POST JSON body로만 전송하고 URL에 넣지 않는다', async () => {
    const result = { tokenId: 'issued-id', enrollmentToken: 'fixture-token-not-real', expiresAt: '2026-10-19T01:00:00Z' }
    const fetch = vi.fn().mockResolvedValue(json(result, 201))
    vi.stubGlobal('fetch', fetch)

    await expect(httpLabbitApi.createConnectorEnrollmentToken('customer-VM')).resolves.toEqual(result)
    const [url, init] = fetch.mock.calls[0] as [string, RequestInit]
    expect(url).toBe('/api/v1/connectors/enrollment-tokens')
    expect(init.method).toBe('POST')
    expect(init.credentials).toBe('include')
    expect(JSON.parse(init.body as string)).toEqual({ name: 'customer-VM' })
    expect(url).not.toContain(result.enrollmentToken)
  })

  it('취소와 폐기는 원문 Token 없이 opaque ID만 encode해 전송한다', async () => {
    const fetch = vi.fn().mockResolvedValue(new Response(null, { status: 204 }))
    vi.stubGlobal('fetch', fetch)

    await httpLabbitApi.cancelConnectorEnrollmentToken('token/one')
    await httpLabbitApi.revokeConnector('connector/a')

    expect(fetch.mock.calls.map(([url, init]) => [url, init.method])).toEqual([
      ['/api/v1/connectors/enrollment-tokens/token%2Fone', 'DELETE'],
      ['/api/v1/connectors/connector%2Fa/revoke', 'POST'],
    ])
    expect(fetch.mock.calls.every(([, init]) => init.credentials === 'include')).toBe(true)
  })

  it('MEMBER/타 조직 거절 403을 조작하지 않고 전달한다', async () => {
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue(
      new Response(JSON.stringify({
        type: 'about:blank', title: 'Forbidden', status: 403,
        code: 'forbidden', requestId: 'req-fixture',
      }), { status: 403, headers: { 'content-type': 'application/problem+json' } }),
    ))
    await expect(httpLabbitApi.listConnectors()).rejects.toMatchObject({ status: 403 })
  })
})
