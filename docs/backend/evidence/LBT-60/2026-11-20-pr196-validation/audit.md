# PR196 CI·PostgreSQL 검증 증분 감사

실행일: 2026-11-20 KST. 사용자 `PRE_MERGE_AUDIT.md`, 저장소 AGENTS/CONTRIBUTING/TESTING 적용.

## 범위와 원본

- 저장소: `ktcloud4-SL/labbit-app`, Draft [PR196](https://github.com/ktcloud4-SL/labbit-app/pull/196).
- base/main: `753eecbff737409a4c9cc8c542f3c6baa6acf0da`.
- 수정 전 HEAD: `5d37437397bebf5bcd5bd10741f8a4937b7832d2`.
- 실제 실행 소스는 미커밋 작업 트리다. `validation.json.source_inventory`에 base부터 누적 변경의 파일별 목적·원본/LF 정규화 SHA256을 기록했다. 이번 감사 파일은 자기 참조 hash에서 제외한다.
- 이전 main-refresh 감사와 과거 Evidence는 당시 결과를 보존한다. 과거 compile-only·DB 미실행을 이번 실제 폐기 DB 결과로 소급 변경하지 않는다.
- 검토 제외한 변경 파일은 없다. source inventory의 기존 Evidence는 이력/추적성으로 읽었으며 제품 실행 코드로 취급하지 않았다. stat상 수정으로 표시되지만 `git diff HEAD`에 실제 변경이 없는 기존 8개 파일은 수정·stage하지 않는다.
- ignored 도구·로그는 `.local/`, 작업 PC `outputs/pr196-validation-20261120/`에 있다. 런타임 의존성이 아니며 로그 경로·크기·SHA256 및 재현 명령은 validation에 기록한다. 재현에는 Go, pinned Node/npm 의존성, 폐기용 PostgreSQL16이 필요하다.

## 최신 요구사항·SSOT

1. PR196 최신 리뷰와 [LBT-60 댓글11260](https://samsunglions.atlassian.net/browse/LBT-60?focusedCommentId=11260): WSS CI 실패와 실제 PostgreSQL 검증을 남은 항목으로 구분한다. 11개 migration 고정 fixture가 DB 본문 실행을 막는 최신 CI 로그를 직접 대조했다.
2. LBT-59/205의 #194 운영 차단과 #196 추가 제어는 별도 범위다. 이번에는 서울 closed/open 시험을 반복하거나 공유 설정을 변경하지 않는다.
3. [검증 설계 v4](https://samsunglions.atlassian.net/wiki/spaces/SL/pages/9666637): Local/CI·실제 외부 시스템·DR 결과를 구분하고 실패/미검증을 덮어쓰지 않는다. D-24와 Infra D01–D06/C01–C20의 출처·누적 매핑은 이전 main-refresh 감사와 함께 확인했다.
4. HTTP owning OpenAPI: maintenance 권한/committed replay 우선과 503 유지. 새 운영 제어 HTTP endpoint를 만들지 않는다.
5. Runtime owning `durable_boundaries`/`approved_resume`: opt-in·exact binding, 새로운 승인/점검, 별도 INTENT commit, state/APPLIED 원자성, UNKNOWN 무자동재개를 따른다. 실제 승인/Fencing Adapter는 구현하지 않는다.
6. DB owning migrations: 기존 파일과 적용 checksum을 보존하고 14를 추가한다. schema readiness 소비자는 embedded 최신14를 요구한다. 새 migration은 main/공유 DB 적용 완료를 의미하지 않는다.
7. Connector owning README §4/§15: 인증된 새 연결이 기존 연결을 교체하며 revoke4001/replace4002를 유지한다. Terminal/Live wire·본문 비저장 계약은 변경하지 않는다.

## 수정 전에 보고한 findings

| ID·심각도 | 수정 전 위치·재현/영향 | 처리·의미 있는 회귀 |
| --- | --- | --- |
| PG-FIXTURE-01 / P1 | parent `migrate_integration_test.go:355–364`: embedded13인데11을 요구. 실제 CI 및 폐기PG에서 `TestWorkerControlPostgresCreatePauseAndConcurrentEnter`가 본문 전에 실패. 가상12도 신규 실제12와 충돌 | 최신14 기대·가상15, 과거 v10→v11 시험은11로 고정. 최신/과거 Schema compatibility를 모두 유지. 전체 PostgreSQL integration 및 migration 실패/재시도/동시 runner 회귀 실행 |
| PG-REVIEW-01 / P2 | parent `000013_worker_control_resume.sql:15–16`: APPLIED+NULL의 CHECK 결과가 SQL NULL이어서 허용됨. 조회기는 거절하지만 물리 불변조건 미충족 | 기존13 보존, additive14에서 명시 `IS NOT NULL`. 실제 v13 결함 재현 →14 검증 실패/rollback·기존 row/history 보존 → 잘못된 시험 row만 제거 후14 적용. INTENT/APPLIED 시각5경계 실제 CHECK 시험 |
| WSS-01 / P2, 기존 결함 | parent `handler.go:423–428`, `conn.go:119–124`: ACK 후 retire/MarkReady 실패의 즉시 반환과 비동기 closeFor가 경쟁해 TCP가4001 프레임보다 먼저 닫힐 수 있음. 독립 `-race -count=100`에서 revoke1/100이1006. 해당 기존 파일은 parent PR base 대비 미변경, Router=nil이므로 WorkerControl 직접 회귀 근거 없음 | Registry close 요청 게시와 transport 단일 요청 latch·frame 시도 완료·cleanup을 연결. callback은 lock 밖, late 요청 봉인. ACK-before-ready revoke/replace 실제 루프백, frame write 중 cleanup, 동시 shutdown, late 요청 회귀. 1006 허용/임의 sleep으로 숨기지 않음 |
| WSS-RECHECK-01 / P2, 수정 중 발견 | 첫 수정안의 duplicate retire가 callback 완료를 기다려 동일 Registration의 callback→Retire 재진입 시 자기 자신을 기다릴 수 있음 | 사용자에게 먼저 보고 후 중복 retire 즉시 반환. 명시 cleanup만 기다리게 하고 동일 Registration 재진입 테스트 추가. 독립 반복 race PASS |
| DOC-TRACE-01 / P3 | Runtime migration 참조13/실제DB 전부 미검증 문구와 운영 가이드의 compile-only 설명이 새 증거와 충돌 | Runtime13+14 의미와 폐기PG/공유DB 경계 명시. 과거 감사는 그대로 보존. Contracts CI 프로그램 및 독립 문서 재대조 |

원본 실패 로그는 `baseline-fixture-failure.log`, 실제 PG 추가 시험은 `postgres-targeted.log`에 함께 보존했다. 실패한 initdb 두 시도는 Windows의 한글 설치 경로/UTF8 오류였으며 ASCII 경로의 독립 복사본에서 성공했다. DB 테스트 FAIL로 바꾸거나 숨기지 않는다.

## 요구사항 → 구현 → 실행 → 잔여 공백

| 요구사항·원문 | 구현/consumer | 자동 검증·실행 Evidence | 실제 범위·공백 |
| --- | --- | --- | --- |
| #194 접수 차단·현재 권한/replay 의미 유지 | OpenAPI, app/control_stack, 기존 Service/HTTP mapper | HTTP/maintenance/admission integration 및 누적 기존 회귀. final-app-maintenance-pg 원본 | 실제 PostgreSQL+in-process HTTP; 서울 Pod 및 실제 Provider 미호출 |
| exact binding의 claim/prepare/dispatch/requeue와 query-only 정산 | workercontrol, provision/executor·reconciliation, Connector Router | 기존 unit/race의 stale binding·다른 boot·미검증·동시 실행 차단. final-related-race | Fake/Mock 다중 인스턴스와 PostgreSQL 직접 Store 시험; 실제 다중 Kubernetes Pod/리전은 아님 |
| 권위 DB의 Pause/Enter 직렬화·열린 경계 정산 | postgres/worker_control.go, migration12 | 기존32개 Enter/Leave + 신규 PostgreSQL row lock으로 Pause 먼저 대기 → 다른 session Enter 대기 → Pause commit 뒤 old revision 거절. pg-worker-lifecycle / pg-worker-race | 실제 PG16.15 locking, 단일 loopback Writer. 글로벌 Writer0·이미 전송된 Provider 작업0을 증명하지 않음 |
| 신규 승인/재점검·영속 INTENT/APPLIED 및 재호출 안전성 | workercontrol/resume, postgres/worker_control_resume, migrations13/14 | 실제16 concurrent Apply 중1회 성공, Begin/Apply 실제 commit 뒤 호출 응답 유실 주입 → pool 종료/재연결 → INTENT UNKNOWN·무자동Apply, APPLIED 역사적 조회 | 승인/안전 proof는 합성 trusted port. OS process crash·PG crash·TCP COMMIT 응답 유실 주입은 미실행 |
| 물리 제약·migration history·현재/과거 build 호환성 | migrations1–14, migration_test, migrate_integration_test | 기존13 checksum 보존;14 additive/invalid row rollback·5시각경계; 가상15 실패/재시도/escape 거절; 동시 runner 및 과거 v11 호환 | 폐기 DB에만 적용. 서울/공유 DB schema·migration 적용은 미검증/미수행 |
| revoke4001/replace4002·새 Session 소유권 보존 | connector/registry, connectorwss conn/handler | ACK-before-ready gate, cleanup 중 WriteControl, 최초 reason 단일 전송, 같은 Registration 재진입. 독립 race 반복 로그 | Fake+실제 로컬 TCP/WSS. 실제 Connector/네트워크 단절 E2E 아님 |
| Runtime·DB·HTTP·protocol 정합성 | owning contracts + embedded schema consumers | OpenAPI·JSON Schema8·local refs·Runtime/migration metadata의 실제 Contracts CI Python 프로그램; gofmt/vet/build | Runtime deploy 설정·실승인 Adapter 및 운영 권한은 추가 검증 필요 |
| 기존 DR 판정 유지 | Runtime·운영 문서·기존 Infra 실행기 handoff 경계 | FAIL/NV를 PASS로 덮거나 HOLD를 해제하는 운영 endpoint 없음, source/consumer 재검토 | ReplicaLag 정책 FAIL·필수4 NOT_VERIFIED·HOLD·recovery_authorized=false·cloud_verified=false 유지 |

## 테스트·skip와 재현

정확한 수치·package status·skip 목록은 최종 `validation.json.executions`를 따른다. test/subtest 이벤트 수와 고유 테스트 수를 혼용하지 않는다.

최종 집계: 전체 PostgreSQL integration 5pkg **1629 PASS 이벤트 / FAIL0 / SKIP10**, 최종 WSS 수정 후 App 전체 integration **442 PASS / FAIL0 / SKIP10**. 관련4pkg race **1005 PASS / FAIL0 / SKIP0**, Worker 실제PG race3회 **36 PASS 이벤트 / FAIL0 / SKIP0**. skip은 합성 dump helper 및 Valkey cross-Pod fixture 부재다. 전체 Go 기본 검사는 **3947 PASS / FAIL39 / SKIP15 및 timeout으로 종료 이벤트가 없는1개**이고, Web은 **491/493 PASS·2 EPERM 실패**다. 이 수치를 고유 테스트 개수 또는 실제 DR 검증 개수로 해석하지 않는다.

- 실제 PostgreSQL: localhost 전용 PostgreSQL16.15, port16596, 시험별 `labbit_it_*` DB 생성/삭제. 신규 lifecycle/constraint 시험과 3회 race, 저장소 전체 PostgreSQL·HTTP·Auth·App integration 실행. 단일 실제 DB 결과를 공유/리전 간 운영 검증으로 확대하지 않는다.
- WSS/Registry/WorkerControl/Postgres unit/race 및 독립 reviewer의 WSS100회·최종 Registry20회 반복. WSS 독립 최초 실패와 수정 후 결과를 함께 남긴다.
- Go vet/build, 변경 Go 파일 gofmt, Contracts parser 검증 및 Web typecheck/lint/build 실행. 전체 Go626개는 ignored 복사본의 LF 정규화 후 gofmt PASS다. 원본 중573개는 Windows autocrlf의 CRLF여서 native raw gofmt 목록에 나오며, unrelated source를 일괄 수정하지 않았다.
- 전체 Go/Web 기본 검사도 실행한다. 기존 Windows/POSIX Installer·OpenStack fixture 실패 및 Web installer temp cleanup EPERM을 보존한다. 실패가 남은 기본 검사를 green으로 보고하지 않는다. pristine main 전체를 새로 실행한 결과로 표현하지 않는다.
- Backend Router harness의 Windows compile은 기존 `syscall.Stat_t` 의존으로 실패했다. Linux 전용 경계는 로컬에서 PASS 처리하지 않는다.
- Valkey cross-Pod 시험은 fixture 부재로 skip되며 실제 다중 Pod 인수 대상이다. helper skip도 validation에 별도 기록한다.
- `make`가 없는 Windows에서 동등한 Go/npm 명령을 사용했다. Go modules를 확인하고 lockfile·기존 설치된 Web dependencies를 재사용했다. fresh `npm ci`를 이번에 실행했다고 주장하지 않는다.

재현: 일반 `make test`, 폐기용 PostgreSQL16 DSN으로 `make go-integration-test`를 사용한다. 추가 동시성은 `go test -race -tags integration -run '^TestWorkerControl' -count=3 ./internal/postgres`. WSS 재현은 `go test -race ./internal/server/connectorwss ./internal/server/connector -count=1`. Linux CI에서 test-only Router harness를 별도로 확인한다. 운영 DSN이나 실제 Provider 입력을 사용하지 않는다.

## 실패·보안·수명주기와 두 번째 검토

- DB 오류는 domain error로 감싼다. 원본 DB 오류/자격증명·세션·terminal 본문을 운영 응답에 추가하지 않는다. 시험은 dummy label·UUID·loopback DSN만 사용한다.
- INTENT는 자동 만료/삭제하지 않고, 재접속도 이를 자동 적용하지 않는다. APPLIED receipt는 dispatch 권한이 아니다. 새 binding과 현 authoritative Enter가 계속 필요하다.
- Migration14는 기존 invalid row를 추정 시각으로 고치지 않는다. 공유 DB에 invalid row가 있으면 적용이 실패하며 별도 승인된 정산이 필요하다.
- Registry retire는 callback 완료 대기 채널을 먼저 게시하고 route를 봉인한 뒤 callback을 호출한다. cleanup은 게시된 채널의 완료를 기다린다. callback과 network I/O는 Registry lock 밖에서 수행한다. cleanup은 callback 등록 완료→close-frame 시도 완료→timer 정리→TCP Close 순서다. 기존 peer/grace timeout은 유지하며 frame 시도 성공을 peer 수신 보장으로 과대 해석하지 않는다.
- release 뒤 늦은 close 요청은 새 goroutine/timer/IO를 만들지 않는다. 이미 새로 등록된 Session은 옛 release가 지우지 않는다. callback 재진입은 중복 종료를 기다리지 않는다.
- HTTP/WSS permission·승인 출처·Fencing을 로컬 gate/metric0으로 대체하지 않는다. 실제 Authorizer/Rechecker·모든 Writer/Pod의 binding·서울 데모 연계는 후속이다.
- 구현과 분리된 `/root/review_worker_resume`가 누적 전체 diff·최신 SSOT·SQL/승인/소비자·실행 로그를 재검토했다. `/root/review_admission_local`은 WSS 실패를 독립 재현하고 수정 후 반복 race·재진입을 확인했다. 두 reviewer 모두 파일 수정/실환경 호출 없이 검토했다. reviewer의 local PG 로그 검토와 직접 실DB 실행을 혼동하지 않는다.

- 최종 수명주기 확인: 시험 `labbit_it_*` DB **잔여0**, 직접 띄운 PostgreSQL 서버 종료, loopback16596 listener 없음. 임시 폴더 삭제가 포함된 명령은 자동 승인 검토에서 차단되어 실행되지 않았다(상세 사유 미제공). 삭제 없는 안전한 경로로 서버만 종료했으며 복사한 시험 바이너리·기본 data 폴더는 로컬에 보존한다. 별도 AWS 자원/비용은 생성하지 않았다. 정확한 종료 UTC와 보존 경로는 `validation.json.db.cleanup`을 따른다.

## 판정

요청된 CI fixture 수정·WSS 종료 경쟁 보완과 폐기용 PostgreSQL 검증을 수행한다. 전체 PR은 **보완 필요 / Draft 유지**다. Windows 기본 검사 실패·Linux harness 확인·실제 운영 승인/Fencing/공유 DB/전체 Pod/Provider·DR 연계는 남아 있으며 merge·배포 가능으로 인수하지 않는다. Audit는 branch push·merge·배포·팀 메시지 전송 승인을 생성하지 않는다. 서울 서비스·공유 DB·AWS·Slack 변경은 하지 않았다.
