# Labbit Connector WSS Contract

이 디렉터리는 **Labbit SaaS ↔ 고객 환경 Connector의 WebSocket 계약 SSOT**를 관리합니다.

- Control 전송·인증·연결 수명·호환성: `README.md`
- 기본 Control 메시지: `connector.schema.json`
- TerminalSession lifecycle Control 메시지: `terminal-control.schema.json`
- Terminal Data WSS JSON control frame: `terminal-data.schema.json`

Terminal/Live의 Browser-facing 계약은 `contracts/realtime/README.md` + `terminal-live.schema.json`이 원본입니다.

Terminal/Live INPUT/OUTPUT과 Preview 본문은 **persistent Control WSS에 싣지 않습니다.** Control에는 lifecycle/metadata만 전달하고 실제 PTY byte stream은 별도 Terminal Data WSS를 사용합니다.

## 1. Control 연결 경계

Connector가 고객망에서 중앙 SaaS의 공개 Endpoint로 먼저 연결합니다.

```text
Customer Environment                         Central SaaS

Connector
  └─ outbound WSS 443 ────────────────────> Connector Control Endpoint
```

논리 Endpoint:

```text
wss://<saas-host>/connector/v1/control
Sec-WebSocket-Protocol: labbit.connector.v1
```

실제 ALB/Ingress/Gateway, Kubernetes workload, API Pod, Connector-Control 전용 workload,
Valkey registry, Broker/SQS 같은 **내부 connection owner와 routing 구현은 외부 wire contract에 노출하지 않습니다.**

## 2. TLS와 인증

- 평문 `ws://`는 사용하지 않습니다.
- TLS 1.3을 우선하고 TLS 1.2를 허용합니다.
- Connector는 SaaS 서버 인증서와 Hostname을 검증합니다.
- WSS Upgrade 요청에서 `Authorization: Bearer <connector-credential>`을 사용합니다.
- URL query string에 Credential/Token을 넣지 않습니다.
- Enrollment Token과 OpenStack Credential은 Control/Data WSS 인증에 사용하지 않습니다.
- Connector Credential은 OpenStack Provider Credential과 별도입니다.
- Credential이 revoke되면 현재 Control/Data 연결을 더 이상 신뢰하지 않고 종료하며 이후 같은 Credential 인증을 거절합니다.

Connector identity는 인증된 connection에서 SaaS가 결정합니다. 메시지의 임의 `connectorId` 주장을 identity 원본으로 신뢰하지 않습니다.

## 3. 프로토콜 호환성

Control WebSocket subprotocol은 `labbit.connector.v1`입니다.

호환성 원칙:

- v1 안에서는 기존 의미를 깨지 않는 optional field와 capability를 추가할 수 있습니다.
- 수신자는 사용하지 않는 unknown optional field를 무시할 수 있어야 합니다.
- 기존 required field의 타입·의미를 바꾸거나 제거하지 않습니다.
- 새 message/action을 기존 Connector가 안전하게 처리할 수 없는 경우 capability negotiation 또는 새 major protocol을 사용합니다.
- SaaS와 고객 환경 Connector가 항상 동시에 배포된다고 가정하지 않습니다.

Control WSS의 JSON message validation은 다음 두 Schema 집합을 사용합니다.

```text
connector.schema.json
+ terminal-control.schema.json
```

Terminal Data WSS는 별도 `terminal-data.schema.json`을 사용합니다.

### JSON Text application message 크기 제한

Control WSS와 Terminal lifecycle/Data WSS의 **JSON Text application message는 WebSocket fragmentation 재조립 후 최대 1 MiB(1,048,576 bytes)** 입니다. 이 제한은 JSON decode, Schema validation, 선택 Trace metadata 정상화보다 먼저 적용합니다.

- 수신 구현은 read limit을 먼저 설정해 최대 크기를 넘는 JSON Text message 전체를 메모리에 무제한 적재하지 않습니다.
- 1 MiB를 넘으면 해당 message를 파싱하거나 `traceparent`/`tracestate`를 제거해 계속 처리하지 않고 WebSocket close code **1009 (Message Too Big)** 로 연결을 종료할 수 있습니다. 별도 `ERROR` frame 전송은 요구하지 않습니다.
- Schema의 `traceparent` 512자 / `tracestate` 1024자 제한은 이 전체 message guard를 통과한 뒤 적용되는 field 수준 검증입니다.
- Terminal PTY Binary byte stream은 이 JSON Text 한도의 대상이 아닙니다. Binary transport도 구현에서 bounded read/write를 사용하지만 별도 application payload 한도는 부하 테스트와 Runtime에서 검증합니다.

