# Backend Implementation Boundary v0.1

이 문서는 Labbit Backend를 실제 구현할 때 지켜야 하는 최소 책임 경계와 의존 방향을 정의합니다.

상위 제품 정책은 Confluence, HTTP/WSS는 contracts/, PostgreSQL은 db/migrations/, 실행 설정은 runtime/이 원본입니다. 이 문서는 그 결정을 재정의하지 않고 Go 코드 안에서 어디까지 책임질지만 고정합니다.

## 기본 흐름

    HTTP Handler / Middleware
            ↓
    Application Use Case
            ↓
    Repository / External Port
            ↓
    PostgreSQL / Connector / Other Adapter

package 이름과 파일 개수는 해당 작업에 필요한 만큼만 생성합니다. 위 책임 방향을 유지한다면 handler, application, repository 같은 정확한 디렉터리 이름 자체는 계약이 아닙니다.

## HTTP Handler / Middleware

담당:
- route와 HTTP method
- path/query/header/body parsing
- request shape validation
- Cookie/header extraction
- request-scoped authentication context 연결
- Application 결과를 OpenAPI status/body/header로 변환
- 공통 Problem Details 응답 작성

하지 않는 것:
- SQL/pgx 직접 호출
- Class/Organization 권한 규칙 자체 구현
- Provider/Connector 직접 호출
- DB constraint 이름을 Browser에 노출
- raw internal error를 Problem Details detail에 복사

Auth middleware는 현재 사용자를 결정할 수 있지만 특정 Class를 운영할 수 있는지 같은 resource authorization은 Application에서 대상 관계와 함께 검증합니다.

## Application Use Case

담당:
- Organization/Class authorization
- 도메인 불변조건과 상태 전이
- transaction orchestration
- Idempotency/Conflict 의미
- 여러 Repository/Port 호출 순서
- transport와 무관한 typed application error 반환

Application은 HTTP status나 http.ResponseWriter를 알지 않습니다.

## Repository

담당:
- SQL/pgx query
- row와 persistence model mapping
- transaction 안의 DB 변경
- PostgreSQL constraint/Not Found를 상위에서 해석 가능한 repository error로 정규화

하지 않는 것:
- HTTP status/Problem Details 생성
- UI 권한 판단
- OpenStack/Connector 호출
- request Cookie 처리

DB의 Unique/FK/Check는 경쟁 조건에서 최종 안전망으로 사용하며 Application 사전 검증을 무조건 대체하지 않습니다.

## Transaction 경계

transaction은 Application use case가 필요한 원자성 범위를 결정합니다.

1. 필요한 row를 읽고/잠그고 제품 상태를 변경합니다.
2. DB transaction을 commit합니다.
3. Connector/OpenStack 같은 외부 I/O를 수행합니다.
4. 결과를 별도 transaction으로 반영합니다.

OpenStack/Connector/외부 네트워크 호출 중 PostgreSQL transaction을 열린 채 유지하지 않습니다.

단순 read-only query는 별도 explicit transaction이 필요하지 않으면 Repository 일반 query로 수행할 수 있습니다.

## Error 경계

Application은 최소한 다음 의미를 구분할 수 있어야 합니다.

- unauthenticated
- forbidden
- not found
- conflict
- invalid/semantic validation
- precondition failed
- dependency unavailable
- internal

HTTP layer가 이를 OpenAPI의 401/403/404/409/422/412/503/500 및 Problem Details로 변환합니다.

PostgreSQL error text, Password/hash 오류 원문, Provider raw response, Secret/Token은 사용자 응답에 포함하지 않습니다.

## Context와 비동기 작업

HTTP request에 종속된 DB 조회와 짧은 처리는 request context를 사용합니다.

Provision/Reset/Cleanup 같은 durable Operation은 HTTP request lifetime 이후에도 계속되어야 하므로 request cancellation을 Worker 실행 lifecycle에 그대로 전달하지 않습니다. 이 경계는 기존 Runtime/D-20/D-25 계약을 따릅니다.

## 현재 구현 위치 (계약 아님)

