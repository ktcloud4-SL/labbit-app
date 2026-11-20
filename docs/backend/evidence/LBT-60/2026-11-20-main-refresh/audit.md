# 최신 main 기반 Worker 제어·승인 재개 Draft 검토

실행일: 2026-11-20 KST. 사용자 `PRE_MERGE_AUDIT.md` 및 저장소 AGENTS/CONTRIBUTING/TESTING 기준.
이 기록은 과거 로컬 감사의 최신 main 재검토이며, 실제 DR 완료·merge·배포 승인이 아니다.

## 버전과 전체 범위

- 저장소 `ktcloud4-SL/labbit-app`, 작업 브랜치 `codex/feat/LBT-60-dr-admission-block-20261119`.
- base 및 테스트 시 HEAD: `753eecbff737409a4c9cc8c542f3c6baa6acf0da`.
- PR194는 11/20 11:22:28 KST에 main으로 squash merge된 상태다. 과거 Draft 상태 설명은 현재 상태로 재사용하지 않는다.
- 기존 후보를 별도 백업한 뒤 작업 브랜치만 최신 main으로 갱신했다. 원격 main이나 GitOps를 수정하지 않았다.
- 전체 추가 변경은 source/계약/문서/테스트 **39개**, 과거 감사 기록 **10개**, 이번 감사 **2개**다. 파일별 목적·원본 SHA256·LF 정규화 SHA256은 `validation.json`에 전부 기록했다. 이번 두 감사 파일은 자기 참조 해시 대상에서 제외한다.
- PR194에서 이미 merge된 Service/HTTP mapper/일반 Worker gate·통합 fixture는 main 원본을 유지한다. main의 새 `mutation_test.go` maintenance 회귀도 보존한다. 이 PR에 같은 접수 차단을 중복 구현하지 않는다.
- Runtime은 main의 maintenance admission/barrier/platform 전체 문장을 보존하고 별도 Worker 제어·승인 재개·query 관측을 추가했다. `artifacts.migration.config`의 원래 네 key를 보존했다.
- 미커밋 실행 소스는 `validation.json`의 파일 해시로 식별한다. Go/web 실행 이후 변경은 P3 문서 두 곳뿐이며 최종 OpenAPI/CI 계약 파싱을 다시 수행했다.
- 과거 감사 10개는 당시 base `2bccdbb`·테스트의 이력이다. 해당 기록의 no-push/PR194 Draft 문구는 과거 시점에 한정되며 현재 권한·원격 상태·테스트 결과를 대신하지 않는다. 현재 결과는 이 디렉터리가 기준이다.
- ignored `.local/main-refresh-before/` 백업, `.local/main-refresh-checks/` runner·원본 로그 및 로컬 도구는 배포 소스가 아니다. 원본 로그 SHA를 기록했지만 저장소에 로그 자체를 게시하지 않으므로 아래 명령으로 재현해야 한다.

## 직접 확인한 최신 SSOT

