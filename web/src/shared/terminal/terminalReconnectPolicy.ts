import type { TerminalProtocolError } from './browserTerminalClient'

export type TerminalCloseAction = 'reconnect' | 'ended' | 'error'

export interface TerminalCloseDecision {
  action: TerminalCloseAction
  clearResume: boolean
  message: string
}

export interface TerminalProtocolDecision {
  message: string
  authExpired?: boolean
  action?: Exclude<TerminalCloseAction, 'reconnect'>
  clearResume?: boolean
}

export function decideTerminalClose(code: number): TerminalCloseDecision {
  switch (code) {
    case 1000:
      return {
        action: 'ended',
        clearResume: true,
        message: '터미널 연결이 정상 종료되었습니다.',
      }
    case 1008:
      return {
        action: 'error',
        clearResume: true,
        message:
          '터미널 연결 규칙을 처리하지 못했습니다. 새 터미널 연결을 시작해 주세요.',
      }
    case 1009:
      return {
        action: 'error',
        clearResume: true,
        message:
          '터미널 제어 메시지 크기 제한을 초과했습니다. 새 터미널 연결을 시작해 주세요.',
      }
    case 1011:
      return {
        action: 'reconnect',
        clearResume: false,
        message:
          '터미널 서비스 또는 의존성에 일시적인 오류가 발생해 기존 세션으로 다시 연결합니다.',
      }
    case 1012:
      return {
        action: 'ended',
        clearResume: true,
        message:
          '터미널 서비스가 재시작되어 기존 TerminalSession이 종료되었습니다. 새 터미널을 열어 주세요.',
      }
    case 4001:
      return {
        action: 'error',
        clearResume: true,
        message:
          '터미널 인증 또는 재접속 정보가 더 이상 유효하지 않습니다. 새 터미널을 열어 주세요.',
      }
    case 4002:
      return {
        action: 'error',
        clearResume: true,
        message: '현재 계정에는 이 TerminalSession을 사용할 권한이 없습니다.',
      }
    case 4003:
      return {
        action: 'ended',
        clearResume: true,
        message:
          '기존 TerminalSession이 없거나 60초 재접속 시간이 지났습니다. 새 터미널을 열어 주세요.',
      }
    case 4004:
      return {
        action: 'ended',
        clearResume: true,
        message:
          '이 TerminalSession은 더 최근의 Browser 연결로 전환되었습니다. 이 탭의 연결을 종료합니다.',
      }
    case 4005:
      return {
        action: 'reconnect',
        clearResume: false,
        message:
          '브라우저가 터미널 출력을 충분히 빠르게 처리하지 못해 기존 세션으로 다시 연결합니다.',
      }
    case 4006:
      return {
        action: 'ended',
        clearResume: true,
        message:
          '실습 환경 Reset/Cleanup 또는 Terminal lifecycle 변경으로 기존 세션이 종료되었습니다.',
      }
    default:
      return {
        action: 'reconnect',
        clearResume: false,
        message:
          '터미널 연결이 일시적으로 끊어져 기존 TerminalSession으로 다시 연결합니다.',
      }
  }
}

export function decideTerminalProtocolError(
  error: TerminalProtocolError,
): TerminalProtocolDecision {
  switch (error.code) {
    case 'AUTH_REQUIRED':
      return {
        message: '로그인 세션을 다시 확인해 주세요.',
        authExpired: true,
        action: 'error',
        clearResume: true,
      }
    case 'INVALID_SESSION_TOKEN':
      return {
        message:
          '터미널 재접속 정보가 더 이상 유효하지 않습니다. 새 터미널을 열어 주세요.',
        action: 'error',
        clearResume: true,
      }
    case 'FORBIDDEN':
      return {
        message: '현재 계정에는 이 TerminalSession을 사용할 권한이 없습니다.',
        action: 'error',
        clearResume: true,
      }
    case 'SESSION_NOT_FOUND':
    case 'SESSION_EXPIRED':
      return {
        message:
          '기존 TerminalSession이 없거나 재접속 시간이 지났습니다. 새 터미널을 열어 주세요.',
        action: 'ended',
        clearResume: true,
      }
    case 'CONNECTOR_UNAVAILABLE':
      return {
        message:
          '실습 VM 연결 경로가 일시적으로 준비되지 않았습니다. 현재 TerminalSession은 유지하고 연결 경로 복구를 기다립니다.',
      }
    case 'SLOW_CONSUMER':
      return {
        message:
          '브라우저가 터미널 출력을 충분히 빠르게 처리하지 못해 기존 세션으로 다시 연결합니다.',
      }
    case 'LAB_MUTATION':
      return {
        message:
          '실습 환경이 초기화되거나 정리되어 기존 터미널을 더 이상 사용할 수 없습니다.',
        action: 'ended',
        clearResume: true,
      }
    case 'SERVICE_RESTARTING':
      return {
        message:
          '터미널 서비스가 재시작되어 기존 TerminalSession이 종료됩니다.',
        action: 'ended',
        clearResume: true,
      }
    case 'PROTOCOL_ERROR':
      return {
        message:
          '터미널 연결 규칙을 처리하지 못했습니다. 새 터미널 연결을 시작해 주세요.',
        action: 'error',
        clearResume: true,
      }
    case 'INTERNAL_ERROR':
      return {
        message:
          '터미널 서비스에서 일시적인 오류가 발생했습니다. 현재 연결 상태를 확인하고, 연결이 끊기면 기존 TerminalSession으로 다시 연결합니다.',
      }
    default:
      if (error.fatal) {
        return {
          message:
            '터미널 연결에서 복구할 수 없는 오류가 발생했습니다. 새 터미널 연결을 시작해 주세요.',
          action: 'error',
          clearResume: true,
        }
      }
      return {
        message:
          error.message ??
          '터미널 연결 중 알 수 없는 오류가 발생했습니다. 현재 연결 상태를 확인해 주세요.',
      }
  }
}
