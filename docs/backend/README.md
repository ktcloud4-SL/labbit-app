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
- `internal/server/httpapi`: `/api/v1` Auth·Class·Terminal target 조회·TerminalSession handler와 middleware(Origin/Referer 검증, Session 인증, Cookie, Problem Details). SQL/pgx를 import하지 않고 `auth.Service`, `class.Service`, `terminal.Service`에 위임합니다. Terminal Relay가 없는 구성(`Options.Terminals`가 nil)에서는 Terminal target 조회와 TerminalSession 생성/종료가 인증(과 mutation의 Origin 검증)을 거친 뒤 503(`terminal_unavailable`)입니다.
- `internal/server/connector`, `internal/server/connectorwss`: Connector Control WSS(`contracts/connector/`)의 Backend 쪽 구현입니다. `connector.Registry`는 인증과 Upgrade를 마친 connection을 Connector별 current Session 하나로 **소유**하고, HELLO_ACK를 마친 Session만 command를 받을 수 있는 **protocol-ready route**로 별도로 표시합니다(소유 ≠ ready). `connector.Router`는 `SendOperationCommand`/`SendReconcileRequest`로 ready route에만 보내며, pending correlation을 write보다 먼저 등록하고 write 실패 시 되돌립니다. inbound `OPERATION_ACK`/`PROGRESS`/`RESULT`와 `RECONCILE_RESULT`는 인증된 ConnectorID와 `operationId`/`labInstanceId`/`generation`, `messageId`/`replyToMessageId` 관계가 모두 맞는 pending에만 연결해 typed event로 `EventSink`에 넘기고, 맞지 않으면 다른 pending으로 fallback하지 않고 unmatched event로 알립니다. pending은 프로세스 안의 ephemeral routing 상태이며 `operations`/`operation_items` 같은 durable Operation 상태를 만들거나 바꾸지 않습니다. 재접속은 command retry가 아니므로 Router는 어떤 message도 자동으로 다시 보내지 않습니다. Operation Worker의 durable 상태 반영과 event 소비는 후속 작업(LBT-18)입니다. Trace Context는 표준 OpenTelemetry propagator로 검사해 유효한 값만 전달하는 관측 metadata이며 routing key가 아닙니다. 같은 Router가 TerminalSession lifecycle(`TERMINAL_OPEN`/`TERMINAL_CLOSE` 전송, `TERMINAL_OPEN_RESULT`/`TERMINAL_ENDED` 수신)도 담당합니다. OPEN은 pending을 write보다 먼저 등록하고 OPEN_RESULT는 인증된 ConnectorID, `terminalSessionId`, `labInstanceId`, `generation`, `replyToMessageId`가 모두 맞는 pending에만 연결하며 맞지 않으면 다른 pending으로 fallback하지 않습니다(`TerminalSink`로 전달하며 Operation `EventSink`와 별개). `TERMINAL_ENDED`는 pending이 없는 통지라 인증된 ConnectorID와 주장한 correlation을 그대로 넘기고, 받는 쪽이 권위 있는 상태와 대조합니다.
- `internal/server/terminal`: TerminalSession use case(LBT-99). Browser가 `targetVmKey`를 얻는 공식 경로인 target 조회(`Targets`, `GET .../terminal-targets`)를 함께 제공합니다. 목록의 원본은 해당 LabExecution의 immutable resolved CreationSnapshot(`vms[]`의 `vmKey`/`role`/`instanceIndex`와 `workspaceVmKey`)이며 `vmKey`는 Browser에 opaque입니다. 저장된 snapshot이 모순이면(빈 key, 중복 `vmKey`, `workspaceVmKey`가 `vms`에 없음 등) 일부만 반환하지 않고 fail closed합니다. 목록은 논리적 선택지이며 Provider topology(Server ID, Connector ID, 주소, 이미지·flavor)는 포함하지 않고 VM의 현재 사용 가능 여부도 보증하지 않습니다. 생성은 조회 결과를 근거로 신뢰하지 않고 `targetVmKey`가 CreationSnapshot의 VM인지, 현재 generation의 `PRESENT` `SERVER` ProviderResource가 있는지 다시 판정합니다(조회와 생성은 같은 소유·ClassMembership·`READY` 판정을 씁니다). 현재 User, LabInstance 소유, 현재 ClassMembership, `READY`, 현재 generation의 대상 `SERVER` ProviderResource를 PostgreSQL 상태에서 결정하고(Browser가 보낸 Provider Server ID/Connector ID/generation은 받지 않음) OPENING row와 attach token digest를 저장한 뒤 Connector에 `TERMINAL_OPEN`을 보냅니다. `TERMINAL_OPEN_RESULT SUCCEEDED`만 믿지 않고 같은 TerminalSession의 Terminal Data WSS가 실제로 bind되었을 때만 성공시키며, 어떤 실패에서도 ENDED로 기록해 ghost session을 남기지 않습니다. attach마다 로그인 세션·사용자·Class 권한·LabInstance 소유·현재 generation·lifecycle·token digest를 다시 확인하는 `realtime.Control`을 구현하고, Connector가 알린 종료를 권위 있는 상태와 대조한 뒤 처리합니다. Reset/Cleanup이 호출할 경계(`CloseForLabMutation`)를 제공하지만 Reset/Cleanup 자체는 구현하지 않습니다. DB와 Connector Control에 의존하며 Terminal byte stream은 알지 못합니다.
- `internal/server/realtime`: Browser Terminal WSS(`/realtime/v1/terminal`)와 Connector Terminal Data WSS(`/connector/v1/terminal-data`)의 transport와 **ephemeral** relay 상태입니다. **PostgreSQL, repository, auth, connector package를 import하지 않으며**(경계 test가 전이 의존까지 검사) 자신이 정의한 좁은 interface(`Control`, `ConnectorAuthenticator`)로만 DB-backed authority를 받습니다. TerminalSession마다 active Browser attachment 하나와 Connector data channel 하나, 60초 grace timer(`Clock` 주입)를 가지며 connection마다 writer goroutine이 하나라 concurrent write가 없습니다. Browser attachment의 bounded queue는 전송 중인 byte를 잠시 담을 뿐 history가 아니며, 한도를 넘으면 그 attachment만 `SLOW_CONSUMER`/4005로 종료하고 PTY는 grace를 따릅니다. Terminal INPUT/OUTPUT·token은 저장·재생·log하지 않습니다. v0.1에서는 같은 process의 api role이 `Control` 구현을 제공합니다(runtime/contract.yaml `saas.realtime`).
- `internal/server/jsonnum`: JSON Schema 2020-12의 integer 규칙(1.0, 1e2도 integer, maximum 없음)을 lexical하게 판정하는 작은 leaf package입니다. Schema에 없는 상한을 만들지 않으려고 cols/rows 원문을 그대로 전달하는 데 씁니다.
- `internal/server/app`: `newControlStack`이 Auth, Class, Connector Control과(realtime role이 같은 process에 있을 때) Terminal Relay를 하나의 조립으로 묶습니다. `Run`과 통합 test가 같은 조립을 씁니다. shutdown은 Relay가 active TerminalSession을 `SERVICE_RESTARTING`으로 종료해 Connector에 `TERMINAL_CLOSE`를 보낸 뒤에 Connector Control connection을 닫습니다. realtime role만 enabled된 process는 DB DSN 없이 시작하지만 Terminal route를 열지 않고 `/readyz`가 실패합니다.
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
- Terminal WSS: 실제 WebSocket peer와 주입한 `Clock`으로 protocol/auth/grace를 검증합니다(60초를 기다리지 않습니다). Connector 쪽은 계약대로 동작하는 contract peer(`terminal/terminaltest`)이며 실제 OpenStack/SSH/PTY 검증은 LBT-22(C2) 범위입니다.
- Frontend 실제 consumer: LBT-12에서 HTTP mode E2E

테스트 원칙 전체는 ../../TESTING.md를 따릅니다.
