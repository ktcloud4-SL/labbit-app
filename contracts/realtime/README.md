# Labbit Browser Terminal/Live WSS Contract

이 디렉터리는 **Browser ↔ Labbit SaaS Terminal/Live Relay의 실시간 WebSocket 계약 SSOT**를 관리합니다.

- 전송·인증·frame·연결 수명·backpressure·close 규칙: `README.md`
- JSON Text control frame: `terminal-live.schema.json`

실제 PTY INPUT/OUTPUT은 JSON에 base64로 감싸지 않고 **WebSocket Binary frame의 raw byte stream**으로 전달합니다. Connector ↔ SaaS Terminal Data 경계는 `contracts/connector/terminal-data.schema.json`과 Connector 계약 문서에서 별도로 관리합니다.

## 1. 경계와 Subprotocol

Browser Terminal과 Live는 목적과 권한이 다르므로 논리 Endpoint와 WebSocket subprotocol을 분리합니다.

```text
Terminal
wss://<saas-host>/realtime/v1/terminal
Sec-WebSocket-Protocol: labbit.terminal.v1

Live
wss://<saas-host>/realtime/v1/live
Sec-WebSocket-Protocol: labbit.live.v1
```

실제 ALB/Ingress/Istio workload 경계는 이 계약에 포함하지 않습니다.

## 2. 인증과 권한

Browser WebSocket Upgrade는 현재 Labbit HTTP 로그인 세션의 `__Host-labbit-session` Cookie를 사용하고 서버가 허용된 `Origin`을 엄격히 검증합니다.

Session Token을 URL query string에 넣지 않습니다.

### Terminal

Terminal 연결은 다음 세 조건을 모두 확인합니다.

1. 유효한 Browser 로그인 세션
2. 현재 사용자에게 대상 Class/LabInstance/VM을 사용할 권한이 있음
3. 첫 JSON control frame의 `TERMINAL_ATTACH`가 특정 `terminalSessionId`에 scope된 유효한 opaque `sessionToken`을 포함함

Terminal Session Token은 일반 로그인 Token이 아니며 다른 TerminalSession에 재사용할 수 없습니다. 원문을 로그·trace·DB의 일반 관측 필드에 기록하지 않습니다.

정확한 Token 길이·TTL·회전 방식은 HTTP Session 생성 계약과 Runtime 설정에서 정합니다. 다만 Browser 비정상 단절 직후 D-21의 60초 재접속 grace를 깨지 않도록 Token lifecycle을 구현해야 합니다.

### Live

학생 Live 구독은 **Terminal Session Token을 사용하지 않습니다.**

`LIVE_SUBSCRIBE` 시 서버는 로그인 세션, 대상 Class의 현재 `STUDENT` Membership, active LiveSession을 다시 검증합니다. 새로고침·재접속은 새로운 subscription으로 취급하며 성공한 시점 이후의 OUTPUT만 수신합니다.

## 3. Terminal framing

Terminal WebSocket 하나는 **TerminalSession 하나만** 담당합니다. MVP에서는 여러 TerminalSession을 하나의 Browser WebSocket에 multiplex하지 않습니다.

### JSON Text frame

다음 control message를 사용합니다.

- `TERMINAL_ATTACH`
- `TERMINAL_ATTACHED`
- `TERMINAL_RESIZE`
- `TERMINAL_SESSION_ENDED`
- `ERROR`

정확한 field는 `terminal-live.schema.json`이 원본입니다.

### Binary frame

```text
Browser → Relay     : PTY INPUT raw bytes
Relay   → Browser   : PTY OUTPUT raw bytes
```

Binary frame 경계는 Terminal message/line/UTF-8 문자/ANSI sequence 경계가 아닙니다. 하나의 UTF-8 문자나 ANSI escape sequence가 여러 frame으로 나뉠 수 있으므로 수신자는 byte stream으로 이어서 처리해야 합니다.

연결 중에는 WebSocket/TCP의 순서 보장을 사용합니다. 별도의 OUTPUT sequence/ACK/replay protocol은 두지 않습니다.

## 4. TerminalSession 수명과 재접속

PTY와 TerminalSession은 Connector가 소유합니다.

```text
Browser WSS 정상 사용
       │
       ├─ Browser 비정상 단절
       ▼
TerminalSession DETACHED / PTY 유지
       │
       ├─ 기본 60초 이내
       │  + 유효 로그인 세션
       │  + 유효 Terminal Session Token
       │  + 현재 권한
       │       ↓
       │   같은 TerminalSession / 같은 PTY 재접속
       │
       └─ grace 만료
               ↓
          TerminalSession / PTY 종료
```

