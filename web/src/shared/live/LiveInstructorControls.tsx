import { useQuery, useQueryClient } from '@tanstack/react-query'
import { useRef, useState } from 'react'
import type { LiveSession } from '../api/contracts'
import { HttpError } from '../api/httpClient'
import { useLabbitApi } from '../api/LabbitApiProvider'
import { labbitQueryKeys } from '../api/labbitApi'
import { LoginRedirect } from '../ui/LoginRedirect'

function httpLiveError(error: unknown) {
  if (!(error instanceof HttpError)) return 'Live 요청을 처리하지 못했습니다. 다시 확인해 주세요.'
  if (error.status === 403) return '현재 Class의 Live 공유 권한이 없습니다.'
  if (error.status === 404) return '공유할 터미널을 찾을 수 없습니다. 상태를 확인해 주세요.'
  if (error.status === 409) return '다른 Live 공유가 진행 중이거나 터미널 연결을 사용할 수 없습니다.'
  if (error.status === 503) return 'Live 서비스를 현재 사용할 수 없습니다.'
  return 'Live 요청을 처리하지 못했습니다. 다시 확인해 주세요.'
}

function rejectsPreviousLive(error: unknown) {
  return error instanceof HttpError && [401, 403, 404].includes(error.status)
}

export function LiveInstructorControls({
  classId,
  sourceTerminalSessionId,
}: { classId: string; sourceTerminalSessionId: string | null }) {
  const api = useLabbitApi()
  const queryClient = useQueryClient()
  const [busy, setBusy] = useState(false)
  const pendingRef = useRef(false)
  const [message, setMessage] = useState('')
  const [authExpired, setAuthExpired] = useState(false)
  const [closedSessionId, setClosedSessionId] = useState<string | null>(null)
  const queryKey = labbitQueryKeys.activeLiveSession(classId)
  const [lastVerified, setLastVerified] = useState<{
    classId: string
    session: LiveSession | undefined
  }>(() => {
    const cached = queryClient.getQueryState<LiveSession>(queryKey)
    return { classId, session: cached?.status === 'success' ? cached.data : undefined }
  })
  const activeQuery = useQuery({
    queryKey,
    queryFn: async ({ signal }) => {
      try {
        const session = await api.getActiveLiveSession(classId)
        if (!signal.aborted) setLastVerified({ classId, session })
        return session
      } catch (error) {
        // Record the denial at the response boundary, even when a subsequent
        // failure replaces Query.error before React commits an intermediate UI.
        if (!signal.aborted && rejectsPreviousLive(error)) {
          setLastVerified({ classId, session: undefined })
        }
        throw error
      }
    },
    retry: false,
    refetchInterval: 15_000,
  })

  const lookupError = activeQuery.error
  const noActive = lookupError instanceof HttpError && lookupError.status === 404
  const forbidden = rejectsPreviousLive(lookupError)
  // On a transient lookup failure, we cannot claim the sharing is active.
  // But preserve the last verified ID for an explicit best-effort STOP.
  // Never offer STOP for an auth failure, forbidden membership, or confirmed 404.
  const uncertain = Boolean(lookupError) && !forbidden
  const fallback = lastVerified.classId === classId ? lastVerified.session : undefined
  const queriedActive = forbidden ? undefined : lookupError ? fallback : activeQuery.data
  const active = queriedActive?.id === closedSessionId ? undefined : queriedActive

  async function act(action: 'start' | 'stop') {
    if (pendingRef.current || activeQuery.isPending) return
    const id = action === 'start' ? sourceTerminalSessionId : active?.id
    if (!id || (action === 'start' && !noActive) || (action === 'stop' && !active)) return
    pendingRef.current = true
    setBusy(true)
    setMessage('')
    try {
      if (action === 'start') {
        const created: LiveSession = await api.createLiveSession(id)
        if (created.classId !== classId || created.sourceTerminalSessionId !== id) {
          setMessage('Live 응답 대상이 현재 Class·Terminal과 다릅니다. 새로 확인해 주세요.')
        } else {
          setClosedSessionId(null)
          setLastVerified({ classId, session: created })
          queryClient.setQueryData(queryKey, created)
          setMessage('학생에게 현재 이후의 터미널 출력만 공유합니다.')
        }
      } else {
        await api.closeLiveSession(id)
        // A subsequent failed refetch must not resurrect this confirmed-closed ID.
        setClosedSessionId(id)
        queryClient.removeQueries({ queryKey, exact: true })
        setMessage('화면 공유를 종료했습니다. 원본 터미널은 유지됩니다.')
      }
    } catch (error) {
      if (error instanceof HttpError && error.status === 401) setAuthExpired(true)
      else setMessage(httpLiveError(error))
    } finally {
      pendingRef.current = false
      setBusy(false)
      // Ensure server is authoritative for 404/409 and out-of-band changes.
      void queryClient.invalidateQueries({ queryKey })
    }
  }

  if (authExpired || (activeQuery.error instanceof HttpError && activeQuery.error.status === 401)) {
    return <LoginRedirect reason="sessionExpired" />
  }

  return (
    <div className="terminal-live-controls" aria-label="강사 Live 공유">
      {active ? (
        <>
          <span className={uncertain ? 'terminal-status' : 'terminal-status terminal-status-success'}>
            {uncertain ? '공유 상태 확인 불가 · 마지막 세션 종료 가능' : '화면 공유 중'}
          </span>
          <button className="terminal-button terminal-button-danger" type="button"
            disabled={busy} onClick={() => void act('stop')}>공유 종료</button>
        </>
      ) : (
        <button className="terminal-button terminal-button-secondary" type="button"
          disabled={busy || !sourceTerminalSessionId || !noActive}
          onClick={() => void act('start')}>화면 공유 시작</button>
      )}
      {message && <span role="status" className="terminal-live-message">{message}</span>}
      {activeQuery.error && !noActive && !(activeQuery.error instanceof HttpError && activeQuery.error.status === 401) && (
        <span role="alert" className="terminal-live-message">{httpLiveError(activeQuery.error)}</span>
      )}
      <span className="sr-only">화면 공유는 학생에게 읽기 전용이며 과거 출력은 제공하지 않습니다.</span>
    </div>
  )
}
