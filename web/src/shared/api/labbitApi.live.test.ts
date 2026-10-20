import { afterEach, describe, expect, it, vi } from 'vitest'

import { httpLabbitApi } from './labbitApi'
import { mockCredentials, mockLabbitApi, resetMockApiSession } from './mockLabbitApi'

const live = {
  id: 'live-session-fixture',
  classId: 'class-a',
  sourceTerminalSessionId: 'terminal-session-a',
  createdAt: '2026-10-20T00:00:00Z',
}

const json = (data: unknown, status = 200) =>
  new Response(JSON.stringify(data), {
    status,
    headers: { 'content-type': 'application/json' },
  })

afterEach(() => {
  vi.restoreAllMocks()
  vi.unstubAllGlobals()
  resetMockApiSession()
})

describe('LBT-26 LiveSession OpenAPI HTTP consumer', () => {
  it('GET active LiveSession is scoped to classId, uses session Cookie and returns JSON', async () => {
    const fetch = vi.fn().mockResolvedValue(json(live))
    vi.stubGlobal('fetch', fetch)
    await expect(httpLabbitApi.getActiveLiveSession('class/a')).resolves.toEqual(live)
    const [url, opts] = fetch.mock.calls[0] as [string, RequestInit]
    expect(url).toBe('/api/v1/classes/class%2Fa/live-session')
    expect(opts.credentials).toBe('include')
    expect(opts.method).toBeUndefined()
  })

  it('GET 404 means no current active LiveSession, not empty success', async () => {
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue(new Response(null, { status: 404 })))
    await expect(httpLabbitApi.getActiveLiveSession('class-a'))
      .rejects.toMatchObject({ status: 404 })
  })

  it('POST create sends no token/body, DELETE closes only opaque live ID', async () => {
    const fetch = vi.fn()
      .mockResolvedValueOnce(json(live, 201))
      .mockResolvedValueOnce(new Response(null, { status: 204 }))
    vi.stubGlobal('fetch', fetch)

    await expect(httpLabbitApi.createLiveSession('terminal/a')).resolves.toEqual(live)
    await httpLabbitApi.closeLiveSession('live/a')
    expect((fetch.mock.calls as Array<[string, RequestInit]>).map(([url, opts]) => ({
      url,
      method: opts.method,
      credentials: opts.credentials,
      body: opts.body,
    }))).toEqual([
      {
        url: '/api/v1/terminal-sessions/terminal%2Fa/live-sessions',
        method: 'POST',
        credentials: 'include',
        body: undefined,
      },
      {
        url: '/api/v1/live-sessions/live%2Fa',
        method: 'DELETE',
        credentials: 'include',
        body: undefined,
      },
    ])
  })

  it('409 existing Live or unavailable source is not mapped into success', async () => {
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue(json({
      type: 'about:blank', title: 'Conflict', status: 409,
      code: 'live_session_active', requestId: 'fixture-request',
    }, 409)))
    await expect(httpLabbitApi.createLiveSession('terminal-a')).rejects.toMatchObject({
      status: 409,
      problem: { code: 'live_session_active' },
    })
  })
})

describe('LBT-26 LiveSession isolated mock lifecycle', () => {
  async function createInstructorTerminal() {
    await mockLabbitApi.login(mockCredentials)
    const targets = await mockLabbitApi.listTerminalTargets('lab-instance-heechul')
    return mockLabbitApi.createTerminalSession('lab-instance-heechul', {
      targetVmKey: targets.workspaceVmKey,
      cols: 80,
      rows: 24,
    })
  }

  it('requires login for active Live lookup and does not invent empty success', async () => {
    await expect(mockLabbitApi.getActiveLiveSession('class-kubernetes-basic'))
      .rejects.toMatchObject({ status: 401 })
    await mockLabbitApi.login(mockCredentials)
    await expect(mockLabbitApi.getActiveLiveSession('class-kubernetes-basic'))
      .rejects.toMatchObject({ status: 404 })
  })

  it('creates one Live per class, exposes via GET, rejects duplicate and allows recreation after idempotent close', async () => {
    const source = await createInstructorTerminal()
    const created = await mockLabbitApi.createLiveSession(source.id)
    expect(created).toMatchObject({
      classId: 'class-kubernetes-basic',
      sourceTerminalSessionId: source.id,
    })
    await expect(mockLabbitApi.getActiveLiveSession('class-kubernetes-basic'))
      .resolves.toEqual(created)
    await expect(mockLabbitApi.createLiveSession(source.id))
      .rejects.toMatchObject({ status: 409 })
    await mockLabbitApi.closeLiveSession(created.id)
    await mockLabbitApi.closeLiveSession(created.id)
    await expect(mockLabbitApi.getActiveLiveSession('class-kubernetes-basic'))
      .rejects.toMatchObject({ status: 404 })
    const renewed = await mockLabbitApi.createLiveSession(source.id)
    expect(renewed.id).not.toBe(created.id)
  })

  it('requires current instructor membership to close a Live, including idempotent close', async () => {
    const source = await createInstructorTerminal()
    const created = await mockLabbitApi.createLiveSession(source.id)
    const original = await mockLabbitApi.getClass(created.classId)
    const getClass = vi.spyOn(mockLabbitApi, 'getClass')
      .mockResolvedValue({ ...original, myRole: 'STUDENT' })
    await expect(mockLabbitApi.closeLiveSession(created.id))
      .rejects.toMatchObject({ status: 403 })
    await expect(mockLabbitApi.getActiveLiveSession(created.classId))
      .resolves.toEqual(created)

    getClass.mockRestore()
    await expect(mockLabbitApi.closeLiveSession(created.id)).resolves.toBeUndefined()
    const deniedAgain = vi.spyOn(mockLabbitApi, 'getClass')
      .mockResolvedValue({ ...original, myRole: 'STUDENT' })
    await expect(mockLabbitApi.closeLiveSession(created.id))
      .rejects.toMatchObject({ status: 403 })
    deniedAgain.mockRestore()
    await expect(mockLabbitApi.closeLiveSession(created.id)).resolves.toBeUndefined()
  })

  it('closing the source terminal ends Live but does not invent a second source', async () => {
    const source = await createInstructorTerminal()
    const created = await mockLabbitApi.createLiveSession(source.id)
    await mockLabbitApi.closeTerminalSession(source.id)
    await expect(mockLabbitApi.getActiveLiveSession('class-kubernetes-basic'))
      .rejects.toMatchObject({ status: 404 })
    await expect(mockLabbitApi.createLiveSession(source.id))
      .rejects.toMatchObject({ status: 409 })
    await expect(mockLabbitApi.closeLiveSession(created.id)).resolves.toBeUndefined()
  })

  it('rejects unknown source and unknown LiveSession id without simulated connector/VM mutation', async () => {
    await mockLabbitApi.login(mockCredentials)
    await expect(mockLabbitApi.createLiveSession('unknown')).rejects.toMatchObject({ status: 409 })
    await expect(mockLabbitApi.closeLiveSession('unknown')).rejects.toMatchObject({ status: 404 })
  })
})
