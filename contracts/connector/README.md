# Labbit Connector Control WSS Contract

이 디렉터리는 **Labbit SaaS ↔ 고객 환경 Connector의 Control WebSocket 계약 SSOT**를 관리합니다.

- 전송·인증·연결 수명·호환성 규칙: `README.md`
- 메시지 Envelope와 Payload: `connector.schema.json`

Terminal/Live 입출력과 Preview 데이터 본문은 이 계약에 포함하지 않습니다. 해당 데이터 경로는 별도 실시간 계약에서 관리합니다.

## 1. 연결 경계

Connector가 고객망에서 중앙 SaaS의 공개 Endpoint로 먼저 연결합니다.

```text
Customer Environment                         Central SaaS

Connector
  └─ outbound WSS 443 ────────────────────> Connector Control Endpoint
```

MVP Control Endpoint의 논리 경로는 다음과 같습니다.

```text
wss://<saas-host>/connector/v1/control
```

실제 ALB/Ingress/Gateway, Kubernetes workload, API Pod, Connector-Control 전용 workload,
Valkey registry, Broker/SQS 같은 **내부 connection owner와 routing 구현은 이 wire contract에 노출하지 않습니다.**

## 2. TLS와 인증

- 평문 `ws://`는 사용하지 않습니다.
- TLS 1.3을 우선하고 TLS 1.2를 허용합니다.
- Connector는 SaaS 서버 인증서와 Hostname을 검증합니다.
- WSS Upgrade 요청에서 `Authorization: Bearer <connector-credential>`을 사용합니다.
- URL query string에 Credential/Token을 넣지 않습니다.
- Enrollment Token과 OpenStack Credential은 Control WSS 인증에 사용하지 않습니다.
- Connector Credential은 OpenStack Provider Credential과 별도입니다.
- Credential이 revoke되면 현재 연결을 더 이상 신뢰하지 않고 종료하며 이후 같은 Credential 인증을 거절합니다.

Connector identity는 인증된 connection에서 SaaS가 결정합니다. Connector가 각 메시지에서 임의의 `connectorId`를 주장하는 방식으로 신뢰하지 않습니다.

## 3. 프로토콜 버전

WebSocket subprotocol은 다음 값을 사용합니다.

```text
Sec-WebSocket-Protocol: labbit.connector.v1
```

호환성 원칙:

- v1 안에서는 새로운 optional field를 추가할 수 있습니다.
- 수신자는 사용하지 않는 unknown optional field를 무시할 수 있어야 합니다.
- 기존 required field의 타입·의미를 바꾸거나 제거하지 않습니다.
- 새 message/action이 기존 Connector에서 안전하게 무시될 수 없는 경우 capability negotiation 또는 새 major protocol을 사용합니다.
- SaaS와 고객 환경 Connector가 항상 동시에 배포된다고 가정하지 않습니다.

## 4. 연결 수명

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

현재 기본 Heartbeat 기준은 **15초 주기 / 45초 미수신 시 OFFLINE**입니다.
실제 연결에서는 `HELLO_ACK`가 전달한 설정값을 사용합니다.

WebSocket Ping/Pong은 transport health 용도이고, `HEARTBEAT`은 Connector application 상태를 판단하는 별도 메시지입니다.

### 중복 연결

MVP 기준으로 **Connector 하나당 active Control connection은 1개**입니다.

같은 Connector Credential로 새 Control connection 인증이 성공하면 새 연결을 current connection으로 사용하고 이전 연결은 종료합니다.
이 규칙은 SaaS 내부에서 어떤 Pod/workload가 connection을 소유하는지와 무관합니다.

### 재접속

비정상 종료 후 Connector는 exponential backoff + jitter를 적용해 재접속해야 합니다.
정확한 backoff 수치는 Runtime 설정에서 관리하며 이 계약에 고정하지 않습니다.

**재접속은 Operation Retry가 아닙니다.**
연결이 다시 살아났다는 이유만으로 Provision/Reset/Cleanup을 다시 실행하지 않습니다.

## 5. 메시지 종류

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

`OPERATION_ACK`는 **Provider 작업 성공을 의미하지 않습니다.**
실제 작업 결과는 `OPERATION_RESULT`로 판단합니다.

## 6. 공통 Envelope

모든 메시지는 `connector.schema.json`의 공통 Envelope를 사용합니다.

핵심 식별자 역할은 다음과 같습니다.

- `messageId`: 한 번의 WSS 메시지 전송 단위 식별
- `operationId`: HTTP → Worker → Connector까지 유지되는 durable 작업 correlation key
- `labInstanceId`: 실제 변경 대상 실습환경
- `generation`: Reset 전후 Provider Resource 세대
- `requestId`: 원본 HTTP 요청과 연결 가능한 경우 전달
- `traceparent` / `tracestate`: W3C Trace Context

`messageId`와 `operationId`는 서로 대체하지 않습니다.
하나의 LabExecution Provision Operation에서 여러 LabInstance command가 발생할 수 있습니다.

## 7. Operation Command 단위

**하나의 `OPERATION_COMMAND`는 하나의 LabInstance mutation만 실행합니다.**

```text
LabExecution Provision Operation
  ├─ Instructor LabInstance command
  ├─ Student A LabInstance command
  ├─ Student B LabInstance command
  └─ Student C LabInstance command
```

이 구조는 학생 한 명의 실패가 다른 학생의 성공 환경을 자동 rollback하지 않는 정책과
LabInstance 단위 mutation 충돌 방지 정책을 유지합니다.

