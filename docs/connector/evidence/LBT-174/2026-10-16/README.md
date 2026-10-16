# LBT-174 로컬 Preview 통합 Evidence

[Jira LBT-174](https://samsunglions.atlassian.net/browse/LBT-174)의 로컬 OpenStack + Mock Preview Gateway 검증 기록이다. 정책 원본은 `contracts/connector/README.md` §7b와 Preview Control/Data schema다. 최신 원문 조회 버전은 [ssot-versions.json](ssot-versions.json), 변경별 추적성·실패 기록·최종 판정은 [AUDIT.md](AUDIT.md)에 기록한다.

## 검증 경계

실제 `app.BuildConnector`의 Control WSS listener → Preview WSS → 공유 VMAccess SSH `direct-tcpip` → 로컬 OpenStack Ubuntu Workspace의 HTTP 서버 경로를 실행한다. Provision/Reset/Cleanup도 실제 Provider와 Control Operation 경로다. SaaS 인증·승인·Preview Gateway는 loopback Mock이며 실제 Browser/SaaS 인가 E2E와 KT Cloud 검증은 포함하지 않는다. HTTP·PTY 내용과 credential은 메모리에서 검증하고 로그에 남기지 않는다.

실제 검증은 HTTP/1.1 binary stream 왕복, sequential 두 tunnel, 서로 다른 PreviewSession의 CLOSE 격리, Preview 종료 뒤 PTY/SFTP 사용, 안전한 오류 3종, Reset 후 stale generation 거부·새 generation HTTP, Cleanup과 Lab 자원 부재 확인을 포함한다. stale attach/Control owner 경합/무응답 Gateway/blocked writer/worker 상한은 자동 loopback 또는 Fake 테스트다.

## 재현

Go는 `go.mod` 버전을 사용한다. 일반 회귀는 외부 Provider 설정 없이 `go test ./...`로 실행된다. 전체 저장소 기본 검증은 `make test`이며 Windows에서 make가 없으면 AGENTS.md에 적힌 개별 Go/웹 명령을 실행한다. race 검사는 C compiler가 필요하다.

실환경 fixture는 기본 skip이다. 폐기 가능한 로컬 OpenStack cloud의 보호된 `clouds.yaml`, Connector에서 관리망으로 SSH 가능한 경로, cloud의 정확한 image/flavor/network 이름, OpenSSH Ed25519 개인키를 준비한다. 키는 저장소 밖 또는 ignored 위치에 두며 Public Key를 임시 Nova keypair로 등록하는 작업은 fixture가 수행한다. Ubuntu image에는 SSH와 Python 3가 필요하다.

```powershell
# Repository root. 값은 자신의 로컬 cloud에 맞춘다.
./docs/connector/evidence/LBT-174/2026-10-16/reproduce-local.ps1 `
  -ConfigFile C:/protected/clouds.yaml -Cloud local-test `
  -PrivateKeyFile C:/protected/local-fixture-key `
  -ManagementNetwork sharednet1 -SSHCIDR 172.16.8.1/32
```

fixture는 임시 management security group/keypair와 Lab topology를 생성한다. 추적한 Lab 자원은 정확한 ID로 정리한 뒤 GET 부재를 확인한다. `entries` 수는 generation별 결과 기록의 중복을 포함하며 고유 자원 수가 아니다. management SG/keypair 정리는 기존 helper의 DELETE acknowledgement 수준이며 별도 GET 부재 확인을 보장하지 않는다. 실패 시 원본 결과와 teardown 결과를 함께 보존한다.

## 알려진 한계

- 한 Workspace VM의 로컬 관리망 경로다. 여러 VM의 target 격리는 VMAccess/SSHForwarder 자동 회귀로 확인한다.
- 실제 PTY/SFTP는 공유 SSH channel 경계이며 전체 Browser Terminal 또는 File Data WSS UI의 E2E가 아니다.
- 프로세스 재기동 시 Preview는 ephemeral 상태다. Provider 작업 재시도·DB 복구·Reconciliation 정책 변경은 없다.
- 원격 CI, PostgreSQL tagged integration, 실제 KT Cloud, Browser/SaaS C3는 별도 범위로 기록한다. 로컬 PASS를 해당 검증의 PASS로 확대하지 않는다.
- 초기 실패, 기준 main에서도 재현한 기존 Realtime 테스트 불안정성, 부하 중 SSH timeout은 AUDIT에서 구분한다.
