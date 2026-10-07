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
- `internal/server/httpapi`: `/api/v1` Auth·Class·Terminal target 조회·TerminalSession·Workspace file(Tree/Read/Save) handler와 middleware(Origin/Referer 검증, Session 인증, Cookie, Problem Details). SQL/pgx를 import하지 않고 `auth.Service`, `class.Service`, `terminal.Service`, `workspacefile.Service`에 위임합니다. file handler는 query의 percent-decoding을 한 번만 하고(`path` 중복·잘못된 인코딩은 400) `If-Match`의 strong entity-tag 하나를 revision으로, `ETag`를 응답 header로 옮기며 Save 요청 body는 파일 크기 한도에서 정한 상한으로 읽습니다. 경로 규칙과 본문(UTF-8/NUL/크기) 판단은 Application이 합니다. Terminal Relay가 없는 구성(`Options.Terminals`가 nil)에서는 Terminal target 조회와 TerminalSession 생성/종료가 인증(과 mutation의 Origin 검증)을 거친 뒤 503(`terminal_unavailable`)입니다. PreviewSession 생성(`POST .../preview-sessions`)과 종료(`DELETE /preview-sessions/{id}`)도 같은 인증·Origin 경계 뒤에서 `previewsession.Service`에 위임하며 `targetPort`만 받고(추가 field 거절, JSON Schema integer 1~65535) 승인은 하지 않습니다. 응답은 `id`/`targetPort`/`previewUrl`/`expiresAt`뿐이고 오류는 안정적인 code(`preview_port_not_allowed`, `preview_app_not_running` 등)로 옮기며 Connector·SSH 오류 원문을 싣지 않습니다. Preview Gateway가 없는 구성(`Options.Previews`가 nil)에서는 인증을 거친 뒤 503(`preview_unavailable`)입니다.
- `internal/server/connector`, `internal/server/connectorwss`: Connector Control WSS(`contracts/connector/`)의 Backend 쪽 구현입니다. `connector.Registry`는 인증과 Upgrade를 마친 connection을 Connector별 current Session 하나로 **소유**하고, HELLO_ACK를 마친 Session만 command를 받을 수 있는 **protocol-ready route**로 별도로 표시합니다(소유 ≠ ready). `connector.Router`는 `SendOperationCommand`/`SendReconcileRequest`로 ready route에만 보내며, pending correlation을 write보다 먼저 등록하고 write 실패 시 되돌립니다. inbound `OPERATION_ACK`/`PROGRESS`/`RESULT`와 `RECONCILE_RESULT`는 인증된 ConnectorID와 `operationId`/`labInstanceId`/`generation`, `messageId`/`replyToMessageId` 관계가 모두 맞는 pending에만 연결해 typed event로 `EventSink`에 넘기고, 맞지 않으면 다른 pending으로 fallback하지 않고 unmatched event로 알립니다. pending은 프로세스 안의 ephemeral routing 상태이며 `operations`/`operation_items` 같은 durable Operation 상태를 만들거나 바꾸지 않습니다. 재접속은 command retry가 아니므로 Router는 어떤 message도 자동으로 다시 보내지 않습니다. Operation Worker의 durable 상태 반영과 event 소비는 후속 작업(LBT-18)입니다. Trace Context는 표준 OpenTelemetry propagator로 검사해 유효한 값만 전달하는 관측 metadata이며 routing key가 아닙니다. 같은 Router가 TerminalSession lifecycle(`TERMINAL_OPEN`/`TERMINAL_CLOSE` 전송, `TERMINAL_OPEN_RESULT`/`TERMINAL_ENDED` 수신)도 담당합니다. OPEN은 pending을 write보다 먼저 등록하고 OPEN_RESULT는 인증된 ConnectorID, `terminalSessionId`, `labInstanceId`, `generation`, `replyToMessageId`가 모두 맞는 pending에만 연결하며 맞지 않으면 다른 pending으로 fallback하지 않습니다(`TerminalSink`로 전달하며 Operation `EventSink`와 별개). `TERMINAL_ENDED`는 pending이 없는 통지라 인증된 ConnectorID와 주장한 correlation을 그대로 넘기고, 받는 쪽이 권위 있는 상태와 대조합니다. Registry는 HELLO가 선언한 `capabilities`를 Session별로 기록하고(`SetCapabilities`, 재접속한 Session은 다시 선언해야 함), Workspace File lifecycle(`FILE_OPEN`/`FILE_CLOSE`)은 `file-v1`을 선언한 protocol-ready Session에만 보냅니다(`WithReadyRouteCapability`; 선언하지 않았으면 `ErrCapabilityUnsupported`이고 연결 단절과 구분합니다). `FILE_OPEN` pending도 같은 원칙으로 인증된 ConnectorID, `fileRequestId`/`labInstanceId`/`generation`, `replyToMessageId`가 모두 맞는 `FILE_OPEN_RESULT`에만 연결하며 `FileSink`로 넘깁니다. 파일 경로·목록·본문은 Control에 싣지 않습니다. 같은 Router가 PreviewSession lifecycle(`PREVIEW_OPEN`/`PREVIEW_CLOSE` 전송, `PREVIEW_OPEN_RESULT` 수신)도 담당합니다. `preview-v1`을 선언한 protocol-ready Session에만 보내고(`ErrCapabilityUnsupported`는 연결 단절과 구분), `PREVIEW_OPEN_RESULT`는 인증된 ConnectorID, `previewSessionId`, `labInstanceId`, `generation`, `replyToMessageId`가 모두 맞는 pending에만 연결해 `PreviewSink`로 넘깁니다. `PreviewOpen.OnRoute`는 Registry의 route lock 안에서 PREVIEW_OPEN을 쓰기 직전 그 message를 받을 정확한 Control Session을 호출자에게 알려, Connector가 Data WSS를 붙이기 전에 PreviewSession을 그 Control Session에 묶게 합니다. Registry의 `SessionObserver`는 Control Session의 교체와 revoke를 알립니다(단순 disconnect는 아님). Preview 경로·Cookie·본문은 Control에 싣지 않습니다.
- `internal/server/terminal`: TerminalSession use case(LBT-99). Browser가 `targetVmKey`를 얻는 공식 경로인 target 조회(`Targets`, `GET .../terminal-targets`)를 함께 제공합니다. 목록의 원본은 해당 LabExecution의 immutable resolved CreationSnapshot(`vms[]`의 `vmKey`/`role`/`instanceIndex`와 `workspaceVmKey`)이며 `vmKey`는 Browser에 opaque입니다. 저장된 snapshot이 모순이면(빈 key, 중복 `vmKey`, `workspaceVmKey`가 `vms`에 없음 등) 일부만 반환하지 않고 fail closed합니다. 목록은 논리적 선택지이며 Provider topology(Server ID, Connector ID, 주소, 이미지·flavor)는 포함하지 않고 VM의 현재 사용 가능 여부도 보증하지 않습니다. 생성은 조회 결과를 근거로 신뢰하지 않고 `targetVmKey`가 CreationSnapshot의 VM인지, 현재 generation의 `PRESENT` `SERVER` ProviderResource가 있는지 다시 판정합니다(조회와 생성은 같은 소유·ClassMembership·`READY` 판정을 씁니다). 현재 User, LabInstance 소유, 현재 ClassMembership, `READY`, 현재 generation의 대상 `SERVER` ProviderResource를 PostgreSQL 상태에서 결정하고(Browser가 보낸 Provider Server ID/Connector ID/generation은 받지 않음) OPENING row와 attach token digest를 저장한 뒤 Connector에 `TERMINAL_OPEN`을 보냅니다. `TERMINAL_OPEN_RESULT SUCCEEDED`만 믿지 않고 같은 TerminalSession의 Terminal Data WSS가 실제로 bind되었을 때만 성공시키며, 어떤 실패에서도 ENDED로 기록해 ghost session을 남기지 않습니다. attach마다 로그인 세션·사용자·Class 권한·LabInstance 소유·현재 generation·lifecycle·token digest를 다시 확인하는 `realtime.Control`을 구현하고, Connector가 알린 종료를 권위 있는 상태와 대조한 뒤 처리합니다. Reset/Cleanup이 호출할 경계(`CloseForLabMutation`)를 제공하지만 Reset/Cleanup 자체는 구현하지 않습니다. DB와 Connector Control에 의존하며 Terminal byte stream은 알지 못합니다.
- `internal/server/workspacefile`: Workspace file Tree/Read/Save use case(LBT-100). 경로 규칙은 `Path`(`ParseFile`/`ParseDirectory`) 하나가 소유하며(HTTP decode 뒤 한 번만 해석, 조용히 고치지 않고 거절, percent-escape·`..`·절대 경로·백슬래시·제어 문자 거절) Transport에는 이 package가 만든 canonical `Path`만 전달됩니다. 현재 User, LabInstance 소유, 현재 ClassMembership, `READY`, 그 LabExecution의 immutable CreationSnapshot의 `workspaceVmKey`(Browser가 VM을 고르지 않음), 현재 generation의 `PRESENT` `SERVER` ProviderResource(정확히 하나), CreationSnapshot의 ProviderConnection/Connector를 하나의 짧은 transaction(`FOR SHARE`)에서 결정하고 commit한 뒤에만 `Transport`(Tree/Read/Save 세 동작뿐인 external port)를 호출하며, 성공 결과를 돌려주기 전에 LabInstance의 generation과 `READY`가 그대로인지 다시 확인합니다(Reset race는 성공이 아니라 `ErrTargetChanged`). 저장된 관계가 모순이면(workspaceVmKey가 비었거나 vms에 없음/중복, PRESENT Workspace VM이 둘 등) fail closed입니다. 파일 본문은 UTF-8 text(NUL 없음)만 받고 크기 한도는 `Options.MaxFileBytes`(test에서 주입, 기본값은 보수적인 구현 값이며 계약 수치가 아님)입니다. Save는 기존 파일의 낙관적 교체이며 revision 비교는 Connector가 쓰기 직전에 합니다. HTTP status와 Connector protocol을 모르며 파일 본문과 경로를 저장하거나 log에 남기지 않습니다. VM 안의 Workspace root, SFTP, symlink containment는 이 package의 범위가 아닙니다(LBT-21).
- `internal/server/filetransport`: `workspacefile.Transport`의 Connector adapter(`Broker`)와 Connector File Data WSS(`/connector/v1/file-data`, `labbit.connector-file.v1`). 1 HTTP file 요청은 `FILE_OPEN`(Control) 하나와 요청별 File Data WSS 하나이며 요청 frame 하나와 결과 frame 하나 뒤에 SaaS가 정상 종료합니다(복잡한 persistent multiplexer 없음). 파일 본문은 JSON이 아니라 요청/결과 frame 바로 다음의 Binary frame 하나로 오갑니다. attach는 Upgrade의 Connector Credential 인증 결과(Connector identity), `fileRequestId`, `labInstanceId`, `generation`, Workspace VM 식별(`targetVmKey`, `providerServerId`)이 모두 기대한 값일 때만 성립하고 하나라도 다르면 요청을 건드리지 않고 그 connection만 거절합니다. 결과 frame의 correlation·type·replyTo·Schema가 어긋나면 성공으로 처리하지 않고 연결을 `1008`로 끝내며, Save 본문을 보낸 뒤의 실패는 자동 재전송 없이 `ErrSaveOutcomeUnknown`입니다. 취소·시간 초과·`FILE_OPEN_RESULT=FAILED`·Credential/Connector revoke(`4001`)·shutdown에서 pending 상태, Router의 FILE_OPEN pending, Data WSS를 모두 정리하고 필요하면 Connector에 `FILE_CLOSE`를 보냅니다. pending은 process 안의 ephemeral 상태이며 새 FileSession table을 만들지 않습니다. `filetest`는 계약대로 동작하는 contract peer(fake Connector)이며 실제 SSH/SFTP/VM filesystem은 구현하지 않습니다.
- `internal/server/previewsession`: PreviewSession 생성·종료 use case(LBT-101, api role의 DB-backed authority). 현재 User, LabInstance 소유(강사·Organization ADMIN도 타인의 것은 불가), 현재 ClassMembership, `READY`, immutable CreationSnapshot의 `workspaceVmKey`, 현재 generation의 `PRESENT` `SERVER` ProviderResource(정확히 하나), Connector를 하나의 짧은 transaction(`FOR SHARE`)에서 결정하고 commit한 뒤에만 Connector Control을 시작합니다. `targetPort`는 권한·대상 판정 뒤에 Backend의 명시적 허용 목록(`Policy`, `LABBIT_PREVIEW_ALLOWED_PORTS`)에 있는 정확한 값만 승인하며(숫자 범위 승인 없음, 기본 port 없음, 22는 목록에 있어도 거절, 설정이 없으면 startup 실패·`Policy{}`는 아무것도 승인하지 않음) 이는 process 전체의 global 정책이지 LabSpec별 정책이 아닙니다. 생성은 동기적으로 Gateway 등록 → `PREVIEW_OPEN` → Connector의 Preview Data WSS attach를 기다리며(`PREVIEW_OPEN_RESULT SUCCEEDED`만 믿지 않음) 성공을 돌려주기 전에 LabInstance의 generation과 `READY`를 다시 확인합니다(Reset race는 `ErrTargetChanged`). 실패한 생성은 Gateway 등록과 Router pending을 지우고 Connector가 열었을 수 있으면 `PREVIEW_CLOSE`를 요청합니다. Connector의 `APP_NOT_RUNNING`/`PORT_REJECTED`/`VM_UNREACHABLE`을 각각 안정적인 오류로 옮깁니다. `preview.Lifecycle`을 구현해 SaaS가 시작한 종료(명시적 종료, 만료, Reset/Cleanup, 서비스 재시작, 프로토콜 위반)에서만 `PREVIEW_CLOSE`를 보냅니다. Reset/Cleanup이 호출할 경계(`CloseForLabMutation`)를 제공하지만 Reset/Cleanup 자체는 구현하지 않습니다. PreviewSession을 PostgreSQL에 저장하지 않으며 Preview 본문·경로·credential을 알지 못합니다.
- `internal/server/preview`: Preview Gateway(LBT-101, preview role). **PostgreSQL, repository, auth, connector package를 import하지 않으며**(경계 test가 전이 의존까지 검사) 좁은 interface(`ConnectorAuthenticator`, `Lifecycle`)로만 authority를 받습니다. PreviewSession별 ephemeral 상태를 `Expect`(권한 검증과 port 승인을 마친 정확한 correlation) → `Bind`(PREVIEW_OPEN을 전달한 Control Session) → Data attach → `Activate`로 관리합니다. Connector Preview Data WSS(`/connector/v1/preview-data`, `labbit.connector-preview.v1`)는 Upgrade의 Connector Credential 인증 결과, **PREVIEW_OPEN을 전달한 Control Session을 인증한 Credential**, `previewSessionId`, `labInstanceId`, `generation`, Workspace VM(`targetVmKey`, `providerServerId`), 승인된 `targetPort`가 모두 기대한 값일 때만 attach하고 하나라도 다르면 그 connection만 `1008`로 거절합니다(다른 PreviewSession으로 fallback하지 않음). attach 뒤에는 Binary byte stream만 허용하며(Text frame은 위반) `net.Pipe` 기반 `net.Conn`으로 Gateway의 HTTP/1.1 Transport에 연결하므로 별도 queue나 history가 없고 WSS 하나가 TCP 연결 하나입니다. 개별 TCP/Data WSS 연결이 종료되어도 logical PreviewSession은 유지되고 후속 HTTP 요청 시 새 sequential tunnel(0..N)을 열 수 있습니다. Credential revoke는 4001, Control Session 교체는 4002, 서비스 종료는 1001로 tunnel을 닫습니다. Preview Origin(`LABBIT_PREVIEW_ORIGIN_TEMPLATE`, 예 `https://{sessionId}.preview.example.com`, 실제 hostname은 Platform이 정함)은 request Host로 SaaS 본 서비스와 가르며, Public Suffix 기반으로 본 서비스 Origin(`LABBIT_PUBLIC_ORIGIN`)과 separate-origin이면서 same-site인지 검증합니다(cross-site나 동일 origin은 fail-closed). `/__labbit/` prefix만 Gateway가 처리하며 `/__labbit/bootstrap`은 정적 HTML이고(CSP는 script hash만 허용, fragment를 주소에서 지움) `/__labbit/exchange`는 같은 Origin의 일회용 bootstrap credential(CSPRNG 32 bytes, digest만 저장, 짧은 TTL, 한 번만 성공)을 Preview 전용 Cookie(`__Host-labbit-preview`, HttpOnly, Path=/, Domain 없음, HTTPS에서 Secure, SameSite=Lax; digest 비교)로 바꿉니다. 그 밖의 모든 요청은 Cookie로 인증한 뒤 Workspace application으로 전달하며 Preview Cookie와 로그인 Session Cookie는 제거하고 application의 Cookie와 Authorization은 보존하며 application의 `Set-Cookie`는 `Domain`을 제거해 host-only로 유지합니다. HTTP Upgrade는 `501`로 거절하고 Browser의 요청 중단이 tunnel을 끊지 않도록 응답을 끝까지 읽습니다. 만료·종료는 같은 `endSession` 경로 하나로 credential, tunnel, transport를 한꺼번에 무효로 합니다. 상태는 process 안의 ephemeral 상태이며 경로·query·Cookie·본문은 log나 trace에 남기지 않습니다. `previewtest`는 계약대로 동작하는 contract peer(fake Connector)이며 실제 SSH TCP forwarding은 구현하지 않습니다(LBT-24).
- `internal/server/realtime`: Browser Terminal WSS(`/realtime/v1/terminal`)와 Connector Terminal Data WSS(`/connector/v1/terminal-data`)의 transport와 **ephemeral** relay 상태입니다. **PostgreSQL, repository, auth, connector package를 import하지 않으며**(경계 test가 전이 의존까지 검사) 자신이 정의한 좁은 interface(`Control`, `ConnectorAuthenticator`)로만 DB-backed authority를 받습니다. TerminalSession마다 active Browser attachment 하나와 Connector data channel 하나, 60초 grace timer(`Clock` 주입)를 가지며 connection마다 writer goroutine이 하나라 concurrent write가 없습니다. Browser attachment의 bounded queue는 전송 중인 byte를 잠시 담을 뿐 history가 아니며, 한도를 넘으면 그 attachment만 `SLOW_CONSUMER`/4005로 종료하고 PTY는 grace를 따릅니다. Terminal INPUT/OUTPUT·token은 저장·재생·log하지 않습니다. v0.1에서는 같은 process의 api role이 `Control` 구현을 제공합니다(runtime/contract.yaml `saas.realtime`).
- `internal/server/jsonnum`: JSON Schema 2020-12의 integer 규칙(1.0, 1e2도 integer, maximum 없음)을 lexical하게 판정하는 작은 leaf package입니다. Schema에 없는 상한을 만들지 않으려고 cols/rows 원문을 그대로 전달하는 데 씁니다.
- `internal/server/app`: `newControlStack`이 Auth, Class, Connector Control, Workspace file(`workspacefile.Service` + `filetransport.Broker`, api role만 필요)과(realtime role이 같은 process에 있을 때) Terminal Relay를 하나의 조립으로 묶습니다. Registry의 revoke observer 자리는 하나이므로 Terminal Data WSS와 File Data WSS에 함께 전달합니다. `Run`과 통합 test가 같은 조립을 씁니다. shutdown은 Relay가 active TerminalSession을 `SERVICE_RESTARTING`으로 종료해 Connector에 `TERMINAL_CLOSE`를 보낸 뒤에 Connector Control connection을 닫습니다. realtime role만 enabled된 process는 DB DSN 없이 시작하지만 Terminal route를 열지 않고 `/readyz`가 실패합니다. preview role도 같습니다. `api`와 `preview`가 같은 process에 있으면 `previewsession.Service`와 `preview.Gateway`를 함께 조립하고(서로를 필요로 하므로 `SetLifecycle`로 순환을 끊음), Registry의 revoke observer(Terminal/File/Preview에 함께 전달)와 `SessionObserver`(Control Session 교체·revoke → Gateway)를 연결하며 `applicationHandler`가 request Host로 Preview Origin 요청을 Gateway로 보냅니다(본 서비스 route에 도달하지 못함). shutdown은 Connector Control connection을 닫기 전에 Gateway가 active PreviewSession을 `SERVICE_RESTARTING`으로 끝내며 `PREVIEW_CLOSE`를 동기로 보냅니다. preview role이 enabled되면 허용 port, TTL, Origin template이 모두 있어야 시작하고(없거나 틀리면 실패) production에서는 https Origin이어야 하며 `LABBIT_PUBLIC_ORIGIN`과 separate-origin이면서 same-site여야 합니다(cross-site나 동일 origin은 fail-closed).
- `internal/observability/tracing`: SaaS의 OpenTelemetry SDK `TracerProvider`와 선택적 OTLP exporter(`grpc`, `http/protobuf`) 조립(LBT-144). SDK와 exporter module은 이 package와 `internal/server/app`만 import하며 제품 package는 `trace.Tracer`(API)와 `internal/observability/spanattr`의 attribute key만 씁니다. 아래 "Trace (OpenTelemetry) 구현"을 참고합니다.
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

