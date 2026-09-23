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

package 이름과 파일 개수는 Story에 필요한 만큼만 생성합니다. 위 책임 방향을 유지한다면 handler, application, repository 같은 정확한 디렉터리 이름 자체는 계약이 아닙니다.

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

## SL-64 첫 Vertical Slice

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
- Frontend 실제 consumer: SL-65에서 HTTP mode E2E

테스트 원칙 전체는 ../../TESTING.md를 따릅니다.
