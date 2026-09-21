# PostgreSQL Physical Schema Draft v0.1

이 디렉터리의 SQL Migration 파일(`*.sql`)은 **Labbit PostgreSQL Physical Schema의 현재 구현 초안**입니다.

Confluence는 Domain/Data 의미·ownership·결정 이유를 관리하고, 실제 개발 단계에서 사용할 Table/Column/PK/FK/Unique/Index/Check/Trigger 초안은 이 디렉터리에서 관리합니다.

> 현재 상태는 **설계 초안**입니다. 아직 공용 개발 PostgreSQL에 baseline으로 적용·검증한 상태가 아니므로, 백엔드 개발 시작 시 pgx Query와 실제 Migration apply 결과에 따라 `000001`~`000005`를 재정리할 수 있습니다. 최초 공용 개발 DB에 baseline이 적용된 이후에는 기존 Migration을 수정하지 않고 새 번호 Migration을 추가합니다. D-25의 `000006`은 기존 파일을 수정하지 않는 additive Migration이며, 공용 DB 적용 여부를 새로 확인했다는 의미는 아닙니다.

## 현재 v0.1 Draft Migration

적용 예정 순서는 파일 번호 순서입니다.

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
6. `000006_operation_trace_context.sql`
   - `operations.traceparent` / `operations.tracestate` nullable metadata
   - D-25 API → Worker Context 복원용이며 기존 업무 제약/상태를 바꾸지 않음

## 확정된 Domain/Data 모델과의 관계

Physical Schema 초안은 다음 논리 모델과 불변조건을 구현하기 위한 시작점입니다.

- Organization은 User/Class/Connector/ProviderConnection의 tenant 경계입니다.
- User의 Organization 관리 권한과 ClassMembership의 INSTRUCTOR/STUDENT 역할은 분리합니다.
- LabSpec은 편집 가능한 정의이고 LabExecution은 한 번의 실제 실행입니다.
- LabExecution은 하나의 immutable resolved CreationSnapshot과 여러 LabInstance를 가집니다.
- Operation은 사용자 요청 단위이고 OperationItem은 LabInstance별 Provider mutation 실행 단위입니다.
- ProviderResource는 LabInstance generation별 실제 OpenStack resource 이력을 보존합니다.
- TerminalSession/LiveSession은 lifecycle metadata만 영속하고 Terminal/Live 본문은 저장하지 않습니다.

이 논리 관계가 바뀌는 경우 Confluence Domain/Data 모델을 먼저 갱신하고, 단순 Column/Index 변경은 Git Migration에서만 관리합니다.

## 설계 원칙

### 관계형 코어

Organization/User/Class/Membership/LabSpec/LabExecution/LabInstance/Operation/ProviderResource 같은 관계·권한·정합성 데이터는 관계형 Table과 FK/Unique/Check로 관리합니다.

`jsonb`는 D-19의 **immutable resolved CreationSnapshot**처럼 생성 당시 복합 정의를 통째로 보존해야 하는 경계에 제한적으로 사용합니다. 제품 관계나 권한 상태를 범용 JSON 문서로 옮기지 않습니다.

### ID

Physical Schema 초안의 제품 식별자는 `uuid`를 사용합니다. UUID 생성은 Application/Bootstrap 책임이며 DB extension이나 `gen_random_uuid()` default에 의존하지 않습니다.

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

## DB 초안에 반영한 핵심 불변조건

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

이 제약들은 개발 단계에서 실제 PostgreSQL과 동시 요청 Integration Test로 검증해야 합니다. Unique violation은 정상적인 경쟁 조건 결과로 처리하고 제품 계약의 Conflict로 변환합니다.

## LabSpec과 CreationSnapshot

`lab_specs`는 현재 편집 가능한 정의입니다. `revision`은 HTTP `ETag/If-Match` 구현에 사용할 수 있는 명시적 revision이며 PostgreSQL 내부 `xmin`을 외부 계약으로 사용하지 않습니다.

LabSpec의 Image/Size는 `provider_image_mappings` / `provider_flavor_mappings`의 논리 ID를 참조합니다. LabExecution 시작 시 현재 mapping을 실제 Provider ID와 사양으로 resolve한 뒤 `creation_snapshots.snapshot` JSONB에 고정하는 방향입니다.

