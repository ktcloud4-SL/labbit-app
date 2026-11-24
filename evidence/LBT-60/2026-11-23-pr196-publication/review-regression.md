# PR196 독립 회귀·Evidence·WSS 리뷰 — 2026-11-23

Reviewer: /root/audit_pr196_regression. 제품 구현과 분리된 read-only 검토.
`C:/Users/user/Documents/ChatGPT/3차 2/PRE_MERGE_AUDIT.md`를 읽고 적용했다.

## 고정 범위

- Repository: ktcloud4-SL/labbit-app, checkout `tmp/labbit-dr-stage3-offline-20261121`.
- base origin/main `753eecbff737409a4c9cc8c542f3c6baa6acf0da`; HEAD/PR196 `c11b70a8e9fb81691e0fadb6b4114ee9a5f47fac`.
- 새 로컬 변경은 아직 미커밋이다. 이 보고서는 저장·push 권한이나 배포 승인 자체가 아니다.
- 담당: installer/OpenStack 6개 test diff 전체, 이전 PR의 registry/connectorwss close 변경과 직접 consumer, 11/21 Evidence 세 디렉터리, workflow trigger/deploy guard.
- operator/worker 신규 기능 전체 감사는 다른 reviewer 담당이다. 나는 새 cloud IAM/PG/실Pod/Provider 시험을 수행하지 않았다.

## Findings

검토한 범위에서 새 confirmed blocking defect 없음. 기존 미검증은 아래 제한으로 유지한다.
소스 수정은 하지 않았다. 따라서 수정 전 finding 보고 후 수정이라는 절차를 필요로 하는 새 결함은 없다.

## 요구→구현→검증 추적

| 요구/SSOT | 구현·review | 검증 | 한계 |
|---|---|---|---|
| TESTING: production 보호 권한 유지, 환경 불가 경로는 명시 | `ktcloud_d1_cidr_test.go` Windows guard/fail-closed 테스트, runtime.GOOS 기반 outer guard | Windows full raw JSON의 4527PASS/0FAIL/88SKIP 및 race566PASS/80SKIP 독립 재집계; Linux raw645PASS/15SKIP | Linux 실제 Provider14개 opt-in 미실행 + Windows-only1개 SKIP |
| TESTING: 실제 Linux positive 실행과 compile-only 구분 | `requireProtectedCIDRFixture`, late ACTIVE/readiness/management-route 및 CIDR concurrency | Linux JSON에서 outer3개 및 concurrent Test 각각 PASS 확인. stage3 source29개와 payload source103개 size/SHA 현재 일치 | QEMU Linux RAMfs/loopback fixture, non-race; 디스크 powerloss/Ubuntu 운영 시험 아님 |
| fixture 조기 종료에서 hang 방지 | concurrent entered/finished/deadline/releaseOnce, timeout ctx | retained baseline timeout, Linux final concurrency PASS 확인 | 실제 Provider concurrency 검증 아님 |
| shell proof/host pin/auth 유지 | readiness_test의 Windows SSH peer만 Git sh 선택 및 Start error 검사 | 기존 full JSON positive와 source 대조. 실제 Connector shell fallback 추가 없음 | 실제 VM SSH 미실행 |
| installer Linux semantics 유지 | syntax/behavior 분리, Windows behavior만 skip | Linux final5PASS/0FAIL/0SKIP; 최초 curl누락3PASS/2FAIL 보존 및 guest result tuple0:1:0:0 확인 | systemctl/curl/helper는 fixture. Ubuntu/systemd 설치 인수 아님 |
| Connector 계약 README §4/§15의 replace/revoke | registry.closeNotified, Retire/Release lock scope; controlConn request latch/release; handler defer 순서 | independent go test -race -count=3 targeted7 tests, 두 패키지 성공 | 실제 remote network/Valkey peer 아님 |
| 변경 source와 원본 Evidence 연결 | 세 source/artifact manifests, Linux payload/harness | 원본 hash/size 아래 참조 | Git LF checkout에서는 raw hash와 CRLF 파일 원본 차이를 canonical 비교와 별도 설명해야 함 |
| AWS/서울 변경 금지 | 기존 Backend pull_request build `push:false`, AWS OIDC/ECR 및 GitOps 갱신은 PR 제외; main/tag만 publish; Connector tag-only | .github/workflows diff 없음, trigger/step if 원문 확인 | 실제 workflow 실행·원격 effect는 이 reviewer가 수행 안 함. PR build에는 GHCR login/cache가 있으나 AWS/배포 mutation은 없음 |

## 무결성·집계 독립 확인

1. `2026-11-21-stage3-offline/source-manifest.json`: 23/23 현재 source size/SHA 일치.
2. 같은 artifact-manifest: 24/24 size/SHA 일치(이 manifest는 repository-relative path).
3. `2026-11-21-regression-diagnosis/source-manifest.json`: 29/29 현재 source size/SHA 일치.
4. 같은 artifact-manifest: 17/17 size/SHA 일치(directory-relative path).
5. `2026-11-21-linux-offline/artifact-manifest.json`: 18/18 size/SHA 일치.
6. Linux harness-manifest: helper6/6 size/SHA 일치. payload source103/103 현재 raw size/SHA 일치.
7. JSON terminal Test 이벤트 독립 재집계: Stage3 related368PASS; PG integration1699PASS/10SKIP; Windows full4527PASS/88SKIP; Connector race566PASS/80SKIP; Linux OpenStack645PASS/15SKIP; installer final5PASS; symlink1PASS; credential5PASS. FAIL 모두0이며 초기 실패는 별도 원본에 보존.
8. 초기 fullGo4002PASS/39FAIL/15SKIP, initialInstaller3PASS/2FAIL, baseline timeout/PTY/installer 실패도 원본 그대로 확인. 최종 PASS로 과거 FAIL을 덮지 않았다.
9. Linux guest `@@GUEST_RESULTS:0:0:0@@`와 Power down, initial failure tuple 및 Power down을 각각 확인. QEMU exit0을 테스트 전체 성공으로 해석하지 않음.
10. AWS access key 형식/Slack webhook/private-key PEM 고신호 패턴을 Evidence 전 파일에 검사해0개. 이 제한된 패턴 검사가 모든 종류 비밀의 부재를 증명하는 것은 아니다. 합성 fixture/localpath가 원본 로그에 존재하며 문서에서 명시한다.

