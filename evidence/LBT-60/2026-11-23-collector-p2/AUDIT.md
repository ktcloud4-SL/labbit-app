# PR196 P2: DB 미정산 관측의 독립 FAIL 보존

2026-11-23 / LBT-60 #11266 / 로컬 수정·미커밋·미푸시. 이전 Infra/GitOps Stage3 준비는 사용자 요청으로
중단했고 이 수정과 혼합하지 않았다. 실제 AWS·서울·공유 DB·Provider·Slack 작업은 하지 않았다.

## 범위·기준

- 저장소 labbit-app, 브랜치 `codex/feat/LBT-60-stage3-live-prep-20261123`.
- 전체 PR base/main `753eecbff737409a4c9cc8c542f3c6baa6acf0da`, 로컬/원격 PR HEAD `f5413c91282888115ad2b497d5c2183ab58f2e70`.
- 원격 PR196은 Draft/open/미병합임을 GitHub read-only로 확인했다. 새 원격 HEAD/CI는 만들지 않았다.
- `PRE_MERGE_AUDIT.md`, AGENTS/CONTRIBUTING/TESTING, Backend 책임 경계, Runtime0.1.14와
  runtime/dr-control.md, SQLWorkReader, Observation/서명/Verifier, ResumeProof의 양쪽 모드를 대조했다.
- 최신 LBT-60 본문·댓글11266, PR 독립 리뷰6015992658을 직접 읽었다. 같은 작업 중 읽은 Confluence
  테스트 전략9666637 v4·DR Gate63275012 v14는 ssot-source.json에 버전을 기록했다. 문서/원격 쓰기는 없다.

이 증분은 **관측 상태 분류 수정**이다. HTTP/Connector/Browser 프로토콜·Runtime 필드·DB migration·SQL·
승인 요구·Mode 전이·네트워크/배포 입력은 변경하지 않는다. 따라서 해당 API/schema를 새로 정의하거나
실제 PostgreSQL/Provider를 조작할 필요가 없다. 원래 전체 PR에 남은 실환경 선행조건은 그대로다.

## 수정 전 finding과 재현

| ID·심각도 | 원래 위치(f5413c9) | 조건·영향 | 수정·회귀 |
| --- | --- | --- | --- |
| P2-COLLECTOR-01 / P2 | internal/drcontrol/collector.go:209–221,254–274 | validWork=true, Operations=1(또는 다른5카운트 양수), safety=nil/만료/서명 오류/다른대상. 원래 WorkInventory PASS/WorkReconciled NV. 외부 ok&&validWork 안에만 양수 FAIL 분기가 있어 확인된 미정산 사실이 NV로 기록됨. Runtime의 확인된 양수=FAIL·독립 FAIL 보존 기준과 불일치 | validWork 내부에서 양수 FAIL을 먼저 설정. 같은 predicate를 기존 외부 Provider 분기에서도 사용. 6counter×6safety 및 unknown/zero/expiry/서명 소비자 회귀 |

사용자에게 파일·라인·원인·수정 방향을 먼저 보고했다. 제품 소스를 바꾸기 전에 새 회귀를 실행해
**24개 부정 subcase + parent FAIL(25 test events)**, 외부 유효/Provider NV 조합12 PASS를 재현했다.
처음 테스트 작성 시 Mode에 문자열을 넣어 compilation이 실패한 기록도 보존했다. fixture를 owning
`wc.Active`로 고친 뒤 제품 baseline에서 실패를 재현한 것이며, 최초 build failure를 제품 결함 재현으로 세지 않는다.

FAIL/NV 모두 해당 누락 safety 조건에서는 ACTIVE를 막고 전체 HOLD를 유지했다. 이 finding은 실제
전체 재개 우회/P1로 확대하지 않는다. RECONCILE_ONLY는 공통5조건/승인 등이 PASS이면 미정산 작업의
통제된 정산을 허용하는 별도 owning semantics이며 이번 수정으로 바꾸지 않았다.

## 전체 diff 및 영향 경계

| 파일 | 변경·검토 |
| --- | --- |
| internal/drcontrol/collector.go | 확인된 DB 미정산을 외부 attestation 이전에 독립 FAIL 처리. PASS는 여전히 validWork+모든카운트0+유효한 Provider PASS 필요 |
| internal/drcontrol/collector_adapters_test.go | 6종×6safety, zero/DB조회 불확실12조건, expiry시 FAIL 보존, SQL 실패에도 외부3종 FAIL 보존 |
| internal/drcontrol/verified_test.go | 다른5조건 PASS인 signed WorkReconciled FAIL이 신규 INTENT/Apply에 도달하지 않는 소비자 부정행 |
| runtime/dr-control.md | 기존 분류 의미를 명시. Runtime0.1.14 YAML/schema는 불변 |
| 이 Evidence 디렉터리 | 원본 gzip 로그·source/artifact SHA·초기 실패·SSOT·독립 검토·재현 도구 |