이 한도를 넘는 정상 control payload가 필요해지면 v1 구현마다 임의 값을 키우지 않고 Connector 계약을 먼저 변경합니다.

## 4. Control 연결 수명

연결이 성립되면 Connector가 `HELLO`를 보내고 SaaS가 `HELLO_ACK`로 현재 heartbeat 설정을 전달합니다.

```text
WSS Upgrade + Connector Credential 인증
        ↓
HELLO
        ↓
HELLO_ACK
        ↓
필요 시 Reconciliation
        ↓
ONLINE
```

현재 기본 Heartbeat 기준은 **15초 주기 / 45초 미수신 시 OFFLINE**입니다. 실제 연결에서는 `HELLO_ACK`가 전달한 설정값을 사용합니다.

WebSocket Ping/Pong은 transport health 용도이고 `HEARTBEAT`은 Connector application 상태를 판단하는 별도 메시지입니다.

### 중복 Control 연결

MVP 기준으로 **Connector 하나당 active Control connection은 1개**입니다.

같은 Connector Credential로 새 Control connection 인증이 성공하면 새 연결을 current connection으로 사용하고 이전 연결은 종료합니다.

### 재접속

비정상 종료 후 Connector는 exponential backoff + jitter를 적용해 재접속해야 합니다. 정확한 수치는 Runtime 설정에서 관리합니다.

**재접속은 Operation Retry가 아닙니다.** 연결이 다시 살아났다는 이유만으로 Provision/Reset/Cleanup을 다시 실행하지 않습니다.

## 5. 기본 Control 메시지

| Message | 방향 | 의미 |
| --- | --- | --- |
| `HELLO` | Connector → SaaS | Connector version/runtime 정보와 capability 전달 |
| `HELLO_ACK` | SaaS → Connector | 연결 수락 후 heartbeat 설정 전달 |
| `HEARTBEAT` | Connector → SaaS | Connector application 생존 확인 |
| `PROVIDER_REQUEST` | SaaS → Connector | Provider 연결 검증·Image/Flavor 조회 |
| `PROVIDER_RESPONSE` | Connector → SaaS | Provider 조회 결과 |
| `OPERATION_COMMAND` | SaaS → Connector | Provision / Reset / Cleanup 실행 명령 |
| `OPERATION_ACK` | Connector → SaaS | 명령을 수신·수락했는지 확인 |
| `OPERATION_PROGRESS` | Connector → SaaS | 작업의 현재 stage 전달 |
| `OPERATION_RESULT` | Connector → SaaS | 성공·실패·결과 불명과 ProviderResource 전달 |
| `RECONCILE_REQUEST` | SaaS → Connector | DB 기록과 실제 Provider 현실 대조 요청 |
| `RECONCILE_RESULT` | Connector → SaaS | OpenStack에서 관측한 실제 리소스 상태 반환 |
| `ERROR` | 양방향 | Protocol/control 수준 오류 |

`OPERATION_ACK`는 Provider 작업 성공을 의미하지 않습니다. 실제 결과는 `OPERATION_RESULT`로 판단합니다.

## 6. TerminalSession lifecycle Control

TerminalSession의 생성·종료 신호는 기존 persistent Control WSS를 사용하되 PTY byte stream과 분리합니다.

| Message | 방향 | 의미 |
| --- | --- | --- |
| `TERMINAL_OPEN` | SaaS → Connector | 권한 검증이 끝난 resolved VM target에 PTY 준비 요청 |
| `TERMINAL_OPEN_RESULT` | Connector → SaaS | PTY와 Terminal Data WSS attach 준비 결과 |
| `TERMINAL_CLOSE` | SaaS → Connector | 명시적 종료·grace 만료·Reset/Cleanup에 따른 PTY 종료 |
| `TERMINAL_ENDED` | Connector → SaaS | Shell/PTY/SSH channel이 실제 종료됐음을 통지 |

정확한 필드는 `terminal-control.schema.json`이 원본입니다.

### 중복 lifecycle command

`terminalSessionId`를 Terminal lifecycle의 안정적인 식별자로 사용합니다.

