# PR #194 접수 차단·Worker·승인 재개 로컬 통합 검토

실행일: 2026-11-20 KST. 대상은 같은 브랜치의 **미커밋 오프라인 구현**이다.
실제 서울 서비스·DB·AWS·Slack·Provider를 조작하지 않았고 commit/push/배포하지 않았다.
검토 절차는 사용자 `PRE_MERGE_AUDIT.md`와 저장소 AGENTS/CONTRIBUTING/TESTING을 따른다.

## 기준과 전체 변경 범위

- 저장소: `ktcloud4-SL/labbit-app`.
- 브랜치: `codex/feat/LBT-60-dr-admission-block-20261119`.
- base/HEAD: `2bccdbb9264749251f65e710d9acd2ebf3bc7d8c`. HEAD와 원격 main ref는 그대로다.
- PR194: Draft/Open의 `34ed442538d45bc3ce9d47f73a3bef0584cf6dc6`을 조회·fetch해 로컬에 통합했다. 해당 PR을 수정하거나 merge하지 않았다.
- 전체 cumulative source 47개와 과거 감사 metadata 8개를 구분했다. 새 감사 산출물 2개는 자기 자신을 source hash에 포함하지 않는다.
- 작업 전 46개 파일의 원본/해시는 ignored `.local/maintenance-integration-before/{inventory.json,files/}`에 보존했다. 이전 미추적 `labexecution/admission.go`를 제거하고 startup enum·별도 error를 PR194 경계로 통합했다.
- `validation.json`은 전체 source의 실제 바이트 SHA256·크기·목적, raw log SHA256과 실행 결과를 고정한다. 과거 8개 감사는 수정하지 않았으며 현재 소스 검증으로 재사용하지 않는다.
- 무의미한 두 integration/test 파일의 줄바꿈 변경은 제거했다. staged 변경 없음. ignored runner·도구·로그는 로컬 재현 보조이며 서비스에 배포되지 않는다.

## 확인한 최신 SSOT

