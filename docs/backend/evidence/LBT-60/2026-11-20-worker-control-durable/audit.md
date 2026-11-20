# LBT-205 · 영속 Worker 제어 오프라인 Audit

2026-11-20 KST · PRE_MERGE_AUDIT.md 적용 · Admission + local Worker + durable Worker 누적 diff

## 범위·판정

요청한 공통 내부 제어 계약·영속 저장소 adapter·여러 실행 인스턴스의 claim/dispatch 연결을 구현하고
Fake/driver Mock·로컬 자동 검사로 검증했다. 실제 PostgreSQL lock/MVCC/내구성이나 Kubernetes 다중 Pod를 인수한 것은 아니다.
LBT-205 전체 완료·운영 승인 재개·DR GO·전체 CI 성공·PR/merge 준비 완료는 선언하지 않는다.
서울 서비스·실제 DB·AWS·외부 Connector·Slack을 조회/변경하지 않았다. commit/push/PR/merge/배포도 하지 않았다.

- Repo `ktcloud4-SL/labbit-app`, 같은 branch `codex/feat/LBT-60-dr-admission-block-20261119`.
- Base/HEAD/main 원격 읽기 모두 `2bccdbb9264749251f65e710d9acd2ebf3bc7d8c`.
- 시험 소스는 해당 HEAD + validation.json의 **누적 source_inventory 31개**다. 미커밋 상태이므로 raw/LF SHA256으로 식별한다.
- 이전 11/19 admission/local Worker audit 4개 파일은 당시 증분의 역사적 기록이다. 이전 SHA를 이번 source PASS로 재사용하지 않는다.
- 새 audit/validation 2개까지 전체 변경 inventory는 source31 + historical metadata4 + new metadata2다.
- ignored `.local/durable-checks/`에 원본 로그가 있다. formatter는 `.local/durable-fmt-lf/`의 폐기 사본을 사용했다.
  node_modules/고정 Python validators와 Go runtime은 검사 도구이며 dependency/lockfile 변경은 없다.

## 직접 확인한 SSOT와 AC 경계

