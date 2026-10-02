import { FitAddon } from '@xterm/addon-fit'
import { Terminal } from '@xterm/xterm'
import '@xterm/xterm/css/xterm.css'
import { useMutation, useQuery } from '@tanstack/react-query'
import {
  useCallback,
  useEffect,
  useRef,
  useState,
} from 'react'

import { HttpError } from '../api/httpClient'
import { useLabbitApi } from '../api/LabbitApiProvider'
import { labbitQueryKeys } from '../api/labbitApi'
import { LoginRedirect } from '../ui/LoginRedirect'
import { BrowserTerminalClient } from './browserTerminalClient'
import {
  decideTerminalClose,
  decideTerminalProtocolError,
} from './terminalReconnectPolicy'
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

interface Disposable {
  dispose(): void
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

export function TerminalPanel({ labInstanceId, generation }: TerminalPanelProps) {
  const api = useLabbitApi()
  const clientRef = useRef<BrowserTerminalClient | null>(null)
  const resumeRef = useRef<TerminalResumeState | null>(null)
  const retryTimerRef = useRef<number | null>(null)
  const reconnectStartedAtRef = useRef<number | null>(null)
  const connectionSequenceRef = useRef(0)
  const suppressReconnectRef = useRef(false)
  const connectRef = useRef<(state: TerminalResumeState, reconnect: boolean) => void>(
    () => {},
  )

  const terminalHostRef = useRef<HTMLDivElement | null>(null)
  const terminalRef = useRef<Terminal | null>(null)
  const fitAddonRef = useRef<FitAddon | null>(null)
  const terminalDataRef = useRef<Disposable | null>(null)
  const terminalResizeRef = useRef<Disposable | null>(null)
  const hostResizeObserverRef = useRef<ResizeObserver | null>(null)

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
  const [resumed, setResumed] = useState(false)
  const [authExpired, setAuthExpired] = useState(false)

  const targetsQuery = useQuery({
    queryKey: labbitQueryKeys.terminalTargets(labInstanceId, generation),
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

  const ensureTerminal = useCallback(() => {
    if (terminalRef.current) return terminalRef.current

    const host = terminalHostRef.current
    if (!host) return null

    const terminal = new Terminal({
      cursorBlink: true,
      cursorStyle: 'block',
      fontFamily:
        '"Cascadia Code", "SFMono-Regular", Consolas, "Liberation Mono", monospace',
      fontSize: 12,
      lineHeight: 1.15,
      scrollback: 5_000,
      allowProposedApi: false,
      theme: {
        background: '#171b23',
        foreground: '#e7ebf2',
        cursor: '#8ee0bd',
        cursorAccent: '#171b23',
        selectionBackground: '#4a526380',
        black: '#171b23',
        brightBlack: '#6f7787',
        red: '#ff7f87',
        brightRed: '#ffabb2',
        green: '#78d9ad',
        brightGreen: '#9be7c6',
        yellow: '#e8c85f',
        brightYellow: '#f3d36d',
        blue: '#8c8aee',
        brightBlue: '#aaa8f4',
        magenta: '#c58be8',
        brightMagenta: '#d8a7f2',
        cyan: '#72ced6',
        brightCyan: '#9ce0e5',
        white: '#d8dde7',
        brightWhite: '#ffffff',
      },
    })
    const fitAddon = new FitAddon()

    terminal.loadAddon(fitAddon)
    terminal.open(host)

    terminalRef.current = terminal
    fitAddonRef.current = fitAddon

    try {
      fitAddon.fit()
    } catch {
      // 첫 layout 전 fit 실패는 이후 ResizeObserver에서 다시 맞춘다.
    }

    terminalDataRef.current = terminal.onData((data) => {
      clientRef.current?.sendInput(data)
    })

    terminalResizeRef.current = terminal.onResize(({ cols, rows }) => {
      clientRef.current?.resize(cols, rows)
    })

    if (typeof ResizeObserver !== 'undefined') {
      const observer = new ResizeObserver(() => {
        try {
          fitAddon.fit()
        } catch {
          // 숨겨진 panel처럼 크기를 계산할 수 없는 순간은 다음 resize를 기다린다.
        }
      })
      observer.observe(host)
      hostResizeObserverRef.current = observer
    }

    return terminal
  }, [])

  const terminalSize = useCallback(() => {
    const terminal = ensureTerminal()
    if (!terminal) {
      return { cols: DEFAULT_COLS, rows: DEFAULT_ROWS }
    }

    try {
      fitAddonRef.current?.fit()
    } catch {
      // 현재 계산된 xterm 크기를 그대로 사용한다.
    }

    return {
      cols: Math.max(1, terminal.cols || DEFAULT_COLS),
      rows: Math.max(1, terminal.rows || DEFAULT_ROWS),
    }
  }, [ensureTerminal])

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

      const terminal = ensureTerminal()
      if (!terminal) {
        setStatus('error')
        setStatusMessage('터미널 화면을 준비하지 못했습니다. 페이지를 다시 열어 주세요.')
        return
      }

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
          terminal.focus()
        },
        onOutput(data) {
          if (sequence !== connectionSequenceRef.current) return
          terminal.write(data)
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

          const decision = decideTerminalProtocolError(error)
          setStatusMessage(decision.message)

          if (decision.authExpired) {
            setAuthExpired(true)
          }

          if (decision.action) {
            suppressReconnectRef.current = true
            if (decision.clearResume) invalidateResume()
            setStatus(decision.action)
          }
        },
        onClose(event) {
          if (sequence !== connectionSequenceRef.current) return

          const decision = decideTerminalClose(event.code)

          if (suppressReconnectRef.current) {
            if (decision.action !== 'reconnect') {
              setStatusMessage(decision.message)
            }
            return
          }

          setStatusMessage(decision.message)

          if (decision.action !== 'reconnect') {
            suppressReconnectRef.current = true
            if (decision.clearResume) invalidateResume()
            setStatus(decision.action)
            return
          }

          const current = resumeRef.current
          if (current) {
            scheduleReconnect(current)
          }
        },
      })