Connector에는 Nova/Neutron raw request를 그대로 전달하지 않습니다.
SaaS는 `PROVISION`, `RESET`, `CLEANUP`이라는 Labbit domain command와 필요한 resolved 입력만 전달하고,
Connector 내부 OpenStackProvider Adapter가 실제 Provider API 호출 순서를 담당합니다.

## 8. CreationSnapshot과 Reset

Provision/Reset에서 사용하는 `creationSnapshot`은 D-19의 **immutable resolved CreationSnapshot**입니다.

- 실제 Glance Image UUID
- 실제 Flavor ID와 생성 당시 주요 사양
- 구체 VM 목록과 Workspace VM
- 생성 당시 Internet outbound 정책
- 선택적 Startup Script 본문 + SHA-256
- ProviderConnection 식별자

Reset에서 최신 LabSpec이나 비슷한 최신 Image를 다시 선택하지 않습니다.

Reset은 기존 generation을 파괴하기 전에 재현 가능성을 Preflight 해야 합니다.
원본 Image/Flavor/Provider 연결 등 재현 조건이 충족되지 않으면 기존 환경을 먼저 삭제하지 않습니다.

## 9. Result와 결과 불명

`OPERATION_RESULT.payload.outcome`은 다음 세 값을 사용합니다.

```text
SUCCEEDED
FAILED
UNKNOWN
```

`UNKNOWN`은 실패가 아니라 **Provider side effect 발생 여부를 현재 확정할 수 없는 상태**입니다.

예:

```text
SaaS → OPERATION_COMMAND
Connector → OpenStack Create 성공
Result 전송 전에 WSS 단절
```

이 경우 동일 Create를 자동 재전송하지 않습니다.

```text
PostgreSQL Operation / ProviderResource
        +
Connector가 직접 조회한 OpenStack 현실
        ↓
RECONCILIATION
```

을 먼저 수행합니다.

## 10. Reconciliation

`RECONCILE_REQUEST`는 PostgreSQL에 기록된 ProviderResource와 실제 OpenStack 상태를 대조하기 위한 요청입니다.

- PostgreSQL: 제품 소유관계·작업 의도·Operation/ProviderResource 이력
- OpenStack 직접 조회: 리소스가 실제 존재하는지, 현재 어떤 상태인지에 대한 Provider 현실

`RECONCILE_RESULT`는 알려진 ProviderResource의 존재/상태와 추가로 발견된 고아 후보를 반환할 수 있습니다.

`DISCOVERED_CANDIDATE`는 조사 대상일 뿐, Provider tag만으로 제품 소유관계를 자동 확정하거나 자동 삭제하지 않습니다.

## 11. 민감정보와 관측

메시지와 로그에 다음 정보를 포함하지 않습니다.

- OpenStack Credential
- Keystone Token
- Connector Credential
- Enrollment Token
- Authorization Header
- Password
- Provider raw request/response
- Terminal/Live INPUT/OUTPUT 본문

운영 상관관계에는 가능한 시점부터 `request_id`, `operation_id`, `lab_instance_id`, `connector_id`,
`trace_id`와 관련 session ID를 유지합니다.

Connector raw log/raw metric을 중앙으로 상시 전송하는 것은 MVP 기본 범위가 아닙니다.
SaaS가 WSS로 이미 수신하는 Heartbeat, version, reconnect, Operation stage/result, `error_code`, duration 같은 운영 metadata를 중앙 관측하고,
필요하면 같은 correlation ID로 Connector 로컬 구조화 로그를 대조합니다.

## 12. Close 규칙

연결 이후 애플리케이션 수준 종료 사유가 필요한 경우 다음 v1 close code를 사용합니다.

| Close code | 의미 |
| --- | --- |
| `4001` | Connector Credential이 revoke되어 연결 종료 |
| `4002` | 같은 Connector의 새 Control connection이 current connection을 대체 |
| `4003` | 지원하지 않는 protocol/subprotocol |
| `4004` | 복구 불가능한 protocol message 오류 |

일시적인 네트워크 단절은 application close frame 없이 발생할 수 있으므로 Connector는 abnormal disconnect도 처리해야 합니다.

## 13. 검증 기준

v0.1 구현 시 최소한 다음 시나리오를 검증합니다.

- Connector 2개가 동시에 연결돼도 command가 다른 Connector로 전달되지 않습니다.
- 정상 Provision에서 하나의 HTTP Operation과 여러 LabInstance command/result를 연결할 수 있습니다.
- 같은 LabInstance에 충돌 mutation을 동시에 실행하지 않습니다.
- Command 전송 직후 연결이 끊겨도 동일 Provider Create를 무조건 반복하지 않습니다.
- Provider 성공 후 Result 유실 시 Reconciliation으로 기존 리소스를 발견합니다.
- Worker/SaaS 재시작 뒤 durable Operation과 Provider 현실을 대조합니다.
- 새 Control connection이 기존 connection을 안전하게 대체합니다.
- Credential/Token/Authorization/Provider raw payload/Terminal 본문이 메시지·로그에 남지 않습니다.
- JSON Schema validation에서 정상 메시지는 통과하고 required/type 위반 메시지는 실패합니다.

## 14. SSOT 경계

Confluence의 D-17/D-20/D-24/D-25는 **왜 이런 정책을 택했는지와 의미**를 관리합니다.
이 디렉터리는 **실제 wire contract**를 관리합니다.

따라서 메시지명·필드명·required/optional·payload 구조를 Confluence에 다시 복제하지 않습니다.
