# LBT-147 · VM 접속 검증 및 SSH 연결 관리 전체 변경

Jira: [LBT-147](https://samsunglions.atlassian.net/browse/LBT-147). 실행일: 2026-10-12 KST.
Branch: `feat/LBT-147-vm-access`. Merge-base: `6d675c7`. 게시 전 최신 main: `e850567`.
코드 커밋: `74c052f25e3778cafa6ae4fa75bd8775af46827a`.

이 Evidence는 LBT-147 전체 구현과 최종 P1/P2 수정을 포함한다. 로컬 OpenStack에서 실제 Management VM 접속·채널·두 generation lifecycle 및 app/Provider 실패·취소 경계를 검증했다. KT Cloud 선행 조건과 SaaS/Browser 전체 통합 완료는 별도로 남아 있다.

## 전체 구현 범위

- 현재 LabInstance/generation/논리 VM key/Provider Server 및 소유 metadata를 대조하고 잘못된 대상은 SSH 전에 거절한다.
- 기존 Management NIC/private IP·SSH 사용자·고객 로컬 키 참조와 Server별 host-key 검증을 재사용한다.
- VM별 SSH Connection을 lazy 생성하고 같은 VM의 PTY/SFTP Channel이 공유한다. 정상 Channel 종료는 다른 Channel을 닫지 않는다.
- 마지막 Channel 종료 뒤 configurable idle timeout으로 Connection을 정리한다. Runtime SSOT 0.1.7의 `LABBIT_OPENSTACK_SSH_IDLE_TIMEOUT` 기본값은 `1m`이다.
- 인증 실패, 도달 불가, handshake/setup timeout·cancel, 단절·shutdown에서 Connection/Channel을 정리하고 새 요청에서만 다시 연결한다.
- 대상 교체 및 Reset/Cleanup 후 기존 transport를 폐기하고 generation fence로 지연된 resolve/setup과 stale 접속을 차단한다.
- Terminal Control/Data의 오류 응답에 개인키·SSH raw error를 노출하지 않는다. Terminal/SFTP 본문을 Evidence에 저장하지 않는다.
- P1: 부분 Cleanup/Reset 및 replacement 생성 실패로 결과가 FAILED여도 삭제 시도가 있으면 기존 SSH를 폐기한다. 모든 DELETE_NOT_ATTEMPTED preflight 실패는 기존 연결을 보존한다.
- P2: Provider 초기화 중 network factory는 mutex 밖에서 수행한다. 공유 초기화 waiter가 자신의 deadline/cancel을 지키고 owner/live waiter·cache·실패 후 다음 요청의 재시도 경계를 유지한다.

정확한 요구사항 → 구현 → 자동 테스트 → 실제 실행 → 공백 표, 전체 구현 29파일 및 이 Evidence 파일별 검토는 [AUDIT.md](AUDIT.md)에 있다. 최초 구현·socket timeout 수정·2026-10-10 기록을 포함한 branch 전체 diff를 검토했으며 P1/P2만 감사한 것이 아니다.

## 실행 결과와 소스 일치

실환경은 Windows amd64, Go 1.27.1 + CGO/race, 로컬 `labbit-test`의 실제 Keystone/Glance/Nova/Neutron 및 Ubuntu VM이다. 실행 당시 HEAD `3996817` + 미커밋 6개 수정본을 이후 `74c052f`로 커밋했다. 전체 29개 실제 실행 byte SHA와 커밋 Git blob을 [source-manifest.json](source-manifest.json)에 연결했다. 이 디렉터리의 후속 커밋은 Evidence만 추가하며 검증 당시 코드와 같다는 근거를 남긴다.

| 검증 | 결과 | 경계 |
| --- | --- | --- |
| 전체 Go unit | 31 test package, 2,828 tests/subtests PASS | 7 SKIP: opt-in cloud 6개 + Windows POSIX 0600 assertion 1개. no-test package 9개는 별도 |
| 변경 5 package race | 364 PASS, cloud 6 SKIP | P1/P2 반복 race 20회 PASS; 수정 전 production 회귀 실패도 archive에 보존 |
| Go static / build | format, vet, build, diff check PASS | 같은 29개 소스의 앞선 감사 실행 |
| Web | typecheck/lint/build 및 13파일 163 tests PASS | 변경 없음. 기존 jsdom canvas/build chunk 경고 기록 유지 |
| Contracts | OpenAPI, JSON Schema 8개, Runtime/migration metadata, Reset schema 11 tests PASS | owning SSOT 및 metadata 검증 |
| 실제 정상 VM, race | PASS, 229.83s | Provision gen1 → pinned/shared SSH, target 거절, PTY IO/resize, SFTP probe, idle → Reset gen2/stale1 거절 → tracked14 ABSENT |
| 실제 app P1/P2, race | 부모 + 7 subtests PASS, 227.71s | P1 5분기, P2 2분기. tracked16 및 임시 SG/keypair ABSENT |
| 사전·사후 read-only discovery | PASS | 서버0/이미지3/flavor4/network4. matching test server/network/subnet/SG/port/keypair/router 모두0 |

선택한 실환경 실행은 parent/subtest 포함 12 PASS, 0 fail, 0 skip이다. unique tracked 자원은 정상14 + app16 = 30개다. 두 fixture의 보조 SG/keypair는 별도로 정리했다. 공유 image/flavor/network와 기존 자원은 그대로 유지했다. exact tracked-ID Reconcile/GET 부재가 주근거이며 prefix 목록은 보조 근거다.

P1/P2 app 검증에서는 **정확한 테스트 소유 DELETE409 3회, lab/gen2에 한정한 Nova POST409 1회 및 정상 Keystone 인증 응답 지연**을 loopback proxy로 주입했다. 실제 API 응답·Provider/app 처리·SSH·삭제/독립 조회·새 Neutron 생성은 실제 시스템이다. OpenStack 자체가 발생시킨 outage를 관측했다고 주장하지 않는다. Cleanup partial 사례의 worker404는 앞선 Reset에서 이미 삭제된 worker다.

전체 자동 검증과 실제 실행 결과를 각각 [automated-validation-summary.json](automated-validation-summary.json), [validation-summary.json](validation-summary.json)에 분리했다. 이번 packaging에서 과거 검사를 새 실행으로 표시하거나 cloud를 다시 생성하지 않았다. 원본 실패·fixture 초기 panic·독립 reviewer·전체 자동 로그 및 두 보조 harness는 [audit-logs.zip](audit-logs.zip)에 있다. 원본 실환경 JSONL 네 개는 이 폴더에서도 직접 읽을 수 있다.

## 남은 미검증·제약

- **KT Cloud / LBT-145:** 최신 댓글 기준 gen2 전체 Reset 성공 재실행과 테스트 소유 Tier 2개의 실제 부재가 미완료다. 이 로컬 PASS로 KT 선행 조건 또는 Jira/OP-02 전체 완료를 처리하지 않는다.
- **Browser/SaaS/배포 CC-02:** Terminal Control/Data WSS의 실제 VM 왕복·reconnect·public Gateway routing은 미검증이다. LBT-148 및 C2 LBT-22/26/27 후속 범위다.
- **Production File:** SFTP INIT/VERSION/REALPATH 및 PTY 종료 뒤 채널 유지만 검증했다. 파일 read/write와 File Control/Data WSS는 LBT-149/150에서 검증한다.
- 실제 SSH 인증 거절·host-key 공격/변경·packet loss/handshake stall, connector process restart, 장기 soak/다중 VM 부하는 이번 실제 실행에서 미검증이다. unit/in-process SSH peer/race 근거와 구분한다.
- API409·인증 지연은 통제된 장애 주입이다. 자연 발생 backend outage 및 다른 opt-in Provider suite는 실행하지 않았다. 모든 동시 waiter 도착 순서를 실환경에서 강제하지 않았으며 결정적 fixture 회귀를 함께 사용한다.
- 기존 `Session.Setup` 취소/timeout은 context 없는 SSH setup을 해제하기 위해 같은 VM의 shared transport를 폐기할 수 있다. 정상 채널 close 독립성은 검증했지만 setup 취소 시 다른 채널 유지까지 검증하지 않았다.
- PostgreSQL integration은 DB 변경이 없어 실행하지 않았다. scoped secret scan은 모든 unknown token 또는 Git history 전체 검사와 다르다.

## 재현과 artifact

Go/race/CGO compiler와 실제 VM SSH 경로를 준비한다. 아래 스크립트는 **실제 자원을 생성·삭제**한다. 앱 보조 harness는 해당 실행의 고정 fixture인 `ubuntu`, `m1.small`, `sharednet1`, `ubuntu` 사용자, Connector SSH CIDR `172.16.8.1/32`, Lab CIDR `198.20.0.0/24`와 `198.21.0.0/24`를 사용한다. HTTP private-IP catalog만 허용하는 로컬 환경용 harness다. 다른 환경에서는 정확한 조건을 준비하거나 보조 harness를 별도 감사해야 한다.

코드 커밋 또는 이 Evidence를 포함한 branch를 checkout한 뒤 실행한다. Cloud config/private key는 로컬 입력이며 archive에 포함하지 않는다.

```powershell
& ./docs/connector/evidence/LBT-147/2026-10-12/reproduce.ps1 `
  -CloudConfig '<local clouds.yaml>' `
  -PrivateKey '<matching local private key>'
```

스크립트는 코드 커밋 전체에 대해 Evidence 폴더 밖의 tracked/staged/untracked 변경과 ignored Go source를 거절하고, Git blob 및 archive/harness SHA도 확인한 다음 별도 임시 폴더에 overlay를 만든다. Windows PowerShell 5.1 및 PowerShell 7에서 읽을 수 있는 BOM 없는 UTF-8 overlay JSON을 쓴다. discovery → 정상 VM → app 경계 → 사후 read-only inventory 순서이며 원본 결과를 출력 폴더에 남긴다. checkout을 reset/apply하거나 production 파일을 바꾸지 않는다. 마지막 조회는 이름/prefix로 삭제하지 않는다.

게시 전 새 스크립트의 문법과 가상 overlay compile, raw JSONL/summary·archive entry/hash/CRC·코드 일치를 검증한다. [secret-scan.json](secret-scan.json)은 로컬 credential exact-value/private-key body/PEM/possible-token 검사 범위와 한계를, [artifact-checksums.json](artifact-checksums.json)과 [archive-manifest.json](archive-manifest.json)은 배포 원본을 고정한다. [publication-review.md](publication-review.md)에 독립 reviewer의 게시 전 재검토를 기록한다.

이 폴더의 `.gitattributes`는 자동 줄바꿈 변환을 막아 기록 artifact SHA를 유지한다. [publication-checks.json](publication-checks.json)에 게시 시 검증을 분리해 기록한다.

재현 script의 source guard는 PowerShell 7 및 Windows PowerShell 5.1에서 각각 6케이스를 기존 Go wrapper 유/무로 반복해 모두 PASS다. [verify-reproduce-guards.ps1](verify-reproduce-guards.ps1)은 별도 local clone에서 Go를 실패 stub으로 바꾸어 실제 cloud 호출 없이 source 변경 거절·정상 준비·BOM 없는 JSON을 검증한다. 기존 wrapper 복원·임시 wrapper 제거도 PASS이며 [reproduce-guard-regression.json](reproduce-guard-regression.json)에 원본 케이스 결과를 기록했다.
