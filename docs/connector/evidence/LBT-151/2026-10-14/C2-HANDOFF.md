# LBT-22 / C2 인계

LBT-151은 KT D1의 production Connector→실제 VM 구간을 검증한다. SaaS peer는
synthetic trusted TLS server이며 Browser/Backend authority/DB/Relay의 공동 통합은
LBT-22에서 수행한다. 실제 실행 결과와 실패·Known Limit은 `AUDIT.md`,
`validation-summary.json` 및 per-attempt raw logs/ledgers를 확인한다.

C2는 API credentials가 있는 `ktcloud-d1` cloud alias, verified CA trust, project 권한,
미사용 `/24` Lab·Management 대역, 기존 Connector DMZ Tier의 NSM networkId와
private IPv4 `/32`, Nova에 등록한 전용 Ed25519 key와 protected per-Server known_hosts,
Ubuntu SSH user가 필요하다. 웹 콘솔 로그인은 API 인증의 선행조건이 아니다.
이번 Ubuntu24.04 image/flavor는 bootstrap README에 기록했다. 생성 당시 snapshot의
image/flavor/VM key/generation과 현재 ProviderResource exact IDs를 SaaS authority가
resolved target으로 보내야 한다. Browser cookie/sessionToken을 Connector에 보내지 않는다.

Connector Runtime은 production environment, HTTPS SaaS base URL,
connector credential **file**, Provider config **file**, SSH key/known-hosts,
`LABBIT_WORKSPACE_ROOT=/var/lib/labbit-workspaces/workspace`,
`LABBIT_WORKSPACE_SFTP_HELPER=/usr/local/libexec/labbit-workspace-sftp`를 준비한다.
두 workspace 값은 이 fixture의 입력이며 제품의 고정 경로 정책이 아니다.
C2의 VM template/startup은 `cmd/labbit-workspace-sftp`에서 빌드한 현재 Linux helper를
root-owned executable로 설치하고 workspace root의 실제 user/권한을 준비해야 한다.
delay/connection-probe wrapper는 검증용이며 운영 설치에 필요하지 않다.

Control `/connector/v1/control` (`labbit.connector.v1`) HELLO의 `file-v1` capability를
확인한다. Terminal은 `/connector/v1/terminal-data`
(`labbit.connector-terminal.v1`), File은 `/connector/v1/file-data`
(`labbit.connector-file.v1`)로 별도 outbound WSS를 사용한다. endpoint URL에는 raw
token을 넣지 않는다. Terminal은 current Lab/generation/Terminal identity에 bind하고
File은 인증 Connector 및 FileRequest/Lab/generation/VM/server 모두 exact match한다.
Tree/Read/Save path/body는 File Data에만 보내며 Binary0byte도 frame1개다.
정상 File 결과 후 SaaS가1000으로 종료한다.

default idle SSH1m, PTY grace60s, File request30s를 변경하지 않고 검증했다. VM SSH
Ready는 현재 Provider3m이며 attempt2는 이 제한에서 실제 실패했다. 통합 환경의
image/cloud-init/route/firewall/키가 이 조건을 만족하는지 먼저 확인한다. UNKNOWN
mutation/cleanup은 actual known-ID Reconcile 후 판단하며 blind retry하지 않는다.
Reset/Cleanup 전에 SaaS가 active Terminal/File을 close하고 기존 runtime 작업을
정리해야 한다. actual old-resourceabsence와 generation authority도 확인한다.

C2에 남은 actual 전체 경로: login/Class 권한/READY/Workspace authority, HTTP File
consumer/timeout·cancel/unknown-save 표시, Browser Terminal refresh/sessionStorage/
grace, credential revoke 전체Data4001+다음401, Live read-only/nohistory/slowconsumer,
Backend Reset/Cleanup orchestration 및 runtime owner routing 제약. LBT-151의 단일
File4001 또는 synthetic application-result loss를 이 범위의 완료 증거로 쓰지 않는다.
Terminal/Live/File body·목록·경로·raw credential을 logs/trace/DB에 남기지 않는다.

테스트 VM과 리소스는 검증 뒤 정리한다. C2 환경은 fresh owned fixture로 준비하며,
이 Evidence의 삭제된 VM ID·keypair를 실제 접속 정보로 재사용하지 않는다.
