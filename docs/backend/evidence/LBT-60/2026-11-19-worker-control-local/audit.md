# LBT-205 · Worker claim/dispatch 로컬 제어 Audit

2026-11-19 KST · PRE_MERGE_AUDIT.md 적용 · Admission + Worker 누적 working diff 검토

## 범위 판정

요청한 **동일 브랜치의 프로세스 내부 Worker 제어 구현·단위 검증** 범위를 확인했다.
전체 저장소 검사는 아래 baseline/Windows 실패가 남아 있으므로 전체 CI 성공·PR/merge 준비 완료로 선언하지 않는다.
LBT-205 전체 완료, 실환경 pause/재개·다중 Pod 제어 또는 DR 승인으로 확대하지 않는다.
서울 서비스·AWS·실 DB·외부 Connector·Slack을 호출/변경하지 않았고 commit/push/PR/merge/배포도 하지 않았다.

- 저장소: `ktcloud4-SL/labbit-app`.
- 브랜치: `codex/feat/LBT-60-dr-admission-block-20261119` (사용자의 동일 브랜치 지시 유지).
- Base/HEAD: `2bccdbb9264749251f65e710d9acd2ebf3bc7d8c`; main 원격 읽기에서도 동일 SHA.
- 시험 소스는 base + `validation.json.source_inventory`의 누적 소스/계약/테스트/문서 21개다.
- 이전 `2026-11-19-admission-local/`의 2개 감사 파일은 **이전 증분의 역사적 스냅샷**이다.
  그 inventory를 현재 소스 해시로 재사용하지 않는다. 이번 전체 변경은 이전 metadata 2개 및 이번 metadata 2개까지 포함한다.
- ignored `.local/worker-checks`에 원본 실행 로그, `.local/worker-fmt-lf`에 폐기 가능한 포맷 검사 사본이 있다.
  기존 node_modules/validator 설치는 검증 도구이며 제품 의존성/lock/workflow 변경은 없다.

## 직접 확인한 최신 SSOT