full base→HEAD+local/staged/untracked inventory는 source-manifest.json이다. 미커밋 실행 소스는 raw와
LF-canonical SHA로 식별한다. 기존 전체 PR344파일은 앞선 publication/linkage 감사의 **불변 소스**에
한해 검토를 재사용했다. 이번에는 새 diff 전체와 producer/consumer를 직접 재검토했다. 수십만 줄의
과거 Evidence를 새 live 결과로 재인수하거나 모든 과거 로그를 처음부터 다시 감사했다고 주장하지 않는다.
ignored .local은 이번 원본 로그·로컬 빌드 산출물이다. 운영 Policy·실제 Secret·DSN을 읽지 않았다.

## 요구사항 추적

| 요구사항·원문 | 구현·consumer | 자동 검증 및 실행 근거 | 실환경·공백 |
| --- | --- | --- | --- |
| LBT-60 #11266/PR6015992658: 유효 DB 양수는 외부 safety 없어도 FAIL | collector.go validWork/dbUnreconciled | TestCollectorKnownDBBacklogDoesNotRequireExternalSafetyToFail: Operation/Item/boundary/Resume INTENT/Restriction INTENT/Provider 미해결×6safety, baseline24실패→수정후 PASS | Fake WorkReader/PodReader와 합성 signed 원본. 실제 RDS 조회 아님 |
| Runtime/dr-control: zero alone≠완료, 불확실한 조회=NV | 기존 DB/user/Writer/scope/revision/epoch/PAUSE/time/음수 검증 유지 | TestCollectorUnverifiedDBCannotEstablishReconciledFailOrPass: 12조건 | 실제 SQL 구현·schema는 불변. 이번 PG integration 미실행 |
| 독립 FAIL은 다른 근거 누락/만료로 낮추지 않음 | 외부 signed FAIL 및 Collect 종료 TTL 처리 | TestCollectorKnownDBBacklogFailSurvivesCollectionExpiryAndActiveVerifier, 기존 external FAIL+SQLerror 강화 | Fake 시간·서명/Reader. 실제 시계·SDK 장애 주입 아님 |
| Runtime approved_resume: ACTIVE는 WorkReconciled PASS 필수 | Verifier.RecheckResume→ResumeProof.Validate | TestUnverifiedProofCannotCreateIntent/WorkReconciled_FAIL: 다른5조건 PASS, 올바른 서명 경로, begin/apply0 | 운영 승인 시스템·키 설치·다중 Pod 실행은 미검증 |
| Runtime의 RECONCILE_ONLY 별도 조건·기본 MAINTENANCE 유지 | owning proof/maintenance/Worker 코드는 불변 | 영향4package race/기존 Mode 회귀와 전체 Go | 실제 Provider exact-ID 정산은 별도 |
| 기존 FAIL/4NV/전체 HOLD 유지 | CollectionReport·CLI 소비자 불변 | 새 matrix에서 execution_gate HOLD, recovery_authorized/cloud_verified false. 영향 CLI race | 실제 DR GO/복구 인수 없음 |

## 실패·보안·수명주기 검토

- 입력 경계: 유효한 전체 DB snapshot에 한해 양수를 신뢰한다. 잘못된 DB/user/Writer/대상/세대/시각,
  음수 카운트나 query error는 양수가 섞여도 새 FAIL/PASS를 만들지 않는다. valid zero도 외부 근거 없으면 NV다.
- 외부 근거: 없음/만료/잘못된 서명/다른 DB 원본이 확인된 DB FAIL을 지우지 않는다. 동시에 SQL 조회가
  실패해도 유효한 외부 SingleWriter/Fencing/Provider FAIL은 보존한다. 불확실한 safety를 PASS로 올리지 않는다.
- 시간/상태: collector 종료 TTL이 지나면 PASS를 NV로 낮추지만 이미 확인된 FAIL은 남는다. 역사적 FAIL은
  현재 work completion이나 재개 허가가 아니다. 새로운 현재 관측·승인은 기존 경로에서 요구한다.