## Trace (OpenTelemetry) 구현 (LBT-144, 계약 아님)

계약의 원본은 `runtime/contract.yaml`(`saas.config.OTEL_*`, `saas.observability.tracing`)과 D-25입니다. 이 절은 그 계약을 현재 코드가 어떻게 구현하는지의 기록이며 계약을 바꾸지 않습니다.

- **조립**: `app.Run`이 `tracing.Start`로 `TracerProvider`를 만들고 `Tracer`만 `httpapi`와 `terminal.Service`에 넘깁니다. global `TracerProvider`와 global propagator는 쓰지 않습니다(`tracecontext` package가 W3C 정상화를 계속 소유). Connector binary는 SDK/exporter/gRPC를 포함하지 않으며 `internal/connector/app`의 dependency test가 이를 고정합니다.
- **`none`과 `otlp`**: `none`(기본)은 외부 export만 끕니다. SDK `TracerProvider`와 유효한 `SpanContext`, Connector wire의 `traceparent`/`tracestate`, log의 `trace_id`는 그대로입니다. `otlp`는 `OTEL_EXPORTER_OTLP_TRACES_ENDPOINT`(userinfo 없는 http/https URL)와 `OTEL_EXPORTER_OTLP_TRACES_PROTOCOL`(`grpc`, `http/protobuf`)이 모두 있어야 합니다. endpoint는 검증해 exporter에 명시로 넘기고, header·CA·client certificate/key·timeout은 official exporter가 표준 `OTEL_EXPORTER_OTLP_TRACES_*`에서 직접 읽습니다. 값은 코드에 hard-code하지 않습니다.
- **오류는 업무 오류가 아님**: 잘못된 exporter 값, endpoint/protocol 누락·오류, 잘못된 sampler/sampler_arg 설정(`OTEL_TRACES_SAMPLER`, `OTEL_TRACES_SAMPLER_ARG`), 읽을 수 없는 인증서, 잘못된 header, exporter 생성 실패는 모두 `labbit-server` 시작을 막지 않고 안전한 진단(환경변수 **이름**과 분류 code) 뒤 export만 끕니다(fail-closed). 특히 sampler typo가 upstream의 `ParentBased(AlwaysSample)` fallback과 맞물려 active exporter 상태로 남는 것을 방지하며, 버려지는 첫 `TracerProvider`의 `BatchSpanProcessor`(goroutine/timer) 및 exporter 수명은 bounded `provider.Shutdown`으로 완전하게 정리하여 자원 누수를 막습니다. 값 원문(sentinel 포함)은 로그에 남기지 않습니다. 일부 설정으로 계속 export하지 않습니다(예: header 해석 실패 시 인증 없이 보내지 않음). `/livez`, `/readyz`, 업무 요청은 Collector와 무관합니다. PostgreSQL 같은 업무 의존성 실패는 그대로 `/readyz`에 드러납니다.
- **진단과 Secret**: SDK/exporter는 process-global `otel.Handle`과 internal logger로 오류를 내보내며 기본 구현은 `err.Error()`나 입력 원문(header 한 쌍, 파일 경로, Collector 응답 본문)을 stderr로 출력합니다. `tracing`이 둘 다 분류 code(`collector_unreachable`, `export_timeout`, `export_rejected`, `invalid_headers`, `invalid_sampler` 등)만 기록하는 handler로 교체하고 같은 code는 1분에 한 번만 기록합니다. 이는 process-global 상태이므로 `tracing` package의 test는 병렬 실행하지 않습니다.
- **Span 경계(현재 실제 경로)**: HTTP server Span(`httpapi`의 직접 구현 middleware, 이름 `HTTP {METHOD} {route template}`, `/api/v1` 아래만), `TERMINAL_OPEN`/`TERMINAL_CLOSE` Connector command client Span, `TERMINAL_OPEN_RESULT` 수신 Span(command Span의 자식, 계약 §9). OpenTelemetry HTTP 자동 계측(`otelhttp`)은 `url.query`를 수집해 Workspace file `path`가 trace에 남을 수 있어 쓰지 않습니다. Span attribute는 method, route template, status code, 제품 ID, 고정 분류뿐이며 raw URL·query·header·body·Terminal 내용은 없습니다. `TERMINAL_OPEN_RESULT`는 pending command가 존재할 때 local command SpanContext를 authoritative trace로 삼아 result Span과 실패 보고 로그 양쪽의 `trace_id`를 동일하게 유지합니다. pending waiter가 이미 사라진 late result는 새 result Span을 만들지 않으나, Connector가 보낸 유효한 inbound Trace가 있다면 실패 보고 로그의 `trace_id`로 보존합니다(LBT-143 호환). Connector가 돌려준 Trace Context는 전파 검증 메타데이터(`labbit.connector.result_trace`: `same_trace`, `different_trace`, `absent`)로만 취급하며, 동일 Trace ID라도 span ID/flags/tracestate가 변경된 경우 완전한 전파 보존(`same_trace`)으로 보지 않습니다.
- **Export와 종료**: `BatchSpanProcessor`(SDK 기본 queue/batch/timeout)라 요청 경로는 network I/O를 기다리지 않고 queue가 차면 Span을 버립니다. `OTEL_BSP_*`는 Labbit-owned contract config가 아닌 upstream SDK의 optional tuning knob로 취급하며, upstream SDK 규칙에 따라 유효하지 않은 정수는 SDK 기본값으로 fallback합니다(Labbit의 trace 설정 fail-closed 정책과 분리). 종료는 업무 HTTP/WSS drain과 정리가 끝난 뒤 `LABBIT_SHUTDOWN_GRACE`의 **남은** budget 안에서만 flush하며 실패해도 `Run`의 결과를 바꾸지 않습니다. Collector가 응답하지 않으면(특히 gRPC exporter의 retry backoff) 종료가 남은 budget 전체까지 걸릴 수 있습니다. 그 상한을 줄이려면 표준 `OTEL_BSP_EXPORT_TIMEOUT`을 **정수 밀리초**로 씁니다(예: `300`; `300ms`는 upstream fallback으로 기본 30초 복귀). Integration test가 gRPC Collector 불능에서 기본값 3초(= test의 grace) 대비 약 0.3초 종료를 확인합니다.
- **아직 없는 경로**: Operation 등록 HTTP(LBT-17)와 durable Worker(LBT-18)가 없으므로 `operations.traceparent/tracestate` 저장·복원과 Worker Span은 구현하지 않았습니다. 그 경로는 같은 `Tracer`와 `tracecontext`를 재사용해 이어 붙입니다.

