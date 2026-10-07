# Labbit Connector WSS Contract

이 디렉터리는 **Labbit SaaS ↔ 고객 환경 Connector의 WebSocket 계약 SSOT**를 관리합니다.

- Control 전송·인증·연결 수명·호환성: `README.md`
- 기본 Control 메시지: `connector.schema.json`
- TerminalSession lifecycle Control 메시지: `terminal-control.schema.json`
- Terminal Data WSS JSON control frame: `terminal-data.schema.json`
- Workspace File 요청 lifecycle Control 메시지: `file-control.schema.json`
- Workspace File Data WSS JSON control frame: `file-data.schema.json`
- PreviewSession lifecycle Control 메시지: `preview-control.schema.json`
- Preview Data WSS JSON control frame(attach 핸드셰이크): `preview-data.schema.json`

Terminal/Live의 Browser-facing 계약은 `contracts/realtime/README.md` + `terminal-live.schema.json`이 원본입니다. Preview의 Browser-facing 계약(PreviewSession HTTP, Preview Origin의 bootstrap/인증)은 `contracts/http/openapi.yaml`이 원본입니다.

Terminal/Live INPUT/OUTPUT, Preview 본문, Workspace file 본문과 디렉터리 목록은 **persistent Control WSS에 싣지 않습니다.** Control에는 lifecycle/metadata만 전달하고 실제 PTY byte stream은 별도 Terminal Data WSS, Workspace file 내용은 요청별 File Data WSS, Preview HTTP byte stream은 PreviewSession별 Preview Data WSS를 사용합니다.

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

Control WSS의 JSON message validation은 다음 Schema 집합을 사용합니다.

```text
connector.schema.json
+ terminal-control.schema.json
+ file-control.schema.json
+ preview-control.schema.json
```

Terminal Data WSS는 별도 `terminal-data.schema.json`, File Data WSS는 별도 `file-data.schema.json`, Preview Data WSS는 별도 `preview-data.schema.json`을 사용합니다.

### Capability 협상

기존 Connector가 새 message/action을 모두 이해한다고 가정하지 않습니다. Connector는 `HELLO.payload.capabilities`로 지원하는 선택 기능을 선언하고, SaaS는 **현재 protocol-ready Control connection이 선언한 capability에만 해당 기능의 Control message를 보냅니다.** 버전 문자열(`connectorVersion`)로 capability를 추론하지 않습니다. capability는 connection마다 HELLO로 다시 선언하며 재접속한 connection이 선언하지 않으면 사용할 수 없습니다.

