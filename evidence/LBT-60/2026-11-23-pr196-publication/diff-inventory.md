# Full product diff inventory

Base main `753eecbff737409a4c9cc8c542f3c6baa6acf0da` → prior PR HEAD `c11b70a8e9fb81691e0fadb6b4114ee9a5f47fac` + local supplement.

This list includes all non-Evidence changed files; exact hashes are in publication-source.json. Historical Evidence is retained under the original paths; new Evidence is covered by artifact-manifest.json. No source files excluded from the assigned primary/independent review boundaries. GitOps/Infra sibling worktrees are outside this App PR.

| File | Purpose | Reviewer coverage |
| --- | --- | --- |
| `Makefile` | implementation/config: 실제 폐기 PG 통합 target에 신규 drcontrol 검증 package 포함; TESTING | worker + primary |
| `cmd/labbit-dr-control/main.go` | implementation/config: exact operator 신원·승인/관측 서명·고정 원본·DB target 검증 및 preview/재개 후보; LBT60, Runtime | operator + primary |
| `cmd/labbit-dr-control/main_test.go` | test: exact operator 신원·승인/관측 서명·고정 원본·DB target 검증 및 preview/재개 후보; LBT60, Runtime | operator + primary |
| `contracts/http/openapi.yaml` | contract: 503 maintenance와 committed replay/권한 우선순위 공개 계약; HTTP SSOT | worker + primary |
| `db/migrations/000012_worker_control.sql` | schema: 기존 additive12~14 durable Scope/경계/Resume journal·APPLIED 시간 제약 및 schema 이력; DB SSOT | worker + primary |
| `db/migrations/000013_worker_control_resume.sql` | schema: 기존 additive12~14 durable Scope/경계/Resume journal·APPLIED 시간 제약 및 schema 이력; DB SSOT | worker + primary |
| `db/migrations/000014_worker_control_resume_applied_time.sql` | schema: 기존 additive12~14 durable Scope/경계/Resume journal·APPLIED 시간 제약 및 schema 이력; DB SSOT | worker + primary |
| `db/migrations/README.md` | schema: 기존 additive12~14 durable Scope/경계/Resume journal·APPLIED 시간 제약 및 schema 이력; DB SSOT | worker + primary |
| `docs/backend/dr-maintenance-integration.md` | documentation: LBT59/60/205 시험/배포·백업/복구·구현과 운영 미검증/인수 경계 문서화 | worker + primary |
| `docs/backend/dr-mutation-admission.md` | documentation: LBT59/60/205 시험/배포·백업/복구·구현과 운영 미검증/인수 경계 문서화 | worker + primary |
| `docs/backend/dr-stage3-deployment.md` | documentation: LBT59/60/205 시험/배포·백업/복구·구현과 운영 미검증/인수 경계 문서화 | worker + primary |
| `docs/backend/dr-worker-control.md` | documentation: LBT59/60/205 시험/배포·백업/복구·구현과 운영 미검증/인수 경계 문서화 | worker + primary |
| `internal/connector/installer/installer_test.go` | test: test fixture 플랫폼 경계·권한 fail-closed·Linux positive 회귀; TESTING, Connector SSOT | regression + primary |
| `internal/connector/provider/openstack/active_readiness_test.go` | test: test fixture 플랫폼 경계·권한 fail-closed·Linux positive 회귀; TESTING, Connector SSOT | regression + primary |
| `internal/connector/provider/openstack/ktcloud_d1_cidr_test.go` | test: test fixture 플랫폼 경계·권한 fail-closed·Linux positive 회귀; TESTING, Connector SSOT | regression + primary |
| `internal/connector/provider/openstack/ktcloud_management_route_test.go` | test: test fixture 플랫폼 경계·권한 fail-closed·Linux positive 회귀; TESTING, Connector SSOT | regression + primary |
| `internal/connector/provider/openstack/readiness_diagnostic_test.go` | test: test fixture 플랫폼 경계·권한 fail-closed·Linux positive 회귀; TESTING, Connector SSOT | regression + primary |
| `internal/connector/provider/openstack/readiness_test.go` | test: test fixture 플랫폼 경계·권한 fail-closed·Linux positive 회귀; TESTING, Connector SSOT | regression + primary |
| `internal/drcontrol/awscli.go` | implementation/config: exact operator 신원·승인/관측 서명·고정 원본·DB target 검증 및 preview/재개 후보; LBT60, Runtime | operator + primary |
| `internal/drcontrol/awscli_test.go` | test: exact operator 신원·승인/관측 서명·고정 원본·DB target 검증 및 preview/재개 후보; LBT60, Runtime | operator + primary |
| `internal/drcontrol/contract.go` | implementation/config: exact operator 신원·승인/관측 서명·고정 원본·DB target 검증 및 preview/재개 후보; LBT60, Runtime | operator + primary |
| `internal/drcontrol/database.go` | implementation/config: exact operator 신원·승인/관측 서명·고정 원본·DB target 검증 및 preview/재개 후보; LBT60, Runtime | operator + primary |
| `internal/drcontrol/database_integration_test.go` | test: exact operator 신원·승인/관측 서명·고정 원본·DB target 검증 및 preview/재개 후보; LBT60, Runtime | operator + primary |
| `internal/drcontrol/verified.go` | implementation/config: exact operator 신원·승인/관측 서명·고정 원본·DB target 검증 및 preview/재개 후보; LBT60, Runtime | operator + primary |
| `internal/drcontrol/verified_test.go` | test: exact operator 신원·승인/관측 서명·고정 원본·DB target 검증 및 preview/재개 후보; LBT60, Runtime | operator + primary |
| `internal/postgres/migrate_integration_test.go` | test: 이전버전 fixture·새 migration/checksum·rollbackless 이력 검증; DB SSOT | worker + primary |
| `internal/postgres/migration_test.go` | test: 이전버전 fixture·새 migration/checksum·rollbackless 이력 검증; DB SSOT | worker + primary |
| `internal/postgres/worker_control.go` | implementation/config: authoritative CAS·경계 ticket·INTENT/APPLIED 이력 영속화와 경쟁/응답유실 회귀; LBT60 | worker + primary |
| `internal/postgres/worker_control_integration_test.go` | test: authoritative CAS·경계 ticket·INTENT/APPLIED 이력 영속화와 경쟁/응답유실 회귀; LBT60 | worker + primary |
| `internal/postgres/worker_control_lifecycle_integration_test.go` | test: authoritative CAS·경계 ticket·INTENT/APPLIED 이력 영속화와 경쟁/응답유실 회귀; LBT60 | worker + primary |
| `internal/postgres/worker_control_resume.go` | implementation/config: authoritative CAS·경계 ticket·INTENT/APPLIED 이력 영속화와 경쟁/응답유실 회귀; LBT60 | worker + primary |
| `internal/postgres/worker_control_resume_test.go` | test: authoritative CAS·경계 ticket·INTENT/APPLIED 이력 영속화와 경쟁/응답유실 회귀; LBT60 | worker + primary |
| `internal/postgres/worker_control_test.go` | test: authoritative CAS·경계 ticket·INTENT/APPLIED 이력 영속화와 경쟁/응답유실 회귀; LBT60 | worker + primary |
| `internal/server/app/admission_test.go` | test: production 조립·설정 거부·default194/opt-in196 경계 및 실제 PG 두 stack 통합/metrics; LBT205/60 | worker + primary |
| `internal/server/app/app.go` | implementation/config: production 조립·설정 거부·default194/opt-in196 경계 및 실제 PG 두 stack 통합/metrics; LBT205/60 | worker + primary |
| `internal/server/app/control_stack.go` | implementation/config: production 조립·설정 거부·default194/opt-in196 경계 및 실제 PG 두 stack 통합/metrics; LBT205/60 | worker + primary |
| `internal/server/app/dr_maintenance_integration_test.go` | test: production 조립·설정 거부·default194/opt-in196 경계 및 실제 PG 두 stack 통합/metrics; LBT205/60 | worker + primary |
| `internal/server/app/worker_control_test.go` | test: production 조립·설정 거부·default194/opt-in196 경계 및 실제 PG 두 stack 통합/metrics; LBT205/60 | worker + primary |
| `internal/server/app/worker_metrics_integration_test.go` | test: production 조립·설정 거부·default194/opt-in196 경계 및 실제 PG 두 stack 통합/metrics; LBT205/60 | worker + primary |
| `internal/server/connector/registry.go` | implementation/config: ACK 후 revoke/replace Close notification과 TCP cleanup 경쟁·세션 소유권 보존; Connector/Realtime | regression + primary |
| `internal/server/connector/registry_close_notification_test.go` | test: ACK 후 revoke/replace Close notification과 TCP cleanup 경쟁·세션 소유권 보존; Connector/Realtime | regression + primary |
| `internal/server/connector/router.go` | implementation/config: claim 이후 typed dispatch 최종 제어·query 구분·peer Pause와 기존 Route 계약; LBT205/60 | worker + primary |
| `internal/server/connector/worker_control_test.go` | test: claim 이후 typed dispatch 최종 제어·query 구분·peer Pause와 기존 Route 계약; LBT205/60 | worker + primary |
| `internal/server/connectorwss/close_cleanup_test.go` | test: ACK 후 revoke/replace Close notification과 TCP cleanup 경쟁·세션 소유권 보존; Connector/Realtime | regression + primary |
| `internal/server/connectorwss/conn.go` | implementation/config: ACK 후 revoke/replace Close notification과 TCP cleanup 경쟁·세션 소유권 보존; Connector/Realtime | regression + primary |
| `internal/server/connectorwss/handler.go` | implementation/config: ACK 후 revoke/replace Close notification과 TCP cleanup 경쟁·세션 소유권 보존; Connector/Realtime | regression + primary |
| `internal/server/httpapi/admission_test.go` | test: 기존194 인증·권한·503·committed replay·3종 접수 경계의 회귀; HTTP SSOT | worker + primary |
| `internal/server/httpapi/dr_maintenance_integration_test.go` | test: 기존194 인증·권한·503·committed replay·3종 접수 경계의 회귀; HTTP SSOT | worker + primary |
| `internal/server/labexecution/admission_test.go` | test: 기존194 인증·권한·503·committed replay·3종 접수 경계의 회귀; HTTP SSOT | worker + primary |
| `internal/server/maintenance/controlled_worker_test.go` | test: HTTP closed·기수락 drain 기본 경계와 opt-in Worker durable authority 분리; LBT205/Runtime | worker + primary |
| `internal/server/maintenance/gate.go` | implementation/config: HTTP closed·기수락 drain 기본 경계와 opt-in Worker durable authority 분리; LBT205/Runtime | worker + primary |
| `internal/server/maintenance/gate_test.go` | test: HTTP closed·기수락 drain 기본 경계와 opt-in Worker durable authority 분리; LBT205/Runtime | worker + primary |
| `internal/server/provision/controlled_maintenance_test.go` | test: claim/prepare/requeue/reconciliation 전체 제어 및 승인 후 backlog 처리·UNKNOWN 보존; LBT205 | worker + primary |
| `internal/server/provision/executor.go` | implementation/config: claim/prepare/requeue/reconciliation 전체 제어 및 승인 후 backlog 처리·UNKNOWN 보존; LBT205 | worker + primary |
| `internal/server/provision/maintenance_resume_test.go` | test: claim/prepare/requeue/reconciliation 전체 제어 및 승인 후 backlog 처리·UNKNOWN 보존; LBT205 | worker + primary |
| `internal/server/provision/reconciliation.go` | implementation/config: claim/prepare/requeue/reconciliation 전체 제어 및 승인 후 backlog 처리·UNKNOWN 보존; LBT205 | worker + primary |
| `internal/server/provision/worker_control_test.go` | test: claim/prepare/requeue/reconciliation 전체 제어 및 승인 후 backlog 처리·UNKNOWN 보존; LBT205 | worker + primary |
| `internal/server/provision/worker_resume_test.go` | test: claim/prepare/requeue/reconciliation 전체 제어 및 승인 후 backlog 처리·UNKNOWN 보존; LBT205 | worker + primary |
| `internal/server/workercontrol/control.go` | implementation/config: exact Scope/epoch/Pod/boot 및 승인·fresh recheck·drain·UNKNOWN fail-closed 계약/상태 전이; LBT60/205 | worker + primary |
| `internal/server/workercontrol/control_test.go` | test: exact Scope/epoch/Pod/boot 및 승인·fresh recheck·drain·UNKNOWN fail-closed 계약/상태 전이; LBT60/205 | worker + primary |
| `internal/server/workercontrol/durable.go` | implementation/config: exact Scope/epoch/Pod/boot 및 승인·fresh recheck·drain·UNKNOWN fail-closed 계약/상태 전이; LBT60/205 | worker + primary |
| `internal/server/workercontrol/durable_test.go` | test: exact Scope/epoch/Pod/boot 및 승인·fresh recheck·drain·UNKNOWN fail-closed 계약/상태 전이; LBT60/205 | worker + primary |
| `internal/server/workercontrol/maintenance_resume.go` | implementation/config: exact Scope/epoch/Pod/boot 및 승인·fresh recheck·drain·UNKNOWN fail-closed 계약/상태 전이; LBT60/205 | worker + primary |
| `internal/server/workercontrol/maintenance_resume_test.go` | test: exact Scope/epoch/Pod/boot 및 승인·fresh recheck·drain·UNKNOWN fail-closed 계약/상태 전이; LBT60/205 | worker + primary |
| `internal/server/workercontrol/resume.go` | implementation/config: exact Scope/epoch/Pod/boot 및 승인·fresh recheck·drain·UNKNOWN fail-closed 계약/상태 전이; LBT60/205 | worker + primary |
| `internal/server/workercontrol/resume_test.go` | test: exact Scope/epoch/Pod/boot 및 승인·fresh recheck·drain·UNKNOWN fail-closed 계약/상태 전이; LBT60/205 | worker + primary |
| `internal/server/workercontrol/settings.go` | implementation/config: exact Scope/epoch/Pod/boot 및 승인·fresh recheck·drain·UNKNOWN fail-closed 계약/상태 전이; LBT60/205 | worker + primary |
| `internal/server/workercontrol/settings_admission_test.go` | test: exact Scope/epoch/Pod/boot 및 승인·fresh recheck·drain·UNKNOWN fail-closed 계약/상태 전이; LBT60/205 | worker + primary |
| `internal/testsupport/workercontrolfake/resume.go` | implementation/config: 위 소비자가 사용하는 deterministic Store/Resumer Fake; 운영 mutation 경로 아님 | worker + primary |
| `internal/testsupport/workercontrolfake/store.go` | implementation/config: 위 소비자가 사용하는 deterministic Store/Resumer Fake; 운영 mutation 경로 아님 | worker + primary |
| `runtime/contract.yaml` | contract: Runtime0.1.13 기본 MAINTENANCE·APPROVED_CONTROL opt-in과 별도 운영 CLI 미인수 경계 명시 | operator + primary |
| `runtime/dr-control.md` | contract: Runtime0.1.13 기본 MAINTENANCE·APPROVED_CONTROL opt-in과 별도 운영 CLI 미인수 경계 명시 | operator + primary |