로컬에서 Collector로 보내 보려면(예시는 현재 Local 플랫폼 measured 값이며 코드 기본값이 아닙니다. 실제 값은 플랫폼 상태를 다시 확인합니다):

```bash
OTEL_TRACES_EXPORTER=otlp \
OTEL_EXPORTER_OTLP_TRACES_PROTOCOL=http/protobuf \
OTEL_EXPORTER_OTLP_TRACES_ENDPOINT=http://alloy.observability.svc.cluster.local:4318/v1/traces \
make server
```

## 테스트

- Application rule: Repository/Port fake를 사용한 Go unit test
- HTTP transport: httptest 기반 request/response 계약 test
- PostgreSQL constraint/session/query: 실제 PostgreSQL Integration Test
- Workspace file: `workspacefile`은 Repository/Transport fake로 경로 규칙·권한 판정·Reset race·본문 검증을, `filetransport`는 실제 Connector Control handler와 `connector.Router`/`Registry` 위에서 contract peer(`filetest`)로 capability·correlation·framing·취소/시간 초과/revoke 정리를, `httpapi`는 handler의 query/If-Match/body/Problem Details를, `app`의 integration test는 실제 PostgreSQL과 contract peer로 HTTP → 권한 → target 결정 → Control → File Data WSS → HTTP 전체를 검증합니다. 이 검증은 실제 Workspace VM의 SSH/SFTP가 동작한다는 뜻이 아닙니다(LBT-21).
- PreviewSession: `previewsession`은 Store/Connectors/Gateway fake로 권한 판정 전체(소유, 타 Organization, Membership, READY, CreationSnapshot 모순, ProviderResource 없음/중복, Reset race)와 허용 port 정책(목록, 22, 비설정 fail closed, 형식 오류, 범위 불가), Connector 결과·시간 초과·취소·정리를 검증합니다. `preview`는 실제 WebSocket·HTTP 서버와 실제 TCP fake application으로 attach 거절 전체, bootstrap/Cookie 인증, proxy, credential stripping, 오류 매핑, 만료·종료·revoke·Control Session 교체·shutdown, 민감정보 비노출을 검증하고 `app`의 integration test는 실제 PostgreSQL과 contract peer(`previewtest`)로 HTTP → 권한 → 허용 port 승인 → Workspace VM 결정 → PREVIEW_OPEN → Preview Data WSS → bootstrap → Gateway proxy 전체와 negative 흐름을 검증합니다. 이 검증은 실제 Workspace VM의 SSH TCP forwarding이 동작한다는 뜻이 아닙니다(LBT-24, 공동 통합은 LBT-25 C3).
- Terminal WSS: 실제 WebSocket peer와 주입한 `Clock`으로 protocol/auth/grace를 검증합니다(60초를 기다리지 않습니다). Connector 쪽은 계약대로 동작하는 contract peer(`terminal/terminaltest`)입니다. 실제 VM 검증은 두 단계로 구분합니다: LBT-20에서 Connector→실제 Workspace VM SSH/PTY INPUT/OUTPUT·resize·close를 먼저 검증하고, 이후 LBT-22(C2)에서 Browser/SaaS → Connector → 실제 VM 전체 왕복과 reconnect/close를 공동 통합 검증합니다.
- Frontend 실제 consumer: LBT-12에서 HTTP mode E2E

테스트 원칙 전체는 ../../TESTING.md를 따릅니다.
