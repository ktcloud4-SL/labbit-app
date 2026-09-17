# PostgreSQL Physical Schema SSOT

이 디렉터리의 명시적 SQL Migration 파일(`*.sql`)이 **Labbit PostgreSQL Physical Schema의 SSOT**입니다.

Confluence는 도메인 의미와 결정 이유를 관리하고, 실제 Table/Column/PK/FK/Unique/Index/Check/Trigger는 이 디렉터리가 원본입니다.

## 현재 v0.1 Migration

적용 순서는 파일 번호 순서입니다.

1. `000001_identity_and_class.sql`
   - `organizations`
   - `users`
   - `local_accounts`
   - `auth_sessions`
   - `classes`
   - `class_memberships`
2. `000002_connector_and_provider.sql`
   - `connectors`
   - `connector_credentials`
   - `provider_connections`
   - `provider_image_mappings`
   - `provider_flavor_mappings`
3. `000003_lab_spec.sql`
   - `lab_specs`
   - `lab_spec_vm_roles`
4. `000004_execution_operation_resource.sql`
   - `lab_executions`
   - `creation_snapshots`
   - `lab_instances`
   - `operations`
   - `operation_items`
   - `provider_resources`
5. `000005_realtime_sessions.sql`
   - `terminal_sessions`
   - `live_sessions`

## 설계 원칙

### 관계형 코어

Organization/User/Class/Membership/LabSpec/LabExecution/LabInstance/Operation/ProviderResource 같은 관계·권한·정합성 데이터는 관계형 Table과 FK/Unique/Check로 관리합니다.

`jsonb`는 D-19의 **immutable resolved CreationSnapshot**처럼 생성 당시 복합 정의를 통째로 보존해야 하는 경계에 제한적으로 사용합니다. 제품 관계나 권한 상태를 범용 JSON 문서로 옮기지 않습니다.

### ID

Physical Schema의 제품 식별자는 `uuid`를 사용합니다. UUID 생성은 Application/Bootstrap 책임이며 DB extension이나 `gen_random_uuid()` default에 의존하지 않습니다.

HTTP/WSS에서는 계속 opaque string ID로 취급하므로 Physical ID 생성 방식이 외부 계약으로 노출되지 않습니다.

### Tenant 경계

주요 제품 리소스는 `organization_id`를 유지하고, 가능한 관계는 `(organization_id, id)` Composite FK로 연결해 다른 Organization의 User/Class/Provider Mapping을 잘못 결합하는 것을 DB에서도 차단합니다.

MVP에서는 PostgreSQL RLS를 필수로 도입하지 않습니다. Application authorization과 DB FK/Unique/Check를 함께 사용합니다.

### Local Account / Session Secret

- Password 원문은 저장하지 않고 `local_accounts.password_hash`에 검증용 one-way hash 문자열만 저장합니다.
- Browser session opaque token 원문은 저장하지 않고 `auth_sessions.token_hash`만 저장합니다.
- Connector Credential 원문은 저장하지 않고 `connector_credentials.credential_hash`만 저장합니다.
- Terminal attach token 원문은 저장하지 않고 `terminal_sessions.attach_token_hash`만 저장합니다.
- OpenStack Credential/Keystone Token은 중앙 PostgreSQL에 저장하지 않습니다.

## DB가 직접 보장하는 핵심 불변조건

- 동일 `(class_id, user_id)` ClassMembership 중복 금지
- ClassMembership의 Organization과 User/Class Organization 일치
- Class당 `finished_at IS NULL`인 active `LabExecution` 최대 1개
- Execution 안에서 동일 User의 `LabInstance` 최대 1개
- Execution당 Instructor `LabInstance` 최대 1개
- LabInstance당 `PENDING/RUNNING/RECONCILING` Mutation `operation_item` 최대 1개
- 동일 Idempotency-Key hash를 같은 사용자·Operation 종류에 다른 요청으로 중복 등록할 수 없음
- 실제 Provider Resource ID를 `(provider_connection_id, resource_type, provider_id)`로 중복 추적하지 않음
- Class당 active `LiveSession` 최대 1개
- `CreationSnapshot` UPDATE 금지

Application은 Unique violation을 정상적인 경쟁 조건 결과로 처리해야 합니다. 예를 들어 두 Replica가 동시에 같은 Class에 Execution을 만들면 partial unique index가 마지막 방어선이며, loser는 제품 계약에 맞는 Conflict로 변환합니다.

## LabSpec과 CreationSnapshot

`lab_specs`는 현재 편집 가능한 정의입니다. `revision`은 HTTP `ETag/If-Match` 구현에 사용할 수 있는 명시적 revision이며 PostgreSQL 내부 `xmin`을 외부 계약으로 사용하지 않습니다.