## 이번 reviewer 직접 실행

2026-11-23 Windows Go1.27.1, `go test -race -count=3 -timeout 90s ./internal/server/connector ./internal/server/connectorwss -run 'Test(CloseNotificationWaitsAcrossSealedRouteAndLateCallback|RetireCallbackMayReenterRetireWithoutWaitingOnItself|ReleaseWaitsForRequestedCloseFrameAndSealsLateRequests|ConcurrentShutdownAndRetireShareOneCloseAttempt|RetirementAfterAckBeforeReadyPreservesCloseCodeAndNewSession|RevokeConnectorHookClosesActiveConnectionWith4001|NewConnectionReplacesOldWith4002)$'`.

두 패키지 exit0, 각각3.046s/4.723s. WSS real loopback과 fake control-frame 경계이며 AWS/서울/DB/Slack/Provider 호출 없음.
전체Go/VM/PG를 이번 reviewer가 다시 실행한 것은 아니며 원본+소스+hash/집계를 검토한 것이다.

## 보안·동시성·수명주기 및 제한

- Registry.retire는 entry.mu 밖에서 route seal과 transport callback을 수행하며 callback 재진입을 기다리지 않는다. handler는 Release 후 이미 publish된 notification만 기다린다. controlConn는 requestMu로 late-close/release를 seal하고 이미 요청된 frame attempt 후 TCP cleanup 한다. 새 session을 이전 release가 지우지 않는 소유권이 유지된다.
- c.mu write timeout와 closeGrace timer는 기존 정책을 유지한다. 실제 OS/network cancellation 동작이나 모든 peer failure 경우를 증명하지 않는다.
- Windows POSIX skip은 제품 권한 검사를 낮추지 않는다. 새 negative는 callback/HTTP0 및 기존 intent bytes 불변을 확인한다.
- Linux source/binary/build/environment/rawlog가 구분되어 있고, noNIC/noDisk/noHostShare 구성과 빈 credential payload를 기록한다. RAMfs fsync API는 실제 디스크 재부팅 내구성 증거가 아니다.
- kernel/APK publisher signature 미검증, old QEMU dirty build, Windows APP 고정경로와 download pinning 재현 한계는 Linux AUDIT에 명시돼 있다.
- main merge 후에는 기존 backend Delivery 및 Seoul GitOps/PreSync migration 경로가 있으므로 Draft·merge 금지와 실제 migration/release gate를 유지해야 한다.
- 기존 ReplicaLag FAIL, 4NOT_VERIFIED, execution_gate=HOLD/recovery_authorized=false/cloud_verified=false 유지. 실제 multi-Pod/승인시스템/SingleWriter/Fencing/Failover/Failback 미검증.

## 판정

담당한 회귀 test diff·Evidence·WSS·PR push 영향 범위에서 새 blocking finding 없음. 현재 raw Evidence는 집계와 해시가 맞으며 알려진 초기실패/skip/운영 제한을 보존한다. 이 판정은 PR196 전체 승인·실환경 운영 인수·DR GO나 merge 허가가 아니다. 최종 parent audit에서 나머지 operator/worker 범위와 최종 commit의 source identity/CI를 합쳐야 한다.
## 증분 검토 — Evidence Git 바이트 보존 (2026-11-23)

Parent가 공개 준비 중 확인한 F-01 Medium: core.autocrlf=true에서 원본 로그의 raw blob과 Git path-filtered blob이 달라져 manifest SHA/원본 인계가 깨질 수 있음. Parent는 수정 전에 finding을 보고한 뒤 `evidence/.gitattributes`에 `* -text`를 추가했다. 이번 reviewer는 제품 코드나 Git 상태를 수정하지 않았다.

Read-only 재검증 결과:

- `evidence/.gitattributes`의 적용 범위는 App `evidence/` 하위이며 `* -text`로 checkin/checkout 줄바꿈 변환을 비활성화한다.
- 현재 Evidence 전 파일 93개를 각각 `git hash-object --no-filters`와 `git hash-object --path=<same-relative-path>`로 비교: **93/93 일치, 차이0**.
- 기존 11/21 artifact manifests 재검증: Stage3 24/24, regression17/17, Linux18/18 모두 원본 크기/SHA256 일치.
- 제품 source29개 수정이 없는 상태라는 parent 보고와 별개로 이번 증분은 Evidence만 담당했다. 새 11/23 PG 재시험은 아직 진행 중이므로 PASS·완료로 수용하지 않았다.
- 첫 표적 확인 명령은 `pg-start.log`의 디렉터리를 11/21로 잘못 지정해 file-not-found를 반환했다. 전파일93개 검사는 실제 존재 파일을 열어 독립 수행한 결과이며 이 경로 오타와 무관하다. 실제 경로의 표적 blob은 후속 출력으로 parent에게 공유했다.

F-01에 대한 바이트 보존 교정은 검토한 파일 범위에서 유효하다. 최종 commit/index 등록 이후 원본 blob 크기·SHA256을 다시 대조하면 공개 결과까지 검증할 수 있다. 기존 운영 미검증·HOLD와 신규 PG 진행중 상태는 그대로 유지한다.