| 원본 | 확인한 버전과 영향 |
| --- | --- |
| 사용자 지시 | 동일 브랜치 Worker claim/dispatch 구현·단위 검증, 서울 서비스 미변경, push 보류 |
| 개인/저장소 지침 | PRE_MERGE_AUDIT.md, AGENTS.md, CONTRIBUTING.md, TESTING.md, docs/backend/README.md. 적용 경로에 별도 하위 AGENTS 없음 |
| [LBT-205](https://samsunglions.atlassian.net/browse/LBT-205) | updated 2026-11-19 18:15:49+09:00. 새 Backend 구현 추적 작업 직접 조회. 전체 AC 중 전역/durable/승인 재개는 이번 increment의 미충족 항목으로 보존 |
| [LBT-59 #11242/#11246](https://samsunglions.atlassian.net/browse/LBT-59?focusedCommentId=11246) | updated 18:57:09+09:00. B안 개발 범위와 LBT-205 추적, 실제 DR GO 구분 |
| [LBT-60 #11245](https://samsunglions.atlassian.net/browse/LBT-60?focusedCommentId=11245) | updated 18:57:22+09:00. D01–D06/C01–C20 문서 QA; 실제 전체 제어·전역 barrier는 별도 |
| [Confluence 63275012](https://samsunglions.atlassian.net/wiki/spaces/SL/pages/63275012) | v14 직접 조회, 11/19 B안 개발 결정·Stage2 scoped 인수와 기존 FAIL/4NV/HOLD 구분 |
| [Infra PR57 D02–D05](https://github.com/ktcloud4-SL/labbit-infra/blob/5987cb061092d406ba8effaf3259d56e260246b1/runbooks/operations/aws-dr-backend-controls-proposal.md) | 고정 5987cb0 로컬 원본. PAUSE_NEW/RECONCILE_ONLY/ACTIVE, 기수락/UNKNOWN 보존·무자동 재전송. 전역 제어 미구현 시 전역 PASS 금지 |
| Runtime/HTTP | runtime/contract.yaml 0.1.9에 owning mode·local_pause·재시작 한계 추가. 누적 OpenAPI 0.1.7의 admission code/replay/검사 우선순위도 검토 |
| DB | migration 000004의 durable Item/lease/active uniqueness, repository worker/reconciliation port 및 PostgreSQL claim/renew/prepare/write의 exact-ID/generation 경계를 확인. SQL/schema/transaction 변경 없음 |
| Connector/Realtime | contracts/connector/README.md의 기존 Operation/Reconcile 의미와 Router pending/correlation, 별도 Terminal/File/Preview 경계 확인. wire message/schema/Realtime contract 변경 없음 |
| Producer/consumer | production Executor, Router, tagged router_harness 직접 send 경로를 대조. 모든 production Operation/Reconcile send는 Router guard 통과. FE에는 새 Worker endpoint가 없고 기존 admission 503 consumer 경계 유지 |

## 전체 diff 목적

| 파일 | 목적/검토 경계 |
| --- | --- |
| internal/server/workercontrol/control.go | 새 순수 Go 모드·permit·단방향 Pause/drain, nil/zero fail-closed |
| internal/server/workercontrol/control_test.go | mode/invalid·nil/zero·중복 release·병렬·cancel 후 latch 유지 |
| internal/server/provision/executor.go | mutation claim/prepare/send/미전송 재등록 제어, 결과 대기 permit 해제, Pause/ControlState |
| internal/server/provision/reconciliation.go | mutation-first 우회와 정산 전용 claim, 정산 prepare/query guard, lease/result 경계 유지 |
| internal/server/provision/worker_control_test.go | 3종 claim/prepare/lifecycle 경합·소유권/미전송 fallback·정산 전용·UNKNOWN/결과·unsupported 회귀 |
| internal/server/connector/router.go | 직접 mutation/query 호출도 공유 Control 통과. inbound/기존 pending 미변경 |
| internal/server/connector/worker_control_test.go | 직접 우회 차단·전송 중 drain·늦은 결과 보존 |
| internal/server/app/app.go | 기존 admission 설정과 새 Worker mode 파싱·초기 validation·stack 전달 |
| internal/server/app/control_stack.go | 하나의 Control을 production Executor와 Router에 전달, 기존 admission Service 연결 |
| internal/server/app/worker_control_test.go | role별 설정/invalid 비노출, 초기 거절, 실제 production 조립의 공유 latch(Fake 실행) |
| internal/server/app/admission_test.go | 기존 admission 설정 회귀; 이번 누적 소스에서 재실행 |
| internal/server/labexecution/admission.go | 기존 시작 접수 guard; Worker 제어와 분리 |
| internal/server/labexecution/service.go | Provision의 committed replay 뒤 등록 전 guard |
| internal/server/labexecution/mutation.go | Reset/Cleanup의 같은 guard |
| internal/server/labexecution/admission_test.go | 무등록·기존 작업 replay/권한·조회 실패·병렬·semantic 우선순위 |
| internal/server/httpapi/labexecution.go | 누적 503 Problem 매핑, Worker의 내부 제어 오류를 HTTP로 새 노출하지 않음 |
| internal/server/httpapi/admission_test.go | 기존 admission HTTP 회귀 |
| contracts/http/openapi.yaml | 누적 admission owning code/replay/검사 우선순위 |
| runtime/contract.yaml | 누적 admission + 새 Worker 제어 owning 계약 |
| docs/backend/dr-mutation-admission.md | 앞선 접수 시작 정책의 연결/한계 |
| docs/backend/dr-worker-control.md | 이번 Worker 연결/제어 의미·미구현/검증 경계 |
| 이전·이번 audit.md/validation.json 각2개 | 이전 시험 이력과 이번 전체 source/Evidence 추적; 과거 SHA를 현재 PASS로 재사용하지 않음 |

배포 workflow·GitOps·Docker·DB migration·의존성 manifest/lock·Worker generic repository API는 변경하지 않았다.
모든 누적 파일을 두 번째 검토 범위에 포함했고, ignored 검증 도구·로그는 민감정보 없는 실행 재현용으로만 사용한다.

## 요구사항 → 구현 → 검증 추적

| AC/Decision | 구현/라인 기준 | 자동 테스트와 실제 실행 | 실환경·잔여 공백/판정 |
| --- | --- | --- | --- |
| D01/LBT205-1 신규 접수 | admission Service/HTTP 누적 diff | 기존 admission unit·HTTP·boundary가 이번 7package race에도 PASS | 프로세스 시작 정책만 충족. 모든 Pod/재시작 제어는 미구현 |
| D02 새 claim 차단 | reconciliation.go:29, Control.acquire | TestWorkerControlPauseCreatesNoClaimsOrSideEffects 3종, Control 병렬96호출 | Fake Store만 사용. DB/다중 Pod 실측 아님 |
| claim commit 뒤 pause | executor.go:229의 execute 재획득 | TestWorkerControlDelayedClaimCannotDispatchAfterPause 3종 | 기존 durable RUNNING claim 보존, 취소/재등록 0 |
| prepare/lifecycle 뒤 dispatch 차단 | executor.go의 전송 전 두 번째 acquire, Router:184 | PauseDuringPreparation/ PauseDuringLifecycleClose 및 DirectRouterCalls 회귀 | Pause 전 시작한 짧은 작업은 drain 전까지 끝날 수 있음 |
| 미전송 자동 재등록 금지 | executor.go:478 deferUnsent gate | PauseBlocksOwnershipAndUnsentRequeue 3종×2, PauseWaitsForAlreadyEnteredUnsentRequeue | 먼저 진입한 DB 재등록 transaction은 Pause 성공 전 완료 가능 |
| 정산 전용 | reconciliation.go claim 우회/prepare/query gate, Router:244 | ReconcileOnlySkipsPendingMutationQueue 3종, PauseDuringReconciliationPreparation, unsupported Store 거절 | Provider query만 read-only. lease/result DB writes·Writer 승인/계보 실증은 미검증 |
| 기수락/UNKNOWN 보존 D05 | sent 뒤 permit release, inbound/lease/result 미차단 | PausePreservesAlreadySentResultsAndUnknown, RouterPauseDrainsSendAndPreservesLateResult, 기존 uncertainty 회귀 | Provider 종료/0건/Single Writer를 뜻하지 않음 |
| timeout/cancel·동시성 | control.go:118, Snapshot | PauseWaitsForLocalBoundariesAndDoesNotReopenAfterCancellation, ConcurrentPause 및 기존 cancellation 회귀 | 취소 후 latch 유지. durable 복원·프로세스 장애 후 영속 상태는 미구현 |
| 복합 오류의 UNKNOWN | executor.go:321/reconciliation.go:117 bare 차단만 구분 | WrappedPauseSendFailureKeepsUnknownOrConfirmedResult 2분기 및 기존 오류 회귀 | 결과 불명을 성공/미시도로 변환하지 않음 |
| production 공유/내부 우회 | app Control 생성→Executor/Router 주입 | ProductionAssemblySharesExecutorAndRouterGate, 직접 Router gate | 일반 DB 직접 writer/타 프로세스는 통제하지 않음 |
| LBT205-2 재개·epoch | 이번 runtime/docs에 미구현 명시 | ACTIVE 시작은 기존 regression, runtime restrictive Pause만 제공 | 승인된 동적 재개/전역 revision/epoch/stale 명령 차단은 **미구현** |
| LBT205-3 Writer/승인 | 운영 endpoint·승인 우회 flag 추가 없음 | 제어를 실제 승인/Single Writer로 반환하는 필드 없음 | 승인·RECHECK/INTENT·Writer 검증과 영속 journal **후속 범위** |
| LBT205-4 HTTP/FE | 기존 admission 503 및 generic FE error 처리 대조 | HTTP와 Web 기본 검사 범위 구분 | 전용 DR 안내 UX·FE 독립 QA는 미구현/별도. Worker HTTP endpoint 없음 |
| LBT205-5 GET/Terminal 등 | operation 외 Router 함수/handler 미변경 | 기존 Connector·HTTP·App·작업/접수 회귀 및 전체 Go 중 해당 packages PASS | 실 Browser/실 VM·Terminal/Live/File/Preview E2E 미실행 |
| 정확 tested source/CI | validation.json의 21개 source raw/LF SHA·원본 log SHA | 로컬 7package race 1,159 test/subtest event PASS, 상위350개, skip0 | 원격 CI는 push 금지로 **미실행**. 전체 repo 결과 아래 별도 |

LBT-205 전체 AC를 이 로컬 증분만으로 완료 처리하지 않는다. Fake를 실제 PostgreSQL lock·Provider 실행·전역 barrier 근거로 사용하지 않는다.

## 수정 전 Findings 및 재검증

| ID/심각도 | 수정 전 경로·재현·영향 | 보고·처리·의미 있는 회귀 |
| --- | --- | --- |
| WC-01 / P2 | 초기 Worker diff executor.go 약333 / reconciliation.go 약119의 errors.Is(ErrPaused). Router write 오류가 ErrSendFailed와 Pause를 함께 감싸면 실제 미전송/UNKNOWN 구분과 즉시 결과 수집을 건너뜀 | 사용자에게 수정 전 보고 후 bare ErrPaused만 no-write 취급. 현재 executor:321/reconciliation:117. wrapped ambiguous/confirmed 2분기 race PASS; 독립 reviewer 해결 확인 |
| WC-02 / P2 | 초기 executor의 OwnerLocator 및 send no-write fallback → deferUnsent. 준비/전송 중 Pause latch 뒤 PENDING 재등록이 permit/drain 밖에서 발생 가능 | 독립 reviewer 발견, 사용자 보고 후 deferUnsent:478에 공유 gate 추가. owner/send 각각 3종 및 먼저 들어간 DB 재등록 drain 회귀 PASS |
| WC-TEST-01 / P3(test fixture) | 초기 controlledExecutor helper가 RESET/CLEANUP claim의 Operation.Type을 PROVISION으로 둠. lifecycle close hook이 호출되지 않아 신규 테스트가 entered 채널 대기 | 사용자 보고 후 kind를 실제 claim에 지정. root 초기 race의 소유 provision.test 자식만 PID/부모 검증 후 종료; 첫 전체 Go는 90초 timeout. reviewer도 자기 실행만 종료. 수정 후 최종 7package race 및 독립4package focused race PASS |
| REPO-BASELINE / 검증 한계 | 전체 Go의 OpenStack CIDR/Installer와 Web installer 환경 검사의 Windows EPERM | 기존 admission 감사의 동일 pristine base 대표 실패·timeout 자료와 대조. unrelated 코드 수정 없음. 새 Web focused 재실행도 같은 EPERM이 남았으며 PASS로 치환하지 않음 |

위 2개 제품 경계 finding은 수정·회귀·독립 재검토로 해결했다. 검토 범위의 추가 확정 P0/P1/P2 결함은 확인하지 못했다.

## 실패·보안·수명주기 검토

- **입력:** enum/허용 문자열만 사용하고 잘못된 원문을 오류에 복사하지 않는다. zero/nil Control은 허용으로 변환하지 않는다.
  기존 constructor의 nil 옵션은 legacy ACTIVE이며 production은 명시 공유 Control을 생성한다.
- **경합:** 동일 lock 아래 mode latch와 permit 진입을 결정한다. nested permit은 pause 시 즉시 실패하므로 pause를 기다리는 재귀 deadlock을 만들지 않는다.
  이미 획득한 경계는 완료될 수 있고 Pause 성공 전에는 모든 claim/send 정지를 주장하지 않는다.
- **취소/timeout:** latch 전 이미 취소된 요청은 상태를 바꾸지 않는다. latch 후 취소는 PAUSE_NEW를 보존하고 drain 성공을 반환하지 않는다.
  pause 함수는 permit 안에서 동기 호출하지 않으며 운영 endpoint나 자동 재개 경로가 없다.
- **멱등·재시작:** Pause 반복은 개방하지 않는다. release는 sync.Once라 중복으로 count가 underflow하지 않는다.
  restart 시 같은 PAUSE_NEW 입력 재구성 회귀는 있지만 runtime latch는 영속하지 않으며 입력 유실 시 기본 ACTIVE다.
  이것을 영속 DR fail-closed 보장으로 주장하지 않는다.
- **리소스:** 미전송 기존 claim을 실패/삭제/재시도 가능 상태로 자동 바꾸지 않는다. 이후 승인된 정산이 필요하다.
  이미 보낸 Provider 실행을 취소하지 않고 결과·UNKNOWN·lease 갱신 및 기존 generation/소유권 쓰기 fencing을 유지한다.
- **보안/우회:** 공개 mode setter, 임의 header/body 우회, 승인 boolean, 실제 Credential/DSN/Token/Webhook을 추가/기록하지 않는다.
  production tagged harness 직접 send도 Router guard를 통과한다. 임의 DB query·프로세스 외 Provider writer는 별도 통제 대상이다.
- **DB/계보:** claim/preparation은 기존 transaction 경계와 정확한 Operation/Item/lease/ID/generation을 재사용한다.
  Reconciliation은 결과 기록에 DB write가 필요하므로 read replica/Writer=NONE에서는 안전하다고 주장하지 않는다.
- **소유권/채널:** 기존 Operation/Item/provider 자원 및 Terminal/File/Preview 소유권 schema를 바꾸지 않는다.
  Pause 전에 시작한 Reset/Cleanup lifecycle close는 완료될 수 있다. 전체 채널 유지/종료는 별도 실환경 시험 대상이다.
- **상태 의미:** Snapshot.InFlight는 Provider 작업 수가 아니고 Quiesced는 로컬 짧은 경계 drain이다.
  HTTP 200·Ready·모드 값·quiescence를 DR PASS/전역 Single Writer 근거로 사용하지 않는다.

## 실제 실행·Evidence

Windows/amd64, Go1.27.1(+race), Node24.22.3, Python3.13.14에서 실행했다.
외부 경계는 Fake·loopback/in-process fixture뿐이며 API 서비스나 실제 DB/클라우드를 시작하지 않았다.
Go 기본 검사 실행 시 상속 LABBIT/OS/AWS/OTEL/VALKEY/KTCLOUD 환경을 **해당 검사 자식 shell 안에서만** 제거했다.

| 검사 | 결과 및 한계 |
| --- | --- |
| 초기 focused unit | workercontrol/provision/connector/app 4package PASS |
| 최종 7package race | 1,159 test/subtest event·상위350개 PASS, 7package PASS, skip0·race 경고0 |
| 독립 focused race | workercontrol/provision/connector/app 4package PASS, -timeout90s. Reviewer가 직접 실행 |
| go vet/build ./... | exit0, 제품 마지막 코드 이후 실행; 이후 변경은 test fixture/문서/metadata |
| gofmt | 전체603 Go 파일의 LF 정규화 폐기 사본 PASS. 원본 CRLF 일괄 수정 없음 |
| Contracts | OpenAPI, Runtime mode/구조, JSON Schema8개/ref, Migration 번호 유일성 PASS. CI 고정 validator 버전 사용 |
| 전체 Go | 최종 -timeout90s 실행 및 pass/fail/skip 상세를 validation.json에 보존. OpenStack/Installer baseline 실패를 성공으로 치환하지 않음 |
| Web typecheck/lint/build | 모두 exit0. 기존 chunk 크기 권고만 존재 |
| Web test | 최초492/493 PASS, installer env 임시폴더 삭제 Windows EPERM1건. 해당 파일 focused 재실행도29/30 PASS로 같은 EPERM 남음 |
| PostgreSQL integration/원격 CI/실환경 | **미실행**. 기본 Go의 opt-in live integration·POSIX/Windows·Valkey 미가동 skip 사유를 보존. mock 성공과 실환경 성공 구분 |

이전 감사의 baseline-failures.jsonl/baseline-hang.jsonl은 동일 base에서 대표3실패와 CIDR 동시성 timeout을 확인한 과거 근거다.
모든 OpenStack 실패를 개별 pristine 재현했다고 확대하지 않는다. 초기 fixture 실패·종료와 최종 실행을 구분한다.
이번 원본 로그 SHA, 정확한 테스트/skip/fail 목록, source hash는 validation.json에 있다.

## 독립 검토·재현·잔여 업무

`/root/review_admission_local`이 작성자와 분리해 누적 소스·consumer·owning Runtime/docs를 검토하고
새 fallback finding과 회귀를 대조했다. 최종 focused race도 직접 통과했다. Reviewer의 최신 Jira 확인은
부모가 직접 조회한 원문을 전달받은 범위이며 별도 외부 조회로 확대하지 않는다.
이번 audit/JSON 생성 후 부모가 source/log 해시·결과 집계·전체 변경 목록을 별도 metadata 패스로 검증한다.

같은 source inventory와 Go1.27을 준비해 다음을 실행한다.

```text
go test -race ./internal/server/workercontrol ./internal/server/provision ./internal/server/connector ./internal/server/operation ./internal/server/labexecution ./internal/server/httpapi ./internal/server/app -count=1 -timeout=120s
go vet ./...
go build ./...
go test ./... -count=1 -timeout=90s
```

Web는 Node24에서 npm ci 후 typecheck/lint/test/build, 계약은 .github/workflows/contracts.yml의 고정 validator를 사용한다.
실 DB/클라우드 credential은 필요 없다. unit/Mock을 실 PostgreSQL·실제 DR 재현 방법으로 안내하지 않는다.

후속은 영속 제어 상태·전역 revision/epoch·재시작 복원, 전체 Pod 관측·stale 명령 차단, 운영 인증/승인·동적 재개,
Writer/기수락 정산 검증, GitOps standby/active 프로필, 승인된 실제 통합 시험이다.
기존 ReplicaLag FAIL·4개 NOT_VERIFIED·execution_gate=HOLD·recovery_authorized=false·cloud_verified=false는 유지한다.
원격 push와 환경 변경은 이번 audit 통과로 허용되지 않는다.
