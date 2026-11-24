# 운영 제어 연결 1~3 오프라인 구현 감사 — 2026-11-23

사용자 PRE_MERGE_AUDIT.md를 읽고 구현과 분리된 reviewer의 재검토를 포함해 수행했다.
범위는 승인된 init/Pause, 읽기 전용 실제 어댑터, 보호된 키/Policy와 기존 Resume 연결의 **로컬 구현·모의 검증**이다.
AWS·서울 서비스·공유 DB·Slack을 호출하지 않았다. 원격 push/commit/merge/배포도 하지 않았다.

## 기준과 전체 diff

- 저장소: ktcloud4-SL/labbit-app, Draft PR196의 후보를 보완한 별도 로컬 branch.
- branch: `codex/feat/LBT-60-stage3-live-prep-20261123`.
- base main: `753eecbff737409a4c9cc8c542f3c6baa6acf0da`.
- HEAD 및 원격 PR196: `94fe1f61d7e398761291a967fab9820bfb08bc3d`.
- 최종 Git fetch/ls-remote에서 main과 PR head 불변 확인. 새 소스는 미커밋 상태이므로 HEAD CI가 이를 검증했다고 주장하지 않는다.
- base→HEAD 및 staged/unstaged/untracked 합계 277개 경로를 `diff-inventory.md`와 `source-manifest.json`에 고정했다.
  이 중 이번 작업 전 readiness 증거 27개와 제품/계약/문서/테스트 로컬 변경 37개를 포함한다.
  source manifest는 원본 및 LF 정규화 SHA256을 구분한다.
- 이전 전체 PR 감사 `../2026-11-23-pr196-publication/AUDIT.md`의 unchanged source 55개는 canonical hash로 확인하고 재사용했다.
  이전 문서·Evidence는 당시 관측 기록으로만 사용한다. 새로운 기능 및 영향받는 소비자·공유 상태는 다시 읽었다.
  `.gitleaks.toml`의 원래 공개 Alpine 인증서 오탐 예외는 고정 두 파일/고정 match의 AND로 제한됨을 재확인했다.
- 로컬 구현 전부터 있던 두 backend 문서와 readiness 원본은 보존했다. readiness manifest 26개 대상은 독립 재계산 26/26 일치.
- ignored `.local`은 폐기 PG fixture, raw logs, 빌드 바이너리, 테스트 harness다. 배포 입력이나 운영 승인 자료가 아니다.
  생성된 개인키는 테스트 t.TempDir의 합성 키이며 저장소에 포함하지 않았다. 운영 Secret/개인키를 만들지 않았다.

## 최신 SSOT와 요구사항 추적

직접 조회한 Jira LBT-205(updated 11/23 09:04 KST), LBT-60(updated 09:59), Confluence 검증 전략9666637 v4를
`ssot-sources.json`에 식별했다. 외부 문서/댓글은 수정하지 않았다. 저장소 AGENTS·CONTRIBUTING·TESTING,
Runtime Contract·maintenance/Worker 제어 문서, DB migration과 호출자를 대조했다.
HTTP OpenAPI 및 Connector/Realtime wire 계약은 새 변경에서 그대로다. 신규 readback은 Runtime 소유의 비공개 Admin route다.
기존 PR의 WSS 경쟁 교정·maintenance HTTP 계약은 이전 전체 감사와 이번 전체 Go 회귀에 연결했다.