| capability | 의미 |
| --- | --- |
| `file-v1` | [§7a](#7a-workspace-file-transport)의 Workspace File transport(`FILE_OPEN`/`FILE_OPEN_RESULT`/`FILE_CLOSE`와 File Data WSS)를 지원합니다. |
| `preview-v1` | [§7b](#7b-preview-transport)의 Preview transport(`PREVIEW_OPEN`/`PREVIEW_OPEN_RESULT`/`PREVIEW_CLOSE`와 Preview Data WSS)를 지원합니다. |

`file-v1`을 선언하지 않은 Connector에는 `FILE_OPEN`/`FILE_CLOSE`를 보내지 않으며, SaaS는 그 Workspace File 요청을 사용 불가(HTTP `503`)로 처리합니다. 같은 원칙으로 `preview-v1`을 선언하지 않은 Connector에는 `PREVIEW_OPEN`/`PREVIEW_CLOSE`를 보내지 않으며, SaaS는 그 PreviewSession 생성을 사용 불가(HTTP `503`)로 처리합니다.

### JSON Text application message 크기 제한

Control WSS와 Terminal lifecycle/Data WSS, File lifecycle/Data WSS, Preview lifecycle/Data WSS의 **JSON Text application message는 WebSocket fragmentation 재조립 후 최대 1 MiB(1,048,576 bytes)** 입니다. 이 제한은 JSON decode, Schema validation, 선택 Trace metadata 정상화보다 먼저 적용합니다.

- 수신 구현은 read limit을 먼저 설정해 최대 크기를 넘는 JSON Text message 전체를 메모리에 무제한 적재하지 않습니다.
- 1 MiB를 넘으면 해당 message를 파싱하거나 `traceparent`/`tracestate`를 제거해 계속 처리하지 않고 WebSocket close code **1009 (Message Too Big)** 로 연결을 종료할 수 있습니다. 별도 `ERROR` frame 전송은 요구하지 않습니다.
- Schema의 `traceparent` 512자 / `tracestate` 1024자 제한은 이 전체 message guard를 통과한 뒤 적용되는 field 수준 검증입니다.
- Terminal PTY Binary byte stream, File Data WSS의 파일 본문 Binary frame, Preview Data WSS의 Binary byte stream은 이 JSON Text 한도의 대상이 아닙니다. Terminal Binary transport와 Preview Binary transport도 구현에서 bounded read/write를 사용하지만 별도 application payload 한도는 부하 테스트와 Runtime에서 검증합니다. Preview Binary frame 하나의 크기는 이 계약이 고정하지 않으며 한 frame이 byte stream의 임의 구간일 뿐 HTTP message 경계가 아닙니다. File 본문 Binary frame의 크기는 요청 frame이 선언한 byte 수(Save의 `size`, Read의 `maxBytes`와 결과의 `size`)로 제한하며 선언과 다른 크기의 frame은 protocol 오류입니다.

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

### Data WSS와 Credential revoke

[§2](#2-tls와-인증)의 Credential revoke는 Terminal Data WSS에도 적용됩니다. SaaS는 revoke를 관측하면 그 Credential로 인증한 모든 Terminal Data WSS를 더 이상 신뢰하지 않고 close code **`4001`** (§15, Connector Credential revoke)로 종료합니다. 다른 Credential이나 다른 Connector의 Data WSS는 종료하지 않습니다. 이후 종료된 connection으로는 INPUT, OUTPUT, resize를 처리하지 않으며 같은 Credential의 새 Upgrade는 `401`로 거절합니다.

이 종료는 **Data connection의 trust 상실**이며 TerminalSession이나 PTY의 종료가 아닙니다. 위 재접속 규칙을 그대로 따라, TerminalSession이 살아 있는 동안 Connector는 **유효한** Credential로 같은 TerminalSession에 다시 attach할 수 있습니다. revoke된 Credential로 재연결을 반복하지 않습니다.

SaaS는 Terminal Data WSS가 자신의 heartbeat를 갖지 않으므로 저장소 상태를 바꾼 것을 관측한 Control 경로(HEARTBEAT 기록 실패, revoke lifecycle hook)에서 Data WSS에 revoke를 전달합니다. PTY Binary frame마다 Credential을 다시 검증하지 않습니다.

## 7a. Workspace File transport

Browser가 Workspace VM의 SFTP나 private network에 직접 접근하지 않고 SaaS File HTTP API(`contracts/http/openapi.yaml`)가 Connector를 거쳐 Workspace VM의 파일을 읽고 쓰는 경계입니다. VM 안에서는 Workspace VM SSH Connection의 SFTP Channel을 사용합니다(D-18). 이 절은 SaaS ↔ Connector 구간의 wire만 정하며 실제 SSH/SFTP 구현, VM 안의 Workspace root 절대 경로, symlink가 root를 벗어나지 않는지의 검증은 Connector 구현(OP-02, `LBT-21`)이 닫습니다.

Control에는 요청의 lifecycle/correlation만 싣고 경로·디렉터리 목록·파일 본문은 별도의 **File Data WSS**로 전달합니다. 1 HTTP File 요청은 1 File Data WSS이며 여러 요청을 하나의 연결에 multiplex하지 않습니다.

```text
HTTP File 요청 (Tree/Read/Save)
  → SaaS: 권한 검증, Workspace VM 결정
  → Control:  FILE_OPEN            (SaaS → Connector)   lifecycle/correlation만
  → Connector outbound File Data WSS 연결
  → FILE_DATA_ATTACH               (Connector → SaaS)
  → FILE_DATA_ATTACHED             (SaaS → Connector)
  → FILE_TREE | FILE_READ | FILE_SAVE (+ Binary)  (SaaS → Connector)  요청 frame 하나
  → FILE_*_RESULT (+ Binary)       (Connector → SaaS)  결과 frame 하나
  → SaaS가 WSS를 정상 종료(close 1000)
```

```text
wss://<saas-host>/connector/v1/file-data
Sec-WebSocket-Protocol: labbit.connector-file.v1
Authorization: Bearer <connector-credential>
```

### Control message

`file-control.schema.json`이 원본입니다. 모든 message는 `fileRequestId`(SaaS가 요청마다 발급하는 ID), `labInstanceId`, `generation`을 포함합니다.

| Message | 방향 | 의미 |
| --- | --- | --- |
| `FILE_OPEN` | SaaS → Connector | 권한 검증을 마친 resolved Workspace VM(`targetVmKey`, `providerServerId`)에 대해 이 `fileRequestId`의 File Data WSS를 열라는 요청. `operation`(`TREE`/`READ`/`SAVE`)만 알리며 경로와 본문은 싣지 않습니다. |
| `FILE_OPEN_RESULT` | Connector → SaaS | Connector가 이 요청을 진행하지 못하면(`FAILED`) 알립니다. `SUCCEEDED`는 attach 성립 통지일 뿐이며 SaaS는 실제 Data WSS attach만 근거로 삼습니다. |
| `FILE_CLOSE` | SaaS → Connector | 요청을 더 이상 기다리지 않음(취소, 시간 초과, Workspace 변경, 서비스 재시작). Connector는 진행 중인 작업과 Data WSS를 정리합니다. idempotent이며 응답이 없습니다. |

Browser Session Cookie, Password, Session Token은 Connector로 전달하지 않습니다. `FILE_OPEN`은 `fileRequestId`로 요청 하나만 가리키며, 같은 `fileRequestId`의 중복 `FILE_OPEN`은 두 번째 Data WSS를 만들지 않습니다.

### File Data WSS

`file-data.schema.json`이 원본입니다. Connector가 `FILE_OPEN`을 받으면 위 endpoint에 outbound로 연결합니다. WSS Upgrade에서 SaaS가 Connector Credential을 인증하고(Connector identity는 이 인증 결과이며 message가 주장하는 값이 아닙니다), Connector는 첫 application message로 `FILE_DATA_ATTACH`를 보냅니다.

SaaS는 다음이 **모두** 기대한 값과 같을 때만 attach를 성립시킵니다. 하나라도 다르면 다른 요청으로 fallback하지 않고 연결을 거절하며 기다리던 요청은 그대로 남습니다.

- 인증된 Connector (요청을 `FILE_OPEN`으로 보낸 그 Connector)
- `fileRequestId`, `labInstanceId`, `generation`
- `payload.targetVmKey`, `payload.providerServerId` (Workspace VM 식별)

attach 뒤에는 각 방향으로 frame이 정확히 이 순서로 오갑니다. 요청과 응답의 모든 JSON frame은 같은 `fileRequestId`, `labInstanceId`, `generation`을 가지며 응답은 요청의 `messageId`를 `replyToMessageId`로 돌려줍니다. 어긋난 frame은 성공으로 처리하지 않고 연결을 종료하며(close `1008`) 요청은 실패합니다.

| 작업 | SaaS → Connector | Connector → SaaS |
| --- | --- | --- |
| Tree | `FILE_TREE` `{path}` | `FILE_TREE_RESULT` `{outcome, entries[{name, kind}]}` |
| Read | `FILE_READ` `{path, maxBytes}` | `FILE_READ_RESULT` `{outcome, revision, size}` + **Binary frame 하나(`size` byte)** |
| Save | `FILE_SAVE` `{path, expectedRevision, size}` + **Binary frame 하나(`size` byte)** | `FILE_SAVE_RESULT` `{outcome, revision}` |

- 파일 본문은 JSON에 감싸지 않고 raw byte로 전달합니다. 본문이 비어 있어도 Binary frame을 보냅니다(빈 frame). 본문이 UTF-8 text인지, NUL을 포함하는지, 크기 한도를 넘는지는 SaaS가 판단합니다. Connector는 본문을 해석하지 않습니다.
- `path`는 SaaS가 HTTP 경로 규칙으로 검증하고 URL decoding이 끝난 **canonical workspace-relative POSIX path**입니다. Connector는 다시 URL decode하거나 정규화하지 않고 값 그대로 Workspace root에 붙이며, canonical하지 않거나(절대 경로, `..`, 빈 segment, 백슬래시, NUL 등) root 밖을 가리키면 `INVALID_PATH`로 실패시킵니다. SaaS의 검증은 Connector의 검증을 대체하지 않습니다.
- `revision`은 opaque이며 생성 방식은 호환성 계약이 아닙니다(문자 집합과 길이만 Schema가 제한). Save에서 Connector는 **쓰기 직전**에 현재 파일의 revision과 `expectedRevision`을 비교해 같을 때만 쓰고, 다르면 파일을 바꾸지 않고 `REVISION_CONFLICT`로 실패시킵니다. 파일이 없으면 만들지 않고 `NOT_FOUND`로 실패시킵니다. Read의 `revision`은 반환한 본문과 같은 시점의 값이어야 합니다.
- 결과 `outcome`이 `FAILED`이면 `error.code`로 이유를 알립니다. 이 계약이 쓰는 code는 `NOT_FOUND`, `NOT_A_FILE`, `NOT_A_DIRECTORY`, `TOO_LARGE`, `REVISION_CONFLICT`, `PERMISSION_DENIED`, `INVALID_PATH`, `UNAVAILABLE`, `INTERNAL_ERROR`이며 알 수 없는 code는 SaaS가 `UNAVAILABLE`로 취급합니다. `error.message`에 경로, 파일 본문, SSH/SFTP raw 오류를 싣지 않습니다.
- Tree의 `entries`는 직계 항목만이고 `kind`는 `file`, `directory`뿐입니다. 그 밖의 종류(symlink, device 등)는 Connector가 포함하지 않습니다. 목록이 JSON Text 1 MiB 한도를 넘으면 일부만 보내지 않고 `TOO_LARGE`로 실패시킵니다. SaaS는 HTTP 경로 규칙으로 표현할 수 없는 이름을 목록에서 제외하며 그 때문에 요청을 실패시키지 않습니다.
- `FILE_READ`의 `maxBytes`보다 큰 파일은 본문을 보내지 않고 `TOO_LARGE`로 실패시킵니다. 한도의 수치는 SaaS 구성이며 이 계약의 값이 아닙니다.
- 결과를 받은 SaaS가 연결을 정상 종료(close `1000`)합니다. SaaS는 `FILE_SAVE`를 보낸 뒤 결과를 받지 못해도 자동으로 다시 보내지 않으며, 저장 여부를 알 수 없는 상태로 HTTP 요청에 알립니다.

### 수명, 취소, 정리

- `FILE_OPEN`을 보낸 뒤 attach가 시간 안에 오지 않거나, HTTP 요청이 취소되거나, 결과가 시간 안에 오지 않거나, `FILE_OPEN` 전송 결과가 불명확하거나, SaaS가 종료 중이면 SaaS는 그 요청의 상태를 지우고 Data WSS를 닫은 뒤 `FILE_CLOSE`를 보냅니다(보낼 수 있는 경우). Connector가 `FILE_OPEN_RESULT=FAILED`로 이미 실패를 알린 요청은 SaaS가 상태만 지우고 `FILE_CLOSE`를 보내지 않으며, Data WSS의 계약 위반·단절로 실패한 요청도 Data WSS 종료(`1008` 등)로 알리고 `FILE_CLOSE`를 따로 보내지 않을 수 있습니다. 그래서 Connector는 `FILE_CLOSE`뿐 아니라 Data WSS 종료로도 그 요청의 작업을 정리해야 합니다. 이미 정리된 요청의 늦은 attach/frame은 어떤 요청에도 연결되지 않으며 연결은 종료됩니다.
- Data WSS가 중간에 끊기면 요청은 실패하고 자동으로 재연결·재전송하지 않습니다. Connector는 `FILE_CLOSE`나 연결 종료를 받으면 그 요청의 SFTP 작업을 정리합니다. 이미 시작한 Save 쓰기를 되돌린다고 보장하지 않습니다.
- Connector Credential이 revoke되면 §2에 따라 그 Credential로 인증된 File Data WSS도 close `4001`로 종료하며 진행 중이던 요청은 실패합니다. 같은 Credential의 새 Upgrade는 `401`입니다.
- 서로 다른 요청은 서로 다른 `fileRequestId`와 Data WSS를 가지므로 한 Connector에 동시에 여러 요청이 진행돼도 서로 섞이지 않습니다. 이 pending 상태는 SaaS process 안의 ephemeral 상태이며 PostgreSQL에 저장하지 않습니다.
- v0.1은 `FILE_OPEN`을 보낸 SaaS process가 Data WSS도 받는다고 가정합니다(같은 process의 api role). multi-replica owner routing은 이 계약이 제공하지 않습니다.

### 민감정보

파일 본문, 디렉터리 목록, 경로를 persistent Control WSS, PostgreSQL, 구조화 log, trace, metric label에 남기지 않습니다. 허용되는 관측 metadata는 `fileRequestId`, `requestId`, `labInstanceId`, `connectorId`, `generation`, 작업 종류(`operation`), 안전한 `error_code`, `duration_ms`입니다.

## 7b. Preview transport

Browser가 Workspace VM의 private IP/Port에 직접 접근하지 않고, 사용자 코드가 실행되는 **별도 Preview Origin**의 SaaS Preview Gateway가 Connector를 거쳐 Workspace VM SSH Connection의 TCP forwarding Channel로 허용된 application port에만 접근하는 경계입니다(D-18). 이 절은 SaaS ↔ Connector 구간의 wire만 정하며 Browser-facing PreviewSession HTTP와 Preview Origin의 인증은 `contracts/http/openapi.yaml`이, 실제 SSH TCP forwarding·VM 관리 주소 조회·Connector의 outbound Preview WSS 구현은 Connector 구현(CC-03 `LBT-23`, OP-03 `LBT-24`)이 닫습니다. 실제 Workspace VM 왕복 검증은 C3(`LBT-25`)입니다.

Control에는 PreviewSession의 lifecycle/correlation과 승인된 target만 싣고, Preview HTTP 요청/응답 본문·경로·query·Cookie·header는 별도의 **Preview Data WSS**의 Binary byte stream으로만 전달합니다.
MVP에서 **Preview Data WSS 하나는 Workspace VM application port로의 TCP 연결 하나**입니다(1 Data WSS = 1 TCP connection).
동시에 유지되는 active tunnel은 PreviewSession당 최대 1개이며, 여러 PreviewSession이나 여러 TCP 연결을 하나의 Data WSS에 multiplex하지 않습니다.
하나의 logical PreviewSession은 수명 주기 동안 여러 개의 TCP tunnel을 순차적(sequential)으로 가질 수 있습니다(1 PreviewSession = 0..N sequential TCP tunnels).

```text
PreviewSession 생성 (HTTP) 또는 후속 요청 시 터널 재개
  → SaaS: 권한 검증, Workspace VM 결정, Backend 허용 목록에 대한 targetPort 승인
  → Control:  PREVIEW_OPEN (messageId M1)  (SaaS → Connector)   lifecycle/correlation/승인된 target
  → Connector: Workspace VM의 targetPort로 TCP forwarding channel을 연다
       열지 못함 → Control: PREVIEW_OPEN_RESULT FAILED {error.code}  (Data WSS를 열지 않음)
  → Connector outbound Preview Data WSS 연결
  → PREVIEW_ATTACH (replyToMessageId = M1) (Connector → SaaS)
  → PREVIEW_ATTACHED                       (SaaS → Connector)
  → Binary ↔ Binary                        (Preview HTTP byte stream)
  → upstream TCP close / Data WSS 정상 종료(1000) (tunnel만 종료, PreviewSession 유지)
  ... 후속 HTTP 요청 시 PREVIEW_OPEN (messageId M2)로 새 tunnel 순차 오픈 ...
  → PREVIEW_CLOSE (Control) (PreviewSession 명시적 종료/만료 시 세션 및 활성 tunnel 정리)
```

```text
wss://<saas-host>/connector/v1/preview-data
Sec-WebSocket-Protocol: labbit.connector-preview.v1
Authorization: Bearer <connector-credential>
```

### Control message

`preview-control.schema.json`이 원본입니다. 모든 message는 `previewSessionId`(SaaS가 PreviewSession마다 발급하는 ID), `labInstanceId`, `generation`을 포함합니다.

| Message | 방향 | 의미 |
| --- | --- | --- |
| `PREVIEW_OPEN` | SaaS → Connector | 권한 검증과 port 승인을 마친 resolved target(`targetVmKey`, `providerServerId`, `targetPort`)에 이 `previewSessionId`의 다음 TCP forwarding channel과 Preview Data WSS를 준비하라는 요청. 고유 `messageId`를 가집니다. 경로·Cookie·본문은 싣지 않습니다. |
| `PREVIEW_OPEN_RESULT` | Connector → SaaS | Connector가 이 PreviewSession tunnel을 열지 못하면(`FAILED`, `error.code`) 알립니다. `SUCCEEDED`는 TCP forwarding channel이 열렸고 Data WSS attach를 진행한다는 통지일 뿐이며 SaaS는 실제 Data WSS attach만 근거로 삼습니다. |
| `PREVIEW_CLOSE` | SaaS → Connector | PreviewSession을 더 이상 유지하지 않음(명시적 종료, 만료, Reset/Cleanup, 생성 실패, 서비스 재시작). Connector는 TCP forwarding channel과 Data WSS를 정리합니다. idempotent이며 응답이 없습니다. |

**Connector → SaaS의 별도 세션 종료 통지 message(`PREVIEW_ENDED` 등)는 없습니다.** PreviewSession의 수명 주기(TTL, 명시적 삭제, 만료)는 SaaS가 주관합니다. 개별 TCP tunnel의 종료는 Data WSS의 정상 종료(`1000`)로 표현되며, 이는 단순 터널 종료일 뿐 logical PreviewSession의 종료가 아닙니다.

`PREVIEW_OPEN`은 `labbit.connector.v1` Control에서 SaaS가 `preview-v1`을 선언한 protocol-ready Control connection으로만 보냅니다. Browser Session Cookie, Preview bootstrap credential, Preview Browser auth Cookie/token, Password, 사설 IP, SSH/OpenStack Credential은 Connector로 전달하지 않습니다. Connector는 SaaS가 보낸 `providerServerId`로 실제 Provider 상태와 관리 주소를 스스로 확인하며 Browser나 SaaS가 주장하는 VM IP를 신뢰하지 않습니다.

### 허용 port

`targetPort`는 Backend가 **명시적으로 설정한 허용 목록**(Runtime Contract `LABBIT_PREVIEW_ALLOWED_PORTS`)에 대해 SaaS가 PreviewSession마다 승인한 정확한 값 하나입니다. 숫자 범위로 자동 승인하지 않으며, 3000 같은 값도 기본값이 아닙니다. Connector는 SaaS의 승인을 대체하지 않는 심층 방어로 SSH 관리 port(`22`)를 거절하고 `PORT_REJECTED`로 실패시킵니다. Control이 승인한 `targetPort`와 다른 port를 forwarding하는 Data WSS attach는 거절됩니다.

### `PREVIEW_OPEN_RESULT`의 error.code와 SaaS의 해석

Connector는 Data WSS를 attach하기 **전에** TCP forwarding channel을 먼저 열고, 열지 못하면 attach하지 않고 `FAILED`로 알립니다. 그래야 PreviewSession이 "활성"인 시점에 application에 실제로 도달할 수 있음이 확인됩니다. 이 계약이 쓰는 `error.code`와 SaaS의 PreviewSession 생성 HTTP 결과는 다음과 같으며 알 수 없는 code는 `UNAVAILABLE`로 취급합니다.

| `error.code` | 의미 | PreviewSession 생성 HTTP |
| --- | --- | --- |
| `APP_NOT_RUNNING` | Workspace VM에는 도달했지만 targetPort에서 listen하는 application이 없음(connection refused) | `502` `preview_app_not_running` |
| `PORT_REJECTED` | Connector가 이 port를 forwarding하지 않음(예: SSH 관리 port) | `403` `preview_port_rejected` |
| `VM_UNREACHABLE` | Workspace VM 또는 SSH Connection에 도달할 수 없음(시간 초과 포함) | `504` `preview_target_unreachable` |
| `UNAVAILABLE`, `INTERNAL_ERROR` | 그 밖에 Connector가 열 수 없음 | `503` `preview_open_failed` |

`error.message`에 Credential, Cookie, 경로, 응답 본문, SSH/provider raw 오류를 싣지 않습니다.

### Preview Data WSS

`preview-data.schema.json`이 원본입니다. Connector가 TCP forwarding channel을 연 뒤 위 endpoint에 outbound로 연결합니다. WSS Upgrade에서 SaaS가 Connector Credential을 인증하고(Connector identity는 이 인증 결과이며 message가 주장하는 값이 아닙니다), Connector는 첫 application message로 `PREVIEW_ATTACH`를 보냅니다.

`PREVIEW_ATTACH`는 이 tunnel 생성을 요청한 `PREVIEW_OPEN`의 `messageId`를 `replyToMessageId`로 반드시 포함해야 합니다.

SaaS는 다음이 **모두** 기대한 값과 같을 때만 attach를 성립시킵니다. 하나라도 다르면 다른 PreviewSession으로 fallback하지 않고 그 connection만 거절(close `1008`)하며 기다리던 PreviewSession은 그대로 남습니다.

- 인증된 Connector (`PREVIEW_OPEN`을 보낸 그 Connector)
- 인증한 Credential이 `PREVIEW_OPEN`을 전달한 Control Session을 인증한 Credential과 같음 (Connector ID가 같다는 이유만으로 신뢰하지 않습니다)
- `previewSessionId`, `labInstanceId`, `generation`
- `replyToMessageId`가 현재 대기 중인 `PREVIEW_OPEN`의 `messageId`와 정확히 일치함 (stale attach 거부)
- `payload.targetVmKey`, `payload.providerServerId`, `payload.targetPort` (Workspace VM과 승인된 port)

attach가 성립하면 SaaS가 `PREVIEW_ATTACHED`를 보내고, **그 이후 양방향 모두 Binary frame의 raw byte stream만** 오갑니다.

```text
Gateway   → Connector : Workspace application으로 쓸 raw byte (HTTP request byte stream)
Connector → Gateway   : Workspace application에서 읽은 raw byte (HTTP response byte stream)
```

- Binary frame 경계는 HTTP message, header, line 경계가 아닌 byte stream의 임의 구간입니다. SaaS도 Connector도 byte를 해석하지 않고 전달합니다.
- attach 뒤의 JSON Text frame은 protocol 위반이며 SaaS는 연결을 `1008`로 종료합니다.
- Connector는 TCP 연결이 끝나면(application이 연결을 닫거나 SSH channel이 끊김) 남은 byte를 모두 전달한 뒤 Data WSS를 정상 종료(`1000`)합니다. SaaS가 Data WSS를 종료하면 Connector는 TCP forwarding channel을 닫습니다.
- upstream TCP 연결 종료(예: application의 `Connection: close`, keep-alive timeout 등)는 해당 Data WSS tunnel만 종료시키며, logical PreviewSession 자체는 종료되지 않습니다. PreviewSession이 유효한 동안 후속 HTTP 요청은 새로운 sequential Data tunnel을 열 수 있습니다.

### 수명, 취소, 정리

- **초기 PreviewSession 생성(initial create) 실패/취소**: `PREVIEW_OPEN`을 보낸 뒤 `PREVIEW_OPEN_RESULT=FAILED`가 오거나, attach가 시간 안에 오지 않거나, HTTP 생성 요청이 취소되거나, `PREVIEW_OPEN` 전송 결과가 불명확하면 SaaS는 그 PreviewSession 생성을 abort하고 Gateway 등록과 Router pending을 지우며 Data WSS를 닫고 `PREVIEW_CLOSE`를 보냅니다(보낼 수 있고 Connector가 `FAILED`를 이미 알리지 않은 경우).
- **활성 PreviewSession의 후속 터널 개방(follow-up sequential tunnel open) 실패/취소**: 이미 생성되어 활성인 PreviewSession에서 후속 HTTP 요청을 위해 tunnel을 열 때 timeout, context 취소, `PREVIEW_OPEN_RESULT=FAILED`, Connector 전송 오류 등이 발생하더라도, 해당 open attempt와 Router pending, Data WSS 시도만 정리(clean)되고 logical PreviewSession 자체는 유지됩니다. 브라우저는 다음 HTTP 요청에서 새 sequential tunnel 개방을 재시도(retry)할 수 있습니다.
- **stale/late attach 및 late OPEN_RESULT**: 이미 정리된 open attempt나 이전 시도의 stale/late attach는 `replyToMessageId` 불일치 또는 대기 상태 부재로 인해 거절(close `1008`)되며 활성 PreviewSession이나 다른 tunnel에 연결되지 않습니다. 대기 중인 open attempt가 이미 종료된 뒤 뒤늦게 도착한 late `PREVIEW_OPEN_RESULT`는 unmatched로 안전하게 무시되며 현재의 새 attempt나 활성 PreviewSession을 건드리지 않습니다.
- **TCP/Data WSS 연결 종료**: 일반적인 TCP/Data WSS 연결의 정상 종료(`1000`)는 단일 데이터 터널의 수명 종료일 뿐이며 logical PreviewSession 자체의 종료가 아닙니다.
- **logical PreviewSession 종료**: PreviewSession은 절대 TTL(만료 시각)을 가집니다. 사용자/API의 명시적 종료, 만료, Reset/Cleanup, SaaS 종료에서는 열려 있는 Data WSS를 닫고 `PREVIEW_CLOSE`를 보냅니다. Connector가 알린 종료(Data WSS 종료)와 Credential revoke·Control Session 교체는 Connector가 이미 알고 있거나 보낼 수 없으므로 `PREVIEW_CLOSE`를 따로 보내지 않을 수 있고, 그래서 Connector는 `PREVIEW_CLOSE`뿐 아니라 Data WSS 종료로도 TCP forwarding channel을 정리해야 합니다.
- Connector Credential이 revoke되면 §2에 따라 그 Credential로 인증된 Preview Data WSS도 close `4001`로 종료하고 해당 PreviewSession(attach를 기다리는 것 포함)을 끝냅니다. 같은 Credential의 새 Upgrade는 `401`입니다.
- Connector의 새 Control connection이 기존 Control connection을 대체하면(§4, close `4002`) SaaS는 이전 Control Session으로 `PREVIEW_OPEN`을 보낸 PreviewSession을 trust 대상에서 제외합니다. attach를 기다리던 PreviewSession은 정리하고 이미 attach된 tunnel은 close `4002`로 종료합니다. 이전 Data WSS는 새 Control Session의 PreviewSession을 완료시키지 못하며 Connector ID가 같다는 이유로 다시 신뢰하지 않습니다.
- 서로 다른 PreviewSession은 서로 다른 `previewSessionId`와 Data WSS를 가지므로 한 Connector에 동시에 여러 PreviewSession이 있어도 서로 섞이지 않습니다. 이 상태는 SaaS process 안의 ephemeral 상태이며 PostgreSQL에 저장하지 않습니다.
- v0.1은 `PREVIEW_OPEN`을 보낸 SaaS process가 Data WSS와 Browser Preview 요청도 받는다고 가정합니다(같은 process의 `api`와 `preview` role). multi-replica owner routing은 이 계약이 제공하지 않습니다.

### MVP 범위의 한계 (보장하지 않는 것)

- HTTP/2 upstream, Workspace application의 WebSocket upgrade, SSE 완전 지원, 임의 TCP multiplexing, PreviewSession당 동시 여러 upstream TCP 연결은 보장하지 않습니다 (동시 active tunnel <= 1).
- 일반 Web application과의 호환성은 C3(`LBT-25`)에서 검증하며 필요하면 `PREVIEW_OPEN`에 optional field를 추가하는 v1 호환 방식으로 확장합니다.

### 민감정보

Preview HTTP 요청/응답 본문, 경로, query, Cookie, Authorization header를 persistent Control WSS, PostgreSQL, 구조화 log, trace, metric label에 남기지 않습니다. 허용되는 관측 metadata는 `previewSessionId`, `requestId`, `labInstanceId`, `connectorId`, `generation`, 안전한 `error_code`, `duration_ms`입니다. `previewSessionId` 같은 고유 식별자를 Prometheus label로 쓰지 않습니다.

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
- `fileRequestId`: Workspace File 요청 하나(File Data WSS 하나)의 lifecycle correlation
- `previewSessionId`: PreviewSession 하나(Preview Data WSS 하나, TCP 연결 하나)의 lifecycle correlation
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
- OpenStack Provider는 갱신 가능한 Password/Application Credential에 한해 Keystone 토큰 만료의 확정적 `401` 응답 뒤 SDK 재인증과 해당 API 요청 1회 재전송을 허용합니다. 동시 갱신은 SDK token lock으로 묶으며 초기 인증·재인증에는 각각 최대 15초(호출자 deadline이 더 짧으면 그 값)를 적용합니다. 일반 `5xx`·timeout·응답 유실에 대한 mutation 자동 재실행은 추가하지 않습니다. Token-only/일회용 인증은 갱신하지 않으며 만료 시 로컬 Credential 교체·Connector 재시작이 필요합니다. 인증 실패는 안전한 Provider 오류로 보고하고 Control WSS를 강제 종료하지 않습니다. 이 인증 제한시간은 모든 Service API/SDK catalog discovery HTTP의 제한시간을 보장하지 않습니다.
- Provider Image/Flavor 조회 응답은 envelope·correlation·JSON escaping을 포함한 최종 JSON이 1 MiB를 넘으면 목록을 일부만 성공으로 보내지 않고 `items` 없는 `FAILED`/`ERR_CONNECTOR_INTERNAL` 응답으로 바꿉니다. 실패 응답 자체도 같은 한도를 검사합니다. 비정상적으로 큰 correlation 때문에 실패 응답도 초과하면 필수 ID를 자르지 않고 로컬 전송 오류로 처리하며 WSS는 유지합니다. 새 pagination·wire field·오류 enum은 추가하지 않습니다.
- `discoverCandidates`가 생략된 `RECONCILE_REQUEST`는 Control 변환 단계에서 `true`로 적용하고, 명시적인 `false`는 그대로 전달합니다.
- Mock Provider 메서드가 설정되지 않은 경우 Dispatcher는 내부 설정 오류를 호출자에게 반환해 테스트가 실패하게 합니다. 이를 Provider 작업의 `UNKNOWN`으로 취급하지 않으며 오류 원문을 wire에 싣지 않습니다. Reconcile의 미분류 내부 오류는 `DispatchReconcile`이 원문을 숨기고 `RECONCILE_RESULT`의 일반 `SafeError`(`PROVIDER_RECONCILE_UNAVAILABLE`)와 빈 `observations`로 변환합니다. 조회 실패만으로 리소스 부재를 확정하지 않습니다.

이 경계는 Connector 내부 역할 분담입니다. 위 규칙으로 새로운 wire 필드나 메시지를 추가하지 않습니다. 메시지 구조와 필수 필드는 `connector.schema.json`을 따릅니다.

## 11. CreationSnapshot과 Reset

Provision/Reset에서 사용하는 `creationSnapshot`은 D-19의 immutable resolved CreationSnapshot입니다.

RESET은 `creationSnapshot`과 **비어 있지 않은 `providerResources`**를 요구합니다. 각 resource는 `resourceType`, `providerId`, `generation`과 **빈 문자열이 아닌 `logicalName`**을 포함해야 합니다. 정확한 필드 타입·required·길이 제약은 `connector.schema.json`이 원본입니다.

- RESET 명령의 `generation`은 새로 만들 generation(2 이상)이며 각 resource는 정확히 그 이전 generation(`command.generation - 1`, 1 이상)이어야 합니다.
- 필드 존재·목록 크기·문자열 길이는 JSON Schema로 검증합니다. 명령 generation과 resource generation의 관계는 envelope와 payload 사이의 의미 조건이므로 Backend outbound validator와 Connector inbound validator에서 별도로 검증합니다.
- 리소스 목록 누락·빈 목록·logicalName 누락·빈 문자열·이전 generation 불일치는 기존 환경이 없다는 뜻으로 해석하거나 새 Provision으로 대체하지 않습니다. Backend는 wire 전송 전에 거절(`ErrInvalidCommand`)하고 Connector는 Provider dispatch 전에 거절(`INVALID_COMMAND` ACK)합니다.
- 이 변경은 Confluence 결정 [**D-26(최초 지원 배포 전 RESET 안전성 계약 정정)**](https://samsunglions.atlassian.net/wiki/spaces/SL/pages/28672005)에 따른 pre-release contract correction입니다. 지원 배포가 시작된 이후의 변경에는 기존 `labbit.connector.v1` compatibility/capability/versioning 원칙이 그대로 적용됩니다.
- 목록의 존재만으로 리소스 소유권이나 이전 generation의 전체 구성 확인이 끝난 것은 아닙니다. SaaS는 LabInstance/ProviderResource 기록에서 소유관계를 확인해 해당 이전 generation의 추적된 ID 목록을 전달해야 합니다. Provider는 전달된 목록을 CreationSnapshot의 구성·generation과 대조하고 정확한 Provider ID만 처리하며, LabInstance 소유권을 독립 검증하는 것은 아닙니다. 기존 generation Cleanup 완료 후에만 새 generation을 Provision하며 부분 삭제 실패·결과 불명은 새 생성이나 blind retry로 전환하지 않습니다.

Reset에서 최신 LabSpec이나 비슷한 최신 Image를 다시 선택하지 않습니다. 기존 generation을 파괴하기 전에 원본 Image/Flavor/Provider 연결 등 재현 가능성을 Preflight하고 재현 불가하면 기존 환경을 먼저 삭제하지 않습니다.

- Preflight는 Image, Flavor, Management/External Network, Key Pair와 함께 Nova의 Instance/vCPU/RAM 및 Neutron의 Network/Subnet/Port/Router/Security Group/Rule 상세 quota를 확인합니다. Reset은 삭제가 확정된 기존 generation 리소스를 quota 사용량에서 차감해 판단하되, Preflight 이후의 동시 사용 변화까지 성공으로 보장하지는 않습니다.
- `internetOutbound=true`이면 generation별 Lab Router를 만들고 External Gateway와 Lab Subnet interface를 연결합니다. `false`이면 Router를 만들지 않고 Lab Subnet gateway를 비활성화합니다.
- Lab NIC에는 동일 Lab 대역 ingress를 가진 Lab Security Group만, Management NIC에는 Connector SSH CIDR의 TCP 22 ingress를 가진 Management Security Group만 연결합니다.
- Startup Script가 있으면 VM `ACTIVE`와 SSH banner만으로 `SUCCEEDED`를 반환하지 않습니다. Connector 전용 SSH key와 TOFU로 고정한 host key를 사용해 `cloud-init status --wait` 성공까지 확인합니다.

### KT D1 호환 프로필 (LBT-158)

KT D1은 표준 Neutron 경로가 제공되지 않아 OP-01 내부 Network API만 NSM Tier/Firewall로 처리합니다. 현재 호환 범위는 Linux 50 GiB 부팅 볼륨, VM 한 대, `internetOutbound=false`입니다. 범위를 벗어나는 Snapshot은 mutation 전에 거절하며 Reset에서도 기존 generation을 먼저 삭제하지 않습니다. 이 제한은 C1 전체 수용 완료를 뜻하지 않습니다.

- generation별 Lab Tier와 해당 VM 전용 Management Tier를 생성합니다. VM별 Management Tier는 같은 L2에 다른 Lab VM이 연결되는 것을 막습니다. 두 NIC는 Nova에 각각 Tier의 물리 `refId`로 연결합니다.
- Connector Tier의 설정된 IPv4 `/32`에서 Management Tier로 향하는 TCP 22만 NSM 방화벽으로 허용합니다. Lab Tier의 동일 L2 통신과 SSH 응답을 제외한 허용 정책·NAT·인터넷 Router는 생성하지 않습니다. 전역 허용 정책이 있거나 필요한 정책 조회가 불완전하면 Preflight를 거절합니다.
- Preflight는 원본 Image/Flavor/Keypair, SSH key 일치, Project affinity, Connector Tier, CIDR 중복, 방화벽 인벤토리, Nova Instance/vCPU/RAM 상세 quota를 확인합니다. NSM의 상세 quota API는 공개되어 있지 않고 KT 볼륨 quota 경로는 실제 500을 반환합니다. 이 제한을 무제한 quota로 취급하지 않으며 NSM/Cinder mutation의 실제 거절·결과 불명을 그대로 전달합니다.
- 실제 리소스 종류는 `KT_TIER`, `KT_FIREWALL_POLICY`, `SERVER`, `VOLUME`입니다. Tier `providerId`는 NSM `networkId`이며 `refId` 또는 가상의 Neutron Port/Subnet/SG ID로 대체하지 않습니다. Nova가 만드는 루트 볼륨도 실제 ID로 추적하고 서버 삭제 후 부재를 확인합니다.
- 기존 CC-01, ACTIVE/인증된 SSH/Startup Ready 확인, immutable Snapshot Reset, 결과 불명 판정과 알려진 ID에 대한 Cleanup/Reconcile을 재사용합니다. NSM Create 후 ID가 확정되지 않으면 UNKNOWN과 이미 확정된 ID만 반환하며, 이름·설명으로 발견된 후보는 자동 소유·삭제하지 않습니다.
- Cleanup은 서버 → 루트 볼륨 → 방화벽 정책 → Tier 순서로 정확한 추적 ID만 처리합니다. NSM의 전체 페이지 조회가 성공했을 때만 목록에서 없는 ID를 ABSENT로 판정합니다. 공유 Connector Tier와 설치 단계의 Keypair는 Lab Cleanup 대상이 아닙니다.
- KT Nova의 일반 서버 DELETE는 soft delete로 루트 볼륨을 보존할 수 있습니다. 알려진 서버 ID의 상세 조회가 400이고, 전체 서버 목록에서 그 ID가 없으며, Cinder의 전체 조회에서 해당 서버에 연결된 볼륨이 확인되면 Gophercloud `forceDelete`를 한 번 수행합니다. 실제 서버 상세 조회의 404와 루트 볼륨 부재를 확인하기 전에는 삭제 완료로 판정하지 않습니다. 결과 불명 응답에서는 mutation을 반복하지 않고 Reconcile로 돌려보냅니다.

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
- Workspace file 본문, 디렉터리 목록, 경로, SSH/SFTP raw 오류
- Preview bootstrap credential, Preview Browser auth Cookie/token, Preview HTTP 요청/응답 본문·경로·query·Cookie

중앙에서는 Heartbeat, version, reconnect, Operation stage/result, Terminal lifecycle, `error_code`, duration 같은 운영 metadata를 관측하고 필요하면 같은 correlation ID로 Connector 로컬 구조화 로그를 대조합니다.

Connector 내부 OpenStack/VM 접근의 raw log·metric·상세 Span을 중앙에 상시 반출하지 않습니다. 중앙 SaaS의 command 전송/결과 수신 계측은 Connector 내부 개별 OpenStack API 호출 시간을 측정한 것과 다릅니다.

### SafeError 기본 코드

`SafeError.code`는 확장 가능한 문자열입니다. consumer는 아래 기본 코드를 처리하고 unknown code에 일반 fallback을 제공해야 합니다.

| Code | 의미 |
| --- | --- |
| `ERR_CONNECTOR_OFFLINE` | Connector Control 연결을 사용할 수 없음 |
| `ERR_INFRA_OPENSTACK` | OpenStack 인증·quota·API 또는 Provider 상태 때문에 작업을 완료할 수 없음 |
| `ERR_CONNECTOR_INTERNAL` | Connector 입력 구성·내부 처리 오류 |
| `ERR_VM_BOOT_TIMEOUT` | 제한 시간 안에 VM이 준비 상태에 도달하지 못함 |
| `ERR_PORT_NOT_LISTENING` | Workspace VM의 승인 Application Port에서 응답을 받을 수 없음 |
| `ERR_RESOURCE_QUOTA_EXCEEDED` | OpenStack resource quota가 부족함 |
| `ERR_UNKNOWN_RECONCILING` | Provider side effect 여부를 확정할 수 없어 Reconciliation이 필요함 |

기본 한국어 사용자 표시 문구는 다음 의미를 유지합니다. consumer가 locale에 맞게 번역할 수 있지만 내부 원문 오류로 대체하지 않습니다.

- `ERR_CONNECTOR_OFFLINE`: `실습 에이전트와 연결이 끊겼습니다. 관리자에게 문의하세요.`
- `ERR_VM_BOOT_TIMEOUT`: `가상머신 생성 시간이 초과되었습니다. 실습 환경을 재설정(Reset)해 주세요.`
- `ERR_PORT_NOT_LISTENING`: `실습 VM 내 웹 애플리케이션이 실행되지 않았습니다. 포트 번호를 확인하세요.`
- `ERR_RESOURCE_QUOTA_EXCEEDED`: `실습실 자원 한도가 초과되었습니다. 미사용 환경을 정리해 주세요.`
- `ERR_UNKNOWN_RECONCILING`: `자원 생성 상태를 확인 중입니다. 잠시 후 새로고침해 주세요.`

Provider mutation 요청 뒤 5xx·timeout처럼 side effect 여부가 불명확한 경우에는 단순히 `ERR_INFRA_OPENSTACK`의 확정 실패로 축소하지 않습니다. outcome을 `UNKNOWN`으로 두고 `ERR_UNKNOWN_RECONCILING`을 사용해 실제 Provider 상태를 먼저 확인합니다.

위 code와 함께 보내는 message는 사용자·운영자에게 노출 가능한 안전한 설명이어야 합니다. Credential, Authorization, Provider raw payload, 내부 endpoint 또는 SDK 원문 오류를 포함하지 않습니다.

## 15. Control Close 규칙

persistent Control connection의 v1 application close code는 다음을 사용합니다.

| Close code | 의미 |
| --- | --- |
| `4001` | Connector Credential revoke |
| `4002` | 같은 Connector의 새 Control connection이 기존 connection을 대체 |
| `4003` | 지원하지 않는 protocol/subprotocol |
| `4004` | 복구 불가능한 protocol message 오류 |

File Data WSS는 정상 완료 `1000`, protocol 위반·correlation 불일치 `1008`, 메시지 크기 초과 `1009`, Connector Credential revoke `4001`을 사용합니다([§7a](#7a-workspace-file-transport)).

Preview Data WSS는 정상 종료 `1000`(PreviewSession 종료 또는 TCP 연결 종료), SaaS 종료 `1001`, protocol 위반·attach 거절·correlation 불일치 `1008`, 메시지 크기 초과 `1009`, Connector Credential revoke `4001`, 이 PreviewSession에 `PREVIEW_OPEN`을 전달한 Control Session의 교체 `4002`를 사용합니다([§7b](#7b-preview-transport)).

Terminal Data WSS의 Session 종료 의미는 `terminal-data.schema.json`과 Browser realtime 계약을 따릅니다. 다만 Connector Credential revoke로 Data WSS를 종료할 때는 위 `4001`을 같은 의미로 사용합니다([Data WSS와 Credential revoke](#data-wss와-credential-revoke)).

## 16. 검증 기준

- Connector 2개가 연결돼도 command/TerminalSession이 다른 Connector로 전달되지 않습니다.
- 정상 Provision에서 하나의 HTTP Operation과 여러 LabInstance command/result를 연결할 수 있습니다.
- Command 전송 직후 단절/Provider 성공 후 Result 유실에서도 동일 Create를 무조건 반복하지 않습니다.
- 같은 `terminalSessionId`의 중복 OPEN이 새 PTY를 만들지 않습니다.
- TerminalSession마다 독립 Data WSS를 열고 Binary INPUT/OUTPUT을 중계할 수 있습니다.
- Browser detach만으로 PTY가 즉시 종료되지 않습니다.
- Data WSS 재연결에서 과거 OUTPUT replay를 제공하지 않습니다.
- Credential revoke 뒤 그 Credential의 Control과 Data 연결이 모두 `4001`로 종료되고, 다른 Credential/Connector의 연결은 유지되며, 종료된 Data connection으로 PTY byte가 오가지 않습니다. TerminalSession은 끝나지 않고 유효한 Credential의 재attach로 이어질 수 있습니다.
- Reset/Cleanup은 관련 TerminalSession을 terminal lifecycle 종료로 처리할 수 있습니다.
- Live 학생 fan-out이 Connector Data protocol로 확산되지 않습니다.
- Credential/Token/Authorization/Provider raw payload/Terminal 본문이 메시지·로그에 남지 않습니다.
- `file-v1`을 선언하지 않은 Connector에는 `FILE_OPEN`/`FILE_CLOSE`를 보내지 않고 그 Workspace File 요청은 사용 불가로 처리합니다. 버전 문자열로 capability를 추론하지 않습니다.
- Workspace File 요청마다 독립 File Data WSS를 열고, 인증된 Connector·`fileRequestId`·`labInstanceId`·`generation`·Workspace VM 식별이 하나라도 다른 attach/frame은 다른 요청을 완료시키지 않습니다.
- 동시에 진행되는 Workspace File 요청이 서로 섞이지 않으며, 취소·시간 초과·연결 단절 뒤 pending 상태가 남지 않습니다.
- Save 결과를 받지 못해도 `FILE_SAVE`를 자동으로 다시 보내지 않습니다.
- Workspace file 본문, 디렉터리 목록, 경로가 Control WSS, PostgreSQL, log, trace, metric label에 남지 않습니다.
- `preview-v1`을 선언하지 않은 Connector에는 `PREVIEW_OPEN`/`PREVIEW_CLOSE`를 보내지 않고 그 PreviewSession 생성은 사용 불가로 처리합니다. 버전 문자열로 capability를 추론하지 않습니다.
- PreviewSession마다 독립 Preview Data WSS를 열고, 인증된 Connector·Credential·`previewSessionId`·`replyToMessageId`·`labInstanceId`·`generation`·Workspace VM 식별·승인된 `targetPort`가 하나라도 다른 attach는 다른 PreviewSession을 완료시키지 않고 거절(close `1008`)됩니다.
- upstream TCP 연결 종료 시 Data WSS tunnel만 정상 종료되고 PreviewSession은 유지되며, 후속 HTTP 요청 시 새 sequential tunnel을 열 수 있습니다.
- Control이 승인한 `targetPort`와 다른 port의 attach, SSH 관리 port(`22`)는 PreviewSession이 되지 않습니다.
- Credential revoke와 Control Session 교체 뒤 이전 Preview Data WSS가 trust 대상으로 남지 않고, attach를 기다리던 PreviewSession과 진행 중인 tunnel이 정리됩니다.
- 초기 생성 실패/취소, 후속 tunnel open 실패/취소, 연결 단절, shutdown 뒤에 leak된 pending correlation(Router pending 및 Gateway pending)과 미정리 Preview Data WSS가 남지 않습니다.
- Preview HTTP 본문, 경로, query, Cookie, bootstrap/auth credential이 Control WSS, PostgreSQL, log, trace, metric label에 남지 않습니다.
- 각 JSON Schema 정상/비정상 message validation이 동작합니다.
- 명령별 유효한 Trace Context가 ACK/PROGRESS/RESULT에 유지되고 병렬 item/다른 generation과 섞이지 않습니다.
- 1 MiB 이하의 message에서 Context 없음/잘못된 타입·길이·W3C 값, 잘못된 tracestate만 존재하는 경우에도 정상 업무 Envelope는 처리됩니다. 전체 JSON Text message 한도 초과, 인증·업무 필드 오류는 계속 거부합니다.
- 미샘플링 Context도 유지하고, Context 유실/재접속을 mutation retry로 처리하지 않습니다.
- Connector가 중앙 OTLP 연결을 만들지 않고, PTY Binary frame에 Trace metadata를 추가하지 않습니다.

위 기준은 후속 producer/consumer 구현의 인수 조건이며 README 변경만으로 검증 완료를 의미하지 않습니다.

## 17. SSOT 경계

Confluence D-17/D-18/D-20/D-21/D-24/D-25는 왜 이런 정책을 택했는지와 제품/운영 의미를 관리합니다.

이 디렉터리는 Connector wire contract를 관리합니다. 메시지명·필드명·required/optional·payload 구조를 Confluence에 복제하지 않습니다.
