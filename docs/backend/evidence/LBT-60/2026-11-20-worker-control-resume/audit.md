# LBT-205 승인 후 Worker 재개 · 오프라인 Audit

2026-11-20 KST · PRE_MERGE_AUDIT.md 적용 · 동일 branch 누적 변경 검토

## 판정과 고정 소스

요청한 승인 후 재개 내부 use case·PostgreSQL 후보 adapter·모의 검증 범위는 구현/검사했다.
실제 승인 시스템·Writer/Fencing·PostgreSQL locking/내구성·Kubernetes Pod를 검증한 것은 아니다.
LBT-205 전체 완료, DR GO, 실제 운영 재개 또는 PR/배포 준비 완료는 선언하지 않는다.

- 저장소 `ktcloud4-SL/labbit-app`.
- 동일 branch `codex/feat/LBT-60-dr-admission-block-20261119`.
- base/HEAD/읽기 전용 원격 main 조회: `2bccdbb9264749251f65e710d9acd2ebf3bc7d8c`.
- tested source는 이 HEAD와 누적 source_inventory **38개**의 미커밋 변경이다. validation.json에 raw/LF SHA256을 고정한다.
- 누적 변경은 source38 + 이전 audit/validation6 + 이번 metadata2 = 46개 파일이다.
- 과거 11/19 admission/local 및 11/20 durable의 audit6개는 그 당시 소스의 기록이다. 과거 SHA를 현재 PASS로 재사용하지 않는다.
- `.local/resume-checks/`는 ignored 원본 실행 로그, `.local/resume-fmt-lf/`는 formatter용 LF 사본이다.
  기존 node_modules/고정 Python validators/Go runtime을 사용했다. dependency/lockfile 변경은 없다.
- actual PostgreSQL/AWS/서울 서비스/배포/Slack/Provider mutation/commit/push/PR/merge는 수행하지 않았다.
  기존 generic unit suite의 로컬 unavailable Valkey probe 등은 skip 결과에 구분하며 실 DB 인수로 취급하지 않는다.

## 최신 SSOT와 충돌 경계