| 자료 | 고정 버전·적용 |
| --- | --- |
| 사용자 | 동일 branch, 공통 계약·영속 store·여러 Pod claim/dispatch offline 시험. 실제 DB/서울 미변경, push 보류 |
| 개인/저장소 지침 | PRE_MERGE_AUDIT.md, AGENTS.md, CONTRIBUTING.md, TESTING.md, docs/backend/README. 적용 경로의 별도 하위 AGENTS 없음 |
| [LBT-205](https://samsunglions.atlassian.net/browse/LBT-205) | updated 2026-11-19 18:15:49+09:00 직접 조회. 전체 replica admission, epoch, 승인 재개, FE와 운영 감사 등 AC를 이 증분으로 낮추지 않음 |
| [LBT-59](https://samsunglions.atlassian.net/browse/LBT-59?focusedCommentId=11242) | updated 11/19 18:57:09+09:00, PM B안 개발 범위 승인 및 #11246 구현 추적. 실제 Writer/배포/복구 GO 아님 |
| [LBT-60](https://samsunglions.atlassian.net/browse/LBT-60?focusedCommentId=11245) | updated 11/19 18:57:22+09:00, D01–D06 문서 QA와 기존 FAIL/4NV/HOLD 유지 |
| [Confluence 63275012](https://samsunglions.atlassian.net/wiki/spaces/SL/pages/63275012) | 직접 읽은 최상단 11/19 증분의 B안/Stage2 제한 인수·Gate. 이번 connector 응답은 수치 version을 노출하지 않아 새 version 번호로 단정하지 않음 |
| [Infra PR57 D02–D05](https://github.com/ktcloud4-SL/labbit-infra/blob/5987cb061092d406ba8effaf3259d56e260246b1/runbooks/operations/aws-dr-backend-controls-proposal.md) | 고정 원본과 기존 local audit의 연결을 대조. PAUSE_NEW/RECONCILE_ONLY/ACTIVE와 기수락/불명 보존 |
| Runtime/DB owning SSOT | Runtime0.1.10 opt-in·binding/ticket·HOLD, 새 migration000012(미적용). 기존000004/000008/000009의 claim/lease/generation·미전송 처리 유지 |
| HTTP/Connector/Realtime | 누적 OpenAPI admission 변경 및 기존 protocol/Router consumer 대조. 이번 Worker 증분은 새 wire/HTTP endpoint나 message/Realtime schema를 추가하지 않음 |

## 전체 diff 목적·consumer 검토

| 파일/그룹 | 목적과 영향 |
| --- | --- |
| workercontrol/control.go, control_test.go | 로컬 permit/Pause 유지, opt-in durable Enter/Leave/Unverified latch/공유 Pause·Report 연결. 기존 병렬·cancel 회귀 포함 |
| workercontrol/durable.go | Scope/Binding/Phase/Ticket/State/Report 및 DurableStore 내부 계약, mode/revision/epoch 고정·새 값 자동 승인 금지 |
| workercontrol/settings.go | local/postgres opt-in 및 전체 필수 설정 validation. 부분 설정의 legacy fallback 거부 |
| workercontrol/durable_test.go | 여러 Control, restart/구 binding, 응답 유실/close 실패, exact ticket, report 변조·timeout, 48인스턴스 경계 drain |
| internal/postgres/worker_control.go | state FOR UPDATE + intent commit, exact Leave, Pause CAS/전체 request 재사용 검사, create-only PAUSE_NEW, 단일 snapshot Report |
| internal/postgres/worker_control_test.go | scripted driver의 commit/rollback·binding·request 변경·report/error 경계. 실제 PostgreSQL locking 대체 아님 |
| internal/postgres/worker_control_integration_test.go | 승인된 폐기 DB에서 나중에 실행할 migration·32 Enter·Pause·exact Leave 검사. 이번 compile-only, 실제 미실행 |
| db/migrations/000012_worker_control.sql | additive state/change/boundary 세 table + open intent index. 자동 만료·cascade 삭제·자동 활성화 없음 |
| internal/postgres/migration_test.go, db/migrations/README.md | embedded migration12 인식, loader/배포 전 schema readiness 영향·미적용 경계 |
| internal/testsupport/workercontrolfake/store.go | 테스트용 공유 journal. 실제 영속성/DB locking의 대체가 아니며 production code에서 사용하지 않음 |
| app/app.go, control_stack.go, worker_control_test.go | opt-in 설정 검증→실 store 주입→fresh boot→Executor/Router 단일 Control. 여러 production stack의 Fake 검증 |
| provision/executor.go, reconciliation.go, worker_control_test.go | claim/prepare/final send/requeue 별 phase, exact bare gate 오류만 미전송으로 처리. peer Pause 사이의 claim 응답/prepare 경합 3종 검증 |
| connector/router.go, worker_control_test.go | 이전 증분의 최종 direct-send guard가 shared Control에도 적용됨. 기존 전송/늦은 결과/inbound·Terminal/File/Preview 경계 유지 |
| labexecution/admission.go, service.go, mutation.go, admission_test.go | 이전 접수 차단·권한/committed replay 우선순위. 새 shared Worker gate와 아직 원자적 통합하지 않음 |
| httpapi/labexecution.go, admission_test.go, app/admission_test.go | 이전 503 mapping/role 설정·내부 호출·인증/권한 회귀 |
| contracts/http/openapi.yaml | 누적 admission owning code/검사 우선순위. 이번에는 추가 HTTP 제어 API 없음 |
| runtime/contract.yaml | 누적 admission/local + 영속 후보의 binding/intent/모드/설정 및 실제 적용 한계 |
| docs/backend/dr-mutation-admission.md, dr-worker-control.md | 누적 접수와 이번 저장소 연결·진행 순서·실DB 미검증·승인 재개 미구현 설명 |
| 이전/이번 audit.md·validation.json | 역사적 시험과 이번 정확 source/log inventory 분리. metadata를 실제 실행 코드로 오인하지 않음 |

변경31개 source의 정확 목록은 validation.json에 있다. 전체 누적 diff와 영향받는 worker.RunOne, DB claim/renew/prepare,
Router의 직접 전송/tagged harness, app 조립과 runtime/schema readiness consumer를 검토했다.
배포 workflow/GitOps/DB 기존 migration/SDK·dependency/기존 generic Worker SQL은 변경하지 않았다.

## 요구사항 → 구현 → 실제 검사 → 공백

| AC/Decision | 구현 위치 | 실행한 자동 검증 | 실제 환경/잔여 판정 |
| --- | --- | --- | --- |
| 공통 대상·revision/epoch 고정 | durable.go:35 Binding, settings.go, app 설정 | InvalidBinding/PartialRuntimeSettings, wrong mode/target/epoch/revision 회귀 | 설정 label은 STS/실 DB Writer/배포 coverage 증거 아님 |
| 영속 state·변경 이력·intent | migration000012, postgres Enter:96/Pause:144 | driver commit/rollback/error redaction, migration loader | 실제 SQL 실행·lock/MVCC·내구성 **미검증** |
| 여러 claim/dispatch 동일 정책 | app control_stack:117, claim wrapper, Control.acquire | ProductionAssemblyBlocksPeersWithoutDatabase, PeerPauseBetweenClaimAndDispatch 3종 | 여러 Control/실제 Executor·Router + 공유 Fake. 실제 Kubernetes Pod 아님 |
| prepare 뒤 새 dispatch 거부 | executor 최종 gate + Router gate | PeerPauseDuringPrepareBlocksFinalRouterAndRequeue 3종, 이전 local lifecycle 회귀 | 이미 진입한 경계는 완료 가능. Pause 접수 즉시 모든 전송0건 보장 아님 |
| 진행 중 경계·옛 revision 보존 | Ticket/Leave:119, Report:229 | PeerPauseBlocksClaimsAcrossInstancesAndRestart, 48인스턴스 LateRelease, exact old ticket 종료 | Report Drained는 참여 경계만. Provider0/전체 DB Fencing은 아님 |
| 응답 유실·중복 재실행 금지 | acquire/close Unverified latch | AmbiguousEntryAndCloseNeverAutoResolve 4분기, 이미 보낸 결과/UNKNOWN 기존 회귀 | 모의 실패만. 실제 commit 응답 유실/프로세스 kill은 미시험 |
| Pause CAS/명령 멱등성 | changes 전체 original binding + Pause | PauseCASAndIdempotency, changed mode/pod/boot/history target, create-only 충돌 | 내부 제한 명령만. 사용자 인증/승인 token 검증 기능 아님 |
| report 실패·만료되지 않는 intent | DurableReport:152, atomic query, auto-GC 없음 | RejectsUntrustedReportAndBoundsQueries, PauseFailureAndReportFailureNeverGlobalPass | 지표 TTL/PASS cache 대신 매번 fresh 작업. 전체 membership 발견/coverage는 후속 |
| 재시작·stale 요청 | immutable per-process pin + 새 BootID | restart old revision, 같은 이름 새 boot·다른 mode 거부 | 영속 기록은 SQL adapter로 작성, 실제 restart durability 실측 없음 |
| timeout·cancel | Enter/Pause/Report2초, Leave5초·WithoutCancel | canceled entry0, query deadline 검사, 기존 latch cancel 회귀 | 외부 Provider 중단·안전 재실행 의미 아님 |
| LBT205 API 전체Replica 차단 | 이전 BLOCK_NEW startup | admission unit·HTTP·권한/replay 이번 누적 race PASS | shared Worker gate와 API 접수의 원자적 연결/전체 deployment 검증 **미구현** |
| 승인 후 재개·epoch 변화 | create-only PAUSE_NEW. resume/epoch update endpoint 없음 | 최신 revision만 아는 ACTIVE 재시작도 authoritative mode 불일치 거부 | Approval/RECHECK/INTENT·Writer 검증·RUN/Plan/작업창과 연계된 재개는 **다음 범위** |
| HTTP·FE·기존 기능 | wire 변경 없음, 별도 admission503 | 기존 HTTP/Connector/App + Web 기본 검사 | 전용 DR 안내 UX, 실제 Browser/Terminal/Live/File/Preview E2E 미실행 |
| 정확 Evidence | validation source/raw log SHA | 최종8package race1278 test/subtest·상위379 PASS, skip0 | 실제 cloud/원격CI는 미실행. 전체 repo 실패는 별도 보존 |

## 수정 전 Findings·처리

| ID/심각도 | 재현·영향/수정 전 위치 | 사전 보고·해결·회귀 |
| --- | --- | --- |
| DC-01/P2 | 초기 postgres/worker_control.go Pause history 비교(약172): expected revision/epoch만 같으면 다른 Mode/PodID/BootID의 동일 RequestID도 이전 결과로 수락. Fake는 전체 Binding을 비교해 adapter와 oracle 차이 발생 | 사용자에게 수정 전 보고, reviewer 독립 확인. changes에 원래 target/mode/pod/boot 저장·전체 비교. changed-mode/pod/boot/history-target driver 회귀 PASS |
| DC-02/P1 | 새 migration000012:3–EOF의 필수 BEGIN/COMMIT 누락. migration.go:105–108이 거부하여 opt-in과 무관한 새 build loader/startup readiness 영향 | root는 최초 P2로 보고 후, reviewer가 전체 build 영향으로 P1 상향. envelope와 migration_test.go의12개 목록 수정. 기존 embedded loader·invalid envelope 포함 최종 postgres race PASS. 실제 적용은 없었음 |
| DC-TEST-01/P3 | app/worker_control_test.go 초기 설정 fixture에 필수 PUBLIC_ORIGIN 누락. api 기본 validation에서 먼저 종료돼 persistent 설정 의도 분기에 도달하지 못함 | 사용자에게 사전 보고 후 fixture origin 추가. 설정 3분기와 fail-before-DB 회귀 PASS. 최초 출력은 대화 tool 결과에만 남고 별도 raw log는 보관하지 못함 |
| HISTORICAL | 이전 audit의 WC-01/WC-02/WC-TEST-01 및 admission findings | 과거 이력으로 유지하며 이번 full 관련 race에 누적 회귀 포함. 이전 기록을 덮어쓰지 않음 |

수정 후 독립 reviewer와 부모의 추가 검토 범위에서 미해결 confirmed P0/P1/P2는 발견하지 못했다.
일반 DB Writer 권한·실행 승인·배포 coverage 등 미검증을 해결된 finding이나 실제 안전성 PASS로 바꾸지 않는다.

## 실패·보안·수명주기 분석

- Enter와 Pause는 같은 state row를 직렬화한다. intent commit 뒤 DB/Connector 작업을 하며 외부 I/O 동안 transaction을 열어두지 않는다.
  이미 진입한 claim/prepare/send/requeue는 끝날 수 있어 Pause가 commit됐다는 사실만으로 중단 완료를 주장하지 않는다.
- 전송 전 새 Router 경계도 exact binding을 요구한다. epoch/revision이 바뀐 지연 claim/prepare는 새 dispatch를 할 수 없으며
  committed claim은 자동 PENDING/FAILED 정리하지 않는다. 기존 정산·lease/generation ownership을 유지한다.
- Enter commit 응답 불명은 자동 Leave나 새 ticket 재시도를 하지 않는다. Local count가0이어도 authoritative open intent는 남을 수 있다.
  Leave 불명은 Unverified를 latch하고 이후 새 진입을 차단한다. 정확 ticket 재조회/멱등 종료와 외부 mutation 재시도는 다르다.
- Pause CAS 실패/timeout은 local latch를 풀지 않고 global PASS를 반환하지 않는다. peer 미완료 경계는 ErrHold다.
  Report는 모든 revision/boot를 같은 snapshot에서 확인하며 잘못된 target/뒤로 간 revision/다른 epoch/관측 누락을 거부한다.
- 시작 binding은 immutable이고 더 새 revision/epoch를 읽는 것으로 자동 재개하지 않는다. Pod name 재사용은 fresh BootID로 구분한다.
  init은 PAUSE_NEW만 생성하고 다른 scope/기존 epoch/ACTIVE를 reset하지 않는다. old intent의 TTL 자동 만료/GC는 없다.
- 이미 보낸 Provider의 결과·UNKNOWN·lease/DB 결과 반영은 계속된다. Reconciliation도 DB writes이므로 Writer=NONE/복제본에서 안전하다고 주장하지 않는다.
- mode/대상 설정·error는 고정 typed 오류를 사용하며 원본 DB/Provider 에러·Secret/DSN을 외부에 복사하지 않는다.
  새 HTTP 운영 endpoint·인증 우회·임의 request boolean은 없다. 운영자 역할/DB role 분리·SQL 관리자 직접 변경 방지는 미인수다.
- 같은 authoritative DB/scope/새 image+opt-in 참여자만 통제한다. 구 Pod/local/다른 DB/async replica/직접 Provider writer는 범위 밖이다.
  비동기 복제본을 control Writer로 재사용하는 DR 경로의 intent 유실·split brain은 이 코드로 해소되지 않는다.
- API admission/Terminal/Live/File/Preview/Job의 전체 writes는 별도다. 공유 Worker pause만으로 full Single Writer/전역 barrier를 주장하지 않는다.
- SQL schema는 additive지만 새 build readiness가 미적용000012도 요구한다. 기본 local이라고 배포 영향이 없는 것은 아니다.
  실제 rollout 전 migration/권한·대상·기존 버전 compatibility를 별도 승인 범위에서 확인해야 한다.
- intent/change history의 보존·수동 조사/정리 담당·공간 증가 관리는 운영 후속이다. 자동 삭제나 만료 처리를 추가하지 않았다.

## 실행·skip·미실행 Evidence

Windows/amd64 Go1.27.1(+race), Python3.13.14+CI 고정 validators, Node24.22.3.
외부 경계는 Fake/scripted driver/in-process·loopback fixture만 사용했다.

| 검사 | 결과·한계 |
| --- | --- |
| 최종 관련8package race | 1278 test/subtest event, 상위379 PASS, package8 PASS, test skip0, race 경고0 |
| 독립 reviewer focused race | durable/Worker/Pause/migration 관련5package PASS, reviewer 직접 실행 |
| go vet/build ./... | exit0 |
| gofmt | 전체610 Go 파일 LF 정규화 폐기 사본 PASS. Windows 원본 일괄 개행 수정 없음 |
| owning 계약 | OpenAPI/Runtime0.1.10/JSON schema8/migration12·envelope PASS |
| PostgreSQL integration 소스 | integration tag + -run '^$' compile-only exit0, **테스트0/실DB0** |
| 전체 Go | 최종 원본·실패/skip 목록 validation.json. 이전 baseline Installer/OpenStack 대표 실패와 대조; 전체 저장소 PASS 아님 |
| Web typecheck/lint/build | exit0. 기존 chunk 크기 권고만 |
| Web full | 491/493 PASS, 동일 installer env 임시폴더 rmSync Windows EPERM2건 |
| Web 동일 파일 focused | 30/30 PASS, 최초 전체 실패를 삭제/대체하지 않음 |
| 초기 focused/full | migration envelope 실패·초기 adapter/fixture 증분 원본 구분. 초기 실패를 최종 PASS로 소급하지 않음 |
| 실 DB/VM/Provider/AWS/Browser·K8s/원격CI | **미실행**. 기본 Go opt-in integration/환경별 skip15개와 이유는 JSON에 보존 |

이전 pristine base 대표 Installer/OpenStack 실패·CIDR timeout 로그는 역사적 참고다.
모든 OpenStack 실패를 이번 새 pristine 실행에서 개별 재현했다고 주장하지 않는다.
새 integration 소스는32개 concurrent Enter가 끝난 뒤 Pause하는 구성이므로 실제 Enter/Pause 경쟁을 시험한 것으로 표시하지 않는다.
승인된 폐기 PostgreSQL 후속 인수에서 실제 동시성·응답 유실/단절·재시작·권한을 별도로 시험해야 한다.

## 독립 검토·재현·최종 한계

`/root/review_admission_local`이 구현자와 분리해 새 binding/store/schema/설정/생산 경계·Fake와 consumer를 검토했다.
DC-01을 독립 확인했고 DC-02를 직접 재현했으며 수정 후 관련5package race를 직접 실행했다.
최신 Jira 원문은 부모가 직접 읽어 제공한 범위이고 reviewer 별도 외부 조회로 확대하지 않는다.
기존 Admission/local Worker 누적 검토와 이번 영향 검토를 연결하고, 최종 metadata/source/log 확인은 부모의 별도 패스로 수행한다.

재현은 동일 HEAD와 source_inventory를 복원하고 프로젝트 dependency를 설치한 뒤 실행한다.

```text
go test -race ./internal/postgres ./internal/server/workercontrol ./internal/server/app ./internal/server/provision ./internal/server/connector ./internal/server/operation ./internal/server/labexecution ./internal/server/httpapi -count=1 -timeout=120s
go vet ./...
go build ./...
go test ./... -count=1 -timeout=90s
go test -tags integration ./internal/postgres -run '^$' -count=1
```

마지막 명령은 compile-only다. 실제 integration 실행은 사용자 금지 범위를 해제한 별도 승인 없이는 진행하지 않는다.
Web는 Node24 npm ci 후 typecheck/lint/test/build, 계약은 .github/workflows/contracts.yml의 고정 validator를 사용한다.
현재 로그는 ignored 로컬 파일이며 source와 로그 SHA를 JSON에 기록한다. 공개 CI/외부 QA에서 실행됐다고 표시하지 않는다.

남은 운영 범위는 실제 제어 저장소 배치/권한/내구성, 전체 replica coverage 및 API gate와의 연결,
운영자 인증·RUN/Plan/작업창·승인/RECHECK/INTENT·Writer/기수락 정산에 묶인 재개, epoch 전환과 실제 DR 통합이다.
이번 요청의 코드·오프라인 검증 범위만 확인하며 더 큰 LBT-205 전체 완료·배포/PR 가능으로 선언하지 않는다.
기존 ReplicaLag FAIL·필수4 NOT_VERIFIED·execution_gate=HOLD·recovery_authorized=false·cloud_verified=false는 유지한다.
