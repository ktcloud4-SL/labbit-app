import { Terminal } from '@xterm/xterm'
import { FitAddon } from '@xterm/addon-fit'
import '@xterm/xterm/css/xterm.css'
import { useEffect, useRef, useState } from 'react'
import { useQuery, useQueryClient } from '@tanstack/react-query'

import { HttpError } from '../api/httpClient'
import { useLabbitApi } from '../api/LabbitApiProvider'
import { labbitQueryKeys } from '../api/labbitApi'
import { LoginRedirect } from '../ui/LoginRedirect'
import { BrowserLiveClient } from './browserLiveClient'

function liveFailure(code: string) {
  switch (code) {
    case 'AUTH_REQUIRED': return '로그인 상태를 다시 확인해 주세요.'
    case 'FORBIDDEN': return '현재 Class의 학생 Live 공유 권한이 없습니다.'
    case 'SESSION_NOT_FOUND':
    case 'SESSION_EXPIRED': return 'Live 공유가 종료되었거나 만료되었습니다.'
    case 'LAB_MUTATION': return '실습 환경이 변경되어 Live 공유가 종료되었습니다.'
    case 'SLOW_CONSUMER': return '출력 수신 속도가 느려 연결이 종료되었습니다. 다시 구독해 주세요.'
    case 'SERVICE_RESTARTING': return '서비스가 재시작되었습니다. 다시 구독해 주세요.'
    default: return 'Live 연결을 사용할 수 없습니다. 다시 확인해 주세요.'
  }
}

function LiveReadOnlyTerminal({ liveSessionId, classId }: {
  liveSessionId: string
  classId: string
}) {
  const queryClient = useQueryClient()
  const terminalHost = useRef<HTMLDivElement>(null)
  const [attempt, setAttempt] = useState(0)
  const [status, setStatus] = useState<'connecting' | 'subscribed' | 'ended' | 'error'>('connecting')
  const [message, setMessage] = useState('강사 화면을 읽기 전용으로 연결하는 중...')
  const [authExpired, setAuthExpired] = useState(false)

  useEffect(() => {
    const host = terminalHost.current
    if (!host) return
    let disposed = false
    const terminal = new Terminal({
      cursorBlink: false,
      disableStdin: true,
      scrollback: 0,
      fontSize: 12,
      theme: { background: '#171b23', foreground: '#e7ebf2' },
    })
    const fit = new FitAddon()
    terminal.loadAddon(fit)
    terminal.open(host)
    try { fit.fit() } catch { /* layout not yet measurable */ }

    let observer: ResizeObserver | null = null
    if (typeof ResizeObserver !== 'undefined') {
      observer = new ResizeObserver(() => {
        try { fit.fit() } catch { /* view hidden before layout */ }
      })
      observer.observe(host)
    }

    // Each new subscription is blank: no history, transcript or PTY snapshot.
    terminal.reset()
    const client = new BrowserLiveClient({
      onSubscribed() {
        if (disposed) return
        setStatus('subscribed')
        setMessage('실시간 공유 중 · 과거 출력은 표시되지 않습니다.')
      },
      onOutput(bytes) {
        if (!disposed) terminal.write(bytes)
      },
      onEnded() {
        if (disposed) return
        setStatus('ended')
        setMessage('강사의 화면 공유가 종료되었습니다.')
        void queryClient.invalidateQueries({ queryKey: labbitQueryKeys.activeLiveSession(classId) })
      },
      onError(code) {
        if (disposed) return
        if (code === 'AUTH_REQUIRED') setAuthExpired(true)
        setStatus('error')
        setMessage(liveFailure(code))
      },
      onClose(code) {
        if (disposed) return
        if (code === 4001) setAuthExpired(true)
        if (code === 4003 || code === 4006 || code === 1000) {
          setStatus('ended')
          void queryClient.invalidateQueries({ queryKey: labbitQueryKeys.activeLiveSession(classId) })
        } else setStatus('error')
        setMessage(code === 4003 || code === 4006 || code === 1000
          ? 'Live 공유가 종료되었습니다.'
          : liveFailure(code === 4005 ? 'SLOW_CONSUMER' : code === 1012 ? 'SERVICE_RESTARTING' : 'UNKNOWN'))
      },
    })
    client.connect(liveSessionId)
    return () => {
      disposed = true
      client.disconnect()
      observer?.disconnect()
      terminal.dispose()
    }
  }, [liveSessionId, classId, attempt, queryClient])

  if (authExpired) return <LoginRedirect reason="sessionExpired" />

  return (
    <div className="live-viewer">
      <div className="live-viewer-toolbar">
        <strong>강사 터미널 실시간 공유</strong>
        <span className="terminal-status">
          {status === 'subscribed' ? '읽기 전용 연결됨' : status === 'connecting' ? '연결 중' : '연결 종료'}
        </span>
        {(status === 'error' || status === 'ended') && (
          <button className="terminal-button terminal-button-secondary" type="button"
            onClick={() => { setStatus('connecting'); setMessage('Live를 다시 구독합니다. 과거 출력은 제공되지 않습니다.'); setAttempt(n => n + 1) }}>
            다시 구독
          </button>
        )}
      </div>
      <p className="live-viewer-message" role="status">{message}</p>
      <div className="live-viewer-terminal" ref={terminalHost} aria-label="강사 화면 읽기 전용 출력" />
      <p className="muted">학생은 화면만 볼 수 있습니다. 입력·크기 변경·강사 Terminal 종료는 지원하지 않습니다.</p>
    </div>
  )
}