| 요구·원문 | 구현·소비자 | 실행한 자동 검증 | 실제 Evidence·남은 공백 |
| --- | --- | --- | --- |
| LBT205/60, 운영 신원·명시적 제한 제어; runtime0.1.14 | `drcontrol/restrict.go:15,24,62`, `cmd/labbit-dr-control/main.go`, `workercontrol/restrict.go:25,66,137` | `TestRestriction*`의 actor/action/Scope/Phase/TTL/목적·서명·요청 hash 부정 사례 및 preview/disabled | Mock STS/S3·합성 키. 실제 IAM 승인/운영 Pause 실행 미검증 |
| 영속 INTENT, mutation+receipt 원자성, UNKNOWN 자동 재Apply 금지 | migration15, `postgres/worker_control_restrict.go:57,82,125`, `workercontrol/restrict.go:102,137` | `TestRestrictionInitPauseCASAndUnknownWithDisposablePostgres`, 동시 Begin·APPLIED NULL 거부·stale binding·이전 boundary 종료 | 실제 폐기 PG16 loopback. 공유 DB 적용·네트워크 응답유실/전원 장애 실측 아님 |
| 미해결 제한 결과도 Resume/drain을 막음 | `postgres/worker_control.go` Report, `worker_control_resume.go` Begin/Apply, `workercontrol/durable.go` Drained | PG 신규 INTENT→Report pending1, Resume HOLD, 기존 lifecycle 회귀 | 운영 전체 Pod·Job/Provider 정산 미검증 |
| 전체 Pod 관측·실제 BootID·고정 대상·TLS | `collector.go:72,96,143`, `collector_adapters.go:29,42,118`, `app/dr_readback.go:13`, `maintenance/gate.go` Readback | snapshot 전후 식별자/재시작/누락/extra/UID/digest/빈 목록/페이지/cluster/CA flag/실제 BootID 및 admin opt-in httptest | Fake Runner/Pod ports, 실제 Kubernetes/RBAC/CA/Pod proxy 미검증. namespace 밖 경로는 별도 전제 |
| 실제 상태 조회와 판정 구분, Single Writer 과대 판정 금지 | `collector_adapters.go:140` read-only SQL, `collector.go` 외부 signed attestation | SQLWorkReader를 실제 폐기 PG에서 실행. 미정산·조회 실패·TTL·누락·잘못된 binding·외부 원본 변조/FAIL 보존 테스트 | SQL은 전체 대상 DB count. 실제 Provider/Fencing 신뢰 수집자는 별도 의존성; 빈 inventory만으로 Single Writer PASS 안 함 |
| 목적·Phase·요청에 고정된 보호 키/Policy | `sign.go:47,81,108`, `protected*.go`, `cmd/labbit-dr-evidence/main.go`, 기존 Verifier | 서명→기존 소비자 roundtrip; wrong key/purpose/phase/public key/TTL; Windows owner/DACL 및 Linux owner/mode/symlink 거부 | 임시 합성 키만 사용. 운영 키 생성·설치·회전·폐기/보관 권한 미검증 |
| FAIL·NOT_VERIFIED·HOLD 유지 | collector→signed observation→고정 원본 reference→기존 Execute/Resume | missing counter/Provider NV는 NV, verified open/positive count는 FAIL, SQL 실패에도 verified external FAIL 보존; global gate 불변 | 기존 ReplicaLag FAIL·필수 Gate4 NOT_VERIFIED·HOLD 유지. 이번 보고서가 DR 승인/전체 복구 성공을 뜻하지 않음 |
| Runtime/DB SSOT와 additive migration | runtime0.1.14, db000015, migration metadata/fixture 소비자 | YAML 파싱·번호 중복 없음·15개 migration, PG 전체 integration 및 기존1~14 원문 hash 동일 | migration15는 새 코드보다 선적용 필요. 공유 DB 백업/복구·호환 rolling 작업창 미실행 |

## 수정 전 findings와 해결

아래 항목은 수정 전 사용자 commentary에 보고했다. independent reviewer 원문 및 초기 실패 로그도 보존한다.

| ID·심각도 | 당시 파일·라인/재현 | 영향·필요 회귀 | 해결·재검증 |
| --- | --- | --- | --- |
| LINK-01 / P1 | `collector_adapters.go:29` 당시 kubectl server만 고정, kubeconfig에 insecure TLS 허용 가능 | endpoint 고정만으로 cluster 신원 입증 불가. CA/hash·hostname·TLS false 명시 검증 필요 | Plan CAFile/CASHA256, protected CA/hash 확인, `--insecure-skip-tls-verify=false`·고정 TLS server-name/CA 추가. 고정 argv 회귀 및 reviewer 재확인 |
| LINK-02 / P2 | `collector.go:236~267` 검증된 external FAIL + SQL 조회 실패 | 확인된 FAIL을 NV로 낮춤. 독립 unavailable source에도 FAIL 보존 필요 | 외부 FAIL은 validWork와 독립 보존. 최초 독립 probe 재현 후 수정본 probe PASS |
| LINK-03 / P2 | `collector.go:188~206,264~267` 필수 counter 또는 Provider 근거 누락 | 실제 위반 아닌 조회 공백을 FAIL로 기록. 둘 다 HOLD이나 Evidence 판정 오류 | exact MaintenanceState+동일 lock count producer 추가; missing/negative/invalid는 NV, open/positive는 FAIL. Provider NV/missing/invalid 회귀 및 독립 missing-counter probe PASS |
| DEV-01 / SQL 식별 오류 | `collector_adapters.go` 초기 Provider 조건에 없는 `status` 컬럼 | 실 조회 항상 실패. schema가 owning SSOT | 실제 `lifecycle_status`로 교정, SQLWorkReader 폐기 PG 실행 PASS |
| DEV-02 / fixture 갱신 | migration count14 기대, 승인 window61초/정책60초, 누락된 negative test switch | 초기 테스트 실패. 정책을 완화하지 않고 fixture 수정 | count15·기존 constraint의 version14 범위 보존, window30초, fixture 분기 추가. 초기 실패 로그와 최종 PASS 분리 |
| ENV-01 / Windows test 환경 | Web Vite fixture 즉시 임시폴더 삭제가 병렬 실행에서 EPERM, 첫1건/재시도2건 | 제품 동작 실패와 다름. 소스 수정·expectation 완화 없음 | 같은 소스 `--maxWorkers=1`로 35파일/493개 PASS. 병렬 실패 원본 보존; Windows 병렬 cleanup flake는 남는 제한 |