- `internal/server/repository`: Application이 의존하는 Repository/Transactor port, persistence record, typed error(`ErrNotFound`, `ErrConflict`, `ErrConstraintViolation`, `ErrInternal`). pgx/SQL을 import하지 않습니다.
- `internal/postgres`: pgx 구현(`Store`). PostgreSQL 오류를 위 typed error로 정규화하며 원본 오류 문자열, SQLSTATE, constraint 이름은 사용자 응답과 외부 계약에 전달하지 않습니다.
- `internal/server/auth`: Login/Session/Logout Application use case와 Argon2id PHC 검증. Session 유효성, disabled 계정 처리, username 존재 여부를 숨기는 dummy 검증을 이 계층이 판단하며 HTTP status와 Cookie는 알지 못합니다.
- `internal/server/class`: Class 목록/상세 조회 Application use case. Class 접근은 ClassMembership 관계로만 결정하며 `organizationRole=ADMIN`도 Membership을 대신하지 않습니다. Class가 없으면 not found, 있지만 다른 Organization이거나 Membership이 없으면 forbidden, 참여 Class가 없는 목록은 오류가 아닌 빈 목록입니다. HTTP status는 알지 못합니다.
- `internal/server/httpapi`: `/api/v1` Auth·Class handler와 middleware(Origin/Referer 검증, Session 인증, Cookie, Problem Details). SQL/pgx를 import하지 않고 `auth.Service`, `class.Service`에 위임합니다.
- `internal/server/connector`, `internal/server/connectorwss`: Connector Control WSS(`contracts/connector/`)의 Backend 쪽 구현입니다. `connector.Registry`는 인증과 Upgrade를 마친 connection을 Connector별 current Session 하나로 **소유**하고, HELLO_ACK를 마친 Session만 command를 받을 수 있는 **protocol-ready route**로 별도로 표시합니다(소유 ≠ ready). `connector.Router`는 `SendOperationCommand`/`SendReconcileRequest`로 ready route에만 보내며, pending correlation을 write보다 먼저 등록하고 write 실패 시 되돌립니다. inbound `OPERATION_ACK`/`PROGRESS`/`RESULT`와 `RECONCILE_RESULT`는 인증된 ConnectorID와 `operationId`/`labInstanceId`/`generation`, `messageId`/`replyToMessageId` 관계가 모두 맞는 pending에만 연결해 typed event로 `EventSink`에 넘기고, 맞지 않으면 다른 pending으로 fallback하지 않고 unmatched event로 알립니다. pending은 프로세스 안의 ephemeral routing 상태이며 `operations`/`operation_items` 같은 durable Operation 상태를 만들거나 바꾸지 않습니다. 재접속은 command retry가 아니므로 Router는 어떤 message도 자동으로 다시 보내지 않습니다. Operation Worker의 durable 상태 반영과 event 소비는 후속 작업(LBT-18)입니다. Trace Context는 표준 OpenTelemetry propagator로 검사해 유효한 값만 전달하는 관측 metadata이며 routing key가 아닙니다.
- `internal/server/bootstrap`: D-11 trusted operator Bootstrap use case. Migration이 아닌 이 경로로 Organization/User/Local Account/Class/ClassMembership을 하나의 transaction으로 생성합니다. 실행 command는 아직 없으며 추가할 때는 Runtime Contract `artifacts`와의 정합성을 함께 확인합니다.

Repository는 Session 유효성, Class 접근 권한, 403/404를 판단하지 않고 저장된 값을 그대로 전달합니다. Transaction callback 안에서는 전달된 Repositories만 사용하고 Connector/OpenStack 같은 외부 I/O를 수행하지 않습니다.

## Auth/Class 첫 Vertical Slice (LBT-10)

    Bootstrap test data
      → Password verify + Session repository
      → POST /auth/login
      → Auth middleware
      → GET /me
      → GET /classes
      → GET /classes/{classId}
      → POST /auth/logout
      → PostgreSQL/API integration tests

Session의 구체 lifecycle/CSRF 기준은 auth-session.md를 따릅니다.

## 테스트

- Application rule: Repository/Port fake를 사용한 Go unit test
- HTTP transport: httptest 기반 request/response 계약 test
- PostgreSQL constraint/session/query: 실제 PostgreSQL Integration Test
- Frontend 실제 consumer: LBT-12에서 HTTP mode E2E

테스트 원칙 전체는 ../../TESTING.md를 따릅니다.
