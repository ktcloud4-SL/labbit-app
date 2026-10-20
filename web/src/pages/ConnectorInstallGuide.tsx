import { useState } from 'react'
import { normalizeInstallerUrl } from '../shared/installer/installerUrl.mjs'

interface ConnectorInstallGuideProps {
  /**
   * URL approved and deployed by LBT-181. Never pass a planning/prototype URL.
   * This component does not fetch, execute, or validate installer binaries.
   */
  approvedInstallerUrl?: string
}

/**
 * Informational UI only. No mock enrollment states or executable shell
 * pipeline; the LBT-181 release owner must approve actual customer commands.
 */
export function ConnectorInstallGuide({
  approvedInstallerUrl,
}: ConnectorInstallGuideProps) {
  const url = normalizeInstallerUrl(approvedInstallerUrl)
  const [copyState, setCopyState] = useState<'idle' | 'copied' | 'failed'>('idle')

  async function copyUrl() {
    if (!url) return
    setCopyState('idle')
    try {
      if (!navigator.clipboard?.writeText) throw new Error('Clipboard unavailable')
      await navigator.clipboard.writeText(url)
      setCopyState('copied')
    } catch {
      setCopyState('failed')
    }
  }

  return (
    <section className="class-card" aria-labelledby="connector-install-title">
      <h2 id="connector-install-title">Connector 설치 안내</h2>
      <p>
        고객사 Linux 관리 VM에서 Connector를 설치합니다. SaaS 연결(ONLINE)과
        OpenStack Provider 준비(READY)는 서로 다른 확인 단계입니다.
      </p>
      <p className="muted">
        Ubuntu 24.04 / amd64는 최초 검증 후보이며 현재 공식 지원 환경으로 확정되지 않았습니다.
        시스템 권한, DNS/HTTPS, GitHub Release 접근, systemd 준비 여부를 담당자와 확인해 주세요.
      </p>
      {url ? (
        <>
          <p><strong>승인된 Installer HTTPS 주소</strong></p>
          <p style={{ overflowWrap: 'anywhere' }}>{url}</p>
          <button className="secondary-button" type="button" onClick={() => void copyUrl()}>
            Installer 주소 복사
          </button>
          {copyState === 'copied' && <p role="status">Installer 주소를 복사했습니다.</p>}
          {copyState === 'failed' && (
            <p className="form-error" role="alert">
              주소 복사에 실패했습니다. 브라우저 클립보드 권한을 확인해 주세요.
            </p>
          )}
          <p className="muted">
            설치 전 스크립트와 고정 Binary 버전·체크섬을 확인하세요. 자동 실행 대신
            파일을 내려받아 내용을 검토한 뒤 승인된 운영 절차로 실행할 수 있습니다.
            설치에 사용할 실제 SaaS HTTPS 접속 주소도 담당자에게 확인해 주세요.
          </p>
        </>
      ) : (
        <p role="status">
          공식 Installer 배포 주소가 아직 확정되지 않았습니다.
          설치 명령을 임의로 실행하지 말고 담당자의 승인·검증을 기다려 주세요.
        </p>
      )}
      <p className="muted">
        Enrollment Token은 URL·명령행 인수·로그에 넣지 않습니다. 설치 시
        화면에 표시되지 않는 별도 입력 절차를 사용하며, Provider Credential과
        네트워크 준비는 별도 작업입니다.
      </p>
    </section>
  )
}