## 실패·보안·수명주기 분석

- 입력: Strict JSON은 크기·중복/Unicode alias·unknown/trailing/UTF8를 거부한다. action discriminator 뒤 실제 typed Strict 검증을 다시 수행한다.
  제어/collection 대상·Scope·Plan hash·요청 SHA·signed domain/Phase/TTL이 맞지 않으면 진행하지 않는다.
- 승인: preview에는 키/DSN/외부 접근이 없다. execute flag와 보호 Policy가 함께 필요하고 historical inspect도 현재 신원을 다시 확인한다.
  새 Restriction은 요청별 승인이 필요하다. APPLIED 조회는 예전 실행 결과일 뿐 재실행 권한이 아니다.
- 영속성/경쟁: scope advisory lock+state row lock, scope당 pending 제한 및 approval unique. INTENT가 먼저 별도 commit되고
  state mutation과 APPLIED receipt는 같은 transaction이다. Begin 이후 결과 불명은 자동 Apply/재요청하지 않는다.
  pending restriction이 drain/Resume 모두를 막는다. 진단/정산은 후속 별도 승인 경로이며 자동 journal 삭제는 없다.
- stale/restart: exact Scope/revision/epoch와 실제 Pod/BootID가 필요하다. 이전 binding 재사용은 거부하며 이전 boundary의 정산 종료는 유지한다.
  일반 Pause와 init tx helper 추출은 기존 SQL 의미를 유지한다. init은 이미 존재하는 state를 재초기화하지 않는다.
- 조회: Kube는 shell/exec/apply 없이 고정 raw GET, 2초 요청 제한·최대64페이지/256Pod·중복 token/RV 확인.
  SQL은 read-only repeatable-read+2초 timeout. 취소 시 bounded rollback. 조회 전후 Pod 변화·불완전 범위·time 이상은 NV다.
- 보안: 공개 HTTP/peer endpoint에 readback을 붙이지 않는다. 운영 Admin listener/RBAC·kubeconfig plugin·상위 경로/실행 파일의
  소유권/변경 동결은 배포 선행조건이다. 보호 파일 자체는 regular file·열린 handle의 owner/ACL·동일 inode를 확인한다.
  signed external proof는 서명자의 신뢰·원본 진실성 책임을 대신하지 않는다. 외부 Fencing/Provider 수집자 구현·설치는 미검증이다.
- 자원: 운영 키·클라우드 자원 생성 없음. loopback PG fixture의 `labbit_it_*` 잔여0을 확인하고 해당 fixture 서버만 정상 종료했다.
  Linux는 기존 캐시 checksum을 검증해 RAM initramfs/noNIC/no disk VM으로 실행·종료했다. 로컬 test binaries/logs는 ignored에 남는다.
- 로그: 공개 에러는 비민감 reason code만 반환하며 raw provider/DB error·키·DSN을 출력하지 않는다. Gitleaks source/Evidence 스캔 결과 별도 첨부.
- 영향 없는 계약: HTTP/WSS/Realtime wire·기존 migration1~14·실제 GitOps/Seoul 설정·운영 IAM/Secret에는 변경을 수행하지 않았다.

## 실행 범위와 결과

test event 숫자는 subtest를 포함하며 서로 다른 suite의 숫자를 합쳐 고유 테스트 수로 사용하지 않는다.
모든 원본 로그는 gzip으로 보존했으며 정량/skip 목록은 `test-summary.json`을 따른다.

