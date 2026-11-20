# LBT-60 · 신규 작업 접수 차단 로컬 구현 Audit

2026-11-19 KST · PRE_MERGE_AUDIT.md에 따른 구현 범위 검토 · 원격 게시/배포 없음

## 판정

**요청한 프로세스 시작 정책의 로컬 구현·단위 검증 범위는 충족했다.**
실제 접수 차단 배포, 전역 barrier, Worker 제어, Single Writer 및 DR 복구 성공은 포함하지 않는다.
저장소 전체 검사에는 아래 별도 실패가 있으므로 전체 CI PASS나 PR/merge 준비 완료로 판정하지 않는다.
원격 push/PR 생성/merge/태그/실환경 호출은 수행하지 않았다.

- 저장소: `ktcloud4-SL/labbit-app`.
- 브랜치: `codex/feat/LBT-60-dr-admission-block-20261119`.
- base 및 시험 시 HEAD: `2bccdbb9264749251f65e710d9acd2ebf3bc7d8c`.
- 시험 소스: 위 base + `validation.json.source_inventory`의 12개 변경 파일(미커밋 working tree).
- 종료 직전 main readback도 동일 SHA. 변경 소스별 raw/LF SHA256·크기와 원본 로그 SHA를 validation.json에 기록했다.
- audit.md/validation.json은 검토 후 생성한 metadata이며 자기참조 inventory에서 제외했다. 실제 전체 diff 검토에는 포함한다.
- ignored `.local/`에는 원본 실행 로그, 설치한 검증 도구, LF 포맷 검사 사본이 있다. node_modules/dist는 일반 로컬 설치·빌드 산출물이다.
  제품 실행이 이 로컬 도구나 생성 파일에 의존하지 않는다. 비밀값·운영 접속 설정을 만들거나 읽지 않았다.

## 최신 SSOT와 범위