function deniesPreviouslyVerifiedLive(error: unknown) {
  return error instanceof HttpError && [401, 403, 404].includes(error.status)
}

function isTransientLiveLookupFailure(error: unknown) {
  return Boolean(error) && (!(error instanceof HttpError) || error.status === 503)
}

export function LiveStudentPanel({ classId }: { classId: string }) {
  const api = useLabbitApi()
  const queryClient = useQueryClient()
  const queryKey = labbitQueryKeys.activeLiveSession(classId)
  // Keep the currently subscribed, verified Live across transient HTTP failures.
  // A rejected session must not be revived from React Query's retained data.
  const [lastVerified, setLastVerified] = useState<{
    classId: string
    sessionId: string | null
  }>(() => {
    const cached = queryClient.getQueryState<{ id: string }>(queryKey)
    return {
      classId,
      sessionId: cached?.status === 'success' ? cached.data?.id ?? null : null,
    }
  })
  const query = useQuery({
    queryKey,
    queryFn: async ({ signal }) => {
      try {
        const session = await api.getActiveLiveSession(classId)
        if (!signal.aborted) setLastVerified({ classId, sessionId: session.id })
        return session
      } catch (error) {
        // Latch revocation at the response boundary, including back-to-back
        // 403/404 -> 503 replies without an intervening React render.
        if (!signal.aborted && deniesPreviouslyVerifiedLive(error)) {
          setLastVerified({ classId, sessionId: null })
        }
        throw error
      }
    },
    retry: false,
    refetchInterval: 15_000,
  })
  if (query.error instanceof HttpError && query.error.status === 401) {
    return <LoginRedirect reason="sessionExpired" />
  }
  const empty = query.error instanceof HttpError && query.error.status === 404
  const transient = isTransientLiveLookupFailure(query.error)
  // After a revocation, retained Query.data is never a source of authority.
  // On a transient refetch failure only the last explicitly verified ID may
  // keep the existing viewer mounted (not a new unverified session).
  const activeId = query.error
    ? (transient && lastVerified.classId === classId ? lastVerified.sessionId : null)
    : query.data?.id ?? null
  const liveState = query.isPending ? 'loading' : empty ? 'empty' :
    activeId ? 'active' : query.error ? 'error' : 'idle'
  return (
    <section className="live-class-panel" data-live-state={liveState} aria-label="강사 Live 화면">
      <h2>강사 화면 공유</h2>
      {query.isPending ? (
        <p className="muted">강사 Live 공유를 확인하고 있습니다.</p>
      ) : empty ? (
        <p className="muted">진행 중인 강사 화면 공유가 없습니다. 시작하면 자동으로 표시됩니다.</p>
      ) : activeId ? (
        <>
          {query.error && (
            <p role="alert">Live 상태 재확인이 일시적으로 실패했습니다. 기존 읽기 전용 연결을 유지합니다.</p>
          )}
          <LiveReadOnlyTerminal key={activeId} liveSessionId={activeId} classId={classId} />
        </>
      ) : query.error ? (
        <p role="alert">Live 화면 공유를 확인하지 못했습니다. 잠시 후 다시 시도해 주세요.</p>
      ) : null}
    </section>
  )
}