1. [App PR194](https://github.com/ktcloud4-SL/labbit-app/pull/194): 위 HEAD의 source/body/comments. `503 maintenance`, 인증/현재 권한·committed replay 우선, 파일 기반 process barrier, 새 Worker claim 차단과 admitted 실행 drain. public 승인 endpoint·Fencing 보장이 아니다.
2. [LBT-205](https://samsunglions.atlassian.net/browse/LBT-205): 조회 시 updated `2026-11-20T10:53:39.295+0900`; #11251/#11253까지 확인. GitOps Draft124 HEAD `e7b1196086e5f268b868191c4dd6e6a149fd4e01` 준비 기록(서울 open/오사카 closed, directory mount)을 참고했지만 GitOps diff/실제 배포는 이번에 검증하지 않았다. release HOLD.
3. [LBT-59](https://samsunglions.atlassian.net/browse/LBT-59): updated `2026-11-20T10:32:14.955+0900`, #11242/#11252. Worker 포함 B 목표, 신규 접수 차단·Worker 제어·단일 Writer·정산 선행. 이 구현이 실제 DR GO를 의미하지 않는다.
4. [LBT-60](https://samsunglions.atlassian.net/browse/LBT-60): updated `2026-11-19T18:57:22.090+0900`, #11244/#11245. 신규 승인·재점검, INTENT/UNKNOWN 보존, 자동 mutation 재시도 금지와 기존 Gate 유지.
5. [D-24](https://samsunglions.atlassian.net/wiki/spaces/SL/pages/6815749): 이번 턴 본문 직접 조회. Provider 실제 UUID·상태 대조, 정산 전 mutation 자동 반복 금지, 운영자 판단, Failback 재보호. RPO/RTO 후보를 보장값으로 삼지 않는다.
6. Infra `5987cb0`의 D01–D06 제어 제안 및 C01–C20 계약, 현재 Runtime·OpenAPI·migration·Connector producer/consumer를 대조했다. 앱 내부 후보를 infra 실행 권한으로 확대하지 않았다.
7. HTTP owning SSOT `contracts/http/openapi.yaml`, Runtime `runtime/contract.yaml` **0.1.12**, migration 000001–000013, Backend 책임 가이드를 읽고 적용했다. Connector/Reconciliation 경계는 typed call과 Router의 최종 send를 함께 검토했다. Terminal/Live wire protocol·payload 저장 경로는 변경하지 않아 해당 프로토콜 변경은 없음으로 분류했다.

## 요구사항 → 구현 → 검증 추적

아래 파일/라인은 현재 uncommitted source 기준이다. 실제 외부 검증은 전 행 미실행이며 Fake를 실제 DB/Pod 근거로 확대하지 않는다.

| 요구사항·원문 | 구현 | 실행한 자동 테스트·분기 | 실제 검증·공백 |
| --- | --- | --- | --- |
| PR194 단일 접수 차단, 503 maintenance, 권한/replay 보존 | `labexecution/service.go:86,94,157`, `mutation.go:106,187`, `httpapi/labexecution.go:218`, OpenAPI | maintenance/admission tests: Provision/Reset/Cleanup, 48 concurrent direct calls, 401/403/409/replay200, 새 row/transaction 0, header 우회 불가 | Fake service 및 HTTP handler. 실제 PostgreSQL HTTP integration은 compile only |
| PR194 process barrier·기존 실행 drain | `maintenance/gate.go:27,38,79`, `operation/worker.go`, `provision/executor.go:130` | Barrier/restart/independent replicas; whole worker counter; 늦은 claim 반환 전 close 이후 admitted 작업 완료, 새 claim0 | 로컬 파일·goroutine. ConfigMap 전파·전체 Pod/재시작 coverage 미검증 |
| 동일 production gate, retired enum 우회 방지 | `app/app.go:130,211,303,383`, `control_stack.go:117,138,175`, Runtime config | `TestMaintenanceConfigAndLegacyModeCannotSilentlyOpen`, production assembly claim0 | 실제 Pod env/mount 변경 없음. 미설정 파일은 PR194 호환 open이며 운영에서 명시 설정 필요 |
| 명시 Pause 및 exact revision/epoch로 claim/prepare/Router send/requeue 차단 | `workercontrol/{control,durable}.go`, `provision/{executor,reconciliation}.go`, `connector/router.go` | stale binding/late ACK/UNKNOWN/pause/error/lock/result tests; 새 Worker2개가 실제 Mock send1건 | Fake shared state + scripted SQL. PostgreSQL isolation/row lock/공유 Pod 실측 아님 |
| D-24 정산 전 mutation 금지·승인된 query-only | `executor.go:136`, `durable.go:139`, `app` early validation | file closed에서도 exact persistent RECONCILE_ONLY의 query+result만 허용, mutation queue0, stale query0, 결과 기록까지 reconciliation counter1 | 실제 조회/정산도 DB writer가 필요. local/DB control은 Fencing·Writer 검증 대체 불가 |
| 신규 승인+새 점검, 중단/응답 유실과 재시작 후 UNKNOWN 보존 | `workercontrol/resume.go`, `postgres/worker_control_resume.go`, migrations12/13 | 누락/만료/재사용 승인, failback 새 승인, hash 충돌, 재시작 INTENT, 중복 실행, Begin/Apply 별도 commit; 미완료 ticket/intent HOLD | Authorizer/Rechecker는 필수 port와 테스트 double. 운영 승인 출처 검증/실제 Writer 검사 adapter 미구현 |
| maintenance와 승인 재개 연결 | `workercontrol/maintenance_resume.go:21,32,52,59` | local invalid/missing/open/http·worker·recon active 거절, external FAIL/NV 보존, INTENT 전후 변동, APPLIED replay로 파일/옛 Worker 활성화 안 됨 | exact closed+local0은 추가 local 검사. 파일 관측과 DB commit은 원자적이지 않으며 global proof·변경 통제 필수 |
| 파일 open과 승인된 새 binding 모두 있어야 새 mutation | `provision/maintenance_resume_test.go:58,111,166` 및 공유 gate/control 조립 | ACTIVE 승인만으로 closed file에서 claim0; 외부 open 뒤 옛 paused worker0; 새 binding2개 중 claim/send1; query-only는 mutation0 | 실제 config writer·rollout·복구 실행기 명령 adapter는 없음 |
| Runtime/HTTP/schema/migration 파싱 가능 | owning contracts 및 migration tests | OpenAPI validate, schema8+local ref, Runtime structural assert, migration1–13 unique/BEGIN/COMMIT, Go fmt | migration 적용/DDL 권한/DB lock 시험 없음 |
| 기존 DR 판정 보존 | runtime·운영 문서 | 실행기의 FAIL/NV를 PASS로 바꾸는 code path 추가 없음; 내부 승인 결과와 DR 승인 구분 | ReplicaLag FAIL, 필수4 NOT_VERIFIED, HOLD, recovery_authorized=false, cloud_verified=false 유지 |

## 수정 전 Findings 및 처리

확인된 문제를 사용자 commentary에 먼저 보고한 뒤 수정했다. 위치는 발견 당시 source 기준이다.

| ID·심각도 | 파일/조건·영향 | 수정과 재검증 |
| --- | --- | --- |
| I-COMPILE-01 / Low | `labexecution/admission_test.go` enum 제거 후 unused strings import로 초기 focused build 실패 | unused import 제거. final race1398 PASS |
| I-TEST-01 / P2 | `maintenance/gate_test.go:111` Windows 동시 read 중 fixture os.Rename AccessDenied. 초기 focused989 PASS/1 FAIL | test fixture writer만 3초 bounded Windows permission retry. production은 retry/write 추가 없음. 독립 reviewer race30회 PASS 및 final race PASS |
| I-02 / P2 | interim `app/control_stack.go:114` api-only 조립에서 Executor를 건너뛰어 local RECONCILE_ONLY Router 생성 가능. 영속 제어 필수 계약 미준수(코드 분석) | LoadConfig/Run/newControlStack 모두 early reject. api-only/worker 구성 및 persistent query-only API 회귀 PASS |
| I-REVIEW-02 / P2 | `runtime/contract.yaml:32–63` maintenance block이 migration.config에 삽입되어 YAML parse 실패·기존 migration 계약 훼손. parent parser와 reviewer가 독립 발견 | saas.maintenance로 이동, migration.config 네 key 원복, 문서0.1.12 정정. YAML parse와 nesting/assert PASS, 독립 재검토 수락 |
| I-COMPILE-02 / P2 | `httpapi/mutation_integration_test.go:40` fixture variadic 인자 누락으로 새 maintenance integration compile 실패 | PR194와 같은 fixture2줄 반영. integration-tag 두 package compile-only PASS, actual DB 실행0 |

확인된 production 잔여 결함은 검토 범위에서 없으나, 아래 전체 검증 실패와 실환경 공백을 해결한 것으로 취급하지 않는다.

## 실패·보안·수명주기 검토

- **입력/우회:** 파일 미존재·권한 문제·unknown·16byte 초과(17byte 이상)는 admission closed. 승인에는 invalid와 exact closed를 구별한다. 기존 env가 nonempty면 startup 거절해 제거된 BLOCK_NEW 설정의 silent open을 막는다. 사용자 header/body는 상태 제어를 받지 않는다.
- **Auth/Replay:** 권한·현재 대상 관계·committed idempotency replay를 먼저 확인한다. 미커밋 실패는 승인된 replay로 취급하지 않는다. 새 create가 차단돼도 조회·기존 결과 접근은 유지한다. readiness semantics는 변경하지 않는다.
- **Graceful vs hard stop:** 파일 close는 admitted 전체 실행의 drain이며 이미 claim한 작업의 send까지 허용할 수 있다. 즉시 mutation을 멈춰야 할 때는 별도 authoritative Pause와 미완료 ticket/UNKNOWN 정산이 필요하다. 한 metric만으로 전역 중단 선언 금지.
- **동시성/재시작:** shared Fake의 revision/epoch 및 per-boundary checks, SQL의 ticket 상태와 INTENT/APPLIED 분리, 오래된 binding/approval 재사용·중복 request·partial result를 검토했다. Fake mutex와 scripted transaction은 실제 PostgreSQL row-lock 증거가 아니다.
- **Query 수명:** RECONCILE_ONLY는 persistent binding일 때만 maintenance의 일반 mutation claim barrier를 통과한다. query부터 결과 쓰기까지 별도 counter로 추적하고 typed permission을 다시 확인한다. telemetry callback은 권한 부여자가 아니다. local Pause는 영속 상태로 되살리는 setter를 제공하지 않는다.
- **승인 수명:** trusted Authorizer/Rechecker 없이 Resumer를 생성하지 않는다. Begin/Apply 직전에 local gate를 재검사한다. 불명확 INTENT는 자동 만료·삭제·재적용하지 않고 HOLD한다. APPLIED 재조회가 mode file, 이전 process/control, 전체 DR gate를 열지 않는다.
- **권한 경계:** public admin endpoint, file writer, AWS/IAM 호출, DB Fencing이 없다. application owner가 쓰지 않는 read-only mode file과 전 Pod mount/coverage는 플랫폼 소유다. 파일과 DB의 검사/commit 사이 경쟁은 외부 변경 통제·trusted 재점검 없이는 보장되지 않는다.
- **민감정보:** 실제 credential·Secret·DB DSN·raw Provider payload를 fixture/새 Evidence에 넣지 않았다. raw test output은 synthetic/local 경로만이다. SQL migration/state는 기존 Provider 자원의 삭제/정리를 수행하지 않는다.
- **Cleanup:** 생성한 것은 temp mode files, 테스트 artifact, web dist뿐이며 t.TempDir 등의 fixture owner가 정리한다. 기존 Windows installer fixture EPERM는 아래 잔여 실패로 공개한다. 실제 자원·DB·채널을 생성/삭제하지 않았다.
- **계약 consumer:** service→HTTP mapper→OpenAPI, app→gate/Executor/Router, workercontrol→postgres migrations, runtime config→LoadConfig를 함께 확인했다. Connector mutation wire와 Terminal/Live payload 계약은 그대로다. frontend는 공통 503 problem 처리이며 새 제어 endpoint 소비가 없다.

## 실제 실행 결과와 재현

도구: Windows amd64, Go1.27.1/race 가능 toolchain, Node24.22.3, Python3.13.14. 별도 local DB를 띄우지 않았다.
원시 로그는 `.local/maintenance-checks/`, 해시·명령·정확한 이벤트 수는 `validation.json`에 있다.
이 경로는 ignored이며 원격 CI 성공이나 팀 공유 Evidence로 주장하지 않는다.

| 검사 | 결과·범위 |
| --- | --- |
| 변경 연관9 package `go test -race -json … -count=1 -timeout=120s` | **1398 test/subtest events PASS**, skip0/fail0. Fake/scripted driver/local HTTP/file/goroutine |
| 독립 reviewer race9 package 및 barrier30회 | PASS. reviewer는 구현 파일을 수정하지 않음 |
| `go vet ./...`, `go build ./...` | PASS |
| `go test -tags integration ./internal/postgres ./internal/server/httpapi -run '^$' -count=1` | 수정 전 fixture compile 실패 보존; 수정 후 두 package PASS **[no tests to run]**. 실제 SQL/HTTP DB integration 실행하지 않음 |
| OpenAPI/schema8/runtime0.1.12 structure/migrations/fmt | PASS. migration DDL 실제 적용은 아님 |
| Web typecheck/lint/build | PASS. 이 요청의 frontend source 변경 없음 |
| Web full test 최초+단독 재실행 | 각각492/493 PASS, 1 FAIL/skip0. 기존 `web/scripts/validate-installer-build-env.test.mjs:90`의 Windows temp rmSync EPERM(최초 process, 재실행 mode-file). 앱 코드를 수정해 숨기지 않음 |
| `go test -json ./... -count=1 -timeout=90s` | **FAIL**. 3941 PASS/39 FAIL/15 SKIP test events; OpenStack test1개 timeout으로 종료 event 없음. installer/openstack2 package. 이전 `.local/resume-checks/full-go.jsonl`와 failed test/package 집합 동일, 해당 production source 미수정. Windows/POSIX fixture 문제·timeout 한계이며 전체 green으로 표현하지 않음 |
| `git diff --check` | PASS |
| actual DB/Seoul/AWS/Slack/Provider/GitOps/롤아웃 | 실행하지 않음. 사용자 범위 제한 |

재현 명령은 `validation.json.commands`에 기록했다. Go binary가 PATH에 있으면 동일한 `go test`, `go vet`, `go build` 명령을 사용할 수 있다.
Web은 `web/`에서 `npm run typecheck`, `npm run lint`, `npm run test`, `npm run build`를 사용한다.
계약 검증에는 PyYAML, openapi-spec-validator, jsonschema가 필요하다(로컬 `.local/contract-validators` 사용).
구조 회귀는 YAML에서 `saas.maintenance`가 mapping이며 `artifacts.migration.config`가 기존 네 key list인지 함께 검사했다.
전용 DB integration을 실제 실행하려면 저장소 TESTING의 격리된 PostgreSQL 준비와 **별도 허용 범위**가 필요하다.

## 독립 검토·판정

사용자 audit 지침에 따라 `/root/review_worker_resume`에게 동일 base/전체 cumulative 변경·최신 Jira/PR194를 제공했다.
reviewer는 구현을 쓰지 않고 PR194 핵심 service/mutation/HTTP mapper/wholeWorker의 내용 동일성,
승인 port·query-only·local/global proof 구분, Runtime 문서, fixture와 integration compile을 별도 검토했다.
Windows fixture 실패를 독립 재현했고 YAML 위치 결함을 발견했다. 두 문제와 누락 fixture의 수정 후 재검토 및
독립 race 결과를 수용했다. 검토 범위에서 새 confirmed production finding은 남지 않았다.

**현재 요청의 오프라인 통합 행동은 검증했지만, repository-wide 검증에는 기존/Windows 실패가 남아 있어 전체 PR 가능·운영 가능 판정은 보완 필요다.**
실제 승인/재점검 adapter·Fencing/단일 Writer·PostgreSQL 경쟁/재시작·모든 Pod coverage·GitOps 모드 전환·복구 연계는 후속이다.
`ReplicaLag=FAIL`, 필수4 `NOT_VERIFIED`, `execution_gate=HOLD`, `recovery_authorized=false`, `cloud_verified=false`를 유지한다.
이 감사와 테스트 PASS는 push·PR·merge·배포 승인으로 해석하지 않는다.

## 전체 source 파일별 검토 목록

아래 목록은 `validation.json.source_inventory`와 동일하며 staging·unstaged·untracked source를 합산했다.
이전 감사8개는 historical metadata로 별도 인덱스하고 current source 검증으로 제외했다.

| 파일 | 검토 목적·영향 경계 |
| --- | --- |
| `contracts/http/openapi.yaml` | HTTP503 maintenance owning contract; HTTP consumer generic problem handling preserved |
| `db/migrations/000012_worker_control.sql` | Durable control/journal schema or migration documentation; no DB applied |
| `db/migrations/000013_worker_control_resume.sql` | Durable control/journal schema or migration documentation; no DB applied |
| `db/migrations/README.md` | Durable control/journal schema or migration documentation; no DB applied |
| `docs/backend/dr-mutation-admission.md` | Assembly/runbook, operation boundaries and verification gaps |
| `docs/backend/dr-worker-control.md` | Assembly/runbook, operation boundaries and verification gaps |
| `internal/postgres/migration_test.go` | Behavior regression/producer-consumer fixture; integration-tag files compile only |
| `internal/postgres/worker_control.go` | Postgres authoritative revision/epoch, tickets and separate INTENT/APPLIED transaction adapter |
| `internal/postgres/worker_control_integration_test.go` | Behavior regression/producer-consumer fixture; integration-tag files compile only |
| `internal/postgres/worker_control_resume.go` | Postgres authoritative revision/epoch, tickets and separate INTENT/APPLIED transaction adapter |
| `internal/postgres/worker_control_resume_test.go` | Behavior regression/producer-consumer fixture; integration-tag files compile only |
| `internal/postgres/worker_control_test.go` | Behavior regression/producer-consumer fixture; integration-tag files compile only |
| `internal/server/app/admission_test.go` | Behavior regression/producer-consumer fixture; integration-tag files compile only |
| `internal/server/app/app.go` | Production config and shared gate/control assembly; no live activation |
| `internal/server/app/control_stack.go` | Production config and shared gate/control assembly; no live activation |
| `internal/server/app/worker_control_test.go` | Behavior regression/producer-consumer fixture; integration-tag files compile only |
| `internal/server/connector/router.go` | Router final typed send check |
| `internal/server/connector/worker_control_test.go` | Behavior regression/producer-consumer fixture; integration-tag files compile only |
| `internal/server/httpapi/admission_test.go` | Behavior regression/producer-consumer fixture; integration-tag files compile only |
| `internal/server/httpapi/labexecution.go` | PR194 service admission and HTTP problem mapping |
| `internal/server/httpapi/maintenance_integration_test.go` | Behavior regression/producer-consumer fixture; integration-tag files compile only |
| `internal/server/httpapi/mutation_integration_test.go` | Behavior regression/producer-consumer fixture; integration-tag files compile only |
| `internal/server/labexecution/admission_test.go` | Behavior regression/producer-consumer fixture; integration-tag files compile only |
| `internal/server/labexecution/maintenance_test.go` | Behavior regression/producer-consumer fixture; integration-tag files compile only |
| `internal/server/labexecution/mutation.go` | PR194 service admission and HTTP problem mapping |
| `internal/server/labexecution/service.go` | PR194 service admission and HTTP problem mapping |
| `internal/server/maintenance/gate.go` | PR194 file admission and local whole-execution telemetry; no file writer |
| `internal/server/maintenance/gate_test.go` | Behavior regression/producer-consumer fixture; integration-tag files compile only |
| `internal/server/operation/maintenance_test.go` | Behavior regression/producer-consumer fixture; integration-tag files compile only |
| `internal/server/operation/worker.go` | PR194 whole-RunOne admission/drain |
| `internal/server/provision/executor.go` | Worker claim/prepare/dispatch/requeue and query-only Reconciliation composition |
| `internal/server/provision/maintenance_resume_test.go` | Behavior regression/producer-consumer fixture; integration-tag files compile only |
| `internal/server/provision/reconciliation.go` | Worker claim/prepare/dispatch/requeue and query-only Reconciliation composition |
| `internal/server/provision/worker_control_test.go` | Behavior regression/producer-consumer fixture; integration-tag files compile only |
| `internal/server/provision/worker_resume_test.go` | Behavior regression/producer-consumer fixture; integration-tag files compile only |
| `internal/server/workercontrol/control.go` | Typed boundary, durable settings, approved resume and maintenance bridge |
| `internal/server/workercontrol/control_test.go` | Behavior regression/producer-consumer fixture; integration-tag files compile only |
| `internal/server/workercontrol/durable.go` | Typed boundary, durable settings, approved resume and maintenance bridge |
| `internal/server/workercontrol/durable_test.go` | Behavior regression/producer-consumer fixture; integration-tag files compile only |
| `internal/server/workercontrol/maintenance_resume.go` | Typed boundary, durable settings, approved resume and maintenance bridge |
| `internal/server/workercontrol/maintenance_resume_test.go` | Behavior regression/producer-consumer fixture; integration-tag files compile only |
| `internal/server/workercontrol/resume.go` | Typed boundary, durable settings, approved resume and maintenance bridge |
| `internal/server/workercontrol/resume_test.go` | Behavior regression/producer-consumer fixture; integration-tag files compile only |
| `internal/server/workercontrol/settings.go` | Typed boundary, durable settings, approved resume and maintenance bridge |
| `internal/testsupport/workercontrolfake/resume.go` | Shared deterministic Fake durable state, tickets, resume journal; not real PostgreSQL |
| `internal/testsupport/workercontrolfake/store.go` | Shared deterministic Fake durable state, tickets, resume journal; not real PostgreSQL |
| `runtime/contract.yaml` | Runtime0.1.12 config, maintenance, durable control and approval bridge owning contract |