| 원본 | 직접 확인한 버전/판단 |
| --- | --- |
| 사용자 현재 지시 | 별도 브랜치 신규 접수 차단·단위 테스트, 서울 서비스 미변경, push 보류 |
| 사용자 개인 지침 | 작업 폴더 PRE_MERGE_AUDIT.md 전체 및 AGENTS 지침을 읽음 |
| App 지침 | base의 AGENTS.md, CONTRIBUTING.md, TESTING.md, docs/backend/README.md. 영향 경로의 별도 하위 AGENTS 없음 |
| LBT-59 | [#11242](https://samsunglions.atlassian.net/browse/LBT-59?focusedCommentId=11242) B안 개발 방향, 실제 DR GO와 구분 |
| LBT-60 | 2026-11-19 14:22:52+09:00 조회 갱신, [#11245](https://samsunglions.atlassian.net/browse/LBT-60?focusedCommentId=11245) PR57 방향·계약 문서 QA; 실제 제어/전역 barrier 별도 |
| 확정 방향 | [Infra PR57 5987cb0 문서](https://github.com/ktcloud4-SL/labbit-infra/blob/5987cb061092d406ba8effaf3259d56e260246b1/runbooks/operations/aws-dr-backend-controls-proposal.md) D01 적용. D02–D06의 Worker/동적 제어/실환경 연계 전체를 구현했다고 주장하지 않음 |
| Confluence | [63275012](https://samsunglions.atlassian.net/wiki/spaces/SL/pages/63275012) 직접 조회. 과거 Stage2 상태가 남은 스냅샷은 최신 Jira #11241/#11243/#11245가 우선. 4NV/HOLD 구분은 유지 |
| HTTP/Runtime | 이번 diff에서 owning contracts/http/openapi.yaml 및 runtime/contract.yaml 함께 수정. HTTP 0.1.7, 503 code와 판정 순서, 시작 설정/한계 명시 |
| DB | db/migrations/000004_execution_operation_resource.sql의 durable Operation/Item·멱등 키·RECONCILING 확인. schema/transaction/locking 변경 없음 |
| Connector/Realtime | contracts/connector/README.md의 async/reconcile 경계와 runtime의 Worker Router 책임 확인. Control/Reconcile/Data WSS를 호출·수정하지 않음. Realtime full lifecycle은 미실행 |
| Consumer | web/src/shared/api/httpClient.ts와 labbitApi.ts의 non-2xx→HttpError/POST 동작 확인. 새 503을 성공이나 자동 재등록으로 전환하지 않음. Web 소스 변경 없음 |

## 전체 변경 목록과 목적

| 파일 | 목적/영향 |
| --- | --- |
| internal/server/labexecution/admission.go | typed immutable mode, 안전한 parser/validation/constructor, 내부 신규 등록 guard |
| internal/server/labexecution/service.go | Service에 mode 보관, Provision committed replay 뒤/ID·transaction 앞 guard |
| internal/server/labexecution/mutation.go | Reset/Cleanup의 같은 경계에 guard |
| internal/server/httpapi/labexecution.go | typed 차단 오류를 고정 503 Problem Details로 변환 |
| internal/server/app/app.go | api role 설정 읽기, Run의 초기 validation, production stack 전달 |
| internal/server/app/control_stack.go | 명시적 mode로 production Service 조립 |
| internal/server/labexecution/admission_test.go | 실제 service 진입점에 Fake Store, 무등록/멱등성/권한/오류/병렬/재구성/우선순위 |
| internal/server/httpapi/admission_test.go | 세 HTTP API의 503/인증/임의 header 비우회/민감정보 비노출 |
| internal/server/app/admission_test.go | role별 parser, 잘못된 mode는 실행 초기 거부 |
| contracts/http/openapi.yaml | 새 Problem code와 기존 검사/replay 우선순위의 owning HTTP 계약 |
| runtime/contract.yaml | optional startup mode 및 한계의 owning Runtime 계약 |
| docs/backend/dr-mutation-admission.md | 결정 근거·연결·적용 전 한계·배포 trigger·재현 방법 |
| 이 audit.md 및 validation.json | 추적성/실행 결과/잔여 위험/독립 검토와 source hash |

Dockerfile, compose, GitHub workflow, GitOps, migration, Worker/Connector, dependency manifests/lock은 변경하지 않았다.

## 요구사항 → 구현 → 자동 검증 → 실제 증거

| 요구사항/Decision | 구현 | 자동 검증/관찰 | 실환경/공백 |
| --- | --- | --- | --- |
| D01 세 신규 mutation 차단 | Service.admitNew, 세 service 진입점 | TestBlockedAdmissionCreatesNoRowsAndDoesNotStartTransaction, 3종 반복; ID/transaction 진입 없음·전체 Fake state 동일 | 실제 DB 미연결. 프로세스별 시작 정책에 한정 |
| HTTP 외 내부 우회 차단 | HTTP 아닌 실제 Service 메서드의 guard | 위 직접 service 호출 + TestBlockedAdmissionConcurrentDirectCallsNeverRegister(3종 각48호출) | 전 Pod 전역 동시 전환·다른 DB writer 미검증 |
| 기존 작업 보존/재시작 | committed replay 뒤 guard | TestBlockedAdmissionReplaysExistingUnknownWorkWithoutResubmission; Service 재구성 뒤 RECONCILING 기존 ID/상태 동일 | 실제 재시작·복제 유실 작업 복원 미검증 |
| 인증/멱등성 | 기존 Class/Org Membership·hash/replay 그대로 | TestBlockedAdmissionPreservesConflictAndAuthorization: 학생403, key400, 동일 key 다른 의미409, 권한 회수403 | LabSpec 등 transaction 내 semantic 검사는 아래503보다 후순위 |
| 조회 실패 보존 | replay 오류를 기존 경로로 반환 | TestBlockedAdmissionDoesNotMaskReplayLookupFailure, 3종·state 동일 | 실 DB 장애 주입 없음 |
| 계약 판정 우선순위 | OpenAPI/runtime/doc 명시 | TestBlockedAdmissionPrecedesNewRegistrationSemanticChecks: OPEN의403/404/422/409와 BLOCK_NEW503 비교, state 동일 | 검증을 우회해 mutation을 허용하는 것이 아님 |
| 조회 유지 | Execution/Instance 경로 미수정 | TestBlockedAdmissionLeavesExecutionAndInstanceReadsAvailable | live API 조회 미실행 |
| 명시 설정/잘못된 값 | ParseAdmissionMode, Validate, LoadConfig→Run→stack→Service | TestAdmissionModeParsingAndConstruction, TestLoadConfigMutationAdmissionMode, TestInvalidAdmissionModeFailsBeforeRuntimeInitialization | 기본 OPEN은 기존 호환성. 미설정 자체가 DR 대기 차단은 아님 |
| 고정 HTTP 응답 | labExecutionError | TestMutationAdmissionBlockedHTTPContract, 세 API; 503/code, Location·Retry-After 없음, 401 선행, raw cause 비노출 | 실제 Ingress/Browser E2E 미실행 |
| 기존 동작/의존 방향 | 기본 NewService 및 Worker/Provider 경계 유지 | 기존3package 전체 + -race, boundary test, go vet/build | 실제 PostgreSQL integration을 대체하지 않음 |
| D02–D06/실 DR Gate | 이번 구현 범위 밖 | 코드·문서에서 의미 구분 | Worker, 동적 barrier, 승인/epoch/저널, GitOps, 실 DR 모두 별도. 기존 FAIL/4NV/HOLD 유지 |

## Findings와 수정 이력

| ID/심각도 | 수정 전 위치·재현·영향 | 처리/회귀 |
| --- | --- | --- |
| REV-ADM-01 / P2 | 구현 중 OpenAPI의 일반 '권한/형식 확인 뒤503'와 기존 LabSpec owner403·Reset instructor422·finished409가 모호함. 유효한 INSTRUCTOR가 BLOCK_NEW에서 타owner spec/강사 instance를 요청하면 실제503이 우선 | 수정 전에 사용자에게 보고. Class/Org 권한·형식·replay 후503, transaction 내 semantic 검사보다 우선이라고 owning 계약 정합화. OPEN/BLOCK_NEW 비교4분기 -race 통과. 독립 reviewer 해결 확인 |
| TEST-FIXTURE-01 / P3(검증 helper) | 첫 신규 test의 state snapshot에 map[[2]uuid.UUID]를 JSON marshal하여 검사 실행 자체 실패 | membership을 정렬한 slice로 표현. 제품 guard 수정 아님. 관련 unit/race 재실행 통과 |
| REPO-BASELINE / 별도 검증 한계 | 전체 Go 검사에서 이번 diff 밖 OpenStack CIDR/Installer 실패, CIDR 동시성 시험 장시간 정지 | 수정 없는 같은 base에서 대표3개 실패 및 동시성 timeout 재현. 원인 전체를 단정하거나 unrelated 파일 수정하지 않음. 전체 PASS로 취급하지 않음 |

미구현이던 baseline 접수 기능은 신규 요구 범위로 기록하며, 실운영 장애나 보안 사고로 소급 분류하지 않는다.

## 실패·보안·수명주기 검토

- **입력/권한:** 기존 초기 형식·opaque ID·Class/Org 권한 검사를 유지. 권한 회수 후 기존 key도 반환하지 않음.
  차단은 transaction 안의 상세 semantic 검사보다 먼저이며 owning HTTP 계약에 명시. HTTP status는 Application에 넣지 않음.
- **실패/UNKNOWN:** 기존 RECONCILING Operation은 그대로 읽고 새 Operation/Item/Provider 명령을 생성하지 않음.
  replay 조회 오류는 차단 성공·작업 부재·재실행 근거로 바꾸지 않음. 503에는 mutation 자동 재시도 지시 없음.
- **동시성:** mode는 unexported Service 필드로 생성 후 고정, setter 없음. 48 동시 신규 호출은 차단되어 등록 없음.
  실행 중 OPEN↔BLOCK_NEW 전환은 제공하지 않으므로 barrier/epoch 전파 race 해결을 주장하지 않음.
- **재시작:** 동일 BLOCK_NEW 입력으로 Service 재구성해도 기존 durable replay 사용. 입력을 잃으면 기본 OPEN이므로,
  실제 DR에는 모든 Pod/새 Pod에 동일 정책 유지와 관측을 확인할 플랫폼 제어가 필요.
- **리소스/소유권:** transaction 이전 차단, 기존 state/Item/instance/generation을 취소·삭제·완료 처리하지 않음.
  이미 수락한 Provider 작업은 진행될 수 있음. queue drain/Worker lease 제어/정산을 구현한 것이 아님.
- **보안:** 요청 header/body에 OPEN setter나 승인 우회 경로 없음. 잘못된 설정 문자열·wrapped 내부 cause를 외부에 복사하지 않음.
  실제 Credential/DSN/SSH/Token/Webhook을 수집·사용·기록하지 않음. 테스트 DSN은 localhost dummy이며 연결하지 않음.
- **전체 writer 경계:** Auth/Terminal/Live/File/Preview/migration/Job 및 Worker의 쓰기에는 영향 없음. 이 세 신규 접수 차단을 Single Writer 증명으로 쓰면 안 됨.

## 실행 결과와 환경 한계

Windows/amd64, Go1.27.1, Node24.22.3, Python3.13.14. API 서버나 DB/Provider를 기동하지 않았다.
테스트의 httptest/네트워크 fixture는 loopback·Fake일 뿐 서울 또는 AWS endpoint를 사용하지 않는다.

| 실행 | 실제 결과 |
| --- | --- |
| 관련3package go test -count=1 | semantic 회귀 추가 전729 test/subtest event PASS,3package PASS |
| 관련3package go test -race -count=1 | 최종734 test/subtest event(상위181개) PASS,3package PASS, skip/race 경고 없음 |
| go vet ./... / go build ./... | 모두 exit0 |
| gofmt | Windows CRLF 원본 검사는 기존 파일도 나열. 폐기 가능한 LF 사본에서 전체598 Go 파일 PASS, 원본 일괄 수정 없음 |
| Contract validator | OpenAPI, Runtime, JSON schema8개/로컬ref, migration 번호 유일성 PASS. CI와 같은 고정 validator 버전 사용 |
| Web 기본 검사 | typecheck/lint/build PASS; 기존 bundle-size 권고만 존재 |
| Web test 전체 | 최초492/493 PASS, installer-build-env의 임시 디렉터리 정리 Windows EPERM1건. 해당 unchanged 파일30개만 재실행해30/30 PASS. 최초 전체 실행을 PASS로 덮어쓰지 않음 |
| go test -json ./... | 실패. 지나치게 넓은 PASS 주장을 하지 않음. 상세 pass/fail/skip 목록은 validation.json. OpenStack/Installer에 복수 실패 및 Windows CIDR 시험 정지 |
| pristine base 대조 | representative late-ACTIVE·CIDR cleanup·Installer3개 동일 실패. CIDR 동시성 시험은 -timeout=15s에서 동일 정지 재현 |
| 정지 시험 종료 | 원래 전체 검사의 go PID와 고유 실행 shell, 자식 openstack.test.exe PID/부모 관계를 검증한 후 해당 로컬 시험 자식만 종료. 일반 서비스/다른 작업 프로세스 종료 없음. 전체 결과는 FAIL 유지 |
| PostgreSQL/Linux/실환경 | PostgreSQL integration tag, Linux CI, AWS/Argo/DB/Connector/Slack/실 Browser/DR 미실행. OS·명시 live integration·Valkey 준비 조건의15개 skip은 validation.json에 사유 보존 |

초기 실패 기록은 위 Findings 및 validation.json.initial_failed_attempts에 남겼다. 기존 validator cache는 Python3.12 바이너리여서
내장 Python3.13에서 로드되지 않았고 `.local/contract-validators`에 맞는 버전을 별도 설치해 최종 검증했다.
도구 설치를 제품 dependency/go.mod/web lock 변경으로 반영하지 않았다.

원본 로그는 validation.json에 기록한 `.local/admission-checks/` 경로에 보존했다. 요약만으로 전체 테스트 PASS를 주장하지 않는다.

## 독립 두 번째 검토

`/root/review_admission_local`이 구현과 분리해 base/전체 tracked+untracked diff12개와 owning SSOT 및 HTTP consumer를 검토했다.
직접 Admission-filtered3package -race와 새 semantic priority 회귀를 실행했다.
REV-ADM-01을 보고했고 수정 후 재검토에서 해결 확인. 추가 확정 P0/P1/P2 결함은 발견하지 못했다.
검토는 startup policy 오프라인 범위에 한정하며 global barrier/Worker/fencing/PostgreSQL 실효·배포 환경은 미검증이다.
본 audit 및 JSON도 별도 final metadata 검사를 거쳐 source inventory·테스트 집계·안전 경계 정합성을 확인한다.

## 재현

같은 working source(또는 향후 승인된 로컬 commit)를 준비하고 Go1.27을 사용한다.

```text
go test -race ./internal/server/labexecution ./internal/server/httpapi ./internal/server/app -count=1
go vet ./...
go build ./...
```

Node24에서 `web/`의 `npm ci`, `npm run typecheck`, `npm run lint`, `npm run test`, `npm run build`로 소비자 기본 검사를 재현한다.
계약 검사는 `.github/workflows/contracts.yml`의 고정 PyYAML6.0.3/openapi-spec-validator0.9.0/jsonschema4.26.0 및 동일 schema/ref 규칙을 쓴다.
실 DB integration/실환경 설정을 지정하지 않는다. OS 특이 실패/skip과 최초 실패를 제거하여 전체 PASS로 바꾸지 않는다.

## 배포·잔여 업무

현재 workflow 정적 확인상 main push는 이미지/GitOps 태그를 갱신하고 PR 빌드는 이미지 push=false다. 저장소 밖 webhook 전체를
검증한 것은 아니다. 사용자 요청에 따라 이번 결과는 로컬 브랜치에만 유지하고 push/PR/merge를 하지 않는다.

후속 구현: 모든 instance의 영속 제어/revision/epoch·실행 중 차단 barrier, Worker claim/dispatch pause 및 정산,
운영자 인증·승인/UNKNOWN journal/실측 readback, GitOps 대기·활성 프로필, 실제 Fencing·Single Writer와 DR 통합.
이번 판정은 위 잔여 작업이나 기존 ReplicaLag FAIL/4개 NOT_VERIFIED/HOLD를 해소하지 않는다.
