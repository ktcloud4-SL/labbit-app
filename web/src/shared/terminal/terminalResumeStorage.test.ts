import { afterEach, beforeEach, describe, expect, it } from 'vitest'

import {
  clearTerminalResumeState,
  readTerminalResumeState,
  saveTerminalResumeState,
} from './terminalResumeStorage'

const future = '2026-10-03T00:00:00Z'
const now = Date.parse('2026-10-02T00:00:00Z')

describe('terminalResumeStorage', () => {
  beforeEach(() => {
    window.sessionStorage.clear()
  })

  afterEach(() => {
    window.sessionStorage.clear()
  })

  it('같은 LabInstance/generation의 최소 resume credential을 복원한다', () => {
    saveTerminalResumeState({
      labInstanceId: 'lab-instance-1',
      generation: 3,
      terminalSessionId: 'terminal-session-1',
      sessionToken: 'opaque-token',
      tokenExpiresAt: future,
    })

    expect(
      readTerminalResumeState(
        {
          labInstanceId: 'lab-instance-1',
          generation: 3,
        },
        now,
      ),
    ).toEqual({
      labInstanceId: 'lab-instance-1',
      generation: 3,
      terminalSessionId: 'terminal-session-1',
      sessionToken: 'opaque-token',
      tokenExpiresAt: future,
    })
  })

  it('generation이 달라지면 stale credential을 삭제한다', () => {
    saveTerminalResumeState({
      labInstanceId: 'lab-instance-1',
      generation: 2,
      terminalSessionId: 'terminal-session-old',
      sessionToken: 'old-token',
      tokenExpiresAt: future,
    })

    expect(
      readTerminalResumeState(
        {
          labInstanceId: 'lab-instance-1',
          generation: 3,
        },
        now,
      ),
    ).toBeNull()
    expect(window.sessionStorage.length).toBe(0)
  })

  it('tokenExpiresAt이 지난 credential을 삭제한다', () => {
    saveTerminalResumeState({
      labInstanceId: 'lab-instance-1',
      generation: 3,
      terminalSessionId: 'terminal-session-old',
      sessionToken: 'old-token',
      tokenExpiresAt: '2026-10-01T23:59:59Z',
    })

    expect(
      readTerminalResumeState(
        {
          labInstanceId: 'lab-instance-1',
          generation: 3,
        },
        now,
      ),
    ).toBeNull()
    expect(window.sessionStorage.length).toBe(0)
  })

  it('손상된 JSON은 fail-closed로 삭제한다', () => {
    window.sessionStorage.setItem('labbit.terminal.resume.v1', '{broken')

    expect(
      readTerminalResumeState(
        {
          labInstanceId: 'lab-instance-1',
          generation: 3,
        },
        now,
      ),
    ).toBeNull()
    expect(window.sessionStorage.length).toBe(0)
  })

  it('명시적으로 resume credential을 제거한다', () => {
    saveTerminalResumeState({
      labInstanceId: 'lab-instance-1',
      generation: 3,
      terminalSessionId: 'terminal-session-1',
      sessionToken: 'opaque-token',
      tokenExpiresAt: future,
    })

    clearTerminalResumeState()

    expect(window.sessionStorage.length).toBe(0)
  })
})