- 같은 active `terminalSessionId`에 `TERMINAL_OPEN`이 중복 전달돼도 새 PTY를 추가 생성하지 않습니다.
- 이미 종료된 TerminalSession을 중복 OPEN으로 자동 부활시키지 않습니다.
- `TERMINAL_CLOSE` 재전달은 같은 terminal state를 유지하도록 idempotent하게 처리합니다.

Browser Session Cookie, Password, Terminal Session Token은 Connector로 전달하지 않습니다. SaaS가 사용자 권한을 검증한 뒤 `labInstanceId`, `generation`, `targetVmKey`, 현재 Provider Server 식별자와 초기 PTY size 같은 resolved target만 전달합니다.

## 7. Terminal Data WSS

실제 Terminal INPUT/OUTPUT은 TerminalSession별 별도 outbound Data WSS를 사용합니다.

```text
Connector
   └─ outbound WSS 443 ────────────────────> Terminal / Live Relay

wss://<saas-host>/connector/v1/terminal-data
Sec-WebSocket-Protocol: labbit.connector-terminal.v1
Authorization: Bearer <connector-credential>
```

MVP에서는 **active TerminalSession 하나당 Terminal Data WSS 하나**를 사용하고 여러 TerminalSession을 하나의 Data WSS에 multiplex하지 않습니다.

WSS Upgrade 인증 후 Connector가 `TERMINAL_DATA_ATTACH`를 보내 현재 `terminalSessionId`에 data channel을 bind합니다.

JSON Text control frame:

- `TERMINAL_DATA_ATTACH`
- `TERMINAL_DATA_ATTACHED`
- `TERMINAL_DATA_RESIZE`
- `TERMINAL_DATA_CLOSE`
- `TERMINAL_DATA_ENDED`
- `ERROR`

Binary frame:

```text
Relay     → Connector : PTY INPUT raw bytes
Connector → Relay     : PTY OUTPUT raw bytes
```

Binary frame 경계는 line, UTF-8 문자, ANSI escape sequence 경계가 아닙니다. byte stream으로 처리합니다.

### Data WSS 재접속

Terminal Data WSS의 transport가 비정상 종료되어도 PTY 자체 종료로 즉시 간주하지 않습니다. TerminalSession이 살아 있는 동안 Connector는 Data WSS를 다시 연결할 수 있습니다.

재연결은 OUTPUT replay가 아닙니다. data connection이 끊긴 동안 발생한 OUTPUT을 중앙 Relay가 history로 복원하거나 Connector가 무제한 보관하지 않습니다.

같은 TerminalSession의 새 인증 Data WSS attach가 성공하면 새 data connection을 current connection으로 사용할 수 있습니다.

## 8. Live와 Connector의 경계

Connector는 학생별 Live connection을 알 필요가 없습니다.

```text
Connector PTY OUTPUT
        ↓
Terminal Data WSS
        ↓
SaaS Relay
        ├─ Terminal owner Browser
        ├─ Live Student A
        ├─ Live Student B
        └─ Live Student C
```

Live fan-out, STUDENT 권한, 학생별 bounded Queue, slow consumer 처리는 중앙 Relay 책임입니다. Connector에 별도 Live stream을 만들지 않습니다.

## 9. 공통 Correlation

기본 Control 메시지는 `connector.schema.json`, Terminal lifecycle/Data 메시지는 각 전용 Schema의 Envelope를 따릅니다.

핵심 식별자 역할:

- `messageId`: 한 번의 JSON control frame 식별
- `operationId`: Provision/Reset/Cleanup durable 작업 correlation
- `labInstanceId`: 실제 실습 환경
- `generation`: Provider Resource 세대
- `terminalSessionId`: PTY/TerminalSession lifecycle correlation
- `requestId`: 원본 HTTP control request와 연결 가능한 경우
- `traceparent` / `tracestate`: W3C Trace Context

PTY Binary content에 Trace Context나 correlation envelope를 붙이지 않습니다.

### D-25: MVP Trace 참여 방식

**SaaS는 OTel Span 생성·OTLP export, Connector는 propagation-only**입니다. Connector에 중앙 Collector/Tempo로 직접 전송하는 exporter나 추가 인증/외부 연결을 만들지 않습니다. 경량 propagator/API 라이브러리 사용은 가능하지만 Connector 로컬 Span을 중앙에 보내는 요구는 아닙니다. Runtime 전송 설정과 API → Worker durable Context는 [Runtime Contract](../../runtime/README.md)가 원본입니다.

