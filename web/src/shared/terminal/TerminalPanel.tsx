import { useMutation, useQuery } from '@tanstack/react-query'
import {
  useCallback,
  useEffect,
  useRef,
  useState,
  type FormEvent,
} from 'react'

import { HttpError } from '../api/httpClient'
import { useLabbitApi } from '../api/LabbitApiProvider'
import { labbitQueryKeys } from '../api/labbitApi'
import {
  BrowserTerminalClient,
  type TerminalProtocolError,
} from './browserTerminalClient'
import {
  clearTerminalResumeState,
  readTerminalResumeState,
  saveTerminalResumeState,
  type TerminalResumeState,
} from './terminalResumeStorage'

interface TerminalPanelProps {
  labInstanceId: string
  generation: number
}

type TerminalUiStatus =
  | 'idle'
  | 'creating'
  | 'connecting'
  | 'attached'
  | 'reconnecting'
  | 'closing'
  | 'ended'
  | 'error'

const DEFAULT_COLS = 100
const DEFAULT_ROWS = 24
const RECONNECT_GRACE_MS = 60_000
const MAX_OUTPUT_CHARS = 120_000

const unrecoverableCodes = new Set([
  'AUTH_REQUIRED',
  'INVALID_SESSION_TOKEN',
  'FORBIDDEN',
  'SESSION_NOT_FOUND',
  'SESSION_EXPIRED',
  'LAB_MUTATION',
  'PROTOCOL_ERROR',
])

const unrecoverableCloseCodes = new Set([
  1000,
  1008,
  1009,
  4001,
  4002,
  4003,
  4004,
  4006,
])

function stripAnsi(value: string) {
  // MVP fallback renderer: transport는 raw PTY byte stream을 유지하고,
  // 화면에는 흔한 ESC/CSI control sequence만 제거한 text를 표시한다.
  let result = ''
  let escaping = false

  for (const character of value) {
    const code = character.charCodeAt(0)

    if (!escaping && code === 27) {
      escaping = true
      continue
    }

    if (escaping) {
      if (code >= 0x40 && code <= 0x7e) {
        escaping = false
      }
      continue
    }

    result += character
  }

  return result
}

function appendBounded(current: string, next: string) {
  const combined = current + stripAnsi(next)
  return combined.length > MAX_OUTPUT_CHARS
    ? combined.slice(combined.length - MAX_OUTPUT_CHARS)
    : combined
}

function statusLabel(status: TerminalUiStatus) {
  switch (status) {
    case 'creating':
      return '세션 생성 중'
    case 'connecting':
      return '연결 중'
    case 'attached':
      return '연결됨'
    case 'reconnecting':
      return '재연결 중'
    case 'closing':
      return '종료 중'
    case 'ended':
      return '종료됨'
    case 'error':
      return '오류'
    default:
      return '대기'
  }
}

function httpTerminalError(error: unknown) {
  if (!(error instanceof HttpError)) {
    return '터미널 요청을 처리하지 못했습니다. 잠시 후 다시 시도해 주세요.'
  }

  if (error.status === 401) {
    return '로그인 세션이 만료되었습니다. 다시 로그인해 주세요.'
  }
  if (error.status === 403) {
    return '현재 계정에는 이 터미널을 사용할 권한이 없습니다.'
  }
  if (error.status === 404) {
    return '대상 실습 환경 또는 터미널 세션을 찾을 수 없습니다.'
  }
  if (error.status === 409) {
    if (error.problem?.code === 'terminal_target_unavailable') {
      return '선택한 VM을 지금 사용할 수 없습니다. VM 상태를 다시 확인해 주세요.'
    }
    return '실습 환경이 아직 터미널을 열 수 있는 상태가 아닙니다.'
  }
  if (error.status === 422) {
    return '선택한 VM 정보가 현재 환경과 맞지 않습니다. VM 목록을 다시 불러와 주세요.'
  }
  if (error.status === 503) {
    return '터미널 연결 경로를 지금 사용할 수 없습니다. 잠시 후 다시 시도해 주세요.'
  }

  return '터미널 요청을 처리하지 못했습니다. 잠시 후 다시 시도해 주세요.'
}

