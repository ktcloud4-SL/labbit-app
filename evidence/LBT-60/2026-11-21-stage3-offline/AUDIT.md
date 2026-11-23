# 2026-11-21 Stage 3 오프라인 pre-merge audit

사용자 `PRE_MERGE_AUDIT.md` 전체를 읽고 적용했다. 검토 대상은 README의 세 base commit부터
미커밋 tracked/untracked 변경 전체다. 변경 파일 목록/바이트 해시는 각 저장소 `source-manifest.json`을 따른다.
실행 환경은 Windows PowerShell, Go1.27.1, Python3.12, PostgreSQL16, Helm3, Node24.22.3이다.
실행 소스는 원격 PR HEAD가 아닌 이 manifest에 고정된 로컬 후보다. Evidence 생성 시각은 원본 로그에 보존한다.

## 원문과 적용 범위

- [LBT-205](https://samsunglions.atlassian.net/browse/LBT-205) 최신 확인 시 11/20: 전체 Replica/internal 접수 차단,
  claim/dispatch 별도 제어, old binding 금지, 승인/재점검/INTENT 및 UNKNOWN 보존.
- [LBT-59](https://samsunglions.atlassian.net/browse/LBT-59), [LBT-60](https://samsunglions.atlassian.net/browse/LBT-60):
  대화에서 확정한 Worker 포함 목표·오프라인/실환경 인수 경계 및 FAIL/4NV/HOLD 보존.
- Confluence 테스트 전략 9666637 v4 및 저장소 AGENTS/CONTRIBUTING/TESTING을 확인했다.
- Runtime0.1.12→후보0.1.13, backend 책임/DR 제어/maintenance 통합 가이드, migrations12–14,
  Resumer·durable Store·Executor·Router·앱 조립을 대조했다. migration 원본은 변경하지 않았다.
- Infra PR57 `5987cb0` D01–D06 및 22단계 고정 모형, GitOps base의 실제 backend/web env·공통 Chart와
  root App 등록 경로를 확인했다. 새 프로필은 기존 등록 경로 바깥이다.
- HTTP/Connector/Browser Realtime의 제품 계약은 변경하지 않았다. 기존 인증/권한/replay/HTTP503·
  WSS 메시지/Terminal 내용 보존 정책을 우회하는 진입점이나 새 public endpoint를 만들지 않았다.

## 요구사항 → 구현 → 자동 검증 → 실제 Evidence

| 요구사항/SSOT | 구현 | 자동 테스트 | 실제 실행 Evidence / 남은 공백 |
| --- | --- | --- | --- |
| D01/#194 기본 접수·Worker 차단 유지 | `settings.go`, `gate.go`, `control_stack.go`, Runtime `WORKER_ADMISSION_POLICY` | `TestControlledWorkerStillRequiresValidFileAndCountsDrain`, 기존 maintenance/App 회귀 | loopback App+PG. 실제 모든 Pod/유입 경로 미검증 |
| D03 접수 closed 동안 승인된 Worker 검증 | 같은 파일 APPROVED_CONTROL opt-in, local fallback 거부 | `TestApprovedControlProcessesExistingWorkWhileNewHTTPStaysClosed` 3종·2Executor, App 승인 재개 두 정책 | 실제 임시 PG+loopback WSS+MockProvider. Kubernetes/Provider 실환경 아님 |
| 운영자 인증·새 승인·TTL/Target | `internal/drcontrol/{contract,verified}.go`, 별도 CLI, Runtime | wrong ARN/Principal/action/Scope, 만료/미래/FAIL/NV, 위변조·대소문자/Unicode alias·서명 purpose | Identity/S3/RDS Fake. 키 배포·신뢰 Collector·운영 승인 인수 미완료 |
| 고정 VersionId·원본 크기/SHA·취소 | `awscli.go`, strict Reference | download 전 Head 대조, cancellation, context·size·rawSHA·정확 임시파일 cleanup | Fake Runner. 실제 AWS CLI 호출/권한/레이턴시 없음 |
| 기존 INTENT/UNKNOWN/중복 의미 보존 | `verified.Execute`→기존 Resumer/ResumeStore | 인증 전 journal 차단, APPLIED history only, UNKNOWN no apply | 실제 임시 PG로 signed proof→INTENT→APPLIED/새 epoch·old binding 거부. TCP commit-loss는 Fake/기존 시험만 |
| 정확 제어 DB·TLS·Writer 검증 | `database.go` exacthost/db/user/TLS, RDS ResourceId 검사 | TLS/옵션/Host/DB 불일치, SQL wrongdb/cancel. 다중 host 거부는 len(Fallbacks) 코드 분석이며 별도 fixture 미실행 | PG16 실제 localhost 시험은 dummy sslmode=disable fixture. 운영 TLS/IAM/Writer identity/Single Writer는 미검증 |
| Worker 포함 standby/active/failback 프로필 | GitOps `profiles.py`/README, sourcehash/pinned commit/newbinding | 6프로필×Backend/Web 실제 Helm template, base 불변·타리전/해시 drift 거부 | 로컬 Helm만. standby 쓰기 격리/Secret/image provenance·Argo 실제 전환 없음 |
| 단계별 안전 조건·FAIL/HOLD 보존 | Infra `dr_recovery_linkage.py` 22 Adapter ID/필수조건 | 22단계 정상·조건 누락, raw binding bool/float, 변조/TTL/중복·Pod coverage·미해결 작업 | 전부 합성. 실 Fencing/RDS/DNS/전역 lock Adapter 미구현 |
| 배포/데이터 수명주기 | `dr-stage3-deployment.md` | 기존 migration/constraint 회귀, startup DDL 없음 코드 검토 | 운영 백업·복원·migration·rollback은 실행 안 함 |

## 수정 전 findings와 해결

모두 수정 전에 사용자 commentary로 보고했다. 원본 검토 라인은 수정 전 기준이며 현재 위치는 manifest와 코드를 따른다.

| ID/심각도 | 원본 위치/재현 | 영향/필요 회귀 | 처리 |
| --- | --- | --- | --- |
| F-JSON/P2 | `contract.go` 원본139, Version/version 및 Unicode long-s/Kelvin alias | Go JSON case folding으로 중복 정책 값 해석 가능 | SimpleFold 기준 duplicate 거부. alias 회귀 PASS |
| F-KEY/P2 | `contract.go` 원본213, 같은 공개키의 CRLF·base64 alias | 승인/관측 key 분리 우회 가능 | canonical encoding+decoded bytes 비교. purpose 회귀 PASS |
| F-BIND/P2 | Infra artifact의 dict equality, revision True/1/1.0 | 잘못된 증거 유형을 같은 binding으로 수용 | raw binding 엄격 유형 검증. bool/float 회귀 PASS |
| F-PROFILE/P2 | GitOps active(closed+ACTIVE)와 기존 `gate.Enter` | Worker 검증 단계가 실제로 차단됨 | 명시 opt-in 분리·기본 유지·HTTP closed/oldbinding 회귀·실제 PG PASS |
| F-CONTRACT/P2 | Runtime maintenance.platform 약136, open 필수 설명 | 새 producer/consumer 의미 충돌 | 기본/opt-in/typed Reconciliation 범위 명시. 독립 재리뷰 해결 |
| F-TEST/P3 | App integration test529 import 누락 | integration build 실패 | import 추가, 실패 원본 보존 및 전체 재실행 |

## 실패·보안·수명주기 검토

- 입력은 bounded strict JSON/unknown field·중복·Unicode alias·invalid UTF8·추가 문서/크기/depth를 거부한다.
  임의 shell/SQL/callback/endpoint를 받지 않는다. AWS 실행파일/profile과 Policy는 보호된 operator 신뢰 경계다.
- 승인과 관측은 별도 Ed25519 key/purpose/Phase. 관측 rawSHA를 최종 요청에 연결해 순환 hash를 피한다.
  서명 자체가 현실의 Fencing 증명은 아니다. 관측 수집자의 정확성·키/시간 신뢰·전체 coverage는 인수 전이다.
- 역사적 APPLIED/INTENT 조회도 현재 신원 인증을 선행한다. INTENT·UNKNOWN에서 Apply 자동 재전송 없음.
  DB 복원으로 journal/승인 소비가 유실되면 blind resume 금지. 전역 DR 잠금은 별도 미구현이다.
- APPROVED_CONTROL은 default가 아니고 postgres+구성 파일을 필수로 하며 Worker마다 durable binding을 검사한다.
  old process는 자동으로 새 epoch를 채택하지 않는다. HTTP gate는 계속 closed. 허용 대상은 특정 ID가 아니라
  기존 backlog 전체이므로 complete inventory/UNKNOWN 정산/변경 동결 없이 운영하지 않는다.
- missing/invalid 파일은 일반 mutation claim을 차단한다. 기존 RECONCILE_ONLY는 별도 typed query 관측 경계다.
  모든 DB 쓰기 차단·기존 DB 세션 종료·Connector/Job 격리를 maintenance metric으로 대체하지 않는다.
- 임시 Artifact 파일은 exclusive 생성·크기 제한·원본 hash 확인 후 정확한 경로만 cleanup한다.
  GitOps render는 기존 values/등록 경로에 쓰지 않는다. 프로필 이름 역순은 failback 실행 절차가 아니다.
- 운영 Secret/DSN/private key/Terminal 본문을 입력·로그에 수집하지 않았다. 테스트 키와 credential은 합성 dummy다.
  원본 테스트 로그의 fixture 실패 메시지는 실 운영 응답이 아니다. 외부 배포/수집·Slack 전송 없음.

## 실제 실행/실패/skip 경계

정량 결과는 `test-summary.json`을 따른다. Go counts는 **Test 이벤트**만 세며 package 이벤트를 더하지 않는다.

- `control-final.jsonl`: 새 코드 포함 6개 package `go test -race -count=1` — 368 Test PASS/0FAIL/0SKIP.
- `maintenance-postgres-final.jsonl`: 폐기용 PG16 App `^TestDRMaintenance` — 11 Test PASS. WSS는 loopback MockProvider.
- `postgres.jsonl`: 신규 operator/PG 테스트 — 54 Test PASS. cloud Identity/Artifact는 Fake, PostgreSQL은 실제.
- `integration-all.jsonl`: Makefile의 integration 6package 전체·race·실제 폐기용 PG — 1699 Test PASS/0FAIL/10SKIP, 6package PASS. skip은 Valkey 미기동 관련9건과 opt-in 샘플 출력1건이다. Valkey 실제 N=2 시험을 대체하지 않는다.
- Infra recovery69개 PASS, 신규 linkage6개 최종 재실행 PASS. SQLite journal은 실제 local filesystem, cloud는 합성.
- GitOps 신규4개 PASS. 전체11개 중10PASS/1ERROR: base에 없는 서울 `backend-valkey-values.yaml` 참조.
- Go 전체 초기 실행: 4002 PASS/39 FAIL/15 SKIP, 실패 package installer/OpenStack, OpenStack10분 timeout.
  변경하지 않은 경로지만 기존 baseline PASS/FAIL을 별도로 재현했다고 주장하지 않는다. Linux ledger/OpenStack opt-in
  미설정 등에 따른 skip 및 timeout으로 미완료된 테스트도 있다. 해당 실패를 이번 변경범위 PASS로 덮지 않는다.
- Web 전체 초기492PASS/1FAIL(Windows 임시 디렉터리 rmSync EPERM). 해당 파일 재실행30/30PASS.
  전체 green 재실행을 했다고 주장하지 않는다. Web typecheck/lint/build 및 Go vet/build는 실행 성공했다.
- 원본 gofmt 검사는 CRLF checkout 파일 632개를 보고했다. 전체640개 파일을 LF로 정규화해 gofmt 출력과 바이트 비교한 검사는 모두 일치했다(`gofmt-canonical.json`). checkout을 일괄 수정하지 않았다.
- 최초 Python alias·PyYAML 누락·cp949 decode·npm wrapper 경로·PG initdb 한글 경로 실패를 환경 준비 중 겪었다.
  실 Python/PyYAML6.0.3/UTF8/직접 npm-cli/ASCII 전용 PG 경로로 해결했다. 초기 integration compile 실패 로그도 보존했다.

## 재현과 Cleanup

Go 관련 테스트는 module root에서 `go test -race -count=1 ./internal/drcontrol ./cmd/labbit-dr-control
./internal/server/maintenance ./internal/server/workercontrol ./internal/server/provision ./internal/server/app`.
실제 PG는 사용자 공유 DSN을 사용하지 않고 `127.0.0.1:55439`의 전용 PostgreSQL16 dummy cluster를 생성했다.
`LABBIT_TEST_DATABASE_DSN`을 이 폐기용 server의 postgres DB로 설정한 뒤 Makefile integration package 목록에
`-race -tags integration -count=1`을 실행한다. test별 DB 생성/정리는 postgrestest helper가 담당한다.
TLS 순수 설정 검사는 운영 verify-full을 강제하지만 실제 loopback fixture는 의도적으로 TLS 인수와 분리한다.
서버 종료/포트 점검 결과는 `cleanup.log` 참조. 데이터 디렉터리는 로컬 `.codex/tmp`에 남으며 Git에 넣지 않는다.

Python3.12 stdlib로 Infra unittest 실행, GitOps는 PyYAML6.0.3/Helm3/PYTHONUTF8=1을 사용한다.
로컬 도구 경로는 실행 의존성일 뿐 배포 설정이 아니다. AWS 인증정보가 없어도 관련 모의 시험은 실행된다.

## 독립 두 번째 검토 / 판정

별도 read-only reviewer `/root/review_stage3_offline`가 세 base+전체 source diff와 Runtime·D01–D06를 대조했다.
F-JSON/F-KEY/F-BIND/F-PROFILE 및 후속 계약 충돌을 독립 확인하고 수정 후 해결로 재판정했다.
직접 실행: App 관련6package unit, GitOps4/4, Infra6/6 PASS. 실제 PG는 원본 Evidence 검토만 했다.
실제 AWS/공유 DB/Argo/Provider/Slack/실환경 Fencing은 reviewer도 실행하지 않았다.
검토한 오프라인 후보 범위에서 추가 확정 blocking 결함은 발견되지 않았다.

**판정: 새 오프라인 후보의 관련 검증·독립 리뷰는 통과. 전체 기본 검사 실패/format 제한이 남아 PR 준비 판정은 보완 필요.**
전체 Stage3 완료·실환경 반자동 복구 성공·배포 승인·DR GO는 아니다. 현재 로컬 변경을 push하지 않았다.
