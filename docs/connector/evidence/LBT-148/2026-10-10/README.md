# LBT-148 · 실제 VM PTY / CC-02 연결 검증

Jira: [LBT-148](https://samsunglions.atlassian.net/browse/LBT-148).
실행일: 2026-10-10 KST. Branch: `feat/LBT-148-pty-cc02`.
Base: LBT-147 local head `3996817` (`feat/LBT-147-vm-access`).
Fetched main: `6d675c74bff11f481d247267faefd4ce960dcd38`.
Tested source: `1af5c8e590c41c97fcbfca0a6b46384ec556035e`.
후속 evidence commit은 구현·테스트·계약·의존성을 변경하지 않는다.

사용자 요청으로 LBT-147의 로컬 결과 위에서 LBT-148을 진행했다. 두 branch 모두 push/PR을 만들지 않았고 Jira도 변경하지 않았다. LBT-145의 KT Cloud 작업은 미완료이며 이번 검증은 고객 로컬 OpenStack만 사용했다.

## 검증 범위

기존 Git Terminal wire 및 main의 CC-02 Handler/SessionManager/DataWSSClient를 유지했다. 실제 VM은 기존 OP-01 Provision으로 생성하고 Management NIC, 고객 로컬 key 및 Provider가 등록한 Server별 host key를 통해 접속했다. Mock SaaS Control WSS와 두 Mock Relay Data WSS를 같은 Go integration harness에서 실제 Connector Handler에 연결했다. 모든 Terminal/SFTP 본문은 메모리에서만 검증하고 기록하지 않았다.

| 요구/회귀 | 결과 |
| --- | --- |
| Control `TERMINAL_OPEN` → 실제 VM PTY → Data `ATTACH/ATTACHED` | attach 이후 OPEN_RESULT=SUCCEEDED. 동일 VM의 두 TerminalSession에 별도 PTY/Data WSS |
| PTY INPUT/OUTPUT, 초기 size, resize | 실제 shell의 `stty size`로 80×24 및 100×30, Data RESIZE 후 120×40 확인 |
| shared SSH 및 채널 독립성 | 같은 VM의 SSH identity 공유. PTY A의 Data CLOSE 뒤 PTY B와 SFTP REALPATH 응답 유지 |
| 중복 OPEN | active PTY와 기존 Data connection 유지. 종료된 TerminalSession ID로 새 PTY를 할당하지 않음 |
| Data reconnect/grace | 실제 shell 변수·TerminalSession·SSH 연결을 유지하며 자동 재attach. harness grace는 3초로 단축, production은 기존 60초 |
| grace 만료 | Relay를 사용할 수 없게 만든 뒤 GRACE_TIMEOUT, PTY 정리, 늦은 중복 OPEN 거절 |
| 원격 shell 종료 | 마지막 출력 확인 후 PTY_EXITED/exitCode=7이 Control/Data 양쪽 ENDED에 전달됨. exitCode=0 및 stderr는 in-process SSH/회귀 테스트로 검증 |
| SSH 단절 | SSH_DISCONNECTED 및 안전한 오류가 Control/Data ENDED에 전달. 세션/PTY 0, 요청 없는 SSH 재접속 없음 |
| 출력/종료 정합성 | Reader의 n>0+EOF 마지막 bytes 보존, SSH stdout/stderr 모두 drain, 관측된 shell 결과를 기다려 exit code와 단절 구분 |
| WSS 취소/교체/전송 실패 | 진행 중 attach 취소 시 socket 정리, reconnect owner 교체 시 이전 client 종료, Data writer가 막혀도 10초 제한 뒤 socket 정리 |

실환경 원본은 [automated-logs.zip](automated-logs.zip)의 `real-pty-cc02-final.jsonl`과 `real-post-cleanup-final.jsonl`이다. 초기 source `513869c`의 통과 기록도 포함했으며, 최종 source의 결과와 구분한다.

## 자동 검사와 정리

- 전체 Go vet/unit/build, 변경된 5개 package race PASS.
- 출력/exit/중복 OPEN/attach 취소/reconnect owner 회귀 5개 top-level test를 race 포함 각각 20회 PASS.
- 멈춘 peer에 대한 실제 unbuffered socket으로 production Data write deadline과 EOF 정리 PASS.
- Go 파일 316개를 LF 정규화 후 gofmt와 비교해 formatting 차이 0. Windows CRLF checkout을 이유로 관련 없는 파일을 다시 쓰지 않았다.
- Web typecheck/lint/build 및 13개 파일 163개 테스트 PASS. Web/의존성 변경 없음.
- OpenAPI, JSON Schema 8개, Runtime/Migration metadata, Reset schema 11개 테스트 PASS.
- 실제 테스트가 추적한 고유 리소스 7개 모두 cleanup 후 ABSENT. 임시 keypair/Management SG는 기존 fixture의 별도 cleanup에서 삭제. 최종 read-only discovery servers=0/networks=4.

Windows에 make/npm이 없어 `make setup`/`make test`에 해당하는 Go/Web target을 직접 실행했다. bundled pnpm으로 기존 package-lock에서 임시 lock을 import해 install했고 그 lock은 제거했다. 일반 suite의 opt-in cloud tests는 SKIP이며 위 real test는 별도로 enable해 PASS를 확인했다. PostgreSQL 변경은 없어 DB integration은 실행하지 않았다.

원본 자동 로그, final source 및 결과 수치는 [validation-summary.json](validation-summary.json), 변경 blob은 [source-manifest.json](source-manifest.json), 로컬 민감값 일치 검사는 [secret-scan.json](secret-scan.json), artifact SHA-256은 [artifact-checksums.json](artifact-checksums.json)에 있다. 텍스트 artifact의 checksum은 Windows checkout의 CRLF를 LF로 정규화한 bytes를 기준으로 한다. 원본 로그에는 Terminal transcript, 실제 key/credential, private endpoint를 싣지 않았다.

LBT-148은 Mock SaaS/Relay ↔ 실제 VM의 CC-02 중간 통합이다. 실제 SaaS/Browser/Live 전체 C2 acceptance, KT Cloud D1 호환성, production SFTP Tree/Read/Save 완료를 주장하지 않는다. SFTP는 이 단계에서 채널 독립성 probe만 실행했다. LBT-147을 나중에 squash merge하면 LBT-148의 자체 commit을 최신 main 위로 정리해 검증한 뒤 PR을 만들 수 있다.

## 재현

1. [reproduce.ps1](reproduce.ps1)을 repository 밖에 저장한다.
2. tested source SHA를 깨끗한 checkout에서 선택한다.
3. 고객 로컬 clouds.yaml, matching SSH private key, 동일 image/flavor/Management network를 준비한다.
4. repository root에서 실행한다. 이 테스트는 실제 VM과 generation-owned 리소스를 생성한 뒤 추적한 ID만 정리한다.

```powershell
& '<repository 밖의 reproduce.ps1>' `
  -ProviderConfigFile '<customer-local clouds.yaml>' `
  -Cloud '<local cloud name>' `
  -SSHPrivateKeyFile '<customer-local private key>'
```

환경별 선택값은 `LABBIT_OPENSTACK_TEST_IMAGE`, `LABBIT_OPENSTACK_TEST_FLAVOR`, `LABBIT_OPENSTACK_TEST_MANAGEMENT_NETWORK`, `LABBIT_OPENSTACK_TEST_SSH_CIDR`, `LABBIT_OPENSTACK_TEST_LAB_CIDR`로 주입한다. defaults 및 cleanup assertions는 `pty_cc02_integration_test.go`와 기존 integration helper가 원본이다.
