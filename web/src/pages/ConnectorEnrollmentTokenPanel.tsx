import { useState } from 'react'

/**
 * FE-only boundary for the one-time enrollment secret returned by an ADMIN
 * mutation. The parent must keep the response in short-lived React state,
 * clear it on close/navigation, and must not put it in a Query cache, URL,
 * browser storage, telemetry, or logs.
 *
 * This component neither fetches nor persists a token and is intentionally
 * not mounted by a production route until LBT-182's OpenAPI is finalized.
 */
interface ConnectorEnrollmentTokenPanelProps {
  token: string
  expiresAt?: string
  onClose: () => void
}

export function ConnectorEnrollmentTokenPanel({
  token,
  expiresAt,
  onClose,
}: ConnectorEnrollmentTokenPanelProps) {
  const [copyState, setCopyState] = useState<'idle' | 'copied' | 'failed'>('idle')

  async function copyToken() {
    setCopyState('idle')
    try {
      if (!navigator.clipboard?.writeText) {
        throw new Error('Clipboard API unavailable')
      }
      await navigator.clipboard.writeText(token)
      setCopyState('copied')
    } catch {
      // Never include the token or raw clipboard error in user-facing text.
      setCopyState('failed')
    }
  }

  return (
    <section className="empty-state" aria-label="Connector 등록 토큰">
      <h2>Connector 등록 토큰</h2>
      <p>
        토큰 원문은 발급 직후 이 화면에서만 표시합니다. 다른 곳에 공유하거나
        설치 명령·URL에 붙여 넣지 마세요.
      </p>
      <p>
        <strong>1회용 토큰</strong>
      </p>
      <output
        aria-label="발급된 1회용 토큰"
        style={{ display: 'block', overflowWrap: 'anywhere', wordBreak: 'break-all' }}
      >
        {token}
      </output>
      {expiresAt && <p className="muted">만료 시각: {expiresAt}</p>}
      <p className="muted">
        설치 프로그램에서 화면에 표시되지 않는 입력창으로 토큰을 입력합니다.
        이 화면을 닫은 후에는 원문을 다시 조회하지 않습니다.
      </p>
      {copyState === 'copied' && <p role="status">토큰을 복사했습니다.</p>}
      {copyState === 'failed' && (
        <p className="form-error" role="alert">
          복사에 실패했습니다. 보안 연결과 브라우저의 클립보드 권한을 확인해 주세요.
        </p>
      )}
      <div className="form-actions">
        <button className="secondary-button" type="button" onClick={onClose}>
          토큰 화면 닫기
        </button>
        <button className="primary-button" type="button" onClick={() => void copyToken()}>
          토큰 복사
        </button>
      </div>
    </section>
  )
}