재접속은 **PTY 복원**이지 OUTPUT replay가 아닙니다. Browser가 단절된 동안 발생한 OUTPUT과 접속 이전의 ANSI 화면 상태를 서버가 재생하거나 재구성하지 않습니다.

### 동시 Browser attachment

MVP에서 TerminalSession 하나에는 active Browser attachment를 하나만 둡니다.

같은 TerminalSession에 새로운 유효 `TERMINAL_ATTACH`가 성공하면 새 연결을 current attachment로 사용하고 이전 Browser 연결을 종료합니다. 브라우저 새로고침에서도 이 규칙을 사용합니다.

새 attach가 성공했다는 이유로 새 PTY를 만들지 않습니다.

## 5. Live framing

Live는 별도 Shell을 생성하지 않습니다. 강사가 선택한 source TerminalSession의 **OUTPUT을 중앙 Relay에서 해당 Class 학생에게 read-only fan-out**합니다.

Live WebSocket 하나는 LiveSession 하나의 subscription만 담당합니다.

### JSON Text frame

- `LIVE_SUBSCRIBE`
- `LIVE_SUBSCRIBED`
- `LIVE_ENDED`
- `ERROR`

### Binary frame

```text
Relay → Student Browser : source Terminal의 새 PTY OUTPUT raw bytes
```

학생 Browser가 Live WebSocket으로 Binary frame을 서버에 보내는 것은 허용하지 않습니다. INPUT/RESIZE/강사 Terminal 종료 같은 제어 요청도 Live protocol에는 존재하지 않으며 protocol/policy 위반으로 처리합니다.

## 6. LiveSession 정책

MVP에서는 **Class당 active LiveSession을 최대 1개**로 둡니다.

강사가 다른 Terminal을 공유하려면 기존 LiveSession을 종료하고 새 source TerminalSession으로 새로운 LiveSession을 시작합니다.

수명 관계는 비대칭입니다.

```text
LiveSession 종료
→ source TerminalSession / PTY는 유지

source TerminalSession / PTY 종료
→ LiveSession 종료
→ 모든 학생 subscriber에 LIVE_ENDED
```

강사 Browser만 비정상 단절되고 Connector의 source PTY가 D-21 grace 동안 살아 있다면 LiveSession도 계속 유지할 수 있습니다.

late join과 Live 재접속에서는 과거 OUTPUT, Transcript, 현재 화면 snapshot을 제공하지 않습니다.

## 7. Backpressure

Terminal/Live content를 무제한 메모리에 쌓지 않습니다.

### Live 학생

학생 연결마다 독립적인 bounded outbound Queue를 둡니다. 한 학생이 Queue 한도를 지속적으로 초과하면 OUTPUT byte를 조용히 drop해서 연결을 유지하지 않고 **해당 학생 WebSocket만 `SLOW_CONSUMER`로 종료**합니다.

이 동작은 강사 PTY와 다른 학생 subscriber를 block하지 않아야 합니다.

### Terminal Browser

Terminal owner Browser의 outbound write도 다른 Live subscriber를 block하지 않도록 독립적인 제한 Queue를 사용합니다. 한도를 넘으면 Browser attachment만 종료할 수 있으며 Connector의 PTY는 D-21의 grace 정책에 따라 유지됩니다.

정확한 byte/message 한도는 Integration/부하 테스트와 Runtime 설정에서 확정하며 wire contract에 숫자를 고정하지 않습니다.

## 8. 종료와 Lab Mutation

다음은 일반 네트워크 단절과 구분합니다.

- 사용자의 명시적 Terminal 종료
- Shell/PTY 자체 종료
- Reset
- Cleanup
- Live source Terminal 종료
- 서비스 배포/draining에 따른 종료

Reset/Cleanup처럼 TerminalSession을 의도적으로 종료하는 사건에서는 `TERMINAL_SESSION_ENDED`/`LIVE_ENDED`의 reason을 전달할 수 있으며 해당 Session은 D-21의 단순 Browser disconnect reconnect 대상이 아닙니다.

D-22의 중앙 Relay connection draining 시간과 Kubernetes `terminationGracePeriodSeconds`는 Platform/Runtime 설정이며 이 WSS Schema에 고정하지 않습니다.