| 검사 | 결과 | 해석 |
| --- | --- | --- |
| 전체 Go `go test -count=1 -json ./...` | 4604 PASS / 88 SKIP / FAIL0, 53 package PASS | Windows unit/mock. script/Unix 및 외부 fixture skip을 별도 기록 |
| 최종 영향 6package `-race` | 335 PASS / FAIL0 / SKIP0 | drcontrol/app/maintenance/workercontrol와 두 CLI; init/Pause 실제 서명 producer→consumer 보강 후 재실행. PostgreSQL package는 아래 suite |
| 폐기 PG16 6package integration `-race` | 1761 PASS / 10 SKIP / FAIL0 | Valkey fixture 없는9건 및 sample dump1건 skip. SQL 구현·migration·locking 실증; 실제 RDS 아님 |
| 최종 분류 수정 후 PG/handler 영향 재실행 | 21 PASS / FAIL0 | Restriction/init/Pause/CAS/UNKNOWN, 기존 signed operator, admin readback |
| 실제 Linux drcontrol test binary | 124 PASS / FAIL0 / SKIP0 | noNIC RAM VM, non-race. Unix 보호 파일/서명/collector mock 포함; 운영 Linux 배포 아님 |
| reviewer 독립 unit/mock 7package | 459 PASS / FAIL0 / SKIP0 | 작성자 결과와 독립. 추가 overlay regression2건 PASS |
| gofmt / vet / Go build / Linux build | PASS | Windows/Linux compile; 최종 영향 파일 format 확인 |
| web typecheck / lint / build | PASS | Web 소스 변경 없음 |
| web test 순차 | 493 PASS / 35파일 PASS | 기존 병렬 EPERM 실패는 삭제하지 않고 보존 |
| Runtime/Migration metadata | PASS | owning YAML0.1.14, migration15개 및 기존 SQL 변경 없음 |

실행 후 제품 소스 manifest와 reviewer의 source SHA가 일치하는지 최종 확인한다. 전체 PG suite 이후 수정은
Gate readback/collector 분류와 그 테스트·문서에 한정되며 SQL/migration 소스는 동일하다. 해당 변경은 최종 전체 Go,
영향 race, PG/handler 재실행 및 Linux에 반영됐다. Linux binary SHA와 이미지 checksum은 환경 JSON/serial에 연결한다.
전체 Go/PG 종료 뒤 마지막 변경은 기존 init/Pause 테스트에서 합성 서명 helper 대신 실제 protected-key SignApproval을
소비하는 assertion을 넣은 test-only 보강이다. 제품 소스는 동일하며 해당 패키지 전체 race와 Linux를 다시 실행했다.
이전 94fe CI 5/5는 역사적 결과이며 이 새 로컬 소스의 원격 CI가 아니다.

## 독립 검토·남은 조건·판정

별도 `/root/audit_pr196_operator`가 수정 없이 SSOT·full local inventory·producer/consumer·새 migration과
실패 분기를 재검토했다. LINK01~03 해결과 독립 probe를 확인했으며 추가 확정 P0/P1/P2는 발견하지 못했다.
전체 raw 보고서 및 테스트 전후 source stability가 첨부돼 있다. reviewer는 실제 PG·Linux·AWS를 실행했다고 주장하지 않았다.

**요청한 1~3의 로컬 코드/어댑터·모의 검증 범위는 충족한다.** 실제 환경 인수나 전체 3단계 완료는 아니다.
다음 조건은 남는다: 승인된 운영 키/Policy 설치·회전·소유권, 실제 STS/RDS/Kube/RBAC 경로, 공유 DB migration15의
백업·선적용·복귀 절차, 고정 이미지·Revision 및 전체 serving Pod/Job 정산, 실제 Provider/Fencing/SingleWriter 증거와
운영 작업창. 이번 수집기는 외부 물리적 Fencing을 수행하지 않으며 서명 seam만으로 그 사실을 입증하지 않는다.
네 가지 필수 Gate NOT_VERIFIED·ReplicaLag FAIL·전체 execution_gate HOLD·recovery_authorized=false·cloud_verified=false를 유지한다.
감사 결과는 push·PR·merge·배포·외부 전송의 승인이 아니며 현재 지시에 따라 모두 수행하지 않았다.

## 재현

- 저장소 AGENTS/TESTING과 Runtime0.1.14를 먼저 확인한다. 현재 source-manifest의 미커밋 파일까지 동일해야 한다.
- Go1.27.1: `go test -count=1 -json ./...`, 영향 package `-race`, `go vet ./...`, `go build ./...`.
- 별도 **폐기용 loopback PG16**에만 `LABBIT_TEST_DATABASE_DSN`을 설정하고 `go test -race -tags=integration`
  로 drcontrol/postgres/postgrestest/app/auth/httpapi 6package를 실행한다. 기존 운영 DSN을 사용하지 않는다.
- Web lockfile 기준 `npm ci`; typecheck/lint/build 및 `npm run test -- --maxWorkers=1`.
- Linux harness는 `linux_linkage.py`·init·환경 JSON과 캐시 manifest를 참고한다. GOOS=linux/CGO_ENABLED=0으로
  drcontrol test binary를 재생성하고 SHA를 확인해야 한다. 바이너리/VM을 AWS 시험으로 해석하지 않는다.
- 로그는 `gzip.open(path, 'rt', encoding='utf-8-sig')`로 읽는다. 원본/압축 해시는 artifact-manifest에 고정한다.