| 원문 | 확인 버전·적용 |
| --- | --- |
| 사용자 | 승인 재개를 같은 branch에서 구현·모의 검사, 서울/실제 DB 미변경, push 보류 |
| 개인·저장소 지침 | PRE_MERGE_AUDIT.md, AGENTS.md, CONTRIBUTING.md, TESTING.md, docs/backend/README.md 및 관련 하위 지침 부재 확인 |
| [LBT-205](https://samsunglions.atlassian.net/browse/LBT-205) | updated 2026-11-20T10:31:49.849+0900, AC 전체와 #11248/#11251 직접 읽음. 승인/RECHECK/INTENT 이후 재개, stale epoch 거부·UNKNOWN 보존 |
| [App Draft PR194](https://github.com/ktcloud4-SL/labbit-app/pull/194) | 직접 metadata/diff 확인. HEAD `34ed442538d45bc3ce9d47f73a3bef0584cf6dc6`, Draft/Open/미병합, base2bcc. maintenance file의 별도 기존 구현으로 이번 후보가 대체/연계한 것이 아님 |
| [LBT-59 #11242](https://samsunglions.atlassian.net/browse/LBT-59?focusedCommentId=11242) | updated 2026-11-20T10:32:14.955+0900 (독립 reviewer 재조회). B안 개발 승인·실 DR 실행 승인 구분 |
| [LBT-60 #11244/45](https://samsunglions.atlassian.net/browse/LBT-60?focusedCommentId=11245) | updated 11/19T18:57:22.090+0900. 단계별 신규 승인/RECHECK/INTENT, Failback 별도 승인, UNKNOWN/HOLD/no retry |
| [Confluence Gate Owner](https://samsunglions.atlassian.net/wiki/spaces/SL/pages/63275012) | 직접 조회한 Gate/Stage2 제한 인수·Writer/기수락 작업 범위. connector가 numeric version을 노출하지 않아 버전을 추정하지 않음. 최신 Jira 결정 우선 |
| [Infra D02–D05](https://github.com/ktcloud4-SL/labbit-infra/blob/5987cb061092d406ba8effaf3259d56e260246b1/runbooks/operations/aws-dr-backend-controls-proposal.md) | 고정 worktree 원본 재독. expected revision, 전체 대상/RUN/Plan/단계/승인 binding, 멱등 명령과 실제 관측 분리 |
| Runtime/DB/HTTP SSOT | Runtime0.1.11 approved_resume·durable/local/admission, 새000013와 기존000012/000004/000008/000009. 누적 OpenAPI/기존 Connector·Realtime caller 변경 경계 검토 |

PR194와 이번 branch는 app/provision/labexecution/Runtime/HTTP 경계에 중첩된다.
**자동 merge·기존 maintenance 대체·운영 OPEN 연동을 하지 않았다.** 사용자에게 최신 중복 경계를 알렸고,
명시 요청한 동일 branch의 격리된 opt-in 후보만 진행했다. 향후 통합 전에 공개 HTTP code/설정·공통 gate 조립을 재검토해야 한다.
운영 승인 port·실환경 Evidence가 없다는 점을 새 제품 안전조건 충족으로 낮추지 않는다.

## 전체 누적 diff 및 consumer 검토

| 파일/그룹 | 요구·영향·검토 범위 |
| --- | --- |
| contracts/http/openapi.yaml | 이전 admission503·권한/committed replay 계약. 이번에는 공개 재개 endpoint/새 wire 추가 없음 |
| labexecution/admission.go·service.go·mutation.go·admission_test.go | 이전 startup BLOCK_NEW와 신규 등록0·기존 권한/replay. Worker 재개와 API OPEN은 아직 별도 |
| httpapi/labexecution.go·admission_test.go, app/admission_test.go | 기존 503 매핑·handler·role·security 회귀 |
| app/app.go·control_stack.go·worker_control_test.go | local/postgres 설정 검증, 동일 Executor/Router gate·fresh boot. startup에 Resumer를 조립/호출하지 않음 |
| workercontrol/control.go·control_test.go | 기존 local pause·inflight·unverified latch 유지. Resumer는 기존 Control을 열지 않음 |
| workercontrol/durable.go·durable_test.go·settings.go | immutable Scope/revision/epoch/Pod/boot, Report에 PendingResumes 추가. 음수·pending 거부, 부분 설정 fail-closed |
| workercontrol/resume.go | exact request SHA·trusted Authorizer/Rechecker·시각/필수 PASS·Readback/INTENT/한 번 Apply·UNKNOWN 처리 |
| workercontrol/resume_test.go | 승인/선행조건 부정·새 binding·16 Control·32 중복 요청·restart·pending·Failback·시간·잘못된 record 회귀 |
| postgres/worker_control.go·worker_control_test.go | 기존 row-lock 경계·Pause·exact Leave 유지, 단일 Report snapshot에 pending resume count 추가 |
| postgres/worker_control_resume.go·worker_control_resume_test.go | separate INTENT commit, state-row lock, exact payload/승인 단일소비·원자 Apply/receipt, 상태·시간 재검증·오류 비노출 |
| postgres/worker_control_integration_test.go | 기존 boundary + 새 승인 journal/16 병렬 Apply source. compile-only, 실제 미실행 |
| postgres/migration_test.go, db/migrations/README.md | embedded 13개 인식·미적용/새 build readiness 영향 설명 |
| db/migrations/000012_worker_control.sql | 이전 additive 후보 그대로. 기존 state/changes/boundaries·자동 GC 없음 |
| db/migrations/000013_worker_control_resume.sql | 요청/검증/approval ID·INTENT/APPLIED 보존, approval unique·scope pending unique. BEGIN/COMMIT envelope |
| provision/executor.go·reconciliation.go·worker_control_test.go | 이전 claim/prepare/send/requeue와 late result/UNKNOWN 보존. 새 재개 상태도 같은 Enter 소비 |
| provision/worker_resume_test.go | 2 Executor + 공유 Fake queue/journal. 3 mutation 각각 승인 전0·승인 후 queued item 전송1건·옛 Control 차단 |
| connector/router.go·worker_control_test.go | 이전 최종 direct-send gate. 새 binding도 같은 authoritative Enter. inbound/late result/Terminal·File·Preview 비변경 |
| testsupport/workercontrolfake/store.go·resume.go | 테스트 전용 공유 journal·중단 전/후 fault·clock·별도 INTENT/APPLIED. production import 없음 |
| runtime/contract.yaml, docs/backend/dr-worker-control.md·dr-mutation-admission.md | owning Runtime0.1.11와 구현/한계, PR194 연동 공백. 이전 admission 문서 유지 |
| 이전6/이번2 metadata | 역사적 시험과 현재38개 source/hash 분리. 실행 코드 아닌 감사 기록 |

validation.json은 staged/unstaged/untracked를 포함한 모든 파일과 hash를 명시한다.
기존 Worker DB claim/lease/prepare·Router 소비자·app/schema readiness·HTTP/Connector/Realtime 계약 경계를 대조했다.
기존 migration1~11·operation DB SQL·GitOps·배포 workflow·dependency는 수정하지 않았다.

## 요구사항 → 구현 → 실행 테스트 → 실제 Evidence 공백

| 요구/AC | 구현 | 실제 실행한 자동 검증 | 실제 환경·판정 |
| --- | --- | --- | --- |
| 인가된 재개만 | resume.go:41/96/195, mandatory Authorizer/Rechecker | RejectsApprovalAndRecheckFailuresBeforeIntent 18분기, MissingPorts | mock port만. 실제 인증/권한·승인 원문 검증 미구현 |
| Target/RUN/Plan/Phase/요청 동일성 | canonical request SHA + proof binding | ConcurrentCommandsOnlyOneTransitionAndExactReplay, wrong-request/evidence/principal | 내부 값/해시 확인, 실제 원본 Evidence 취득은 port 책임 |
| 별도 승인·작업창/신선도 | UTC microsecond 1~9999, strict expiry, DB now 재검증 | expired/notyet/future/old, ExpiredStagedProof, UnencodableTimes | 운영 승인 TTL/clock trust 미확정, Stage2 TTL 재사용 안 함 |
| Fencing/Single Writer/정산 선행 | Check PASS 조건, ACTIVE WorkReconciled 추가 | writer/fencing/inventory/coverage/reconciled 부정 → INTENT0 | 해당 PASS는 trusted port 합성값. 실제 Fencing/Single Writer 미검증 |
| 승인→RECHECK→INTENT→Apply | postgres BeginResume:73 / ApplyResume:133 | BeginRecordsIntentSeparately, ApplyRechecksAndCommitsStateReceiptAtomically | driver Mock은 실제 transaction/row lock 증명 아님 |
| open/불명 상태 보존 | PendingResumes·scope pending unique·noTTL/noGC | OpenOldIntent, PendingIntentKeepsAuthoritativePauseHold | 공유 Fake/driver. 미해결 INTENT 강제 clear/조사 도구 후속 |
| 응답 유실·재시작 | Resume/Inspect + persisted INTENT/APPLIED | UnknownNeverAutoReappliesAcrossRestart 5분기, MissingDuplicateReceipt | 실제 process kill/network partition/DB durability 미시험 |
| 단일 명령·승인 소비 | commandPK, approvalUNIQUE, scopependingUNIQUE, state lock | 32 samecommand·duplicate replay·ApprovalConsumedOnce | 실제 unique/lock/MVCC 통합 test는 compile-only |
| 여러 Worker 소비 | 새 rev/epoch immutable binding + 기존 Enter | ApprovedTransitionRequiresNewBindingAcrossWorkers, 2 Executors 3종 | 실제 Kubernetes 전체 coverage 아님. 새 승인 배포 연결 후속 |
| 옛 Writer/Failback | 각 Apply rev+1/epoch+1, 새 승인/RUN/Phase | ApprovalConsumedOnceAndFailbackNeedsFreshAuthorization | 같은 authoritative store의 참여 binding만. cross-region split brain 미검증 |
| 기존 접수·결과/UNKNOWN 보존 | 기존 service·Executor/Router 범위 유지 | 관련8package race1373 PASS·기존 local/durable 회귀 | API OPEN 자동 연동 없음, 이미 수락한 Provider 작업취소 아님 |
| migration/계약 | Runtime0.1.11·13개 loader/envelope | Contracts/Gofmt/Vet/Build PASS, integration compile-only | 실제 DB 조회/적용0. opt-in off여도 새 build readiness에 영향 |

## 수정 전 Findings 및 해결

| ID·심각도 | 수정 전 위치·재현 조건·영향 | 사전 보고·수정·회귀 |
| --- | --- | --- |
| R-01/P2 | 초기 resume.go !created 분기(기존약224): Begin=false 뒤 Lookup=false일 때 found를 버려 빈 receipt,nil 반환. 실제 적용 근거 없이 성공처럼 보일 수 있음 | reviewer source 확인→사용자 보고 후 수정. !found면 ErrUnknown. MissingDuplicateReceiptNeverReportsSuccess 회귀 PASS |
| R-02/P2 | 초기 resume.go:37 exactTime/53 SHA 및 postgres encoding: UTC·microsecond만 검사해 음수/>9999 연도의 JSON 실패를 무시. 서로 다른 요청이 빈 바이트 hash로 충돌하거나 빈 payload INTENT가 남을 수 있음 | reviewer source 확인→사용자 사전 보고. 연도1~9999/Marshal error·invalid hash 거부. UnencodableTimesAndCanonicalHashCollision PASS |
| R-03/P2 | 기존 durable.go Report.Drained + postgres Report: 열린 경계0이어도 pending resume INTENT를 누락하여 Pause가 nil/Drained=true 가능 | reviewer source 확인→사용자 보고. PendingResumes 단일 snapshot·음수 거부·Drained/HOLD 연결. PendingIntentKeepsAuthoritativePauseHold PASS |
| R-TEST-01/P2 | 초기 resume_test.go unresolved-boundary fixture: 같은 Control에서 open permit을 defer release한 채 동기 Pause, release 이전 drain 대기해 90s timeout | 독립 reviewer 확인→사용자 보고 후 peer Control이 Pause하도록 수정. 최초 timeout 원본 로그 보존, 최종 관련 race PASS |
| HISTORICAL | 이전 admission/local/durable audit findings | 이전6개 기록 보존. 신규 audit로 과거 결과를 덮어쓰지 않고 현재 누적 회귀를 재실행 |

초기 구현의 source snapshot 전체 hash는 보관하지 못했으며 initial-worker raw log와 수정 전 위치/재현 경로를 보존한다.
최종 검증 source는 별도 inventory로 고정한다. 원문·수정·회귀와 독립 재검토 뒤 미해결 confirmed P0/P1/P2는 발견하지 못했다.

## 실패·보안·수명주기

- 승인 문자열/approved=true는 불충분. 실제 서버 인증·권한·예외수용 근거는 mandatory trusted port의 책임이며 기본 allow 없음.
- bounded label/hash/UUID/시각만 journal에 저장한다. credential·raw provider/auth 오류를 외부 오류에 포함하지 않는다.
- 각 port/DB 호출은 2s context deadline을 받는다. 실제 port의 context 준수·시계 신뢰·권한 분리는 실연계 전 Gate다.
- Begin/Apply는 짧은 transaction이고 외부 검증 호출 중 row lock을 잡지 않는다. Apply 직전 저장된 proof의 binding·시간과 DB state/open count를 재검증한다.
  외부 승인 취소/Fencing 상태 변화를 DB와 원자화하지는 않는다. 신뢰 가능한 검증·승인 유효성 보장과 운영 변경 통제가 여전히 필요하다.
- 승인 유효기간은 제어 명령의 시간 경계이며 이후 모든 작업의 lease가 아니다. 승인 유효기간 만료로 작업을 자동 취소/재실행하지 않는다.
- INTENT 이후 실패는 자동 compensation/clear/새 command 우회 없이 UNKNOWN/HOLD다. commit 응답 유실 후 APPLIED가 있으면 read-only 조회만 한다.
- transaction rollback은 승인 재개 INTENT를 지우지 않는다. timeout 전에 실제 Apply가 commit됐을 수도 있어 결과 확인 없이 새 Control을 기동해서는 안 된다.
- 기존 Control의 pause/unverified/old binding은 그대로이며, 새 승인 배포의 exact new binding에만 Enter를 허용한다. 승인 receipt는 dispatch 권한/전체 DR GO가 아니다.
- open intent·pending command는 TTL·Pod 소멸·재시작으로 삭제하지 않는다. 후속 승인된 조사/정산 없이는 가용성보다 안전한 HOLD를 택한다.
- 현재 storage는 기존 pool을 사용하는 후보다. 비동기 replica/양 리전 별도 DB/유실된 journal로 제어 Writer를 복제하면 안전하지 않다. 실제 저장소·권한·Failback 계보는 별도 확정이다.
- 새 migration은 schema readiness도 변경한다. 실제 DB 적용/서울 배포를 한 적 없으며, 기본 local이라도 새 build의 무영향 배포를 주장하지 않는다.
- PR194 maintenance와 API gate OPEN은 별도다. 실제 Step Functions/SSM/GitOps 배포·Slack 전송·HTTP 관리 endpoint는 이번 범위 밖이다.
- Terminal/Live/File/Preview·auth·기존 Worker result/lease 의미는 기존 계약대로다. 이 경계 제어가 전체 DB 쓰기/Fencing을 보장하지 않는다.
- 실제 생성한 AWS/DB/Provider 자원0, 정리 대상0. 테스트 임시파일/빌드 산출물과 formatter mirror만 남는다.

## 실행·실패·skip·미실행 구분

| 검사 | 결과/한계 |
| --- | --- |
| 최종 관련8package 전체 race | **1373 test/subtest PASS, 상위394 PASS, package8 PASS, skip0, race warning0** |
| 독립 reviewer race | workercontrol/postgres/provision/connector/app **5package PASS** |
| Go vet/build | 각 exit0 |
| Contracts | OpenAPI·Runtime0.1.11·JSON schema8·migration13 번호/BEGIN-COMMIT PASS |
| gofmt | LF 사본 **616 Go 파일 PASS**. Windows argument length 초과로 최초 단일 호출은 실패했고40개 batch로 전부 재실행. 원본 파일을 일괄 LF 변경하지 않음 |
| 전체 Go | exit1, package47 PASS/2 FAIL/14 no-test, test/subtest3916 PASS/39 FAIL/15 SKIP |
| 전체 Go 실패 | 변경하지 않은 Installer behavioral shell suite와 OpenStack CIDR/Windows fsync·잠금 계열 및90s timeout. 기존 동일 base pristine 대표 실패 원본과 구분 연결. 모든 실패 사례를 이번 pristine에서 재실행한 것은 아님 |
| Web typecheck/lint/build | 각 exit0, build 기존 chunk advisory |
| Web full | **493/493 PASS, skip0**. 이전 durable 증분의 Windows temp cleanup EPERM은 역사적 기록으로 유지하고 이번 결과와 섞지 않음 |
| Integration | `go test -tags integration ./internal/postgres -run '^$' -count=1` exit0, **실행 test0·실 DB0**. reviewer도 compile-only |
| 최초 Worker race | fixture 자기 Pause deadlock으로90s timeout exit1. 수정 후 결과와 원본을 모두 보존 |
| 실환경/원격 CI | 실행하지 않음. Cloud/Provider/Browser/실 SQL·migration/실 인증/실 Pod/Seoul/Slack 미검증 |

실제 실행한 테스트 이름·실패/skip 사유·raw log/hash는 validation.json에 있다.
skip15건은 unavailable local Valkey·Windows POSIX/symlink·opt-in OpenStack 실환경 테스트 등 기존 환경 조건이다.
기존 baseline는 `.local/admission-checks/baseline-failures.jsonl`/`baseline-hang.jsonl`의 같은2bcc 소스 대표 재현이며
현재3916 PASS 또는 새 재개 기능의 실제 DB 검증으로 대체하지 않는다.

재현은 고정 base+source inventory, Go1.27.1 Windows/amd64와 race toolchain, Node24.22.3 및 기존 npm dependencies,
고정 Python3.13.14/contract validators가 필요하다. 일반 Go/Web 검사는 DB 없이 수행하며 integration은 별도 승인 DB에서만 실행한다.
원본 로그는 로컬 ignored 경로여서 원격 공유본이 아니다. push/PR 승인 후에는 필요한 비민감 raw artifact를 별도 인계해야 한다.

## 독립 검토와 후속

개인 audit 지침의 명시 요구에 따라 구현하지 않은 `/root/review_worker_resume`가 전체 누적 diff와 새 경계·최신 SSOT를 독립 검토했다.
reviewer도 Jira LBT205/59/60 및 Confluence D24 v13을 직접 읽었고, source findings 세 개와 fixture deadlock을 먼저 보고했다.
수정 후 Runtime/docs·새 migration·driver/Fake·실제 Executor 소비자 및 실패 경로를 다시 확인했다.
5package race와 integration compile-only·git diff --check exit0을 독립 확인했으며 추가 confirmed P0/P1/P2는 없었다.
SQL 검토는 source/scripted driver이고, source hash/log·metadata 정합성은 부모가 별도 두 번째 pass로 확인한다.

후속은 실제 trusted Authorizer/Rechecker 구현, PR194와 조립·계약 통합, 승인된 폐기 DB의 migration/locking/concurrent Enter-Pause-Resume,
승인 배포의 새 binding 적용과 전체 Pod coverage·동시 Writer·실제 Provider 기수락 작업 정산이다.
이번 모의 승인·PASS 값은 기존 실제 ReplicaLag FAIL이나 4개 NOT_VERIFIED를 해소하지 않는다.
`execution_gate=HOLD`, `recovery_authorized=false`, `cloud_verified=false`를 유지한다.

**최종 범위 판정:** 요청한 동일 branch의 승인 후 재개 오프라인 증분은 검증했다.
전체 repository test 실패·실 SQL/인증/coverage·PR194 통합이 남아 전체 LBT-205 완료/운영 인수/PR-ready는 선언하지 않는다.
audit 통과가 push/commit/PR/merge/배포/외부 전송의 승인도 아니다.