## 9. 오류와 Close

JSON `ERROR.payload.code`를 UI 제어의 안정적인 오류 식별자로 사용하고 사람용 `message` 문자열 parsing에 의존하지 않습니다.

알려진 code 예시는 다음과 같습니다.

- `AUTH_REQUIRED`
- `INVALID_SESSION_TOKEN`
- `FORBIDDEN`
- `SESSION_NOT_FOUND`
- `SESSION_EXPIRED`
- `CONNECTOR_UNAVAILABLE`
- `PROTOCOL_ERROR`
- `SLOW_CONSUMER`
- `LAB_MUTATION`
- `SERVICE_RESTARTING`

새 code가 추가될 수 있으므로 Client는 unknown code fallback을 가져야 합니다.

v0.1 close code는 다음 최소 집합을 사용합니다.

| Close code | 의미 |
| --- | --- |
| `1000` | 정상 종료 |
| `1008` | protocol/policy 위반 |
| `1012` | 서버 재시작/배포 |
| `4001` | 인증 또는 Terminal Session Token 실패 |
| `4002` | 현재 사용자에게 Session 권한 없음 |
| `4003` | Session 없음 또는 reconnect grace 만료 |
| `4004` | 새 Browser attachment가 이전 attachment를 대체 |
| `4005` | 느린 수신자 Queue 초과 |
| `4006` | Reset/Cleanup/source 종료 등 제품 lifecycle에 따른 Session 종료 |

네트워크 단절은 Close frame 없이 발생할 수 있으므로 Client는 abnormal close도 처리해야 합니다.

## 10. 기록·관측 경계

Terminal/Live INPUT·OUTPUT 본문은 다음에 저장하지 않습니다.

- PostgreSQL
- Loki
- S3
- CloudWatch
- Connector/Relay disk
- trace payload

구조화 로그에는 content 대신 가능한 시점부터 `terminal_session_id`, `live_session_id`, `lab_instance_id`, `connector_id`, `request_id`와 control event의 Trace Context를 사용합니다.

`traceparent`/`tracestate`를 각 keystroke나 Binary OUTPUT chunk에 붙이지 않습니다. Trace는 Session 생성·attach·subscribe·종료 같은 control event에 사용합니다.

## 11. HTTP Control과 Connector Data 경계

TerminalSession/LiveSession 생성·종료와 Terminal Session Token 발급은 HTTP Control API가 담당하며 정확한 Endpoint/Schema는 Git OpenAPI에서 별도로 관리합니다.

Connector에는 Browser Cookie/사용자 Password/Terminal Session Token을 전달하지 않습니다. SaaS가 권한 검증 후 resolved Terminal target만 Connector Control WSS로 전달합니다.

실제 PTY byte stream은 별도 Connector Terminal Data WSS를 사용합니다.

```text
Browser Terminal WSS
        │
        ▼
Terminal / Live Relay
        │
        │ Connector Terminal Data WSS
        ▼
Connector
        │
        └─ VM SSH Connection → PTY Channel
```

Live는 Connector에 별도 Live stream을 만들지 않고 Relay가 source Terminal OUTPUT을 학생들에게 fan-out합니다.

## 12. 검증 기준

v0.1 구현 시 최소한 다음을 검증합니다.

- 권한 있는 학생/강사만 자신의 TerminalSession에 attach할 수 있습니다.
- 다른 사용자/다른 Class TerminalSession과 유효하지 않은 Token을 차단합니다.
- Browser 단절 후 기본 60초 이내 같은 PTY에 reconnect할 수 있습니다.
- grace 초과 후 기존 TerminalSession은 reconnect할 수 없습니다.
- 같은 TerminalSession에 새 Browser attach가 성공하면 이전 attachment만 종료됩니다.
- reconnect/late join에서 과거 OUTPUT을 replay하지 않습니다.
- Live 학생은 INPUT/RESIZE/강사 Session 종료를 수행할 수 없습니다.
- Class에 active LiveSession이 동시에 둘 이상 생기지 않습니다.
- source Terminal 종료 시 Live가 종료되지만 Live 종료만으로 source PTY가 종료되지 않습니다.
- 느린 학생 하나가 강사/다른 학생을 block하지 않습니다.
- 느린 Terminal Browser가 Live fan-out을 block하지 않습니다.
- Terminal/Live content가 로그·DB·trace·disk에 남지 않습니다.
- JSON Schema 정상/비정상 control message validation이 동작합니다.