      clientRef.current = client
      if (!reconnect) {
        setStatus('connecting')
        setStatusMessage('터미널 WebSocket에 연결하고 있습니다.')
      }

      const size = terminalSize()
      client.connect({
        terminalSessionId: state.terminalSessionId,
        sessionToken: state.sessionToken,
        cols: size.cols,
        rows: size.rows,
      })
    },
    [
      clearRetryTimer,
      ensureTerminal,
      invalidateResume,
      scheduleReconnect,
      terminalSize,
    ],
  )

  useEffect(() => {
    connectRef.current = connectSocket
  }, [connectSocket])

  useEffect(() => {
    let resumeTimer: number | null = null

    if (initialResume) {
      resumeRef.current = initialResume
      resumeTimer = window.setTimeout(() => {
        connectSocket(initialResume, true)
      }, 0)
    }

    return () => {
      if (resumeTimer !== null) {
        window.clearTimeout(resumeTimer)
      }
      clearRetryTimer()
      suppressReconnectRef.current = true
      connectionSequenceRef.current += 1
      clientRef.current?.disconnect()
      clientRef.current = null
    }
  }, [clearRetryTimer, connectSocket, initialResume])

  useEffect(
    () => () => {
      hostResizeObserverRef.current?.disconnect()
      hostResizeObserverRef.current = null
      terminalDataRef.current?.dispose()
      terminalDataRef.current = null
      terminalResizeRef.current?.dispose()
      terminalResizeRef.current = null
      terminalRef.current?.dispose()
      terminalRef.current = null
      fitAddonRef.current = null
    },
    [],
  )

  const createMutation = useMutation({
    mutationFn: async () => {
      const targetVmKey =
        selectedVmKey || targetsQuery.data?.workspaceVmKey || ''
      if (!targetVmKey) {
        throw new Error('Terminal target이 선택되지 않았습니다.')
      }

      const size = terminalSize()
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
      setResumed(false)

      const terminal = ensureTerminal()
      terminal?.reset()
      terminal?.clear()
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
      if (error instanceof HttpError && error.status === 401) {
        setAuthExpired(true)
        return
      }
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
      if (error instanceof HttpError && error.status === 401) {
        setAuthExpired(true)
        return
      }

      setStatus('error')
      setStatusMessage(
        `로컬 재접속 정보는 삭제했습니다. 서버 종료 요청은 확인이 필요합니다. ${httpTerminalError(error)}`,
      )
    }
  }

  const effectiveSelectedVmKey =
    selectedVmKey || targetsQuery.data?.workspaceVmKey || ''
  const targetGenerationMismatch =
    Boolean(targetsQuery.data) && targetsQuery.data?.generation !== generation
  const targetAuthExpired =
    targetsQuery.error instanceof HttpError && targetsQuery.error.status === 401

  if (authExpired || targetAuthExpired) {
    return <LoginRedirect reason="sessionExpired" />
  }

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

          <button
            className="terminal-button terminal-button-secondary"
            type="button"
            disabled={status !== 'attached'}
            onClick={() => terminalRef.current?.clear()}
          >
            화면 지우기
          </button>

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
                : status === 'ended' || status === 'error'
                  ? '새 터미널 열기'
                  : '터미널 연결'}
            </button>
          )}
        </div>
      </div>

      {targetsQuery.error && (
        <div className="terminal-notice terminal-notice-error" role="alert">
          <span>{httpTerminalError(targetsQuery.error)}</span>
          <button
            className="terminal-button terminal-button-secondary"
            type="button"
            disabled={targetsQuery.isFetching}
            onClick={() => void targetsQuery.refetch()}
          >
            {targetsQuery.isFetching ? '다시 확인 중...' : 'VM 목록 다시 불러오기'}
          </button>
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

      <div className="terminal-xterm-shell">
        <div
          ref={terminalHostRef}
          className="terminal-xterm"
          aria-label="터미널 입력 및 출력"
        />
      </div>
    </div>
  )
}
