import { describe, expect, it } from 'vitest'

import {
  decideTerminalClose,
  decideTerminalProtocolError,
} from './terminalReconnectPolicy'

describe('terminalReconnectPolicy', () => {
  it.each([
    [1006, 'reconnect', false],
    [1011, 'reconnect', false],
    [4005, 'reconnect', false],
  ])(
    'close %s는 같은 TerminalSession 재접속을 허용한다',
    (code, action, clearResume) => {
      expect(decideTerminalClose(code)).toMatchObject({
        action,
        clearResume,
      })
    },
  )

  it.each([
    [1000, 'ended'],
    [1012, 'ended'],
    [4003, 'ended'],
    [4004, 'ended'],
    [4006, 'ended'],
  ])('close %s는 reconnect loop 없이 종료한다', (code, action) => {
    expect(decideTerminalClose(code)).toMatchObject({
      action,
      clearResume: true,
    })
  })

  it.each([1008, 1009, 4001, 4002])(
    'close %s는 복구 불가 오류로 저장 credential을 제거한다',
    (code) => {
      expect(decideTerminalClose(code)).toMatchObject({
        action: 'error',
        clearResume: true,
      })
    },
  )

  it('SLOW_CONSUMER는 Browser attachment만 끊고 same-session reconnect로 수렴한다', () => {
    expect(
      decideTerminalProtocolError({
        code: 'SLOW_CONSUMER',
        fatal: true,
      }),
    ).toEqual({
      message:
        '브라우저가 터미널 출력을 충분히 빠르게 처리하지 못해 기존 세션으로 다시 연결합니다.',
    })
  })

  it('CONNECTOR_UNAVAILABLE non-fatal ERROR는 Browser WSS 재접속을 강제로 시작하지 않는다', () => {
    const decision = decideTerminalProtocolError({
      code: 'CONNECTOR_UNAVAILABLE',
      fatal: false,
    })

    expect(decision.message).toContain('TerminalSession은 유지')
    expect(decision).not.toHaveProperty('action')
    expect(decision).not.toHaveProperty('clearResume')
  })

  it('LAB_MUTATION은 stale generation credential을 제거하고 ended로 수렴한다', () => {
    expect(
      decideTerminalProtocolError({
        code: 'LAB_MUTATION',
        fatal: true,
      }),
    ).toMatchObject({
      action: 'ended',
      clearResume: true,
    })
  })

  it('AUTH_REQUIRED는 global login 경계로 올릴 수 있게 표시한다', () => {
    expect(
      decideTerminalProtocolError({
        code: 'AUTH_REQUIRED',
        fatal: true,
      }),
    ).toMatchObject({
      authExpired: true,
      action: 'error',
      clearResume: true,
    })
  })

  it('알 수 없는 non-fatal code는 generic fallback으로 reconnect 가능 상태를 유지한다', () => {
    const decision = decideTerminalProtocolError({
      code: 'NEW_SERVER_CODE',
    })

    expect(decision.message).toContain('현재 연결 상태를 확인')
    expect(decision).not.toHaveProperty('action')
    expect(decision).not.toHaveProperty('clearResume')
  })

  it('알 수 없는 fatal code는 무한 reconnect를 막고 새 연결을 요구한다', () => {
    expect(
      decideTerminalProtocolError({
        code: 'NEW_FATAL_CODE',
        fatal: true,
      }),
    ).toMatchObject({
      action: 'error',
      clearResume: true,
    })
  })
})