LabSpec의 Image/Size는 `provider_image_mappings` / `provider_flavor_mappings`의 논리 ID를 참조합니다. LabExecution 시작 시 현재 mapping을 실제 Provider ID와 사양으로 resolve한 뒤 `creation_snapshots.snapshot` JSONB에 고정합니다.

`creation_snapshots`는 실행당 하나이며 생성 후 UPDATE하지 않습니다. Reset은 최신 LabSpec/Mapping을 다시 읽지 않고 Snapshot을 사용합니다.

## Operation과 Worker Claim

Browser가 보는 durable 작업은 `operations`, 실제 LabInstance별 실행 단위는 `operation_items`입니다.

한 Provision Operation은 강사/선택 학생의 여러 `operation_items`로 fan-out될 수 있고, Reset은 보통 하나의 item을 가집니다.

PostgreSQL polling Worker는 `operation_items`의 `PENDING` row를 `FOR UPDATE SKIP LOCKED` 방식으로 claim하는 것을 기준으로 합니다. Claim transaction을 commit한 뒤 Connector/OpenStack 작업을 수행하며 외부 Provider 호출 중 DB transaction을 열어 두지 않습니다.

`lease_expires_at`은 같은 Provider Mutation을 즉시 다시 실행할 권한이 아닙니다. RUNNING 작업의 결과가 불명확해지면 D-20/D-24에 따라 `RECONCILING`으로 전환하고 Provider 현실을 먼저 확인합니다.

## ProviderResource

`provider_resources`는 LabInstance + generation별 실제 OpenStack ID의 이력을 유지합니다. Reset 때 최신 Server/Network ID로 row를 덮어쓰지 않습니다.

- `lifecycle_status`: Labbit이 해당 Resource를 추적하는 lifecycle (`PRESENT/DELETING/DELETED/MISSING`)
- `observed_state`: OpenStack에서 마지막으로 관측한 Provider 상태

DB에 없는 Provider 리소스를 발견했다고 바로 `provider_resources`에 소유 Resource로 등록하지 않습니다. D-24의 Orphan candidate 관리 테이블은 실제 운영 UI/DR 구현이 필요할 때 별도 Migration으로 추가합니다.

## Terminal / Live

PostgreSQL에는 `terminal_sessions`와 `live_sessions`의 **최소 lifecycle metadata만** 저장합니다.

저장하지 않는 것:

- Terminal INPUT/OUTPUT
- Live OUTPUT
- Transcript / Scrollback / replay history
- 학생별 Live bounded Queue 내용
- Browser/Relay/Connector WebSocket connection owner 같은 ephemeral routing state

학생 Live subscriber 목록도 MVP durable 제품 상태가 아니므로 별도 Table을 만들지 않습니다.

## Application에서 추가로 검증할 의미

DB constraint만으로 자연스럽게 표현하기 어려운 다음 의미는 Application이 검증합니다.

- `lab_specs.workspace_role`이 실제 `lab_spec_vm_roles.role` 중 하나인지
- `workspace_instance_index < vm_count`인지
- LabExecution의 instructor가 대상 Class의 현재 `INSTRUCTOR` Membership인지
- Provision 대상 User들이 요청 시점의 해당 Class `STUDENT` Membership인지
- `terminal_sessions.provider_resource_id`가 실제 Terminal 대상 `SERVER` resource type인지
- Live source TerminalSession이 해당 Class Instructor의 유효한 Session인지

## Migration 운영 규칙

- Application startup AutoMigration은 사용하지 않습니다.
- 배포 전에 별도 Migration 단계에서 한 번 실행합니다.
- 적용된 Migration 파일을 수정하지 않고 변경은 새 번호의 SQL 파일로 추가합니다.
- Organization/User/Class/ClassMembership 같은 운영 Bootstrap 데이터는 Schema Migration에 `INSERT`하지 않습니다. Trusted operator Bootstrap command가 별도로 생성합니다.
- Migration runner/rollback mechanism의 구체 도구는 Runtime/Platform 계약에서 정합니다.
- 되돌리기 어려운 파괴적 Schema 변경은 D-22 기준으로 수업 외 유지보수 창과 Application 호환성을 먼저 검증합니다.

## 아직 만들지 않는 Table

실제 Trigger가 생기기 전에는 다음을 선행 생성하지 않습니다.

- SQS Transactional Outbox
- Kafka/Event Store
- Redis/Valkey cache/lock/session mirror
- Reconciliation orphan finding table
- Terminal/Live history table
- 범용 Audit/Event table

SQS 등 전달 기술이 채택되더라도 `operations` / `operation_items` / `provider_resources`가 제품 작업·복구 상태의 중심이라는 원칙은 유지합니다.
