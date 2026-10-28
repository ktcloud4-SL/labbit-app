import { useEffect, useId, useRef, useState, type FormEvent } from 'react'

import type { PreviewSession } from '../api/contracts'
import { HttpError } from '../api/httpClient'
import { useLabbitApi } from '../api/LabbitApiProvider'
import { LoginRedirect } from '../ui/LoginRedirect'

interface PreviewPanelProps {
  labInstanceId: string
  generation: number
}

type PreviewStatus = 'idle' | 'creating' | 'ready' | 'closing' | 'expired'

type SessionMetadata = Omit<PreviewSession, 'previewUrl'>

function safePreviewError(error: unknown): string {
  if (!(error instanceof HttpError)) {
    return '미리보기 요청을 처리하지 못했습니다. 잠시 후 다시 시도해 주세요.'
  }

  switch (error.problem?.code) {
    case 'preview_port_not_allowed':
    case 'preview_port_rejected':
      return '해당 포트는 미리보기에 사용할 수 없습니다. 허용된 포트를 확인해 주세요.'
    case 'lab_instance_not_ready':
    case 'workspace_target_unavailable':
    case 'workspace_target_changed':
      return '실습 환경이 변경되었거나 준비되지 않았습니다. 환경 상태를 확인해 주세요.'
    case 'preview_app_not_running':
      return '해당 포트에서 실행 중인 웹 애플리케이션이 없습니다.'
    case 'preview_target_unreachable':
      return '실습 VM에 연결할 수 없습니다. 잠시 후 다시 시도해 주세요.'
    case 'preview_open_timeout':
      return '미리보기 연결 시간이 초과되었습니다. 잠시 후 다시 시도해 주세요.'
    case 'preview_unavailable':
      return '현재 배포 환경에서 미리보기 기능을 사용할 수 없습니다. 운영 담당자에게 문의해 주세요.'
    case 'preview_transport_unavailable':
      return '실습 환경 Connector가 미리보기를 지원하지 않습니다. 운영 담당자에게 문의해 주세요.'
    case 'connector_unavailable':
    case 'preview_open_failed':
      return '미리보기 연결 경로를 사용할 수 없습니다. 잠시 후 다시 시도해 주세요.'
  }

  if (error.status === 403) return '미리보기를 사용할 권한이 없거나 포트가 거부됐습니다.'
  if (error.status === 404) return '실습 환경 또는 미리보기 세션을 찾을 수 없습니다.'
  if (error.status === 409) return '실습 환경이 준비되지 않았습니다. 상태를 확인해 주세요.'
  if (error.status === 502) return '해당 포트의 애플리케이션이 응답하지 않습니다.'
  if (error.status === 503 || error.status === 504) {
    return '미리보기 연결이 준비되지 않았습니다. 잠시 후 다시 시도해 주세요.'
  }
  return '미리보기 요청을 처리하지 못했습니다. 잠시 후 다시 시도해 주세요.'
}

// The Backend owns same-site enforcement. The UI rejects malformed/unsafe
// bootstrap URLs without exposing the one-time fragment in DOM or logs.
function validBootstrapUrl(raw: string): boolean {
  try {
    const url = new URL(raw)
    const isLocalHost = (hostname: string) =>
      hostname === 'localhost' || hostname.endsWith('.localhost')
    // Runtime Contract permits local HTTP on *.localhost; production requires HTTPS.
    const isLocalHttp = url.protocol === 'http:'
      && window.location.protocol === 'http:'
      && isLocalHost(window.location.hostname)
      && isLocalHost(url.hostname)
    return (url.protocol === 'https:' || isLocalHttp)
      && Boolean(url.hostname)
      && url.origin !== window.location.origin
      && url.username === ''
      && url.password === ''
      && url.search === ''
      && url.pathname === '/__labbit/bootstrap'
      && url.hash.length > 1
  } catch {
    return false
  }
}

