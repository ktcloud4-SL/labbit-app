# DR 접수 차단 — PR194 maintenance 통합

2026-11-20 · LBT-205 / LBT-59/60 · 최신 main 기반 검토 후보 · 실제 환경 미변경

## 원본과 중복 정리

[App PR194](https://github.com/ktcloud4-SL/labbit-app/pull/194) HEAD
`34ed442538d45bc3ce9d47f73a3bef0584cf6dc6`의 maintenance Gate, service.enter,
일반 operation.Worker admission 및 기존 테스트를 재사용했다. #194는 squash merge되었으며 현재 기준 main은
`753eecbff737409a4c9cc8c542f3c6baa6acf0da`다. 기존 접수·HTTP mapping·일반 Worker 코드는 main 그대로 두고
영속 Worker 제어·Reconciliation 관측·승인 재개 연결을 추가한다. Runtime/HTTP owning SSOT는
`runtime/contract.yaml`0.1.12 / `contracts/http/openapi.yaml`이다.
기존 미배포 startup BLOCK_NEW/OPEN 후보·NewServiceWithAdmission·ErrAdmissionBlocked를 제거했다.
LABBIT_MUTATION_ADMISSION_MODE가 비어 있지 않으면 LoadConfig가 거절하므로 이전 차단 설정이 조용히 OPEN으로 바뀌지 않는다.
실환경 환경변수/파일·GitOps는 변경하지 않았다.

## 서비스 경계

LoadConfig.MaintenanceFile → Run의 단일 maintenance.Gate/metric 등록 → stackOptions.Maintenance →
labexecution.NewService(store, gate.Enter) 및 provision.Options.Admission.

Provision·Reset·Cleanup은 형식·현재 권한·committed replay 뒤 새 ID/transaction 전에 gate.Enter(http)를 호출한다.
설정한 파일이 trim 후 open일 때만 새 등록을 허용하고 closed/누락/읽기 오류/잘못된 내용은 503 maintenance다.
파일 경로 자체가 미설정이면 기존 호환 동작이다. 모든 Pod에 경로를 적용하는 운영 확인은 별도다.
기존 replay 200·권한 거부·다른 의미의 key 충돌·조회 오류·기존 UNKNOWN은 유지한다.
main의 PR194 HTTP integration source를 보존했으며 이 작업에서는 compile-only로 확인한다. 실제 PostgreSQL 시험은 실행하지 않았다.

## Worker/재개 연결

일반 Worker는 전체 RunOne을 maintenance worker count로 추적하며 closed 이후 새 claim을 하지 않는다.
이미 admission한 실행은 WorkerControl ACTIVE이면 끝까지 진행할 수 있다. 이는 graceful drain 단계다.
별도의 영속 WorkerControl Pause는 다음 prepare/dispatch 등을 막는다. 이미 claim한 작업은 보존하고 자동 재등록하지 않는다.
최종 Router send에는 같은 WorkerControl이 적용된다.

RECONCILE_ONLY는 durable binding일 때만 일반 maintenance claim 차단 대신 별도 wholeRunOne reconciliation counter를 사용한다.
이 counter는 관측용이며 query 권한은 authoritative typed Enter가 매 경계에서 확인한다.
API 신규 접수와 mutation은 여전히 차단된다. 결과 기록도 DB 쓰기이므로 승인된 Writer에서만 사용해야 한다.

NewMaintenanceResumer는 configured exact closed 및 모든 local count0을 기존 승인·RECHECK·INTENT/APPLIED 흐름에 추가한다.
외부 Authorizer/Rechecker는 계속 필수다. 실제 승인/Writer/전체 Pod 검증 port는 아직 미구현이다.
Receipt 성공도 파일을 열지 않고 기존 Worker를 재개하지 않는다. 승인된 새 revision/epoch의 Worker 구성과
운영자의 파일 개방·실제 전체 Pod 확인이 후속으로 필요하다. 파일과 DB를 원자 갱신하는 제어기는 아니다.

## 모의 검증과 제한

service/HTTP in-process Fake, maintenance 임시파일, 공유 journal Fake 및 scripted PostgreSQL driver를 사용한다.
신규 무등록·권한/replay·동시 차단, 승인 이후에도 파일 closed면 claim0, 파일 open이어도 old binding 거부,
두 Worker의 동일 item 전송1건, query-only의 mutation0·결과 기록 관측, INTENT 전후 파일/진행상태 변화 HOLD를 검사한다.

readiness/auth/Terminal/Live/File/Preview/기수락 결과 수집을 maintenance로 끄지 않는다.
이 제어는 DB 세션·Jobs·모든 쓰기 경로를 fencing하지 않으며 기존 ReplicaLag FAIL·4 NOT_VERIFIED·DR HOLD를 유지한다.
실제 DB/AWS/서울/Slack/배포는 수행하지 않는다. 사용자 승인에 따라 작업 브랜치 push와 별도 Draft PR만 허용하며 merge하지 않는다.

GitOps #124→#125→#126 이후 main `ab830d7c9e046b0621477192aca39c6b269cbf30`은 서울 open/오사카 closed를 선언한다.
이는 소스 확인이며 이 작업에서 live 상태·설정을 조회하거나 변경한 것은 아니다.
App main merge는 image 게시와 서울 GitOps tag/PreSync migration 갱신·Argo auto-sync로 이어지므로 이 Draft는 merge 금지다.
기존 감사 Evidence는 각 당시 base/소스에 대한 이력이며 최신 main 재검증 결과는 `evidence/LBT-60/2026-11-20-main-refresh/`를 따른다.
운영 적용 전 migration12/13·실 SQL 동시성·실 승인·전체 Pod·별도 GitOps 계획을 재검증해야 한다.