- 권한: 서명·목적·TTL·대상·고정 VersionId·키/Policy·신원 검증은 바꾸지 않았다. signed 관측 FAIL은
  ACTIVE INTENT를 생성하지 못한다. RECONCILE_ONLY의 공통5조건/승인도 완화하지 않았다.
- 동시성/재시작/소유권: read-only 수집기의 결정 분류만 바뀌므로 새 lock·resource·mutation/retry는 없다.
  원래 Resume/Pause INTENT/UNKNOWN/CAS 경로는 불변이며 영향 race로 회귀했다. actual multi-Pod proof는 별도다.
- 민감정보: 테스트는 합성 계정/대상/키와 Fake Reader만 사용한다. 로그/manifest에 운영 DSN·Secret·Provider
  payload를 새로 수집하지 않았다. 변경 소스/증거의 secret scan 결과는 별도 원본으로 기록한다.
- 리소스 수명주기: 새 cloud/shared DB resource가 없고 cleanup 대상도 없다. Web 빌드는 로컬 ignored dist만 생성했다.

## 실행 결과와 한계

Windows Go1.27.1, 기존 lockfile 설치 Web 의존성과 Node를 사용했다. make가 없어 Makefile의 개별
명령을 동등한 Go/Node 실행 파일로 수행했다. dependency 설치·실제 서비스 시작은 하지 않았다.

| 실제 실행 | 결과 |
| --- | --- |
| 전체 Go `go test -count=1 -json ./...` | 4655 PASS / 88 SKIP / 0 FAIL, 53package PASS/14 no-test package. skip명은 test-summary에 보존 |
| 최종 영향4package race | 282 PASS / 0 SKIP / 0 FAIL. 최종 외부FAIL assertion·서명 소비자 행 포함 |
| collector focused race | 79 PASS / 0 FAIL. 최종보다 앞선 테스트 증분으로 구분 |
| go vet/build ./... | PASS |
| tracked/local Go canonical format | 658파일 PASS; raw CRLF gofmt-l 결과는 초기 로그로 보존, 무관 파일 수정 없음 |
| Web typecheck/lint/build | PASS. build의 기존 chunk-size 경고 보존 |
| Web Vitest --maxWorkers=1 | 493 PASS / 35파일. jsdom canvas 경고는 로그 보존 |
| 독립 reviewer | 직접 4package race281 PASS(consumer1행 추가 전), 마지막 consumer focused19 PASS. 원본·소스 snapshot 분리 |

전체 Go는 마지막 test-only 외부 FAIL assertion과 단독 consumer 행 전 결과다. 제품 collector는 동일하며
그 테스트 보강은 최종 영향4package race에 포함했다. 변경 테스트를 이전 PASS로 덮지 않는다. suite는 중복되므로
합산하지 않는다. 초기 compilation/expected-red baseline/Windows CRLF 출력은 성공 로그와 함께 보존했다.

**이번 증분에서 실행하지 않은 범위:** PostgreSQL integration, Linux runtime, 새 GitHub CI, 실제 AWS/
Kubernetes/Provider/다중 Pod·Single Writer/Fencing, 운영 키·Policy·IAM/RBAC, migration12–15 백업/적용/복귀.
이번 결정 분류의 검증은 Fake로 가능하나 실환경 인수는 이 결과로 대신하지 않는다. 이전 PG/Linux/CI PASS는
이 수정의 새 실측으로 합산하지 않는다.

## 독립 검토와 판정

별도 reviewer `/root/audit_pr196_operator`가 소스 수정 없이 PRE_MERGE 지침·owning Runtime·producer/consumer를
대조하고 직접 race를 실행했다. 이전 넓은 검토에서 양수×invalid safety 조합을 놓친 한계를 보고서에 명시했다.
단독 signed WorkReconciled FAIL 소비자 행 보강 제안을 수용했다. 현재 검토한 증분에서 미해결 P0/P1/P2는 없다.

**로컬 P2 수정·회귀 범위 충족.** 기존 Draft PR에는 아직 반영하지 않았고 commit/push/merge/배포/외부 메시지를
수행하지 않았다. 전체 3단계 완료·실환경 시험 승인/DR Gate 해제는 아니다. 기존 ReplicaLag FAIL·필수4NV·HOLD 유지.

재현은 commands.json 및 assemble.py 참고. 원본 gzip은 UTF-8(-sig)로 읽고 source/artifact manifest를 대조한다.
본 감사의 통과를 push·merge·배포 또는 실제 실행 승인으로 해석하지 않는다.