export function PreviewPanel({ labInstanceId, generation }: PreviewPanelProps) {
  const api = useLabbitApi()
  const portInputId = useId()
  const [portInput, setPortInput] = useState('')
  const [status, setStatus] = useState<PreviewStatus>('idle')
  const [error, setError] = useState<string | null>(null)
  const [session, setSession] = useState<SessionMetadata | null>(null)
  const [canOpen, setCanOpen] = useState(false)
  const [authExpired, setAuthExpired] = useState(false)

  // previewUrl may contain a one-time credential. Keep it only in a ref;
  // never in React Query, React state, storage, element attributes or logs.
  const previewUrlRef = useRef<string | null>(null)
  const sessionIdRef = useRef<string | null>(null)
  const inFlightRef = useRef(false)
  const requestSequenceRef = useRef(0)

  useEffect(() => {
    return () => {
      requestSequenceRef.current += 1
      previewUrlRef.current = null
      const id = sessionIdRef.current
      sessionIdRef.current = null
      // Best-effort on navigation/generation change. TTL remains authoritative.
      if (id) void api.closePreviewSession(id).catch(() => undefined)
    }
  }, [api, labInstanceId, generation])

  useEffect(() => {
    if (!session) return
    const remaining = Date.parse(session.expiresAt) - Date.now()
    // Run expiry via timer, not synchronous setState in the effect body.
    const timer = window.setTimeout(() => {
      previewUrlRef.current = null
      sessionIdRef.current = null
      setCanOpen(false)
      setSession(null)
      setStatus('expired')
    }, Math.max(0, Math.min(remaining, 2_147_483_647)))
    return () => window.clearTimeout(timer)
  }, [session])

  async function create(event: FormEvent<HTMLFormElement>) {
    event.preventDefault()
    if (inFlightRef.current || session) return

    const raw = portInput.trim()
    const port = Number(raw)
    if (!/^\d+$/.test(raw) || !Number.isInteger(port) || port < 1 || port > 65535) {
      setError('포트는 1부터 65535 사이의 정수로 입력해 주세요.')
      return
    }

    inFlightRef.current = true
    const sequence = ++requestSequenceRef.current
    setError(null)
    setStatus('creating')
    try {
      // Intentionally imperative: a Mutation cache would retain previewUrl.
      const result = await api.createPreviewSession(labInstanceId, { targetPort: port })
      if (sequence !== requestSequenceRef.current) {
        // The response arrived after navigation or generation change.
        void api.closePreviewSession(result.id).catch(() => undefined)
        return
      }

      if (
        result.targetPort !== port ||
        !validBootstrapUrl(result.previewUrl) ||
        !Number.isFinite(Date.parse(result.expiresAt)) ||
        Date.parse(result.expiresAt) <= Date.now()
      ) {
        void api.closePreviewSession(result.id).catch(() => undefined)
        setStatus('idle')
        setError('미리보기 응답을 안전하게 사용할 수 없습니다. 다시 시도해 주세요.')
        return
      }

      previewUrlRef.current = result.previewUrl
      sessionIdRef.current = result.id
      setSession({ id: result.id, targetPort: result.targetPort, expiresAt: result.expiresAt })
      setCanOpen(true)
      setStatus('ready')
    } catch (cause) {
      if (sequence !== requestSequenceRef.current) return
      previewUrlRef.current = null
      setCanOpen(false)
      if (cause instanceof HttpError && cause.status === 401) {
        setAuthExpired(true)
      } else {
        setError(safePreviewError(cause))
        setStatus('idle')
      }
    } finally {
      if (sequence === requestSequenceRef.current) inFlightRef.current = false
    }
  }

  function openPreview() {
    const url = previewUrlRef.current
    if (!url || !session) return
    // The bootstrap credential can only be used once; never expose a link href.
    previewUrlRef.current = null
    setCanOpen(false)
    window.open(url, '_blank', 'noopener,noreferrer')
  }

  async function closePreview() {
    if (!session || inFlightRef.current) return
    inFlightRef.current = true
    previewUrlRef.current = null
    setCanOpen(false)
    setError(null)
    setStatus('closing')
    const sequence = ++requestSequenceRef.current
    try {
      await api.closePreviewSession(session.id)
      if (sequence !== requestSequenceRef.current) return
      sessionIdRef.current = null
      setSession(null)
      setStatus('idle')
    } catch (cause) {
      if (sequence !== requestSequenceRef.current) return
      if (cause instanceof HttpError && cause.status === 401) {
        setAuthExpired(true)
      } else if (cause instanceof HttpError && cause.status === 404) {
        // The server no longer knows this session (for example after restart).
        sessionIdRef.current = null
        setSession(null)
        setStatus('idle')
      } else {
        setStatus('ready')
        setError(safePreviewError(cause))
      }
    } finally {
      if (sequence === requestSequenceRef.current) inFlightRef.current = false
    }
  }

  if (authExpired) return <LoginRedirect reason="sessionExpired" />

  const busy = status === 'creating' || status === 'closing'
  return (
    <div className="workspace-preview-control">
      <form className="workspace-preview-form" noValidate onSubmit={(event) => void create(event)}>
        <label htmlFor={portInputId}>애플리케이션 포트</label>
        <input
          id={portInputId}
          type="number"
          inputMode="numeric"
          min={1}
          max={65535}
          step={1}
          value={portInput}
          placeholder="예: 3000"
          disabled={busy || Boolean(session)}
          onChange={(event) => setPortInput(event.target.value)}
        />
        <button type="submit" disabled={busy || Boolean(session) || !portInput.trim()}>
          {status === 'creating' ? '미리보기 연결 중...' : '미리보기 시작'}
        </button>
      </form>
      <p className="muted">허용된 포트에서 웹 애플리케이션을 먼저 실행해야 합니다.</p>

      {status === 'ready' && session && (
        <div role="status">
          <p>미리보기 세션이 준비되었습니다. (포트 {session.targetPort})</p>
          <button type="button" onClick={openPreview} disabled={!canOpen}>
            미리보기 새 탭 열기
          </button>
          <button className="secondary-button" type="button" onClick={() => void closePreview()}>
            미리보기 종료
          </button>
          {!canOpen && <p className="muted">새 탭이 열리지 않았다면 팝업 차단을 확인하세요. 보안을 위해 발급 주소는 다시 사용할 수 없습니다.</p>}
        </div>
      )}
      {status === 'closing' && <p role="status">미리보기를 종료하고 있습니다.</p>}
      {status === 'expired' && <p role="status">미리보기 세션이 만료됐습니다. 새 세션을 만들어 주세요.</p>}
      {error && <p role="alert" className="form-error">{error}</p>}
    </div>
  )
}
