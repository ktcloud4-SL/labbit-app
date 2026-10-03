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

  it('login 401 응답을 HttpError로 그대로 전달한다', async () => {
    const fetchMock = vi.fn().mockResolvedValue(
      new Response(
        JSON.stringify({
          type: 'about:blank',
          title: 'Unauthorized',
          status: 401,
          code: 'UNAUTHORIZED',
          requestId: 'req-login-401',
        }),
        {
          status: 401,
          headers: {
            'content-type': 'application/problem+json',
          },
        },
      ),
    )
    vi.stubGlobal('fetch', fetchMock)

    await expect(
      httpLabbitApi.login({
        username: 'unknown-user',
        password: 'wrong-password',
      }),
    ).rejects.toMatchObject({
      status: 401,
      problem: {
        code: 'UNAUTHORIZED',
        requestId: 'req-login-401',
      },
    })
  })

  it('logout 요청을 세션 Cookie 포함 POST로 전송한다', async () => {
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

  it('/me 요청을 세션 Cookie와 함께 전송하고 응답을 반환한다', async () => {
    const me = {
      id: 'user-heechul',
      username: 'heechul',
      organization: {
        id: 'org-samsunglions',
        name: 'SamsungLions Org',
      },
      organizationRole: 'MEMBER',
    }
    const fetchMock = vi.fn().mockResolvedValue(
      new Response(JSON.stringify(me), {
        status: 200,
        headers: {
          'content-type': 'application/json',
        },
      }),
    )
    vi.stubGlobal('fetch', fetchMock)

    await expect(httpLabbitApi.getMe()).resolves.toEqual(me)
    expect(fetchMock).toHaveBeenCalledWith(
      '/api/v1/me',
      expect.objectContaining({
        credentials: 'include',
      }),
    )
  })

  it('Class 목록 요청을 세션 Cookie와 함께 전송하고 목록을 반환한다', async () => {
    const classes = {
      items: [
        {
          id: 'class-kubernetes-basic',
          name: 'Kubernetes Basic',
          myRole: 'INSTRUCTOR',
        },
      ],
    }
    const fetchMock = vi.fn().mockResolvedValue(
      new Response(JSON.stringify(classes), {
        status: 200,
        headers: {
          'content-type': 'application/json',
        },
      }),
    )
    vi.stubGlobal('fetch', fetchMock)

    await expect(httpLabbitApi.listClasses()).resolves.toEqual(classes)
    expect(fetchMock).toHaveBeenCalledWith(
      '/api/v1/classes',
      expect.objectContaining({
        credentials: 'include',
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

  it('Workspace File tree는 root에서 query 없이 조회하고 nested path는 한 번만 encoding한다', async () => {
    const tree = {
      path: 'src',
      items: [
        {
          name: 'main.ts',
          path: 'src/main.ts',
          kind: 'file',
        },
      ],
    }
    const fetchMock = vi
      .fn()
      .mockImplementation(
        async () =>
          new Response(JSON.stringify(tree), {
            status: 200,
            headers: {
              'content-type': 'application/json',
            },
          }),
      )
    vi.stubGlobal('fetch', fetchMock)

    await httpLabbitApi.listWorkspaceFiles('lab-instance/demo')
    await expect(
      httpLabbitApi.listWorkspaceFiles('lab-instance/demo', 'src'),
    ).resolves.toEqual(tree)

    expect(fetchMock).toHaveBeenNthCalledWith(
      1,
      '/api/v1/lab-instances/lab-instance%2Fdemo/files/tree',
      expect.objectContaining({
        credentials: 'include',
      }),
    )
    expect(fetchMock).toHaveBeenNthCalledWith(
      2,
      '/api/v1/lab-instances/lab-instance%2Fdemo/files/tree?path=src',
      expect.objectContaining({
        credentials: 'include',
      }),
    )
  })

  it('Workspace File read는 path를 query로 보내고 ETag를 함께 보존한다', async () => {
    const fetchMock = vi.fn().mockResolvedValue(
      new Response(
        JSON.stringify({
          path: 'src/main.ts',
          content: "console.log('hello')\n",
        }),
        {
          status: 200,
          headers: {
            'content-type': 'application/json',
            etag: '"file-rev-7"',
          },
        },
      ),
    )
    vi.stubGlobal('fetch', fetchMock)

    await expect(
      httpLabbitApi.readWorkspaceFile('lab-instance/demo', 'src/main.ts'),
    ).resolves.toEqual({
      file: {
        path: 'src/main.ts',
        content: "console.log('hello')\n",
      },
      etag: '"file-rev-7"',
    })

    expect(fetchMock).toHaveBeenCalledWith(
      '/api/v1/lab-instances/lab-instance%2Fdemo/files/content?path=src%2Fmain.ts',
      expect.objectContaining({
        credentials: 'include',
      }),
    )
  })

  it('Workspace File save는 If-Match와 전체 content를 보내고 새 ETag를 보존한다', async () => {
    const fetchMock = vi.fn().mockResolvedValue(
      new Response(
        JSON.stringify({
          path: 'src/main.ts',
        }),
        {
          status: 200,
          headers: {
            'content-type': 'application/json',
            etag: '"file-rev-8"',
          },
        },
      ),
    )
    vi.stubGlobal('fetch', fetchMock)

    await expect(
      httpLabbitApi.saveWorkspaceFile(
        'lab-instance/demo',
        'src/main.ts',
        "console.log('updated')\n",
        '"file-rev-7"',
      ),
    ).resolves.toEqual({
      file: {
        path: 'src/main.ts',
      },
      etag: '"file-rev-8"',
    })

    const [url, init] = fetchMock.mock.calls[0] as [string, RequestInit]
    expect(url).toBe(
      '/api/v1/lab-instances/lab-instance%2Fdemo/files/content?path=src%2Fmain.ts',
    )
    expect(init.method).toBe('PUT')
    expect(init.credentials).toBe('include')
    expect(new Headers(init.headers).get('If-Match')).toBe('"file-rev-7"')
    expect(init.body).toBe(
      JSON.stringify({
        content: "console.log('updated')\n",
      }),
    )
  })

  it('Workspace File read/save 응답에 필수 ETag가 없으면 계약 오류로 거절한다', async () => {
    const fetchMock = vi
      .fn()
      .mockResolvedValueOnce(
        new Response(
          JSON.stringify({
            path: 'README.md',
            content: '# demo\n',
          }),
          {
            status: 200,
            headers: {
              'content-type': 'application/json',
            },
          },
        ),
      )
      .mockResolvedValueOnce(
        new Response(
          JSON.stringify({
            path: 'README.md',
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

    await expect(
      httpLabbitApi.readWorkspaceFile('lab-instance-1', 'README.md'),
    ).rejects.toThrow('required ETag')

    await expect(
      httpLabbitApi.saveWorkspaceFile(
        'lab-instance-1',
        'README.md',
        '# updated\n',
        '"file-rev-1"',
      ),
    ).rejects.toThrow('required ETag')
  })

  it('Terminal target 목록을 opaque LabInstance 경로에서 조회한다', async () => {
    const targetList = {
      generation: 3,
      workspaceVmKey: 'vk-web-opaque',
      items: [
        {
          vmKey: 'vk-web-opaque',
          role: 'web',
          instanceIndex: 0,
        },
        {
          vmKey: 'vk-worker-opaque',
          role: 'worker',
          instanceIndex: 0,
        },
      ],
    }
    const fetchMock = vi.fn().mockResolvedValue(
      new Response(JSON.stringify(targetList), {
        status: 200,
        headers: {
          'content-type': 'application/json',
        },
      }),
    )
    vi.stubGlobal('fetch', fetchMock)

    await expect(
      httpLabbitApi.listTerminalTargets('lab-instance/demo'),
    ).resolves.toEqual(targetList)

    expect(fetchMock).toHaveBeenCalledWith(
      '/api/v1/lab-instances/lab-instance%2Fdemo/terminal-targets',
      expect.objectContaining({
        credentials: 'include',
      }),
    )
  })

  it('TerminalSession 생성은 서버가 준 vmKey를 targetVmKey로 그대로 전달한다', async () => {
    const session = {
      id: 'terminal-session-1',
      generation: 3,
      sessionToken: 'opaque-attach-token',
      tokenExpiresAt: '2026-10-03T00:00:00Z',
    }
    const fetchMock = vi.fn().mockResolvedValue(
      new Response(JSON.stringify(session), {
        status: 201,
        headers: {
          'content-type': 'application/json',
        },
      }),
    )
    vi.stubGlobal('fetch', fetchMock)

    await expect(
      httpLabbitApi.createTerminalSession('lab-instance/demo', {
        targetVmKey: 'vk-worker-opaque',
        cols: 120,
        rows: 32,
      }),
    ).resolves.toEqual(session)

    expect(fetchMock).toHaveBeenCalledWith(
      '/api/v1/lab-instances/lab-instance%2Fdemo/terminal-sessions',
      expect.objectContaining({
        method: 'POST',
        credentials: 'include',
        body: JSON.stringify({
          targetVmKey: 'vk-worker-opaque',
          cols: 120,
          rows: 32,
        }),
      }),
    )
  })

  it('TerminalSession 명시 종료는 DELETE를 사용한다', async () => {
    const fetchMock = vi.fn().mockResolvedValue(new Response(null, { status: 204 }))
    vi.stubGlobal('fetch', fetchMock)

    await httpLabbitApi.closeTerminalSession('terminal/session')

    expect(fetchMock).toHaveBeenCalledWith(
      '/api/v1/terminal-sessions/terminal%2Fsession',
      expect.objectContaining({
        method: 'DELETE',
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

  it('Mock Workspace File은 Tree → Read → If-Match Save를 재현한다', async () => {
    await mockLabbitApi.login(mockCredentials)

    const root = await mockLabbitApi.listWorkspaceFiles(
      'lab-instance-heechul',
    )
    expect(root.items).toEqual([
      {
        name: 'README.md',
        path: 'README.md',
        kind: 'file',
      },
      {
        name: 'src',
        path: 'src',
        kind: 'directory',
      },
    ])

    const read = await mockLabbitApi.readWorkspaceFile(
      'lab-instance-heechul',
      'README.md',
    )

    const saved = await mockLabbitApi.saveWorkspaceFile(
      'lab-instance-heechul',
      'README.md',
      '# Updated\n',
      read.etag,
    )
    expect(saved.etag).not.toBe(read.etag)

    await expect(
      mockLabbitApi.saveWorkspaceFile(
        'lab-instance-heechul',
        'README.md',
        '# stale\n',
        read.etag,
      ),
    ).rejects.toMatchObject({
      status: 412,
    })

    await expect(
      mockLabbitApi.readWorkspaceFile(
        'lab-instance-heechul',
        'README.md',
      ),
    ).resolves.toMatchObject({
      file: {
        path: 'README.md',
        content: '# Updated\n',
      },
      etag: saved.etag,
    })
  })

  it('Mock Terminal target은 workspace hint와 multi-VM opaque key를 반환한다', async () => {
    await mockLabbitApi.login(mockCredentials)

    const targets = await mockLabbitApi.listTerminalTargets(
      'lab-instance-heechul',
    )

    expect(targets.generation).toBe(1)
    expect(targets.items).toHaveLength(3)
    expect(targets.items.some((item) => item.vmKey === targets.workspaceVmKey)).toBe(
      true,
    )
    expect(targets.items.map((item) => item.role)).toEqual([
      'control',
      'worker',
      'worker',
    ])
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