function wssErrorMessage(error: TerminalProtocolError) {
  switch (error.code) {
    case 'AUTH_REQUIRED':
      return '로그인 세션을 다시 확인해 주세요.'
    case 'INVALID_SESSION_TOKEN':
    case 'SESSION_EXPIRED':
      return '터미널 재접속 정보가 만료되었습니다. 새 터미널을 열어 주세요.'
    case 'FORBIDDEN':
      return '현재 계정에는 이 터미널을 사용할 권한이 없습니다.'
    case 'SESSION_NOT_FOUND':
      return '기존 터미널 세션의 재접속 시간이 지났습니다. 새 터미널을 열어 주세요.'
    case 'CONNECTOR_UNAVAILABLE':
      return '실습 VM 연결 경로가 일시적으로 준비되지 않았습니다.'
    case 'SLOW_CONSUMER':
      return '브라우저가 터미널 출력을 충분히 빠르게 처리하지 못해 다시 연결합니다.'
    case 'LAB_MUTATION':
      return '실습 환경이 초기화되거나 정리되어 기존 터미널을 더 이상 사용할 수 없습니다.'
    case 'SERVICE_RESTARTING':
      return '터미널 서비스가 재시작 중입니다. 기존 세션으로 다시 연결합니다.'
    case 'INTERNAL_ERROR':
      return '터미널 서비스에서 일시적인 오류가 발생했습니다.'
    case 'PROTOCOL_ERROR':
      return '터미널 연결 규칙을 처리하지 못했습니다.'
    default:
      return error.message ?? '터미널 연결 중 알 수 없는 오류가 발생했습니다.'
  }
}

function measureTerminal(element: HTMLElement | null) {
  if (!element) return { cols: DEFAULT_COLS, rows: DEFAULT_ROWS }

  const width = Math.max(320, element.clientWidth - 28)
  const height = Math.max(150, element.clientHeight - 28)
  return {
    cols: Math.max(20, Math.floor(width / 8.4)),
    rows: Math.max(8, Math.floor(height / 18)),
  }
}

