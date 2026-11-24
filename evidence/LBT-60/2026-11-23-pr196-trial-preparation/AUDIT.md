# PR196 실환경 준비 delta audit

2026-11-23 · 사용자 PRE_MERGE_AUDIT.md 전체 절차 적용. 이 판정은 실환경 기능 시험 준비 자료에 한정한다.

## 버전·범위

- App PR196 HEAD `be4d0ba5fe5a71f8edfd20b4f4a56112f4e3a8d1`, base/latest main `753eecbff737409a4c9cc8c542f3c6baa6acf0da`.
- branch `codex/feat/LBT-60-stage3-live-prep-20261123`; 로컬 미커밋, stage/push/merge 없음.
- 현재 전체 delta: deployment/integration 문서 보완, 새 trial-preparation 문서, 이 Evidence 디렉터리. 실제 파일 목록·바이트 해시는 `artifact-manifest.json` 참조.
- 이번 직접 검토: 현재 delta 전체와 CLI/collector/DB/migration/Runtime/배포 consumer. PR base→HEAD의 기존 제품 변경 전체를 새로 감사·재실행한 것은 아니다. 기존 be4d0ba P2 감사·Jira11267 scoped QA는 출처를 구분해 참고한다. 따라서 이번 판정은 PR 전체 병합 가능 판정이 아니다.
- CONTRIBUTING/TESTING 및 Runtime0.1.14, HTTP maintenance 계약, 관련 Backend 문서, migration 원문을 확인했다. Connector/Browser wire 계약·제품 코드 변경은 없으며 WSS/Provider E2E는 이번 범위가 아니다.
- `source-manifest.json`은 관련 source와 변경 문서의 raw bytes, `migration-source-manifest.json`은 Git 원본/Windows checkout을 구분한다. `.local` 빌드 EXE는 ignored이며 두 SHA와 재현 명령만 보관한다. 자격증명/kubeconfig 자체는 재배포하지 않는다.

## 최신 SSOT와 추적성

| 요구사항·AC | 원문·버전 | 구현·소비 경계 | 이번 실행·자동 검증 | 실제 Evidence·공백 |
| --- | --- | --- | --- | --- |
| P2 수정 후보 고정, Draft/HOLD 유지 | LBT-60 #11267, PR196 be4d0ba | collector.go:143 Collect, Runtime dr-control | source와 GitHub/Jira 직접 조회; 이전 race282/CI5개는 재실행으로 표시하지 않음 | ssot-reviewed.json; 실제 DR 승인 아님 |
| 실행 비활성 시 외부 접근·키 읽기 금지 | cmd/dr-control/main.go:86, cmd/dr-evidence/main.go:91; Runtime0.1.14 | 두 CLI EnableExecution + preview | 정확 HEAD 두 EXE 빌드; 실제 binary preview/disabled4개; 대상 Go 테스트2개 실행 PASS(하위4개 포함) | preview-results.json, local-checks.json; Fake/로컬 검증이며 실제 서명·IAM·DB 제어 미실행 |
| account/cluster/DB/Pod 식별과 baseline 고정 | LBT-60/205; collector/Policy 계약 | collector_adapters.go, awscli.go:81/147 | STS/EKS/RDS/ECR/Kubernetes metadata 읽기 | readonly-observations.json 항목별 UTC; 서명된 live CollectionPlan/실제 SQL identity는 미확인 |
| 과거 migration11과 최신12–15 구분 | db/migrations, postgres/migrate.go:155 | 파일별 transaction/checksum, schema guard | Git1–11 base 동일;15개 source raw SHA/size 확인 | Job 로그11은 현재 DB SQL 증거 아님. SQL 파일 준비만 함; backup restore/DDL 미실행 |
| 기본 maintenance와 APPROVED_CONTROL 분리 | Runtime dr-control, LBT-205, dr-maintenance-integration.md | closed HTTP vs durable Worker; init/Pause/Resume | 기존 문서 기대 행과 후보 소비 경계 직접 대조 | 실제 multi-Pod claim/drain/dispatch/승인 재개 미실행 |
| 실제 변경 전에 영향·복귀 공유, merge 금지 | 사용자 최신 지시 | dr-pr196-trial-preparation.md P01–P08 | 설정 적용·image 게시·Secret/RBAC·DB 변경·동기화 없음 | 변경 묶음과 미확정 필드 명시, live_test_ready=false |
| Fencing/SingleWriter/정산 없는 ACTIVE 금지 | LBT-59/60, Runtime dr-control | signed SafetyAttestation + ACTIVE/RECONCILE_ONLY 소비 | 코드 경계/기존 P2 QA 대조 | 실제 Provider·Fencing·SingleWriter·4개 Gate 미검증, 기존 FAIL/HOLD 유지 |

Jira LBT-60/59/205와 Confluence63275012 v14는 연결 도구로 직접 읽었다. 11/19 문서의 과거 ‘제어 미구현’은 현재 소스/11267로 보완하되 과거 원문·Evidence는 지우지 않는다. 사용자의 기존 기술 결정권 위임은 유지하며, 실제 영향 변경의 작업창·범위 확인과 구분한다.

## 수정 전 findings와 해결