`creation_snapshots`는 실행당 하나이며 생성 후 UPDATE하지 않습니다. Reset은 최신 LabSpec/Mapping을 다시 읽지 않고 Snapshot을 사용합니다.

## Operation과 Worker Claim

Browser가 보는 durable 작업은 `operations`, 실제 LabInstance별 실행 단위는 `operation_items`입니다.

한 Provision Operation은 강사/선택 학생의 여러 `operation_items`로 fan-out될 수 있고, Reset은 보통 하나의 item을 가집니다.

PostgreSQL polling Worker는 `operation_items`의 `PENDING` row를 `FOR UPDATE SKIP LOCKED` 방식으로 claim하는 것을 기준으로 합니다. Claim transaction을 commit한 뒤 Connector/OpenStack 작업을 수행하며 외부 Provider 호출 중 DB transaction을 열어 두지 않습니다.

`lease_expires_at`은 같은 Provider Mutation을 즉시 다시 실행할 권한이 아닙니다. RUNNING 작업의 결과가 불명확해지면 D-20/D-24에 따라 `RECONCILING`으로 전환하고 Provider 현실을 먼저 확인합니다.

### D-25: 비동기 Trace Context

`000006`은 `operations`에 nullable `text` 필드 두 개만 추가합니다. `operation_items`마다 원본 Context를 복제하거나 별도 Trace/Span 저장 테이블을 만들지 않습니다.

| 필드 | 저장 의미 |
| --- | --- |
| `traceparent` | 새 Operation을 등록할 당시 현재 SaaS Span에서 직렬화한 유효한 W3C Context |
| `tracestate` | 유효한 traceparent에 동반되는 선택 vendor state. 없거나 잘못되면 NULL |

API는 업무 Operation INSERT와 같은 transaction에서 최소 Context를 저장합니다. Worker는 parent `operations`의 Context를 읽어 자신의 Span을 새로 만들고 item별 명령에 전달합니다. Span 전체, Go context 객체, Baggage, 전체 HTTP header는 저장하지 않습니다. HTTP cancellation과 Worker 실행 lifecycle은 별개입니다.

입력 Context는 저장 **전**에 W3C propagator/parser로 검증하고 Connector wire field의 길이 한도를 넘는 값도 폐기합니다. `traceparent`가 없거나 잘못됐으면 두 필드를 NULL로, `tracestate`만 잘못됐으면 그것만 NULL로 저장합니다. 원문을 오류 로그에 남기지 않습니다. DB에 W3C 정규식/NOT NULL/Unique를 걸어 업무 등록을 막지 않으며, 이 규칙은 DB 접근·업무 무결성 실패를 무시한다는 뜻이 아닙니다.

기존 row는 NULL을 유지합니다. Worker는 Context가 없는 기존 작업도 정상 처리하고 필요 시 새 Trace를 시작합니다. **`operation_id`가 durable 업무 식별자**이며 Trace metadata는 인증·tenant·멱등성 판단이나 `request_fingerprint`에 포함하지 않습니다. Idempotency replay와 Worker 재시작으로 원본 Context를 덮어쓰지 않습니다. 새 처리 시도는 새 Span ID를 사용합니다.