| 경계 | 전파 규칙 |
| --- | --- |
| SaaS → `OPERATION_COMMAND` | 유효한 현재 command Span Context를 기존 optional `traceparent`와 가능한 `tracestate`로 전달합니다. |
| Connector → `OPERATION_ACK` / `OPERATION_PROGRESS` / `OPERATION_RESULT` | 해당 명령의 유효한 Context를 보존해 반환합니다. propagation-only Connector는 새 Span을 가장하거나 parent-id를 임의로 생성하지 않습니다. |
| `RECONCILE_REQUEST` / `RECONCILE_RESULT`, Provider 조회, Terminal JSON control | 요청/응답 관계가 있는 경우 같은 원칙을 적용합니다. 새 제어 작업은 해당 작업의 Context를 사용합니다. |
| Connector 로컬 로그 | 유효한 Context의 `trace_id`와 알 수 있는 제품 ID를 기록합니다. Context가 없으면 가짜 Trace ID를 채우지 않습니다. |

Context는 **connection 전역이 아니라 명령/작업별**로 관리합니다. 하나의 Operation이 여러 LabInstance로 fan-out될 수 있으므로 `operationId`만으로 Context를 덮어쓰지 않습니다. 인증된 Connector와 `operationId`, `labInstanceId`, `generation`, 적용 가능한 `messageId`/`replyToMessageId` 관계를 함께 사용해 병렬 명령과 응답을 구분합니다. handshake Trace를 모든 명령에 재사용하지 않습니다.

같은 Trace의 SaaS Span들은 서로 다른 Span ID를 가집니다. 따라서 모든 구간의 `traceparent` 문자열이 같아야 하는 것은 아닙니다. Connector는 자신이 받은 명령 Context를 해당 결과까지 보존하고, SaaS는 결과 수신 시 자신의 새 Span을 생성합니다. 미샘플링 Context도 전파하며 `sampled=1`로 강제 변경하지 않습니다.

재접속/프로세스 재시작으로 Context를 복구하지 못해도 제품 ID와 Reconciliation 계약을 유지합니다. Trace 유실을 Provider mutation 재실행 근거로 삼지 않습니다. 늦은 결과를 현재의 다른 명령 Context로 잘못 연결하지 않습니다.

### 선택 Trace metadata의 오류 처리

`traceparent`/`tracestate`는 기존 Schema에서 **optional을 유지**합니다. 다음 규칙은 producer가 올바른 값을 전송해야 한다는 요구를 완화하지 않으며, consumer가 관측 오류를 업무 실패로 확대하지 않기 위한 처리 규칙입니다.

1. 인증 후 위의 **1 MiB JSON Text application message 한도**를 JSON decode·Trace field 정상화보다 먼저 적용합니다. 초과 message는 1009로 종료하며 비정상 JSON, 인증 실패, 필수 업무 field 오류는 기존 방식으로 거부합니다.
2. Schema validation/강타입 decoding 전에 선택 Trace field만 정상화할 수 있어야 합니다. 값의 타입·길이·W3C 유효성이 잘못되면 그 관측 field를 제거한 뒤 나머지 업무 Envelope를 정상 검증합니다. 기존 field 길이 제한을 늘리거나 payload 전체를 검증에서 제외하지 않습니다.
3. `traceparent`가 없거나 유효하지 않으면 `tracestate`도 사용하지 않습니다. `traceparent`는 유효하고 `tracestate`만 잘못됐으면 `tracestate`만 폐기합니다. W3C 유효성은 표준 propagator/parser로 확인합니다.
4. Trace 문제만으로 `OPERATION_ACK` 거절, `OPERATION_RESULT=FAILED`, WSS 종료 또는 Provider retry를 발생시키지 않습니다. 잘못된 값 원문은 로그에 복사하지 않고 안전한 진단만 남깁니다.

Trace Context는 인증·tenant 판정·멱등성 key가 아닙니다. Baggage, Token, 사용자 입력/코드, Provider raw payload를 Trace field에 싣지 않습니다. 본문 수집 금지와 낮은 cardinality metric 정책은 그대로 유지합니다.