export function TerminalPanel({ labInstanceId, generation }: TerminalPanelProps) {
  const api = useLabbitApi()
  const viewportRef = useRef<HTMLPreElement | null>(null)
  const clientRef = useRef<BrowserTerminalClient | null>(null)
  const resumeRef = useRef<TerminalResumeState | null>(null)
  const retryTimerRef = useRef<number | null>(null)
  const reconnectStartedAtRef = useRef<number | null>(null)
  const connectionSequenceRef = useRef(0)
  const suppressReconnectRef = useRef(false)
  const connectRef = useRef<(state: TerminalResumeState, reconnect: boolean) => void>(
    () => {},
  )

  const [initialResume] = useState(() =>
    readTerminalResumeState({ labInstanceId, generation }),
  )
  const [selectedVmKey, setSelectedVmKey] = useState('')
  const [status, setStatus] = useState<TerminalUiStatus>(
    initialResume ? 'reconnecting' : 'idle',
  )
  const [statusMessage, setStatusMessage] = useState(
    initialResume
      ? '새로고침 전 TerminalSession으로 다시 연결합니다.'
      : 'VM을 선택하고 터미널 연결을 시작하세요.',
  )
  const [output, setOutput] = useState('')
  const [command, setCommand] = useState('')
  const [resumed, setResumed] = useState(false)

  const targetsQuery = useQuery({
    queryKey: labbitQueryKeys.terminalTargets(labInstanceId),
    queryFn: () => api.listTerminalTargets(labInstanceId),
    retry: false,
  })

  const clearRetryTimer = useCallback(() => {
    if (retryTimerRef.current !== null) {
      window.clearTimeout(retryTimerRef.current)
      retryTimerRef.current = null
    }
  }, [])

  const invalidateResume = useCallback(() => {
    clearTerminalResumeState()
    resumeRef.current = null
    reconnectStartedAtRef.current = null
  }, [])

  const scheduleReconnect = useCallback(
    (state: TerminalResumeState) => {
      if (suppressReconnectRef.current) return

      const startedAt = reconnectStartedAtRef.current ?? Date.now()
      reconnectStartedAtRef.current = startedAt
      const elapsed = Date.now() - startedAt

      if (elapsed >= RECONNECT_GRACE_MS) {
        invalidateResume()
        setStatus('ended')
        setStatusMessage(
          '60초 재접속 시간이 지났습니다. 새 터미널을 열어 주세요.',
        )
        return
      }

      setStatus('reconnecting')
      setStatusMessage(
        '기존 TerminalSession과 같은 PTY에 다시 연결하고 있습니다. 끊긴 동안의 출력은 재생되지 않습니다.',
      )

      clearRetryTimer()
      const delay = Math.min(4_000, 750 + Math.floor(elapsed / 5_000) * 500)
      retryTimerRef.current = window.setTimeout(() => {
        connectRef.current(state, true)
      }, delay)
    },
    [clearRetryTimer, invalidateResume],
  )

  const connectSocket = useCallback(
    (state: TerminalResumeState, reconnect: boolean) => {
      clearRetryTimer()
      suppressReconnectRef.current = false

      const sequence = ++connectionSequenceRef.current
      clientRef.current?.disconnect()

      const client = new BrowserTerminalClient({
        onAttached(result) {
          if (sequence !== connectionSequenceRef.current) return

          reconnectStartedAtRef.current = null
          setStatus('attached')
          setResumed(result.resumed)
          setStatusMessage(
            result.resumed
              ? '기존 TerminalSession과 같은 PTY에 다시 연결되었습니다. 끊긴 동안의 출력은 재생되지 않습니다.'
              : '터미널에 연결되었습니다.',
          )
        },
        onOutput(text) {
          if (sequence !== connectionSequenceRef.current) return
          setOutput((current) => appendBounded(current, text))
        },
        onEnded(event) {
          if (sequence !== connectionSequenceRef.current) return

          suppressReconnectRef.current = true
          invalidateResume()
          setStatus('ended')
          setStatusMessage(
            event.exitCode === undefined
              ? `터미널 세션이 종료되었습니다. (${event.reason})`
              : `터미널 세션이 종료되었습니다. (${event.reason}, exit ${event.exitCode})`,
          )
        },
        onProtocolError(error) {
          if (sequence !== connectionSequenceRef.current) return

          setStatusMessage(wssErrorMessage(error))
          if (error.fatal || unrecoverableCodes.has(error.code)) {
            suppressReconnectRef.current = true
            invalidateResume()
            setStatus('error')
          }
        },
        onClose(event) {
          if (sequence !== connectionSequenceRef.current) return

          if (unrecoverableCloseCodes.has(event.code)) {
            suppressReconnectRef.current = true
            invalidateResume()
            setStatus(event.code === 1000 ? 'ended' : 'error')
            if (event.code === 4004) {
              setStatusMessage(
                '이 TerminalSession이 다른 Browser 연결로 대체되었습니다.',
              )
            }
            return
          }

          const current = resumeRef.current
          if (current && !suppressReconnectRef.current) {
            scheduleReconnect(current)
          }
        },
      })

      clientRef.current = client
      if (!reconnect) {
        setStatus('connecting')
        setStatusMessage('터미널 WebSocket에 연결하고 있습니다.')
      }

      const size = measureTerminal(viewportRef.current)
      client.connect({
        terminalSessionId: state.terminalSessionId,
        sessionToken: state.sessionToken,
        cols: size.cols,
        rows: size.rows,
      })
    },
    [clearRetryTimer, invalidateResume, scheduleReconnect],
  )

  useEffect(() => {
    connectRef.current = connectSocket
  }, [connectSocket])

  useEffect(() => {
    if (initialResume) {
      resumeRef.current = initialResume
      connectSocket(initialResume, true)
    }

    return () => {
      clearRetryTimer()
      suppressReconnectRef.current = true
      connectionSequenceRef.current += 1
      clientRef.current?.disconnect()
      clientRef.current = null
    }
  }, [clearRetryTimer, connectSocket, initialResume])

  useEffect(() => {
    if (status !== 'attached' || !viewportRef.current) return

    const viewport = viewportRef.current
    const sendResize = () => {
      const size = measureTerminal(viewport)
      clientRef.current?.resize(size.cols, size.rows)
    }

    sendResize()
    if (typeof ResizeObserver === 'undefined') return

    const observer = new ResizeObserver(sendResize)
    observer.observe(viewport)
    return () => observer.disconnect()
  }, [status])

  const createMutation = useMutation({
    mutationFn: async () => {
      const targetVmKey =
        selectedVmKey || targetsQuery.data?.workspaceVmKey || ''
      if (!targetVmKey) {
        throw new Error('Terminal target이 선택되지 않았습니다.')
      }

      const size = measureTerminal(viewportRef.current)
      return api.createTerminalSession(labInstanceId, {
        targetVmKey,
        cols: size.cols,
        rows: size.rows,
      })
    },
    onMutate: () => {
      suppressReconnectRef.current = true
      clearRetryTimer()
      setStatus('creating')
      setStatusMessage('TerminalSession과 PTY를 준비하고 있습니다.')
      setOutput('')
      setResumed(false)
    },
    onSuccess: (session) => {
      if (session.generation !== generation) {
        invalidateResume()
        setStatus('error')
        setStatusMessage(
          'Workspace를 확인한 뒤 실습 환경 generation이 변경되었습니다. 페이지를 새로고침해 주세요.',
        )
        return
      }

      const state: TerminalResumeState = {
        labInstanceId,
        generation: session.generation,
        terminalSessionId: session.id,
        sessionToken: session.sessionToken,
        tokenExpiresAt: session.tokenExpiresAt,
      }

      saveTerminalResumeState(state)
      resumeRef.current = state
      reconnectStartedAtRef.current = null
      suppressReconnectRef.current = false
      connectSocket(state, false)
    },
    onError: (error) => {
      invalidateResume()
      setStatus('error')
      setStatusMessage(httpTerminalError(error))
    },
  })

  async function closeTerminal() {
    const state = resumeRef.current
    suppressReconnectRef.current = true
    clearRetryTimer()
    connectionSequenceRef.current += 1
    clientRef.current?.disconnect()
    clientRef.current = null
    invalidateResume()

    if (!state) {
      setStatus('ended')
      setStatusMessage('터미널 연결을 종료했습니다.')
      return
    }

    setStatus('closing')
    setStatusMessage('TerminalSession을 종료하고 있습니다.')

    try {
      await api.closeTerminalSession(state.terminalSessionId)
      setStatus('ended')
      setStatusMessage('터미널을 종료했습니다.')
    } catch (error) {
      setStatus('error')
      setStatusMessage(
        `로컬 재접속 정보는 삭제했습니다. 서버 종료 요청은 확인이 필요합니다. ${httpTerminalError(error)}`,
      )
    }
  }

  function submitCommand(event: FormEvent) {
    event.preventDefault()
    if (!command) return

    if (clientRef.current?.sendInput(`${command}\r`)) {
      setCommand('')
    } else {
      setStatusMessage('터미널이 연결된 뒤 명령을 입력할 수 있습니다.')
    }
  }

  const effectiveSelectedVmKey =
    selectedVmKey || targetsQuery.data?.workspaceVmKey || ''
  const targetGenerationMismatch =
    Boolean(targetsQuery.data) && targetsQuery.data?.generation !== generation

  return (
    <div className="terminal-consumer">
      <div className="terminal-toolbar">
        <div className="terminal-target-control">
          <label htmlFor="terminal-target">VM</label>
          <select
            id="terminal-target"
            value={effectiveSelectedVmKey}
            disabled={
              !targetsQuery.data ||
              targetGenerationMismatch ||
              ['creating', 'connecting', 'attached', 'reconnecting', 'closing'].includes(
                status,
              )
            }
            onChange={(event) => setSelectedVmKey(event.target.value)}
          >
            {!targetsQuery.data && <option value="">VM 목록 확인 중...</option>}
            {targetsQuery.data?.items.map((target) => (
              <option key={target.vmKey} value={target.vmKey}>
                {target.role} {target.instanceIndex + 1}
                {target.vmKey === targetsQuery.data.workspaceVmKey
                  ? ' · Workspace'
                  : ''}
              </option>
            ))}
          </select>
        </div>

        <div className="terminal-toolbar-actions">
          <span
            className={
              'terminal-status terminal-status-' +
              (status === 'attached' ? 'success' : status)
            }
          >
            {statusLabel(status)}
            {status === 'attached' && resumed ? ' · resumed' : ''}
          </span>

          {status === 'attached' || status === 'reconnecting' ? (
            <button
              className="terminal-button terminal-button-danger"
              type="button"
              onClick={() => void closeTerminal()}
            >
              터미널 종료
            </button>
          ) : (
            <button
              className="terminal-button"
              type="button"
              disabled={
                targetsQuery.isPending ||
                targetGenerationMismatch ||
                !effectiveSelectedVmKey ||
                status === 'creating' ||
                status === 'connecting' ||
                status === 'closing'
              }
              onClick={() => createMutation.mutate()}
            >
              {status === 'creating' || status === 'connecting'
                ? '연결 중...'
                : '터미널 연결'}
            </button>
          )}
        </div>
      </div>

      {targetsQuery.error && (
        <div className="terminal-notice terminal-notice-error" role="alert">
          {httpTerminalError(targetsQuery.error)}
        </div>
      )}

      {targetGenerationMismatch && (
        <div className="terminal-notice terminal-notice-error" role="alert">
          VM 목록의 generation이 현재 Workspace와 다릅니다. 페이지를 새로고침해
          최신 환경을 확인해 주세요.
        </div>
      )}

      <div className="terminal-status-message" aria-live="polite">
        {statusMessage}
      </div>

      <pre
        ref={viewportRef}
        className="terminal-output"
        aria-label="터미널 출력"
        tabIndex={0}
      >
        {output || '$ '}
      </pre>

      <form className="terminal-input-row" onSubmit={submitCommand}>
        <span aria-hidden="true">$</span>
        <input
          aria-label="터미널 명령 입력"
          autoComplete="off"
          spellCheck={false}
          value={command}
          disabled={status !== 'attached'}
          placeholder={status === 'attached' ? '명령을 입력하세요' : '터미널 연결 대기'}
          onChange={(event) => setCommand(event.target.value)}
          onKeyDown={(event) => {
            if (event.ctrlKey && event.key.toLowerCase() === 'c') {
              event.preventDefault()
              clientRef.current?.sendInput('\u0003')
            }
          }}
        />
        <button
          className="terminal-button terminal-button-secondary"
          type="submit"
          disabled={status !== 'attached' || !command}
        >
          보내기
        </button>
        <button
          className="terminal-button terminal-button-secondary"
          type="button"
          disabled={status !== 'attached'}
          onClick={() => clientRef.current?.sendInput('\u0003')}
        >
          Ctrl+C
        </button>
        <button
          className="terminal-button terminal-button-secondary"
          type="button"
          onClick={() => setOutput('')}
        >
          화면 지우기
        </button>
      </form>
    </div>
  )
}