Trace metadata의 보존은 해당 Operation의 접근/보존 경계를 따르며 이 변경만으로 무기한 보존하거나 Trace 본문을 제품 DB에 저장하지 않습니다. 최소 전파 정책/관측 장애 격리는 [Runtime Contract](../../runtime/README.md), Control WSS 처리는 [Connector 계약](../../contracts/connector/README.md#9-공통-correlation)을 따릅니다.

**후속 검증:** 새 DB에 `000001`~`000006` 순차 적용, 기존 `000005` DB에 `000006`만 추가 적용, 기존 row NULL/신규 Context 왕복, HTTP 응답 뒤 Worker 복원, NULL/손상 Context와 Idempotency replay, rollback/재적용 절차를 폐기 가능한 PostgreSQL에서 확인합니다. SQL 파일을 추가한 것만으로 실제 DB 적용 또는 Trace E2E가 완료된 것은 아닙니다.

## ProviderResource

`provider_resources`는 LabInstance + generation별 실제 OpenStack ID의 이력을 유지합니다. Reset 때 최신 Server/Network ID로 row를 덮어쓰지 않습니다.

- `lifecycle_status`: Labbit이 해당 Resource를 추적하는 lifecycle (`PRESENT/DELETING/DELETED/MISSING`)
- `observed_state`: OpenStack에서 마지막으로 관측한 Provider 상태

DB에 없는 Provider 리소스를 발견했다고 바로 `provider_resources`에 소유 Resource로 등록하지 않습니다. D-24의 Orphan candidate 관리 테이블은 실제 운영 UI/DR 구현이 필요할 때 별도 Migration으로 추가합니다.

## Terminal / Live

PostgreSQL에는 `terminal_sessions`와 `live_sessions`의 **최소 lifecycle metadata만** 저장하는 초안을 둡니다.

저장하지 않는 것:

- Terminal INPUT/OUTPUT
- Live OUTPUT
- Transcript / Scrollback / replay history
- 학생별 Live bounded Queue 내용
- Browser/Relay/Connector WebSocket connection owner 같은 ephemeral routing state

학생 Live subscriber 목록도 MVP durable 제품 상태가 아니므로 별도 Table을 만들지 않습니다.

## Application에서 추가로 검증할 의미

DB constraint만으로 자연스럽게 표현하기 어렵거나, 중복 컬럼을 추가해 DB 제약으로 만들기보다 도메인 서비스에서 확인하는 편이 더 단순한 의미는 Application이 검증합니다.

- `lab_specs.workspace_role`이 실제 `lab_spec_vm_roles.role` 중 하나인지
- `workspace_instance_index < vm_count`인지
- 한 LabSpec에서 사용하는 Image/Flavor mapping이 실행에 사용할 동일한 ProviderConnection 경계와 호환되는지
- LabExecution의 instructor가 대상 Class의 현재 `INSTRUCTOR` Membership인지
- Provision 대상 User들이 요청 시점의 해당 Class `STUDENT` Membership인지
- `participant_role = INSTRUCTOR`인 LabInstance의 User가 해당 LabExecution의 `instructor_user_id`와 일치하는지
- `operation_items.lab_instance_id`가 parent Operation의 대상 LabExecution/Mutation 범위에 실제로 속하는지
- `provider_resources.created_by_operation_item_id`가 같은 LabInstance/generation의 실행 item인지
- `terminal_sessions.provider_resource_id`가 실제 Terminal 대상 `SERVER` resource type인지
- Live source TerminalSession이 해당 Class Instructor의 유효한 Session인지

이 검증은 단순 UI 검증으로 대체하지 않고 서버의 domain/application layer에서 Transaction 경계 안팎에 맞춰 수행합니다.

## 개발 시작 시 검증 순서

1. 로컬/공용 개발 PostgreSQL에 draft Migration을 처음부터 적용합니다.
2. FK/Unique/Check/partial unique index가 예상한 경쟁 조건을 실제로 막는지 검증합니다.
3. pgx repository/query를 작성하면서 불필요하거나 누락된 Column/Index를 조정합니다.
4. `FOR UPDATE SKIP LOCKED` Worker claim과 lease/reconciliation 흐름을 Integration Test로 검증합니다.
5. Reset generation/ProviderResource 이력과 Terminal/Live lifecycle을 실제 API/WSS 구현과 맞춥니다.
6. baseline이 팀 공용 개발 DB에 적용된 시점을 기준으로 이후 Migration을 append-only로 전환합니다.

## Migration 운영 규칙

- Application startup AutoMigration은 사용하지 않습니다.
- 배포 전에 별도 Migration 단계에서 한 번 실행합니다.
- **현재 draft baseline이 아직 공용 개발 DB에 적용되기 전에는 초기 파일 재정리가 가능합니다.**
- **baseline 적용 이후에는 기존 Migration을 수정하지 않고 변경은 새 번호의 SQL 파일로 추가합니다.**
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