표준 참고: [W3C Trace Context — Processing Model](https://www.w3.org/TR/trace-context/#processing-model).

## 10. Operation Command 단위

**하나의 `OPERATION_COMMAND`는 하나의 LabInstance mutation만 실행합니다.**

```text
LabExecution Provision Operation
  ├─ Instructor LabInstance command
  ├─ Student A LabInstance command
  └─ Student B LabInstance command
```

Connector에는 Nova/Neutron raw request를 그대로 전달하지 않습니다. SaaS는 `PROVISION`, `RESET`, `CLEANUP`이라는 Labbit domain command와 resolved 입력을 전달하고 Connector 내부 OpenStackProvider Adapter가 실제 Provider API 호출 순서를 담당합니다.

### Control ↔ Provider 내부 경계

두 Connector 모듈을 연결할 때는 `internal/connector/provider`의 `Provider` 인터페이스를 기준으로 합니다. Control/WSS는 wire 메시지를 검증하고 `OperationCommand`로 변환해 `DispatchOperation`을 호출합니다. `RECONCILE_REQUEST`는 내부 `ReconcileRequest`로 변환해 `DispatchReconcile`을 호출합니다. OpenStack API 호출과 실제 side effect 판단은 Provider가 담당하며, Control/WSS에서 Gophercloud를 직접 호출하지 않습니다. Control 테스트는 이 경계에 Mock Provider를 연결할 수 있어야 합니다.

- Control은 wire의 `operationId`, `labInstanceId`, `generation`을 Provider 요청에 전달하고, `requestId` 및 trace context를 Control 응답까지 보존합니다.
- Provider는 OpenStack 작업 결과를 `SUCCEEDED` / `FAILED` / `UNKNOWN`으로 분류하고 관측한 Provider Resource 식별자를 반환합니다. 오류 정보가 필요하면 노출 가능한 `SafeError`만 반환합니다. 실패가 확정된 경우 `FAILED`, side effect 여부가 불명확한 경우 `UNKNOWN`입니다. 분류되지 않은 내부 Go 오류나 Provider raw 오류 원문을 wire 응답에 노출하지 않습니다.
- Control은 Provider 결과를 `OPERATION_RESULT`로 변환하며, `UNKNOWN`을 동일 Create/Delete의 자동 재시도로 바꾸지 않습니다. 이후 SaaS가 `RECONCILE_REQUEST`를 보내면 Control이 Provider 조회로 연결합니다.
- `discoverCandidates`가 생략된 `RECONCILE_REQUEST`는 Control 변환 단계에서 `true`로 적용하고, 명시적인 `false`는 그대로 전달합니다.
- Mock Provider 메서드가 설정되지 않은 경우 Dispatcher는 내부 설정 오류를 호출자에게 반환해 테스트가 실패하게 합니다. 이를 Provider 작업의 `UNKNOWN`으로 취급하지 않으며 오류 원문을 wire에 싣지 않습니다. Reconcile의 미분류 내부 오류는 `DispatchReconcile`이 원문을 숨기고 `RECONCILE_RESULT`의 일반 `SafeError`(`PROVIDER_RECONCILE_UNAVAILABLE`)와 빈 `observations`로 변환합니다. 조회 실패만으로 리소스 부재를 확정하지 않습니다.

이 경계는 Connector 내부 역할 분담입니다. 위 규칙으로 새로운 wire 필드나 메시지를 추가하지 않습니다. 메시지 구조와 필수 필드는 `connector.schema.json`을 따릅니다.

## 11. CreationSnapshot과 Reset

Provision/Reset에서 사용하는 `creationSnapshot`은 D-19의 immutable resolved CreationSnapshot입니다.

Reset에서 최신 LabSpec이나 비슷한 최신 Image를 다시 선택하지 않습니다. 기존 generation을 파괴하기 전에 원본 Image/Flavor/Provider 연결 등 재현 가능성을 Preflight하고 재현 불가하면 기존 환경을 먼저 삭제하지 않습니다.

## 12. Result와 결과 불명

`OPERATION_RESULT.payload.outcome`은 다음 세 값을 사용합니다.

```text
SUCCEEDED
FAILED
UNKNOWN
```

`UNKNOWN`은 실패가 아니라 Provider side effect 발생 여부를 현재 확정할 수 없어 Reconciliation이 필요한 상태입니다.

응답 유실 때문에 동일 Create/Delete를 자동 재전송하지 않습니다.

## 13. Reconciliation

- PostgreSQL: 제품 소유관계·작업 의도·Operation/ProviderResource 이력
- OpenStack 직접 조회: 실제 리소스 존재·현재 상태의 Provider 현실

`RECONCILE_RESULT`의 `DISCOVERED_CANDIDATE`는 조사 대상이며 Provider tag만으로 제품 소유관계를 자동 확정하거나 자동 삭제하지 않습니다.

## 14. 민감정보와 관측

메시지와 로그에 다음 정보를 포함하지 않습니다.

- OpenStack Credential / Keystone Token
- Connector Credential / Enrollment Token / Authorization Header
- Browser Session Cookie / Password / Terminal Session Token
- Provider raw request/response
- Terminal/Live INPUT/OUTPUT 본문

중앙에서는 Heartbeat, version, reconnect, Operation stage/result, Terminal lifecycle, `error_code`, duration 같은 운영 metadata를 관측하고 필요하면 같은 correlation ID로 Connector 로컬 구조화 로그를 대조합니다.

Connector 내부 OpenStack/VM 접근의 raw log·metric·상세 Span을 중앙에 상시 반출하지 않습니다. 중앙 SaaS의 command 전송/결과 수신 계측은 Connector 내부 개별 OpenStack API 호출 시간을 측정한 것과 다릅니다.

## 15. Control Close 규칙

persistent Control connection의 v1 application close code는 다음을 사용합니다.

| Close code | 의미 |
| --- | --- |
| `4001` | Connector Credential revoke |
| `4002` | 같은 Connector의 새 Control connection이 기존 connection을 대체 |
| `4003` | 지원하지 않는 protocol/subprotocol |
| `4004` | 복구 불가능한 protocol message 오류 |

Terminal Data WSS의 Session 종료 의미는 `terminal-data.schema.json`과 Browser realtime 계약을 따릅니다.

## 16. 검증 기준

- Connector 2개가 연결돼도 command/TerminalSession이 다른 Connector로 전달되지 않습니다.
- 정상 Provision에서 하나의 HTTP Operation과 여러 LabInstance command/result를 연결할 수 있습니다.
- Command 전송 직후 단절/Provider 성공 후 Result 유실에서도 동일 Create를 무조건 반복하지 않습니다.
- 같은 `terminalSessionId`의 중복 OPEN이 새 PTY를 만들지 않습니다.
- TerminalSession마다 독립 Data WSS를 열고 Binary INPUT/OUTPUT을 중계할 수 있습니다.
- Browser detach만으로 PTY가 즉시 종료되지 않습니다.
- Data WSS 재연결에서 과거 OUTPUT replay를 제공하지 않습니다.
- Reset/Cleanup은 관련 TerminalSession을 terminal lifecycle 종료로 처리할 수 있습니다.
- Live 학생 fan-out이 Connector Data protocol로 확산되지 않습니다.
- Credential/Token/Authorization/Provider raw payload/Terminal 본문이 메시지·로그에 남지 않습니다.
- 각 JSON Schema 정상/비정상 message validation이 동작합니다.
- 명령별 유효한 Trace Context가 ACK/PROGRESS/RESULT에 유지되고 병렬 item/다른 generation과 섞이지 않습니다.
- 1 MiB 이하의 message에서 Context 없음/잘못된 타입·길이·W3C 값, 잘못된 tracestate만 존재하는 경우에도 정상 업무 Envelope는 처리됩니다. 전체 JSON Text message 한도 초과, 인증·업무 필드 오류는 계속 거부합니다.
- 미샘플링 Context도 유지하고, Context 유실/재접속을 mutation retry로 처리하지 않습니다.
- Connector가 중앙 OTLP 연결을 만들지 않고, PTY Binary frame에 Trace metadata를 추가하지 않습니다.

위 기준은 후속 producer/consumer 구현의 인수 조건이며 README 변경만으로 검증 완료를 의미하지 않습니다.

## 17. SSOT 경계

Confluence D-17/D-18/D-20/D-21/D-24/D-25는 왜 이런 정책을 택했는지와 제품/운영 의미를 관리합니다.

이 디렉터리는 Connector wire contract를 관리합니다. 메시지명·필드명·required/optional·payload 구조를 Confluence에 복제하지 않습니다.
