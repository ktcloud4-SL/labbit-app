# PR196 로컬 보완분 공개 전 최종 감사 — 2026-11-23

사용자 `PRE_MERGE_AUDIT.md`를 읽고 수행한 전체 diff 및 증거 검토다. 결과의 정량 원본은
`test-summary.json`, 실행 명령·UTC는 `commands.json`, 파일별 범위는 `diff-inventory.md`를 따른다.
이 디렉터리는 실제 AWS·서울 서비스·공유 DB·Slack을 호출한 기록이 아니다.

## 고정한 기준과 공개 범위

- 저장소: `ktcloud4-SL/labbit-app`, 기존 Draft [PR196](https://github.com/ktcloud4-SL/labbit-app/pull/196).
- 11/23 원격 fetch main: `753eecbff737409a4c9cc8c542f3c6baa6acf0da`.
- 원격 PR/로컬 시작 HEAD: `c11b70a8e9fb81691e0fadb6b4114ee9a5f47fac`. 두 원격 ref는 11/21 이후 불변이었다.
- 로컬 구현 branch: `codex/feat/LBT-60-stage3-offline-20261121`. 공개 시 HEAD를 기존 PR head branch로
  fast-forward push한다. main·tag·기존 dirty worktree를 수정하지 않는다. force push·merge·ready 전환은 없다.
- base→기존 PR 전체 변경과 새 tracked/untracked 보완분, 계약·migration·test·문서·Evidence를 함께 검토했다.
  제품/계약/테스트/문서 전체72개 파일별 raw SHA·LF 정규화 SHA·예상 Git blob은 `publication-source.json`에 고정했다.
- App 보완 source 29개는 직전 11/21 회귀 manifest와 raw bytes가 동일했다(`source-manifest.json`).
  새 기능을 다시 수정한 것이 아니라 기존 로컬 후보를 검증해 같은 PR로 인계하는 작업이다.
- GitOps/Infra의 별도 로컬 profile·22단계 Adapter 후보 코드는 이 App PR에 포함하거나 push하지 않는다.
  이전 11/21 문서의 세 저장소 서술은 당시 작업 기록이며, 세 저장소의 원격 반영 증거가 아니다.
- 기존 원본 admission worktree의 다른 미커밋 파일 8개는 복사·수정하지 않았다.

## 최신 원문·Owning SSOT

11/23 연결 도구로 [LBT-205](https://samsunglions.atlassian.net/browse/LBT-205)의 최신 본문
(updated 2026-11-23 09:04 KST), [LBT-59](https://samsunglions.atlassian.net/browse/LBT-59),
[LBT-60](https://samsunglions.atlassian.net/browse/LBT-60) 본문과
[검증 전략 9666637 v4](https://samsunglions.atlassian.net/wiki/spaces/SL/pages/9666637)를 확인했다.
GitHub PR196 exact-head 리뷰도 읽고, 이전 WSS/fixture P1 해결과 남은 운영 Gate를 구분했다.
Jira/Confluence에 새 댓글이나 판정을 쓰지 않았다. 사용자 대화의 Worker 포함 B안·결정권 위임은
코드 준비 범위에 적용하며 AWS/운영 조작 승인으로 확장하지 않는다.

AGENTS/CONTRIBUTING/TESTING, `docs/backend/README.md`, HTTP OpenAPI, Runtime0.1.13 후보,
WorkerControl·maintenance 통합 가이드, migrations12~14와 producer/consumer를 대조했다.
Connector/Realtime 메시지는 신규 보완분에서 변경하지 않는다. 기존 WSS 변경은 종료 순서의 경쟁 교정이며
close code4001/4002와 session 소유권 계약을 유지한다. 기존 HTTP 변경은 503 maintenance 및
권한·committed replay200 경계를 명시한다. 새로운 public HTTP/WSS endpoint는 추가하지 않는다.

## 요구 → 구현 → 테스트 → 실제 증거

| 요구·근거 | 구현 위치 | 자동 검증 | 실제 범위·공백 |
| --- | --- | --- | --- |
| LBT205 전체 접수 경로 차단·권한·replay 유지 | `labexecution`/HTTP 기존 #194 재사용, `app/control_stack.go:150` 인근 HTTP gate 유지, OpenAPI | HTTP/App `TestDRMaintenance*`, 기존 admission 회귀 | 폐기 PG·loopback 두 stack. 실제 전체 serving Pod/API·현재 서울 drain 검증 아님 |
| 기본 #194 유지, 승인 Worker만 별도 허용 | `workercontrol/settings.go:14`, `maintenance/gate.go:96`, `app/control_stack.go:143` | `TestControlledWorkerStillRequiresValidFileAndCountsDrain`, `TestApprovedControlProcessesExistingWorkWhileNewHTTPStaysClosed`, 설정/조립 거부 회귀 | default MAINTENANCE 유지. APPROVED_CONTROL은 postgres+configured file 필수. 실제 Pod 배포 미실행 |
| exact Scope/revision/epoch/Pod/boot 및 claim 이후 dispatch 제어 | durable Control·Executor·Router·Reconciliation, migration12 | 기존 WorkerControl/PG 및 M196 peer Pause·UNKNOWN 회귀 | 실제 로컬 PG 경쟁·MockProvider. EKS N=2/Valkey·실 Provider 및 Single Writer 미검증 |
| 새 승인·fresh recheck·고정 원본 | `drcontrol/contract.go`, `verified.go:20,74,87` | strict/casefold/UTF8/크기·목적분리/Target/TTL/만료·FAIL/NV/변조 거부 | Identity/S3 Runner는 Fake, 키는 합성. 신뢰 수집자·키 발급/회전/운영 승인 미인수 |
| S3 VersionId·원본 SHA/크기·bounded cleanup | `drcontrol/awscli.go:93` | `TestFixedVersionReaderAndCleanup`, `TestHeadMismatchStopsBeforeDownload` | Fake CLI Runner·실 로컬 임시파일. AWS 호출·실 IAM·실제 지연 미검증 |
| authoritative DB exacttarget/TLS | `drcontrol/database.go:13,36`, `awscli.go:147` | `TestDatabaseConfigurationNeverFallsBack`, `TestSignedOperatorWithDisposablePostgres` | PG16 실제 SQL/INTENT/APPLIED와 stale binding. 실제 RDS·TLS/IAM·Writer Fencing은 아님 |
| 인증 후 history 조회·INTENT/UNKNOWN 자동 재전송 금지 | `verified.go:104`, 기존 `workercontrol/resume.go`, `postgres/worker_control_resume.go:73,133` | auth-before-journal/unknown/no-reapply/history, PG Begin↔Apply·응답유실 회귀 | Fake 응답 유실과 실제 PG를 구분. 운영 장애 주입·복원 후 journal 유실 미검증 |
| 플랫폼 보안 검사 유지·실제 Linux 실행 구분 | installer/OpenStack test6개, 기존 production 권한 검사 불변 | Windows fresh/INTENT fail-closed·callback0; 11/21 실제 Linux645PASS/15SKIP 및 installer5PASS | noNIC RAM Linux/loopback, non-race. Ubuntu/systemd·실 Provider·디스크 powerloss 내구성 아님 |
| schema·release 수명주기 | 기존 additive12~14, `docs/backend/dr-stage3-deployment.md` | 폐기 PG 전체 integration·migration 원본 checksum/이전버전 fixture 회귀 | 공유 DB 백업/복원·migration·고정 image·작업창은 미실행 Gate |

## 수정 전 findings와 처리

이번 전체 대조·세 독립 검토 범위에서 새 제품 blocking defect는 발견되지 않았다. 공개 준비에서 증거 바이트 보존 결함 F-01을 확인해 아래처럼 교정했다. 따라서
제품 코드를 추가 교정하거나 안전 조건·테스트 기대값을 낮추지 않았다. 이전 JSON alias·key 분리·binding 유형·
APPROVED_CONTROL 조립/계약 및 Windows fixture findings와 해결 근거는 11/21 감사에 보존한다.
이전 WSS/PG fixture P1은 c11b70a 리뷰에서 해결 확인된 항목이며 현재 실패로 다시 기재하지 않는다.
실제 Pause 진입점·수집자 부재는 아래 운영 한계로 유지하며 이번 코드 수용 범위를 낮춰 DR 완료로 처리하지 않는다.

### 실행 harness의 환경 오류 (제품 수정 없음)

| ID·심각도 | 파일·라인 | 재현·영향 | 처리·재검증 |
| --- | --- | --- | --- |
| ENV-01 / 검증 환경 | `verify-initial.py:15`, `internal/server/app/valkey_multi_replica_integration_test.go:105` | Valkey 없는 로컬에서 harness가 CI=1을 넣어, 원래 skip하는 Valkey 9개가 필수 실행으로 실패. 제품 결함·PG 실패가 아님 | 수정 전 사용자에게 원인 보고. 원본 `integration-all.jsonl`과 초기 harness 보존, CI를 주입하지 않는 로컬 기준으로 새 PG fixture에서 전체 6package 재실행. 최종 skip과 PASS 분리 |
| ENV-02 / harness 실행 | retry의 Python metadata 로딩 | Windows cp949 기본 decode가 UTF-8 commands.json을 읽지 못해 시험 시작 전에 중단 | Python `-X utf8`로 재호출. 이 시도는 PG·AWS·제품 테스트를 실행하지 않음 |

ENV-01은 `verify.py`에서만 수정했으며 제품 코드·CI workflow·Valkey 필수 조건은 그대로다.
원격 CI는 기존 Valkey service를 제공하므로 공개 commit의 실제 CI 결과를 별도 확인한다.

### F-01 / Medium — Evidence Git 줄바꿈 변환

- 위치: `evidence/.gitattributes` 부재(교정 후 1~2행), `pg-start.log` 원본.
- 재현: core.autocrlf=true에서 `git hash-object --no-filters`는 `7c5d27ae61fe985df7fe92a6122558e0c50c555c`,
  `--path` 적용 결과는 `0fb0b9795fb4884c3f9d88d91ebb6492510e4ee5`로 달랐다.
- 영향/근거: 원본 Evidence SHA와 Git 보관본이 달라 PRE_MERGE_AUDIT의 원본 무결성·타 환경 재현 조건을 만족하지 못함.
- 수정 전 사용자에게 문제와 범위를 보고했다. `evidence/.gitattributes`에 `* -text`를 추가해 이 폴더만 원본 바이트로 보관한다.
- 회귀: stage 후 Evidence 전 파일의 raw Git blob과 index blob 일치, artifact size/SHA 일치 확인. 제품 source는 canonical LF 기준으로 별도 대조.

## 실패·보안·수명주기 검토

- bounded strict JSON, casefold/Unicode alias 중복·unknown key·trailing document 거부. wildcard actor와
  unsigned caller PASS를 승인으로 받지 않는다. 승인/관측 공개키의 decoded bytes·purpose·Phase를 분리한다.
- exact STS 신원·Principal/action을 history 조회에도 선행한다. 외부 입력이 임의 shell/SQL/endpoint를 고르지 않는다.
  AWS executable/profile·정책·DSN 파일은 trusted operator 소유 전제다. 비신뢰 사용자가 수정 가능한 경로는 운영 불가다.
- 단일 exact RDS/SQL target/TLS 검사는 Single Writer 보장이 아니다. 여러 비동기 복제본을 독립 제어 Writer로
  쓰지 않는다. scope row CAS는 Failover/Failback 전역 실행 잠금이 아니다.
- APPROVED_CONTROL에서는 closed만으로 Worker가 멈추지 않는다. durable Pause와 immutable binding 검사가 필수다.
  HTTP closed를 유지한 처리는 특정 Operation allowlist가 아니라 정산된 backlog 전체에 적용되므로 전체 Inventory/
  UNKNOWN·기수락 작업 정산 및 변경 동결 없이는 활성화하지 않는다. query reconciliation도 DB Writer를 필요로 한다.
- INTENT 뒤 timeout은 UNKNOWN/HOLD로 보존한다. 승인·관측 만료/상태 불명에서 자동 재시도·file open·rollback하지 않는다.
  APPLIED receipt는 과거 결과이며 기존 Pod의 pause latch·epoch를 갱신하지 않는다. 복원으로 이력이 사라져도 blind retry 금지다.
- HTTP auth·현재 권한/replay 의미, Terminal/Live/File/Preview의 기존 경계를 우회하지 않는다. Terminal 본문은 보관하지 않는다.
- temp 파일은 exactpath만 정리하고 PG pool은 Close한다. 이번 시험은 새 loopback 전용 PG cluster와 test별 DB만 생성했다.
  test DB 잔여0·서버 종료·55439 listener 없음은 `cleanup.json` 및 `pg-*.log`에 남겼다. 전용 data directory는 재현용으로 보존한다.
- source/Evidence의 고신호 비밀 패턴 검사는 `secret-pattern-scan.json`을 따른다. 전체 비밀 부재의 수학적 증명은 아니다.
  dummy fixture·공개키·로컬 도구 경로는 실제 credential과 구분한다. private key·운영 DSN·세션·Webhook은 넣지 않는다.

## 실제 실행·실패·SKIP과 재현

11/23 source는 `source-manifest.json`의29개와 동일한 상태에서 기본 Go 전체, 관련8package race,
Makefile integration6package 전체 race를 다시 실행했다. Web 전체35파일493개 및 typecheck/lint/build도 재실행했다.
정확 집계는 `test-summary.json`의 Test 이벤트이며 package 이벤트와 중복 합산하지 않는다.

| 11/23 실제 실행 | PASS | FAIL | SKIP / 범위 |
| --- | ---: | ---: | --- |
| Windows Go 전체 | 4527 | 0 | 88, 개별 사유는 summary 참조 |
| 관련 8package race | 1150 | 0 | 0 |
| 새 폐기 PG16 전체 integration race 최종 | 1699 | 0 | 10: Valkey9 + dump1 |
| Web 전체 | 493 | 0 | 35파일 |
| operator 독립 race | 154 | 0 | 0 |
| 최초 PG integration(ENV-01) | 1699 | 9 | 1; 초기 실패 원본 보존 |

Go vet/build·Web typecheck/lint/build·integration vet·전체640 Go format·계약 metadata PASS.
새 원격 CI는 이 표의 로컬 실행과 분리해 PR 본문에서 확인한다.
Valkey 미구동9개와 opt-in dump1개 integration SKIP을 실제 peer 검증 PASS로 쓰지 않는다.
Windows POSIX/LINUX-only skip은 그대로 명시하고 11/21 실제 Linux 증거로 보완한다.

`commands.json`의 argv/시각·원본 로그를 대조했다. `-final`이 붙지 않은 최초 integration은 ENV-01의 실패 원본이며, 최종 PG 결과는 `integration-all-final.jsonl`이다. 이 기본 검사들은 make target에 해당하는 명령을 직접 실행했다.
Go format은 Windows CRLF checkout의 영향 때문에640개 전체 파일을 LF 정규화해 gofmt와 바이트 비교했다.
format 검사가 repo를 일괄 수정한 것은 아니다. 전체 runtime YAML/migration metadata도 확인했다.
OpenAPI/JSON Schema 전체 validator는 변경 전 c11b70a CI와 공개 후 exact commit CI를 구분해 기록한다.

`verify.py`는 직접 사용한 harness다. Python3.12, Go1.27.1, Node24.22.3 및 npm12.2.1 CLI, 기존 설치된 PostgreSQL16.15 bin을
인자로 주고 Python은 `-X utf8`로 호출하며 **존재하지 않는 ASCII scratch directory**를 지정하면 재현한다. code/data 경로는 로컬 선택이며
배포 설정이 아니다. 동일 port listener·기존 PG data가 있으면 중단하고 임의 서버를 재사용하지 않는다.
실 test 환경은 상속 AWS/OS_/LABBIT/PG 변수를 제거하고 owned loopback DSN만 주입했다.
검증 원본은 Dummy trust PG·Fake cloud·loopback MockProvider이며 실제 AWS/공유 DB/서울/Slack 검증이 아니다.

이전 전체 Go39FAIL/timeout·Web EPERM·Linux guest curl 누락2FAIL은 당시 원본 그대로 보존한다.
오늘 PASS가 과거 실패 로그를 지우거나 당시 source를 사후 수정한 결과가 아니다.
Linux 결과는 실제 QEMU 실행이지만 non-race/RAMfs이다. cross-compile만 했다고도, 운영 내구성을 검증했다고도 하지 않는다.

### raw CRLF와 Git LF의 추적

11/21 source·payload manifest는 **실행 당시 Windows raw bytes**의 SHA다. Git은 text를 LF로 저장하므로
다른 OS의 checkout을 당시 rawSHA와 바로 비교하면 차이가 날 수 있다. `publication-source.json`은 raw size/SHA와
canonical LF size/SHA·Git blob을 함께 기록한다. 검토/실행 source와 공개 blob의 연결은 이 LF 비교로 확인한다.
원본 실행 로그·payload·바이너리 SHA는 변환하거나 덮어쓰지 않는다. Linux guest에는 당시 raw source가 그대로 복사됐다.

## 독립 두 번째 검토

- `review-operator.md`: 신규9개 Go source·CLI/Runtime·기존 Resumer/PG consumer. 독립 race154PASS/0FAIL/0SKIP,
  raw `review-operator.jsonl`,9개 raw source SHA 별도 보관. 실제 PG는 검토만 했다.
- `review-worker.md`: 전체 Worker/maintenance 조립·기존 durable/Router/Executor/PG/migration/HTTP consumer.
  독립6package race PASS. package 결과만 기록한 실행이므로 test-level 무skip 수치로 쓰지 않는다.
- `review-regression.md`: test-only6개·기존 WSS/registry·종속계약, 이전 source23/29·Linux payload103·
  artifact59/harness6 무결성·집계, workflow 영향. Evidence `* -text` 교정 후 당시93개 raw/filtered blob 일치까지 증분 검토했다. WSS/registry7개 targeted 테스트 race×3 독립 실행 두package PASS.
- 세 reviewer 모두 제품 source를 수정하지 않았고 새 confirmed blocker를 보고하지 않았다.
  최신 외부 SSOT/최종 파일 전체 inventory와 이번 폐기 PG·Web 원본 검증은 primary가 합쳤다.

## 공개·배포 경계와 판정

원격 main/PR ref가 위 기준과 같은지 push 직전에 재확인한다. `.github/workflows`/Dockerfile/dependency를 변경하지 않는다.
기존 Backend workflow의 PR build는 `push:false`, AWS OIDC/ECR·GitOps update는 PR에서 skip이다.
Connector release는 tag/manual만, Web delivery는 PR에서 skip이다. 기존 PR head branch에만 push하며
workflow_dispatch·release tag·main push를 실행하지 않는다. GitHub PR CI 비용/빌드는 실행될 수 있다.

**판정: 보완된 App 후보의 오프라인 코드·폐기 PG/Mock 통합 및 독립 검토 범위에서 기존 Draft PR 업데이트 가능.**
새 commit의 원격 CI 결과는 PR 본문에 commit/run 링크와 별도로 기록한다. 이 판정은 배포 가능·Stage3 완료가 아니다.

운영 Pause·실제 safety collector·서명키 운영·authoritative Writer/Fencing·전체 Pod coverage·고정 image/GitOps·
schema12~14 실제 백업/복구/적용·실 Provider 작업 정산·실환경 승인 재개·전체 DR 시험은 남아 있다.
main merge는 기존 Delivery/Argo PreSync migration 경로를 실행할 수 있으므로 Draft·미병합을 유지한다.
ReplicaLag FAIL·필수4개 NOT_VERIFIED·execution_gate=HOLD·recovery_authorized=false·cloud_verified=false를 유지한다.