| ID·심각도 | 수정 전 위치·조건 | 영향·근거 | 수정·재검증 |
| --- | --- | --- | --- |
| PREP-01/P3 | be4d0ba deployment.md:3,11,58 및 integration.md:76,79의 이전 HEAD/12–14/Pause 부재 설명을 현재 준비에 사용 | 이미 존재하는 CLI/15번 migration 누락으로 시험안 오해 | 수정 전 commentary 보고. 최신 기준과 과거 이력을 분리; source/문서 전체 diff 재대조 |
| PREP-02/P3 | 새 Evidence README 말미 ‘schema query를 제외하고’ | 미실행 SQL을 실행한 것으로 오해 가능; 독립 reviewer DOC-01과 동일 | 수정 전 commentary 보고 후 ‘준비만 했으며 공유 DB SQL·migration 미실행’으로 통일 |
| PREP-03/P3 | finalize helper normalize_deploy/초기 projection 보정(수정 전27–30행) | 최초 미수집 generation에 최종값을 주입하는 방식은 독립 전후 비교로 오해 가능 | 수정 전 commentary 보고. generation을 양쪽 비교에서 제외하고 기존 원본 무수정. UID/image/readiness·Pod UID/digest/restart·CM UID/RV/mode 실제 재조회 비교 PASS |

최종 검토 범위에서 새로운 제품 P0/P1/P2 확정 결함은 발견하지 않았다. 아래 미검증을 결함 해소나 전체 시험 허용으로 바꾸지 않는다.

## 실패·보안·수명주기 검토

- 잘못 추정한 ECR repo NotFound는 `.initial.json`에 보존한다. actual backend의 두 exact tag ImageNotFound만 현재 근거다. 전체 registry 부재로 확대하지 않는다.
- Legacy inspect를 companion approve에 준 초기 시도는 INPUT_NOT_VERIFIED로 거부됐다. 요약 기록이며 원본 stdout 보관을 주장하지 않는다. 올바른 합성 resume fixture로 preview4개를 확인했다.
- 조회 실패/timeout/불충분한 입력은 NOT_VERIFIED. 실제 임의 키·attestation·타인의 승인값을 생성해 공백을 채우지 않았다. draft Plan은 runtime input이 아니다.
- AWSCLI.Read가 Policy.Region endpoint를 사용하므로 서울 Policy+기존 오사카 S3의 동작은 아직 미검증이다. 실제 cross-region 오류가 재현된 제품 결함이라고 주장하지 않는다.
- 실제 admin3 접근 성공은 최소권한/키 운영/전체 Pod coverage 인수 증거가 아니다. Pod proxy 권한만 확인했으며 exec나 admin endpoint를 호출하지 않았다. 원본 secret/DSN/키/토큰은 읽거나 보관하지 않았다.
- migration은 파일별 commit이다. 14 실패 후12/13의 부분 적용을 무시하거나 자동 down/DROP/공유 DB 복원하지 않는다. rollback old image의 durable 제어 유실 위험 때문에 단순 image 복귀와 안전 복귀를 구분한다.
- APPROVED_CONTROL은 closed만으로 Worker가 멈추지 않는다. durable init/Pause·기존 작업/UNKNOWN 정산·모든 serving Pod binding·fresh proof를 확인하기 전 변경/재개하지 않는다.
- Argo self-heal·자동 Sync가 있어 live patch는 복구될 수 있다. 동결/선택 적용/원복 spec·migration hook 실행 범위를 먼저 공유한다. 공급자 mutation·응답유실·claim 경쟁의 운영 장애 주입은 이번에 하지 않았다.
- 현재 원격 자원 생성/설정 변경이 없으므로 cloud cleanup은 해당 없음. 로컬 EXE·비활성 fixture만 남긴다. 서울 읽기 snapshot 전후 선택 필드 동일은 서비스 전체 무변경/이용자 없음/업무 기능 정상 증거가 아니다.

## 실행·미실행 구분

- 실행: 최신 main fetch/PR/Jira/Confluence 읽기, 한정 AWS/Kubernetes metadata 조회, CLI2개 build, 비활성 binary4검사, 대상 Go2테스트(하위4개 포함), JSON/AST/해시/선택 baseline/전체 diff 확인 및 secret scan.
- Mock/Fake: 비활성·preview Go 테스트, 공개 합성 fixture. 이 범위에서 외부 호출·키읽기 금지 확인.
- 과거 자료만 확인: be4d0ba race282 및 CI5/5. 이번 전체 Go/Web/PG/race/Linux 회귀로 재사용하지 않음.
- 미실행: 실제 공유 DB SQL·migration·백업/복원, image 게시/rollout, 보호 키/Policy 등록, IAM/RBAC 변경, signed collection/approve, S3 객체 버전 readback/upload, closed/Pause/Resume, Provider/다중 Pod 기능 시험·Fencing/SingleWriter·DR·Slack.
- 이번 선택 테스트 skip0; 다른 테스트는 미실행이며 skip으로 숨기지 않는다. schema 파일 파싱·기대 checksum은 실제 DB 적용 검증이 아니다.

## 독립 리뷰·판정

`audit_pr196_operator`가 구현과 분리해 현재 문서/packet·CLI/S3 endpoint·migration 소비 경계를 직접 재검토했다. 실제 AWS·DB를 별도로 재조회한 리뷰는 아니다. 보고서는 `independent-review.md`에 보관한다. README 모호성 권고는 수용·수정했다. 최종 packet manifest 확인은 보고서 후속 항목과 validation을 함께 참조한다.

준비 자료 delta는 검토·재현 가능한 상태다. **실환경 기능 시험 시작은 보완 필요(live_test_ready=false)**: 보호 운영 키/Policy/실제 IAM·RBAC/DSN, 후보 image digest, SQL schema·직전 백업, 작업창·Provider 시험 한도·실제 안전 원본을 확정하고 영향 변경안을 사용자에게 먼저 공유해야 한다. PR196 전체 인수·merge·배포 승인을 뜻하지 않는다. push/merge/외부 전송 없음.
