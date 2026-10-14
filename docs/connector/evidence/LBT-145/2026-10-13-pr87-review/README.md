# PR #87 재리뷰 수정·검증 — 2026-10-13

PR의 두 P2 리뷰와 독립 재검토에서 확인한 관련 입력 경계를 수정했다. 기존 PR #87을 갱신하며 main에는 push/merge하지 않는다. GitHub 사람의 승인과 thread 처분은 별도 재리뷰다.

- 수정 전 PR HEAD: `b01e901b783691e393fb79cc871a4f5835ce1a49`.
- 수정·자동 시험 source: `943d0bdd5e60221638886b03aa37038976cb8a5f`.
- 최신 main: `f7a9a953d04e0b96d26b3baa4b09f83c9ec80f8a`.
- main 문서 1개를 작업 브랜치에 통합한 reviewed HEAD: `4209eb58d31c39f03a476c415ee591e096e35459`. 실행 source350개는 tested source와 동일하다(`tested-source.json`). 이후 이 디렉터리의 Evidence만 추가한다.

[감사·findings·추적성](PRE_MERGE_AUDIT_RESULT.md), [독립 두 번째 리뷰](INDEPENDENT_REVIEW.md), [파일 목록](candidate-files.json), [실행 수치](execution-summary.json), [보존 확인](preservation-check.json).

## 수정과 검증 범위

- Tier POST가 거절되거나 exact ID의 이름/CIDR/명시적 `shared:false`를 확인하지 못하면 새 ID를 소유 Cleanup 목록에 넣지 않는다. POST 이전 inventory에 있던 ID도 채택하지 않는다.
- 이미 확인한 matching BUILD Tier는 후속 조회 실패·timeout·필드 누락에도 부분 성공 참조를 유지한다. 이후 다른 이름/CIDR/`shared:true`가 명시되면 자동 삭제 권한을 제거하고 UNKNOWN으로 반환한다.
- HTTP 404와 HTTP 200 본문의 숫자/문자열 `httpStatus:404`는 같은 exact-ID 전체 inventory 관측으로 판단한다. 완전한 부재만 삭제 완료, present/불완전/실패는 UNKNOWN 및 참조 보존이다. DELETE는 반복하지 않는다.
- `shared`와 pagination의 누락/null을 false/빈 목록으로 해석하지 않는다. 기존 정상 fixture 3곳에 명시적 `shared:false`만 추가했고 기존 assertion/test는 삭제하지 않았다.
- 신규 Tier20개·Tier/Firewall DELETE60개 회귀는 in-process HTTP server와 production 결과·후속 public Cleanup을 검증했다. 실제 고객 클라우드에 오류를 주입하지 않았다.

## 자동 시험과 최초 실패 기록

원본 `checks/summary.json`은 최초 whole Go 실패를 그대로 보존한다. tracing의 기존 시간 하한 테스트가 300ms 기준에 299.9859ms를 측정해 실패했다. 해당 소스는 main과 동일하며 timeout 초과나 무기한 대기는 관측되지 않았다. 원인을 확정하지 않고 Windows 시간 경계 민감성으로 기록한다. 같은 소스로 isolated20회와 전체 순차 실행을 확인했으며 둘 다 PASS다.

최종 실행: Go LF formatting331개, vet/build, whole sequential993 top-level tests, Provider/acceptance race126 top-level tests, Web typecheck/lint/test13files163tests/build, OpenAPI·8JSON/local refs·Runtime/migration metadata·Reset schema11tests PASS. Test/package 이벤트 수는 `execution-summary.json`에 분리한다. 기본 live gate7개와 Windows OS 조건3개 subcase는 SKIP다. 로컬 PostgreSQL integration은 미실행이며 새 PR CI에서 확인한다.

## 실제 KT Evidence는 다른 revision

실제 KT Connector→CC-01→OP-01→Provision g1→SSH 명령→Reconcile→Reset g2→SSH 명령→Cleanup·소유 잔여0 검증은 **`6a6fb4f97e676a80cfce27f85269f0627970fc9c`**에서 실행했다. [기존 실행 원본](../2026-10-13-premerge/README.md)을 보존한다. 이번 943d 수정본에서 실제 KT를 재실행했다고 주장하지 않는다. 리뷰가 요구한 오류·소유권 경계는 httptest/regression으로 검증했다. C1 전체 Backend/Browser 및 SFTP/PTY는 이번 범위가 아니다.

## 같은 commit에서 재검증

Git에서 이 문서와 같은 commit을 checkout하고 Go 프로젝트 기본 의존성과 Web lockfile 의존성을 준비한다. 인증정보 없이 다음을 실행할 수 있다.

```sh
go test -count=1 ./internal/connector/provider/openstack -run 'TestKTCloudTierOwnershipBeforeResultAndFollowupCleanup|TestKTCloudDelete404ReceiptUsesCompleteInventory'
go vet ./...
go test -count=1 -p=1 ./...
go test -race -count=1 ./internal/connector/provider/openstack ./tools/ktcloud-d1-acceptance
go build ./...
make go-fmt-check web-typecheck web-lint web-test web-build
python contracts/connector/tests/test_reset_schema.py
```

Contracts validators는 `.github/workflows/contracts.yml`의 고정 버전으로 설치·실행한다. 실제 Go/Node/Python 경로와 환경 gate 제거 방식은 `checks/*.py.txt`에 보존한다. Windows에서 Go format은 Git 공개 LF 바이트 기준으로 대조했다. 초기 실패를 재검증하려면 `go test -count=20 -run '^TestShutdownIsBoundedByTheGivenBudgetWhenCollectorHangs$' ./internal/observability/tracing`를 실행한다. 이 검사는 원인 확정이나 모든 스케줄링 조건 재현을 보장하지 않는다.

보호된 KT 설정·개인키, old untracked Evidence 및 ignored helper는 이 무클라우드 재검증의 필수 입력이 아니다. 새 클라우드 생성/삭제·로컬 OpenStack live 실행·SSH Ready 정책 변경은 수행하지 않았다.