| 원문 | 확인 버전·적용 범위 |
| --- | --- |
| [PR194](https://github.com/ktcloud4-SL/labbit-app/pull/194) | merge main `753eecb`; 접수 503 maintenance, 현재 권한·committed replay 우선, 파일 gate와 admitted Worker drain |
| [LBT-205](https://samsunglions.atlassian.net/browse/LBT-205?focusedCommentId=11257) | updated 2026-11-20T11:43:49.420+0900, #11255/#11257. main753/GitOps126 서울 open 배포는 Owner 보고이며 이 작업에서 실조회하지 않음. closed 운영·Fencing은 미검증 |
| [LBT-59](https://samsunglions.atlassian.net/browse/LBT-59?focusedCommentId=11258) | updated 2026-11-20T11:48:08.857+0900, Worker 포함 B 목표·Backend→DR 인계. 실험 작업창/승인 및 전체 Writer 검증 필요 |
| [LBT-60](https://samsunglions.atlassian.net/browse/LBT-60?focusedCommentId=11245) | updated 2026-11-19T18:57:22.090+0900, 신규 승인·재점검, INTENT/UNKNOWN 보존, Failback 별도 승인, 기존 Gate 유지 |
| [D-24](https://samsunglions.atlassian.net/wiki/spaces/SL/pages/6815749) | 본문 직접 조회. Provider 실제 UUID·상태로 Reconciliation, 불명확 mutation 자동 반복 금지, 운영자 판단·Failback/재보호 |
| Infra 제어 결정 | `5987cb0` D01–D06/C01–C20. 실제 실행 권한과 내부 후보 계약 분리 |
| GitOps main | `ab830d7c9e046b0621477192aca39c6b269cbf30`, #126 merge. 서울 replicas2/open, 오사카 replicas0/closed, mode directory mount 및 auto-sync/PreSync migration 경로를 소스에서 확인. 실제 Pod 상태 증거로 사용하지 않음 |
| owning Git 계약 | OpenAPI 0.1.7, Runtime 0.1.12, migration1–13, Backend 책임/의존 방향. Connector typed dispatch/query consumer 적용. Terminal/Live wire format·payload는 변경하지 않음 |

## 요구사항 → 구현 → 실행 검증 → 공백

라인은 현재 후보 기준이다. 표 전 행의 실제 DB·AWS·Pod 검증은 미실행이다.

| 요구사항 | 구현·consumer | 실행한 자동 테스트·결과 | 실제 검증·잔여 공백 |
| --- | --- | --- | --- |
| PR194 접수 차단을 중복하지 않고 권한/replay·503 계약 보존 | main `labexecution/{service,mutation}.go`, `httpapi/labexecution.go`; 추가 `app/app.go`, OpenAPI | HTTP/Service admission, maintenance, mutation 회귀; 권한·replay200·503 및 새 등록0. 관련 9pkg race1398 PASS | HTTP→실 DB는 compile-only. 유지보수 파일은 global Fencing이 아님 |
| 하나의 process gate와 durable WorkerControl을 실제 조립 | `app/control_stack.go:117`, `app/app.go`; `app/{admission,worker_control}_test.go` | retired env 거절, 잘못된 opt-in/빈 binding 거절, api-only RECONCILE_ONLY에 persistent 필수 | mode mount·전체 Pod coverage·기존 image는 미검증 |
| 새 claim/prepare/dispatch/requeue의 exact scope/revision/epoch 검증 | `workercontrol/{control,durable,settings}.go`, `provision/executor.go`, `connector/router.go` | pause·stale binding·결과 불명·응답 유실·late ACK·서로 다른 Control/Executor/Router의 동시 실행 차단 | 공유 Fake/scripted SQL. 실제 PostgreSQL locks/MVCC·다중 Pod 네트워크는 미검증 |
| D-24 query-only 정산과 mutation 차단을 구분 | `provision/reconciliation.go`, `maintenance/gate.go:27,38`, Router typed permission | persistent RECONCILE_ONLY만 query→결과 기록 관측; mutation queue0, 오래된 query0, missing observer/local backend 거절 | query 결과 기록도 DB Writer. 실제 Connector/Provider 정산 및 Single Writer 미검증 |
| 영속 상태·작업 ticket의 수명·재시작 보존 | `postgres/worker_control.go:96`, migration12, fake/store | create-only paused state, 정확 ticket 종료·다른 boot/epoch 거절, pause와 enter 경쟁·UNKNOWN 보존 | integration-tag fixture는 compile-only. DDL 적용·실 DB 장애 내구성 미검증 |
| 새 승인·새 재점검·별도 Failback 승인, 미완료 INTENT 자동 재개 금지 | `workercontrol/resume.go:72,75,138`, `postgres/worker_control_resume.go:73,133`, migration13 | 만료·누락·재사용·hash충돌·대상 불일치·미완료 ticket 거절, Begin 별도 commit, Apply state+APPLIED 원자 commit의 scripted SQL, 재시작 INTENT HOLD | 실제 Authorizer/Rechecker adapter 미구현. 권한/Writer proof 출처와 DB transaction 실측 필요 |
| 재개 직전 exact closed·local drain + 외부 proof 모두 필요 | `workercontrol/maintenance_resume.go:16,52,59`, `provision/maintenance_resume_test.go` | local open/invalid/missing·http/worker/recon active 거절, Begin/Apply 재검사, ACTIVE만으로 closed file claim0, 외부 open 후 옛 Worker0 | file 관측과 DB commit은 원자적이지 않음. 외부 변경 통제·global proof 필수 |
| 계약·migration 소비자 정합성 | Runtime/OpenAPI/schema8/migration1–13 및 migration_test | OpenAPI validate, 저장소 Contracts CI의 JSON/ref/Runtime/migration 프로그램 직접 실행 PASS, main 문장/config key 보존 assertion PASS, gofmt32 PASS | 운영 적용 전 migration 호환성·schema readiness 및 배포 승인 필요 |
| DR 판정 보존 | Runtime/운영 가이드 및 내부 승인 결과 구분 | 실행기를 수정하지 않음. 이 후보에 FAIL/NV를 PASS로 바꾸거나 HOLD를 해제하는 운영 경로 없음 | ReplicaLag FAIL, 필수4 NOT_VERIFIED, HOLD, recovery_authorized=false, cloud_verified=false 유지 |

각 변경 파일의 목적·경계는 `validation.json.source_inventory`에 포함했다. 의도적으로 제외한 변경 파일은 없다. DB/WSS 전체 E2E·실 Provider·배포 적용은 범위 밖이며 PASS로 표시하지 않았다.

## 수정 전 findings와 재확인

이전 구현 중 발견·수정한 문제와 당시 보고 근거는 과거 `2026-11-20-maintenance-integration/audit.md`의 Findings 표를 보존했다. 최신 main에는 fixture 수정도 이미 포함되어 있다. 이번 main 재검토의 신규 finding은 아래 두 문서 오류이며 수정 전에 사용자 commentary로 먼저 보고했다.

| ID·심각도 | 발견 위치·재현/영향·근거 | 수정·검증 |
| --- | --- | --- |
| I-REFRESH-DOC-01 / P3 | `docs/backend/dr-worker-control.md:50` 원격 push/PR도 금지한다는 과거 문구가 이번 사용자 명시 승인과 충돌. 코드 분석/문서 직접 대조 | 작업 브랜치 push·Draft만 허용, merge/배포 금지로 정정. 문서 재독·독립 reviewer 확인; 행동 변경 없음 |
| I-REFRESH-DOC-02 / P3 | `contracts/http/openapi.yaml:711` Cleanup 설명에 Reset 전용 422 역할 검사가 복사됨. Cleanup에 없는 응답 기대를 만들 수 있음 | Cleanup에서 Reset 절만 제거, Reset endpoint 원문 보존. OpenAPI 및 저장소 Contracts CI 프로그램 최종 재실행 PASS; 새 행동 테스트 불필요 |

독립 reviewer `/root/review_worker_resume`는 최신 base 전체 diff·snapshot·SQL/승인/조립·최신 Jira·배포 workflow를 별도로 검토했다. 같은 9pkg race와 두 integration-tag package compile-only를 독립 실행해 PASS했다. 검토 범위에서 신규 confirmed 기능 결함은 찾지 못했고 위 P3 두 건을 수정했다. 이는 실 DB·실제 전체 Writer 검증을 대신하지 않는다.

## 실패·보안·수명주기

- **입력/우회:** 공개 HTTP 제어 endpoint를 추가하지 않았다. 사용자 header/body로 제어 상태를 열 수 없다. retired admission env가 남으면 startup을 거절한다. PR194의 파일 미설정 호환 open은 그대로이므로 배포 시 명시 설정과 mount coverage가 필요하다.
- **승인/동시성:** exact target/scope/revision/epoch·Pod/boot·요청 hash·승인/점검 유효시각을 검사한다. local 상태를 global proof로 확대하지 않는다. INTENT와 APPLIED를 분리하고 미완료 INTENT는 자동 삭제/재적용하지 않는다. Fake mutex/scripted SQL은 실 DB 직렬화 증명이 아니다.
- **Drain/중단:** maintenance close는 admitted Worker 전체 실행의 drain이다. 이미 수락한 작업의 send까지 허용할 수 있다. 즉시 mutation 차단은 authoritative pause와 미완료 ticket·UNKNOWN 정산이 별도 필요하다. metric0은 전체 Writer0을 뜻하지 않는다.
- **재시작/식별자:** CreatePaused는 기존 상태를 덮어쓰지 않는다. 다른 boot/epoch ticket을 종료하거나 old binding을 자동 활성화하지 않는다. migration12/13은 기존 Provider 자원을 생성/삭제하지 않는다.
- **부분 성공/외부 정합성:** 결과 불명 시 mutation 자동 반복 대신 HOLD·Reconciliation 계약을 따른다. query-only도 결과 저장과 Writer 권한을 필요로 한다. 지역별 비동기 DB fork의 유실 intent·전체 Writer 차단은 이 제어 label/epoch로 보장하지 못한다.
- **계약/배포:** 기본 비활성이어도 새 migration이 schema readiness에 포함된다. main merge는 현재 workflow상 Seoul GitOps 이미지 및 PreSync migration으로 이어질 수 있다. merge·deploy·migration 적용은 이번에 하지 않는다.
- **리소스/민감정보:** 실제 계정 credential·DSN·Webhook·Provider raw payload를 새 소스/감사 자료에 넣지 않았다. 로컬 파일·Fake·temp fixture·web dist만 생성했다. Terminal/Live payload 저장 경로는 변경하지 않았다. 기존 installer의 Windows temp cleanup EPERM는 아래 잔여 실패에 공개한다.
- **파일/DB 경계:** exact closed 관측과 DB commit 사이의 경쟁은 외부 변경 통제 및 trusted 재점검이 필요하다. 새 승인 적용만으로 mode 파일을 쓰거나 Pod를 재시작하지 않는다. 실제 GitOps binding 전달·모드 개방·Failback Fencing adapter는 남아 있다.

## 실제 실행 결과와 재현

도구: Windows `LAPTOP-5ICSOOU4`, Go1.27.1, Node24.22.3, Python3.13.14. 아래는 최신 main 위 후보 실행 결과이며 원본 명령·종료 코드·로그 SHA는 `validation.json`에 고정했다.

| 검증 | 결과·정확한 범위 |
| --- | --- |
| 관련 9pkg `go test -race ... -count=1 -timeout=120s` | **1398 test/subtest PASS, 실패/skip0**. 독립 reviewer도 재실행 PASS |
| `go vet ./...`, `go build ./...` | PASS |
| `go test -tags integration ./internal/postgres ./internal/server/httpapi -run '^$' -count=1` | compile-only PASS. 실제 DB 테스트0·migration 적용0 |
| 전체 `go test -json ./... -count=1 -timeout=90s` | **exit1**. 3941 PASS·39 FAIL 이벤트(부모 포함)·15 SKIP·1 started test timeout 미종결. 실패 package는 변경하지 않은 `internal/connector/installer`, `internal/connector/provider/openstack`; POSIX fixture/Windows 제한. 이전 후보 두 실행과 실패 test set 동일. pristine main 전체를 별도 실행한 결과는 아님 |
| Web typecheck/lint/build | PASS |
| Web tests | **exit1**, 492/493 PASS, 1 FAIL, skip0. 변경하지 않은 `web/scripts/validate-installer-build-env.test.mjs:90` temp rmSync EPERM. 전체 green으로 취급하지 않음 |
| Contracts/fmt | OpenAPI·schema8/local refs·Runtime·migration metadata 및 gofmt32 PASS. 마지막 P3 문구 수정 후 OpenAPI/CI 프로그램 다시 PASS |

전체 Go skip 이름·실패 test·unfinished test는 JSON에 전부 기재했다. 실제 PostgreSQL/HTTP integration, 공유 DB, Kubernetes/ConfigMap 전파, AWS, Slack, Provider, 전체 Browser E2E는 실행하지 않았다.

재현은 저장소 `make setup`, `make test` 또는 개별 go/web target을 사용한다. 관련 race 명령의 package는 `./internal/postgres ./internal/server/workercontrol ./internal/server/maintenance ./internal/server/provision ./internal/server/connector ./internal/server/app ./internal/server/operation ./internal/server/labexecution ./internal/server/httpapi`다. Contracts는 `.github/workflows/contracts.yml`의 고정 validator 버전/프로그램을 따른다. ignored 로컬 runner는 필수 runtime 의존성이 아니다. 실 DB 검증은 승인된 폐기 DB에서만 저장소 integration 준비 절차에 따라 별도 수행한다.

## 원격 게시·서울 영향 확인

- `.github/workflows/delivery-backend.yml` 및 web/connector/contracts/go workflow, Dockerfile·Makefile·go.mod/go.sum은 변경하지 않았다.
- 현재 repo workflow에서 일반 작업 브랜치 push는 production delivery 조건에 들지 않는다. PR build는 image push=false, PR AWS 인증/ECR 제외, GitOps 갱신은 push+refs/heads/main 조건이다. PR CI의 임시 PostgreSQL/Valkey는 서울 공유 DB와 별개다.
- GitOps main auto-sync·PreSync migration 경로도 읽었으므로 main merge가 서울에 영향이 없다고 주장하지 않는다. 이번은 태그/main push·merge·릴리즈·배포·공유 DB 적용 없이 **동일 작업 브랜치 push + 별도 Draft PR**까지만 사용자 승인 범위다.
- 검사 범위는 저장소에 정의된 배포 경로다. 저장소 밖 임의 webhook의 부재를 실환경 증명한 것은 아니다.

## 최종 판정

**Audit 판정: 보완 필요.** 전체 기본 검증이 green이 아니며 실제 DB·실 승인/재점검 adapter·운영 배포/전체 Writer 검증이 남아 있어 merge/운영 적용 가능 상태로 단정하지 않는다. 현재 검토 범위에서 신규 confirmed 기능 차단 결함은 발견하지 못했다.

사용자는 이 제한을 기록한 작업 브랜치 push와 별도 Draft 검토 게시를 명시적으로 요청했다. 따라서 본문에 실패·미실행·배포 경계를 공개한 **Draft review-only**로 게시하고 merge하지 않는다. 감사 자체를 게시 승인으로 해석하지 않는다.

기존 ReplicaLag 정책 FAIL, REQUIRED_SETTINGS/RUNTIME_PROVENANCE/SERVICE_FUNCTIONS/SINGLE_WRITER NOT_VERIFIED, execution_gate=HOLD, recovery_authorized=false, cloud_verified=false는 유지한다.
