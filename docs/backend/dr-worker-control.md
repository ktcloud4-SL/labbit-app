# DR Worker claim/dispatch 제어 — 로컬 및 영속 경계 후보

2026-11-20 · LBT-205 / LBT-59 / LBT-60 · 미배포·오프라인 구현

근거는 [LBT-205](https://samsunglions.atlassian.net/browse/LBT-205),
[LBT-59 PM B안 결정 #11242](https://samsunglions.atlassian.net/browse/LBT-59?focusedCommentId=11242),
[Infra PR57 D02–D05](https://github.com/ktcloud4-SL/labbit-infra/blob/5987cb061092d406ba8effaf3259d56e260246b1/runbooks/operations/aws-dr-backend-controls-proposal.md)다.
Runtime의 owning 설정/의미는 `runtime/contract.yaml`의 `LABBIT_WORKER_CONTROL_MODE`, `saas.worker.local_pause`를 따른다.
이 문서는 연결·검증·미구현 경계를 설명하며 LBT-205 전체 인수나 실제 DR GO를 선언하지 않는다.

## 구현 연결

`LoadConfig` → `Config.WorkerControlMode` → `Run` → `newControlStack`에서 **하나의 Control** 생성 →
`provision.Executor`와 `connector.Router`가 공유한다. PR194 maintenance의 API 접수 차단과 함께 조립하지만 별도의 typed 제어다.

| 시작 모드 | 신규 mutation claim/dispatch | 새 Reconciliation claim/query |
| --- | --- | --- |
| ACTIVE | 기존 동작 | 기존 동작 |
| PAUSE_NEW | 차단 | 차단 |
| RECONCILE_ONLY | 차단, PENDING mutation queue를 먼저 읽지 않음 | 기존 exact-ID/generation 정산 경로만 허용 |

미설정/빈 값은 기존 호환성을 위한 ACTIVE, 잘못된 값은 초기화 전 고정 오류다. zero/nil Control 자체는 차단하며
명시적으로 Control을 주지 않는 기존 Executor/Router constructor는 호환 ACTIVE를 생성한다.
Production은 항상 명시적인 공유 Control을 전달한다. Reconciliation Store/Router 미지원 구성은 정산 모드를 성공으로 가장하지 않는다.
Worker 역할이 없어도 api Router의 직접 Operation/Reconcile 호출에 모드가 적용된다.

기본 local backend의 `Executor.Pause(ctx)`는 프로세스의 **단방향 제한**이다. 현재 프로세스에서 ACTIVE로 재개하거나 RECONCILE_ONLY로
전환하는 setter/HTTP endpoint는 없다. Control의 짧은 permit은 claim과 prepare/send를 보호하며 DB transaction을
외부 I/O까지 유지하는 것이 아니다. 기존 DB adapter의 transaction/lease fencing을 그대로 사용한다.

1. Pause는 새 permit을 먼저 닫고 이미 진입한 경계 drain을 기다린다. nested acquire는 대기하지 않고 거절한다.
2. claim commit이 이미 끝났어도 응답이 지연되는 동안 Pause는 drain 완료로 표시하지 않는다.
3. claim 이후 및 prepare/lifecycle 처리 이후 전송 전 재검사한다. 미전송 claim을 자동 PENDING/FAILED로 정리하지 않는다.
4. 소유권 상실/명시적 미전송 오류의 기존 재등록도 새 permit이 필요하다. Pause 전에 들어간 DB 처리는 끝날 수 있다.
5. Send 반환 뒤 permit을 반납하고 ACK/RESULT를 기다린다. Pause가 Provider 실행을 취소하거나 pending 결과를 버리지 않는다.
6. 전송 전 차단이 확실한 bare gate 오류(ErrPaused/ErrStale/ErrUnavailable/ErrHold)만 차단으로 반환한다. 감싸진 전송 실패는 기존 UNKNOWN·즉시 결과 수집을 보존한다.

`ControlState()`의 InFlight는 짧은 로컬 경계 수이며 실행 중 Provider 수가 아니다. Quiesced는 PAUSE_NEW와
로컬 경계 drain을 뜻한다. 승인 상태/PASS/전체 Writer 차단 수치로 변환하지 않는다. timeout/cancel 뒤 latch는 남지만
Pause 성공을 주장할 수 없다. 반복 Pause로 drain을 재확인할 수 있고 자동 재개는 없다.

## 반드시 남는 경계

- RECONCILE_ONLY의 Provider 조회와 달리 DB claim/renew/result는 쓰기다. 승인된 Writer·정확한 계보를 먼저 확인해야 한다.
  이 구현은 Single Writer/승격/작업 승인 검증기를 제공하지 않는다. standby replica나 Writer=NONE 단계에 그대로 연결하면 안 된다.
- 기본 local backend의 pause는 메모리 안에만 있고 재시작 입력이 ACTIVE이면 다시 열린다.
  아래 postgres opt-in은 같은 제어 Writer의 revision/epoch와 durable boundary를 확인하지만 아래 승인 재개 후보와 구분한다. 실제 인증/검증 Adapter·배포 연동은 미구현이다.
- 이미 수락된 Provider 작업과 DB lease/결과 쓰기는 계속될 수 있다. DB fencing은 해당 작업 정산과 별도 순서가 필요하다.
- API 신규 접수는 maintenance 파일 경계다. Auth/Terminal/Live/File/Preview/migration/Job의 쓰기도 이 제어 범위 밖이다.
- 환경/배포/GitOps·서울 서비스·AWS·Slack·외부 Connector를 변경/호출하지 않았다. 사용자 승인 범위는 작업 브랜치 push와 별도 Draft PR까지이며 merge·배포는 하지 않는다.
- 기존 ReplicaLag FAIL, REQUIRED_SETTINGS/RUNTIME_PROVENANCE/SERVICE_FUNCTIONS/SINGLE_WRITER NOT_VERIFIED,
  execution_gate=HOLD, recovery_authorized=false, cloud_verified=false를 유지한다.

## 단위 검증

```text
go test -race ./internal/server/workercontrol ./internal/server/provision ./internal/server/connector ./internal/server/operation ./internal/server/labexecution ./internal/server/httpapi ./internal/server/app -count=1
```

Fake Repository·Router와 in-process route만 사용한다. mode/잘못된 값, zero/nil fail-closed, 병렬 Pause/drain,
cancel 뒤 latch 유지, claim 응답 지연, prepare/lifecycle 경쟁, 직접 Router 우회 차단, 이미 전송한 결과·UNKNOWN 보존,
복합 전송 오류, 정산 전용 mutation queue 우회, production의 공유 gate를 검증한다.
실제 PostgreSQL transaction은 폐기용 DB에서 검증한다. 다중 Kubernetes Pod·AWS·Provider/Browser E2E는 별도 미검증이다.

## 11/20 영속 제어 연결 — opt-in 후보

운영 설정 원본은 Runtime 0.1.12의 `LABBIT_WORKER_CONTROL_BACKEND` 및 `saas.worker.durable_boundaries`,
물리 스키마 원본은 `db/migrations/000012_worker_control.sql`이다. 공용/서울 DB에는 적용하지 않았다. 폐기용 PostgreSQL 16의 실제 적용·경쟁·재접속 검증은 `evidence/LBT-60/2026-11-20-pr196-validation/`에 기록한다.
기존 admission·로컬 제어 audit는 당시 소스의 역사적 기록이고, 이번 tested source는 별도 11/20 audit의 inventory로 식별한다.

`LoadConfig`에서 postgres backend와 전체 Scope/Revision/Epoch/PodID를 검증한 뒤,
`Run`은 기존 DB pool의 `postgres.WorkerControlStore`를 `newControlStack`에 전달한다.
stack은 시작 때 새로운 boot UUID를 만들고 **동일한 Control을 Executor와 Router에 공유**한다.
각 Pod는 자기 immutable binding을 가지며 authoritative state의 최신 revision을 자동으로 따라가지 않는다.
부분 설정·누락 Store를 local ACTIVE로 바꾸지 않는다. 상태 row가 없거나 target/mode가 다르면 새 작업은 거절된다.

| 내부 입력/출력 | 의미 |
| --- | --- |
| Scope: ID/Region/Cluster | 명시적 대상 binding; 실제 AWS 신원/배포 coverage의 증거 아님 |
| Binding: Scope/Revision/Epoch/Mode/PodID/BootID | 시작 때 고정. Pod 이름이 같아도 새 process는 새 BootID |
| Ticket: ID/Binding/Kind/Phase | claim/prepare/dispatch/requeue 또는 reconciliation 단계의 영속 open intent |
| Enter → error 또는 허용 | state row를 잠그고 exact binding/mode 확인 후 intent COMMIT. 허용 후에만 작업 시작 |
| Leave(Ticket) | 현재 state revision이 달라져도 당시 exact ticket만 멱등 종료 |
| Pause(Binding, RequestID) | CAS로 revision+1, PAUSE_NEW만 가능. 동일 요청의 멱등 재확인, 다른 epoch/target 재사용 거부 |
| Report → State/OpenBoundaries/PendingResumes/ObservedAt | 한 statement snapshot에서 모든 revision/boot의 열린 경계와 pending resume INTENT를 조회. cached PASS 없음 |

`Executor.Pause`의 postgres 경로는 로컬 latch → 영속 Pause CAS → 로컬 drain → 새 Report 순서다.
다른 Pod의 open intent가 남으면 `ErrHold`, 조회/CAS 실패는 `ErrUnavailable`로 반환한다.
호출자가 반복 Pause로 새 Report를 확인할 수 있지만 승인 상태를 바꾸거나 자동 재개하지 않는다.
Enter/Pause/Report의 DB 작업 제한은 2초, Leave는 취소된 원래 context와 분리한 5초다.
이 timeout은 외부 작업 취소/안전한 재실행 허가가 아니다.

Pause 이전에 진입한 경계는 끝날 수 있으므로 **Pause 요청 접수 시점에 전송0건을 보장하지 않는다**.
최종 Router guard도 새 intent가 필요하며 peer가 먼저 Pause를 commit하면 오래된 binding으로 진입할 수 없다.
그 전에 Router 경계에 들어간 전송은 완료 가능하고, intent가 닫히기 전 전체 drain은 HOLD다.
이미 전송한 Provider 실행과 ACK/RESULT·lease/결과 DB 쓰기는 계속된다.

응답 유실·재시작 규칙:

- Enter commit 응답이 불명이면 작업을 시작하지 않고 자동 Leave·새 ticket 재시도를 하지 않는다. 남을 수 있는 intent를 보존한다.
- Leave 실패/불명은 프로세스의 미검증 상태를 latch한다. 이후 조회 연결이 복구돼도 새 작업을 자동 허용하지 않는다.
- intent는 시간 경과·Pod 소멸·새 boot/epoch를 이유로 만료/삭제하지 않는다. 승인된 후속 조사·정산이 필요하다.
- 정확한 기존 ticket의 멱등 종료는 가능하지만 외부 작업 재실행과 구분한다. 다른 boot/epoch/phase로 타 ticket을 종료할 수 없다.
- CreatePaused는 create-only PAUSE_NEW/revision1/epoch1이며 기존 scope/target을 초기화하지 않는다.
- 내부 승인 재개 후보는 아래 절과 같다. 실제 인증/검증 Adapter와 배포 연결은 남아 있으며 완성된 운영 제어 API가 아니다.

## 검증·운영 적용 한계

이번 여러 Pod 시험은 **독립 Control/Executor/Router 인스턴스 + 공유 Fake journal**이다.
PostgreSQL adapter는 scripted driver Mock으로 commit/rollback/정확 ticket/원본 오류 비노출을 시험한다.
실제 PostgreSQL row lock은 아래 폐기용 DB 시험으로 검증하며, DB crash/TCP 장애 내구성과 Kubernetes Pod 배포·통신 단절은 검증하지 않았다.
`TestWorkerControlPostgresCreatePauseAndConcurrentEnter` 등 integration-tag 시험은 폐기용 PostgreSQL 16에서 실행한다. Fake와 실제 DB 결과는 최신 Evidence에서 구분한다.

기존 `make go-integration-test`로 migration·constraint·병렬 Enter/Pause를 재현할 수 있다. 실제 승인된 제어 DB·다중 Kubernetes Pod 및 DB crash/TCP commit-response-loss fault injection은 별도 미검증이다.
미적용 migration도 새 build의 schema readiness 기준에 포함되므로 실제 배포 전 적용/호환성을 별도로 확인해야 한다.
**기본 비활성이어도 새 build를 바로 서울에 배포해도 된다는 의미가 아니다.** 작업 브랜치 push·Draft PR만 사용자 승인 범위이며 merge/배포·DB 적용은 수행하지 않는다.

같은 authoritative DB와 scope를 쓰는 새 image/opt-in 참여자에 한정한다. 다른 scope·구 image·local 설정·직접 DB/Provider writer와
서울/오사카 각각의 비동기 DB fork는 포함하지 않는다. DB 연결 자체가 올바른 Writer인지도 이 label/epoch가 증명하지 않는다.
따라서 실제 DR 제어 저장소의 접근 경로·권한 분리·승격 후 계보/유실 intent 방지·전체 배포 coverage는 별도 선행 Gate다.
API 접수는 PR194 maintenance로 통일했다. 파일 변경과 영속 Worker pause를 원자적으로 갱신하는 것은 아니다.

기존 ReplicaLag FAIL·필수4개 NOT_VERIFIED·execution_gate=HOLD·recovery_authorized=false·cloud_verified=false를 유지한다.


## 승인 후 재개 — 오프라인 내부 구현

현재 owning 의미는 Runtime `approved_resume`, 물리 schema는 `000013_worker_control_resume.sql`과 APPLIED 시각을 보강하는 additive `000014_worker_control_resume_applied_time.sql`이다.
[11/20 LBT-205 #11251](https://samsunglions.atlassian.net/browse/LBT-205?focusedCommentId=11251) 및
[App PR194](https://github.com/ktcloud4-SL/labbit-app/pull/194) HEAD34ed442의 접수·일반 Worker gate를
#194 squash main `753eecbff737409a4c9cc8c542f3c6baa6acf0da` 기준으로 중복 없이 연결한다. `dr-mutation-admission.md`와 Runtime0.1.12를 따른다.
기존 startup admission 후보는 제거했고 HTTP code는 maintenance로 통일했다.
Run은 하나의 maintenance Gate를 Service/Executor/metrics에 공유하고 WorkerControl은 Executor/Router에 공유한다.

### 닫기와 재개

1. 플랫폼이 파일 closed를 적용하고 Pod별 http/worker count로 기수락 작업을 drain한다.
2. 별도 WorkerControl Pause가 이후 prepare/dispatch/requeue를 막는다. 중간에 멈춘 claim과 외부 UNKNOWN은 보존한다.
3. 승인된 Writer에서 RECONCILE_ONLY가 필요하면 새 승인·epoch로 전이한다. maintenance 일괄 claim gate 대신
   kind=reconciliation으로 query의 claim부터 결과 기록까지 관측하고, typed durable gate가 모든 권한을 검사한다.
   local backend 또는 관측 port 누락으로 이 경로를 사용하면 구성 오류다.
4. NewMaintenanceResumer는 configured exact closed와 모든 local count0을 진입/Begin/Apply에 추가한다.
   외부 승인·Writer·Fencing·PodCoverage와 WorkInventory/WorkReconciled 검증을 대체하지 않는다.
5. APPLIED는 영속 제어 상태의 전이 기록이다. 앱은 파일을 열거나 Pod를 변경하지 않는다.
   새 binding의 승인 배포와 운영자 파일 개방은 별도로 진행·관측해야 하며 old Control은 자동으로 열리지 않는다.

파일 snapshot과 journal commit은 원자적이지 않다. 외부 변경 통제·전체 Pod coverage가 계속 필요하다.
metric0이나 짧은 boundary0만으로 Provider 작업0·모든 DB 쓰기0·전체 DR GO를 선언하지 않는다.

### 실제 연계 전 남은 사항

실제 Authorizer/Rechecker, 승인된 제어 DB·migration/locking, GitOps 새 binding 전달과 전체 Pod 재검증,
모드 개방 권한·전파·Failback old/new writer 확인이 남았다. startup 자동 재개·공개 제어 endpoint는 없다.
GitOps126 merge 이후 desired state는 서울 open/오사카 closed다. 이 작업에서는 GitOps·AWS·공유 DB·서울 서비스·Slack을 변경하지 않는다. PostgreSQL integration은 loopback의 폐기용 DB만 사용한다.
사용자 승인 범위는 작업 브랜치 push와 별도 Draft PR까지이며 main merge/배포는 금지한다.
최신 main 검증과 기존 감사 이력 구분은 `evidence/LBT-60/2026-11-20-main-refresh/`를 따른다.
기존 ReplicaLag FAIL·네 NOT_VERIFIED·execution_gate=HOLD·recovery_authorized=false·cloud_verified=false를 유지한다.
