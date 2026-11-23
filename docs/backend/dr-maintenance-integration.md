# PR194 기존 동작 + PR196 추가 제어 통합 검증

2026-11-20 · LBT-205 / LBT-59 / LBT-60 · 기존 Draft PR196에서 관리한다.

이 문서의 open 전제는 기존 MAINTENANCE 정책 시험 범위다. 11/21 별도 후보의
APPROVED_CONTROL은 HTTP closed를 유지한 승인 Worker 처리 경로를 추가한다.
추가 계약과 제한은 [Runtime 운영 도구](../../runtime/dr-control.md) 및
[Worker 제어](dr-worker-control.md)의 11/21 절을 따른다. AWS 시험 인수로 합산하지 않는다.

## 목적과 판정 경계

PR194의 제품 코드를 다시 구현하지 않는다. main `753eecb`의 maintenance를 재사용한 PR196 후보에서
기존 동작의 회귀와 새 제어를 함께 시험한다. 성공해도 **PR196 후보에서의 PR194 회귀 검증**이며
원래 PR194 서울 배포본 전체를 시험했다는 뜻은 아니다. 새 PR·merge·운영 자동 배포는 이 절차에 없다.

Owning 근거: [LBT-205](https://samsunglions.atlassian.net/browse/LBT-205),
[LBT-59 인계 #11258](https://samsunglions.atlassian.net/browse/LBT-59?focusedCommentId=11258),
[LBT-60](https://samsunglions.atlassian.net/browse/LBT-60), `contracts/http/openapi.yaml`의 Provision/Reset/Cleanup,
`runtime/contract.yaml`의 maintenance/local_pause/durable/approved_resume,
[테스트 전략](https://samsunglions.atlassian.net/wiki/spaces/SL/pages/9666637).
Jira #11261의 리소스 종료 우선 인계와 과거 #196 CI 실패 기록은 당시 이력으로 남긴다.
이번 사용자 지시는 후보의 통합 테스트 준비·실행이며, 운영 서비스 변경·리소스 종료를 자동 승인하지 않는다.

11/20 기존 서울 시험은 2 Pod의 closed·신규 3종 503·신규 Operation/Item 0·open 및 Argo 정책 복귀에 한정한다.
시작 시 미완료 Item 0이어서 drain/backlog/committed replay를 시험하지 않았다.
그 결과와 이번 폐기 DB/MockProvider 결과, 향후 서울 운영 결과를 각각 다른 RUN/Evidence로 보관한다.

## 자동 통합 검증표

| ID | 경계와 기대 결과 | 자동 테스트 / 관측 |
| --- | --- | --- |
| M194-01 | 3종 신규 요청은 closed에서 503 maintenance, 새 작업 없음 | HTTP `TestDRMaintenanceCommittedReplayAndCurrentAuthorityAgainstPostgres` + 기존 `TestMaintenanceHTTPAgainstPostgres`; 실제 PG Operation/Item 전체 row 비교 |
| M194-02 | 3종 committed key는 원래 receipt/Operation ID를 200으로 반환, Location 없음, 재등록 없음 | 같은 HTTP 테스트: 각 2회 replay와 전체 row 비교. Reset/Cleanup 선행 상태는 fixture settle이며 Worker 완료 증거가 아님 |
| M194-03 | 인증·현재 권한은 replay보다 우선: 401/403 유지, 회원 권한 박탈 후 원래 key도 403 | 같은 HTTP 테스트의 비인증·학생·membership 없는 ADMIN·원래 강사 권한 박탈. 다른 의미 409 추가 검사는 Provision만; Reset/Cleanup 충돌은 기존 HTTP/Service 테스트 재사용 |
| M194-04 | 실제 PENDING backlog가 있어도 두 stack 모두 closed 동안 claim 0 | App `TestDRMaintenanceBacklogAndDrainAgainstPostgres`: 3종 × local/durable ACTIVE, 전체 item row·lease·correlation 불변, MockProvider 추가 호출 0 |
| M194-05 | closed 이전 이미 admission된 작업은 WorkerControl ACTIVE에서 RESULT까지 drain | 같은 App 테스트: 실제 Connector/MockProvider 진입에서 채널 대기 → closed → worker count 1 → 다른 claim 0 → RESULT SUCCEEDED 영속·count 0 |
| M194-06 | open positive control 및 복귀 후 잔여 PENDING만 처리 | 같은 backlog를 open에서 실제 전송해 연결/eligibility 확인. 재개 후 정확한 호출 수와 Operation SUCCEEDED로 중복·유실 검사 |
| M196-01 | committed claim 응답이 늦게 도착해도 peer Pause 뒤 prepare/dispatch 금지 | App `TestDRMaintenancePeerPauseAfterCommittedClaimAgainstPostgres`: 실제 PG claim commit → peer PAUSE_NEW → 응답 반환; Provider 0, RUNNING/lease owner 유지, 자동 requeue 없음 |
| M196-02 | boundary 0을 이미 전송된 작업 0으로 해석하지 않음; Pause 뒤 UNKNOWN 결과 수집 유지 | App `TestDRMaintenanceSentUnknownSurvivesPeerPauseAgainstPostgres`: Report.Drained와 별개로 worker count 1 유지 → RECONCILING 영속; file open만으로 재실행 없음 |
| M196-03 | APPLIED만으로 file open/기존 Worker 활성화 없음; 새 binding + 명시적 open 모두 필요 | App `TestDRMaintenanceResumeRequiresNewBindingAndExplicitOpenAgainstPostgres`: 실제 PG journal, 새 production stack·boot, old binding 거부, 새 binding closed claim0/open dispatch 정확히2, receipt replay 비변경 |
| M196-04 | stale·동시 적용·승인 만료/target 불일치·INTENT/UNKNOWN 자동 재시도 금지 | 기존 workercontrol/provision/connector 및 `TestWorkerControlPostgres*` 재사용. fake 승인·주입 응답 유실과 실제 DB 경계를 구분 |

새 App 시험은 별도 Gate 2개·서로 다른 Pod/boot binding의 **같은 Go 프로세스 내 stack 2개**다.
기존 production 조립을 사용하지만 Kubernetes Pod·ConfigMap 전파·Valkey peer/실제 Provider·전체 Writer 검증이 아니다.
durable ACTIVE 초기 row는 폐기 DB fixture이고, 재개 proof도 request에 고정된 합성 Authorizer/Rechecker다.
이 fixture/직접 SQL을 운영 bootstrap·승인 절차로 복사하지 않는다.
HTTP 시험의 settle은 상태 준비에만 사용하고, Worker drain은 실제 Executor+Connector Supervisor+MockProvider 시험에서 따로 입증한다.

### 재현

PostgreSQL 16 폐기 서버에 CREATE DATABASE 권한이 있는 전용 test DSN을 설정한다.
테스트는 `labbit_it_*` DB를 새로 만들고 종료 시 삭제한다. 공유·운영 DSN을 사용하지 않는다.
기존 `make dev-db-up` 또는 `compose.yaml`을 사용하고, 비밀 DSN은 로그·Git에 남기지 않는다.

```sh
# Unix shell 예시. PowerShell에서는 $env:LABBIT_TEST_DATABASE_DSN으로 설정한다.
export LABBIT_TEST_DATABASE_DSN='<disposable PostgreSQL 16 DSN>'
go test -race -tags integration -run '^TestDRMaintenance' -count=3 ./internal/server/app ./internal/server/httpapi
go test -tags integration -count=1 ./internal/server/app ./internal/server/httpapi
go test -race ./internal/server/maintenance ./internal/server/workercontrol ./internal/server/provision ./internal/server/operation ./internal/server/connector
go test -race -tags integration -run '^TestWorkerControl' -count=1 ./internal/postgres
```

기존 `make test`, `make go-integration-test`와 Go CI가 전체 기본 검사를 담당한다.
새 workflow/controller나 운영 시험 endpoint를 추가하지 않는다.
이번 실행·초기 실패·skip·tested source는 `evidence/LBT-60/2026-11-20-pr196-maintenance-regression/`에 기록한다.

## 실제 환경의 한 작업창에서 진행할 순서 — 미실행

### 시작 전 조건

- 실행자·독립 QA, UTC 작업창, account/region/cluster/DB Writer와 전용 시험 조직·Class·Execution·Instance를 고정한다.
  기존 이용자·데모·다른 시험이 진행 중이면 변경을 시작하지 않는다.
- App 후보 commit·불변 image digest, GitOps revision, 전체 Pod UID/boot/role, HPA/rollout과 ConfigMap source를 고정한다.
  PR CI는 이미지를 게시하지 않으므로 후보 build/publish·배포 위치와 명시적 적용 절차를 별도로 준비한다.
- migration12~14의 실제 DDL 차이·backup/restore 책임·이전/이후 App 호환을 확인한다.
  시험하려고 main에 먼저 merge하지 않는다. migration 전후의 복귀 조건을 나누고, 새 테이블을 기계적으로 DROP하지 않는다.
- **Resume 검증 CLI 후보는 구현됐지만 실제 운영 인수는 미완료다.** `cmd/labbit-dr-control`은 signed approval/observation을
  검증하는 opt-in 경로이며 실제 Pause/init 진입점·신뢰 안전 조건 수집자·키 운영은 제공하지 않는다.
  내부 메서드·합성 proof를 운영 진입점으로 간주하지 않는다. #196 live 제어·승인 재개 전에
  인증된 Pause/init 경로, 실제 서명/수집·승인, 단일 control Writer, scope/revision/epoch 초기화와 Pod binding 배포를 준비·검토한다.
- 전체 serving Pod의 read-only directory mount(subPath 금지), 관측 경로, GitOps 변경·유지·복원 방법을 확정한다.
  자동 Sync/rollout을 잠시 멈추면 대상·기간·원래 spec을 기록하고 다른 담당자의 변경 반영 지연을 먼저 알린다.
- ACK 수신·미완료·UNKNOWN·lease·Connector/Provider 자원을 정산한다. 강제 삭제나 자동 재시도로 미해결 기록을 지우지 않는다.
  실제 VM을 사용하면 개수·비용·소유자·정리 방법을 고정하고, Reset/Cleanup은 전용 시험 자원만 변경·삭제한다.
- 원래 image/digest/설정/Argo policy·State/schema·백업, 복귀 담당과 Evidence 보관처·권한·기한을 확정한다.

### 실행·Evidence

1. **기준선:** 현재 #194 배포 상태를 읽기 전용으로 기록한다. 후보 배포는 별도 RUN으로 구분하고 migration Job UID·실행도 별도로 남긴다.
2. **후보 배포 확인:** #196 digest·전체 Pod·DB schema·제어 binding을 확인한다. 누락이 있으면 기능 시험을 시작하지 않는다.
3. **M194 항목:** WorkerControl ACTIVE 상태에서 3종 committed key와 처리 가능한 backlog를 준비한다.
   전용 작업의 진행을 관측한 뒤 closed로 전환하고, 전체 Pod의 새 claim0과 기존 작업 drain을 나눠 확인한다.
   replay200/같은 receipt, 신규503/row0, 401/403 및 해당409를 API·DB·Provider 원본으로 대조한다.
   임의 sleep·DB 상태 수정으로 외부 처리 성공을 대신하지 않는다. 필요한 관측·제어점이 없으면 해당 행은 NOT_VERIFIED다.
4. **M196 항목:** 별도 fixture/RUN에서 claim 이후 dispatch 전과 이미 전송한 상태를 구분해 trusted Pause를 시험한다.
   승인된 관측·정지점이 없으면 그 경쟁 조건을 live에서 재현했다고 하지 않는다.
   revision/epoch·Report·미완료/UNKNOWN·외부 command ID를 수집하고, 전체 작업0을 짧은 boundary0으로 판정하지 않는다.
5. **재개:** 기존 Writer 격리·전체 Pod coverage·기존 작업 정산을 실제 확인하고 새 승인·새 RUN으로 재점검한다.
   INTENT/APPLIED, 새 binding 배포, 명시적 file open을 나눠 관측한다. UNKNOWN이면 자동 재실행·자동 open하지 않는다.
6. **복귀·정리:** 시험 자원의 공식 Cleanup·Provider 잔존, 원래 허용된 설정·Argo policy·전체 Pod 상태를 다시 확인한다.
   미해결 작업·소유자 불명 자원은 Evidence와 함께 HOLD하고 공유 자원을 삭제하지 않는다.
7. **독립 QA:** M194/M196 항목별 PASS/FAIL/NOT_VERIFIED와 후보 소스·실행 환경·미실행 범위를 기록한다.
   시험·QA 후 merge와 정식 배포는 별도 작업이다. 시험 성공만으로 자동 merge하지 않는다.

Evidence에는 trial/RUN/단계, source SHA/image digest/GitOps revision, 관측 UTC, Pod UID/boot, control scope/revision/epoch,
Operation/Item/command의 비민감 ID, API status/code, 실제 DB 전후 상태, Provider 관측, 원본 참조·SHA256과 복귀 확인을 연결한다.
Cookie·인증정보·DSN·Provider 원문·Terminal 내용은 저장하지 않는다.

### 중단 조건

대상·권한·Revision·Pod coverage 불일치, 예상 밖 신규 접수·claim/dispatch, drain timeout, Provider 결과 불명,
DB/schema 이상, 이용자 출현, image/Argo drift가 발생하면 다음 조작을 멈춘다.
미해결 mutation이 있을 때 finally open이나 무조건 rollback하지 않는다. 실제 상태를 재확인한 뒤 승인된 복귀만 수행한다.
새 Pod/role이 나타나면 전체 Pod barrier를 다시 확인한다. 시작 전부터 조건이 맞지 않으면 후보 배포를 보류한다.

## 현재 판정

자동 시험은 폐기 PG·loopback·MockProvider 범위다. **통합 운영 시험은 미실행이며 준비 조건이 남아 있다.**
기존 ReplicaLag FAIL, REQUIRED_SETTINGS/RUNTIME_PROVENANCE/SERVICE_FUNCTIONS/SINGLE_WRITER의 4 NOT_VERIFIED,
execution_gate=HOLD, recovery_authorized=false, cloud_verified=false를 유지한다.
합성 시험 PASS로 이 상태를 해제하지 않는다. Fencing·DB 승격·DNS·오사카 활성화·Failover/Failback은 이 기능 시험 범위 밖이다.
