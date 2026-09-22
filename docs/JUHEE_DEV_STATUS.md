# 이주희 — Connector Control / WSS 개발 현황 및 산출물

> **담당자**: 이주희 (담당 A)  
> **Confluence 문서**: [Connector > 이주희](https://samsunglions.atlassian.net/wiki/spaces/SL/pages/15892482)  
> **기준 계획서**: [Connector MVP 개발 실행 계획](https://samsunglions.atlassian.net/wiki/spaces/SL/pages/12845071/Connector+MVP)  
> **세부 의사결정**: [현재 결정해야 하는 사안 — 2, 3번 세부 결정](https://samsunglions.atlassian.net/wiki/spaces/SL/pages/13598741/2+3+Preview)  
> **작업 브랜치**: `feat/SL-connector-control-wss`  
> **Git SSOT 저장소**: [ktcloud4-SL/rabbit-app](https://github.com/ktcloud4-SL/rabbit-app.git)

---

## 1. 역할 분담 및 담당 업무 (담당 A)

Connector는 2인이 분담하여 개발하며, 이주희는 **SaaS와의 통신 및 실시간 터미널 세션**을 총괄합니다.

* **WSS Client & TLS**: SaaS 공개 엔드포인트(443)로의 아웃바운드 WSS 연결 (`Sec-WebSocket-Protocol: labbit.connector.v1`)
* **Connector 인증**: `Authorization: Bearer <connector-credential>` 토큰 인증
* **핸드셰이크 & Heartbeat**: `HELLO` 전송, `HELLO_ACK` 수신, 15초 주기 `HEARTBEAT` 루프 및 45초 단절 감지
* **재접속(Reconnect)**: 지수 백오프(Exponential Backoff + Jitter) 기반 안전한 재연결
* **메시지 디스패처**: SaaS `OPERATION_COMMAND` 수신 및 Provider 인터페이스로 전달
* **터미널 세션 스트리밍**: `TERMINAL_OPEN` 수신, 관리망 SSH PTY 셸 생성, Terminal Data WSS 1:1 바이너리 스트리밍 (60초 Grace Period)

---

## 2. 개발 착수 전 준비 완료 내역 (준비 1 ~ 3 완료)

### 1) [준비 1] Scope / SSOT Freeze
* **구현 메시지 체크리스트 확정**:
  * Control 메시지: `HELLO`, `HELLO_ACK`, `HEARTBEAT`, `OPERATION_COMMAND`, `OPERATION_ACK`, `OPERATION_PROGRESS`, `OPERATION_RESULT`, `RECONCILE_REQUEST`, `RECONCILE_RESULT`, `ERROR`
  * Terminal Control 메시지: `TERMINAL_OPEN`, `TERMINAL_OPEN_RESULT`, `TERMINAL_CLOSE`, `TERMINAL_ENDED`
  * Terminal Data WSS: Text 프레임(`ATTACH`, `ATTACHED`, `RESIZE`, `CLOSE`, `ENDED`) + Binary 프레임(PTY 바이트 스트림)
* **개발 우선순위**: Preview는 Git 계약상 'HTTP 후속 범위'이므로, MVP에서는 **Control WSS**와 **Terminal Data WSS**에 집중.

### 2) [준비 2] Local / Repository 기본 세팅
* **Go 1.27.0** 로컬 환경 설치 및 검증 완료 (`go version go1.27.0 windows/amd64`)
* **작업 브랜치**: `feat/SL-connector-control-wss` 생성 완료
* **패키지 의존성**: `github.com/gorilla/websocket v1.5.3` 추가 완료

### 3) [준비 3] Shared Interface / Mock 공동 완료 (E2E 연동 검증 완료)
서빈 님의 PR #24(`internal/connector/provider`) 머지 후, 주희 님의 Control WSS 처리 코드와 공식 `MockProvider`를 연결하여 **"가짜 명령(SaaS) → Provider 호출(DispatchOperation) → 결과 응답(SaaS)"** 전 구간 E2E 검증을 완료했습니다.

* **프로토콜 모델 (`internal/connector/protocol/types.go`)**:
  * `BaseEnvelope`, `SafeError`, `OperationCommandMessage/Payload`, `OperationAckMessage/Payload`, `OperationResultMessage/Payload`, `Reconcile*` 모델 및 스키마 SSOT 정렬.
* **Control WSS Handler (`internal/connector/wss/handler.go`)**:
  * `OPERATION_COMMAND` 수신 시 Envelope 검증 (`operationId`, `labInstanceId`, `generation >= 1`)
  * `OPERATION_ACK(accepted: true)` 즉시 회신
  * `provider.DispatchOperation` 호출 및 결과를 `OPERATION_RESULT`로 회신 (`requestId`, `traceparent`, `replyToMessageId` 보존)
  * Provider 미분류 오류 발생 시 `UNKNOWN` 결과 정규화 및 내부 raw error 보호
  * `RECONCILE_REQUEST` 수신 시 `discoverCandidates` 기본값 `true` 적용 후 `provider.DispatchReconcile` 호출 및 `RECONCILE_RESULT` 회신
* **E2E 연동 테스트 통과 (`internal/connector/wss/handler_test.go`)**:
  * `TestHandler_OperationCommand_Provision_Success`: PASS
  * `TestHandler_OperationCommand_Unknown_OnUnclassifiedError`: PASS
  * `TestHandler_OperationCommand_MissingCorrelation_Rejected`: PASS
  * `TestHandler_ReconcileRequest_Success`: PASS

---

## 3. 핵심 아키텍처 및 통신 규칙 요약

| 항목 | 확정 규칙 | 근거 및 주의사항 |
| :--- | :--- | :--- |
| **통신 방향** | **Outbound-only WSS (443)** | 고객망 인바운드 개방 불필요 (방화벽 우회) |
| **네트워크 분리** | **Management Network (관리망)** | 실습망(Lab Net)과 분리된 폐쇄 관리망을 통해 각 VM의 22번 포트로 SSH 접근 |
| **WSS 소켓 분리** | **Control WSS vs Terminal Data WSS** | 제어 메시지와 PTY 화면 바이트 스트림 간섭 차단 |
| **터미널 소켓 규칙** | **세션당 1:1 독립 소켓 (멀티플렉싱 X)** | 세션별 독립 연결로 구현 복잡도 최소화 (Live fan-out은 SaaS가 담당) |
| **결과 불명 처리** | **UNKNOWN 결과 및 Reconciliation 필수** | 타임아웃 발생 시 즉시 재시도 금지 ➔ SaaS RECONCILE_REQUEST로 실제 자원 대조 |
| **비밀값 관리** | ***_FILE 경로 기반 주입** | 환경변수 문자열이 아닌 Secret 파일 경로로 주입받아 보안 유지 |

---

## 4. 코드베이스 구조 (rabbit-app)

```text
rabbit-app/
├── cmd/
│   └── labbit-connector/main.go       # Connector 프로세스 기동 진입점 (Graceful shutdown)
├── docs/
│   └── JUHEE_DEV_STATUS.md            # [이주희] 개발 현황 및 산출물 정리 문서
├── internal/
│   └── connector/
│       ├── app/app.go                 # 기본 실행 라이프사이클
│       ├── protocol/types.go          # [이주희] WSS Envelope 및 Operation 메시지 모델
│       ├── provider/
│       │   ├── interface.go           # [공동] 서빈님 연계 Provider 인터페이스
│       │   ├── mock.go                # [이주희] 단독 테스트용 Mock Provider
│       │   └── mock_test.go           # 인터페이스 검증 테스트
│       ├── mock/
│       │   ├── saas.go                # [이주희] 로컬 검증용 Mock SaaS WSS 서버
│       │   └── saas_test.go           # WSS 핸드셰이크 테스트
│       └── wss/                       # [다음 작업] WSS Client, Reconnect, Heartbeat
└── contracts/connector/               # Git SSOT JSON Schema & README
```

---

## 5. Milestone 개발 현황

### ✅ [M1-A] 기본 연결 및 HELLO 핸드셰이크 (완료)
* **WSS Client 구현 (`internal/connector/wss/client.go`)**:
  * `LABBIT_SAAS_BASE_URL` 기반 엔드포인트 파싱 (`wss://<host>/connector/v1/control` 또는 `ws://`)
  * `Authorization: Bearer <connector-credential>` 인증 헤더 전송
  * Subprotocol `labbit.connector.v1` 협상
  * **JSON 메시지 상한 (1 MiB)**: 최신 SSOT 계약에 맞춰 `conn.SetReadLimit(1048576)` 설정
* **HELLO / HELLO_ACK 핸드셰이크 구현**:
  * `HELLO` 메시지(`connectorVersion`, `runtimeId`, `startedAt`, `capabilities`) 전송
  * `HELLO_ACK`(`heartbeatIntervalSeconds`, `offlineTimeoutSeconds`) 수신 및 `replyToMessageId` 검증
* **단위 테스트 통과 (`internal/connector/wss/client_test.go`)**:
  * `TestClient_DialAndHello_Success`: MockSaaS와의 정상 연결 및 핸드셰이크 검증 (PASS)
  * `TestClient_Dial_AuthFailure`: 잘못된 토큰 시 401 Unauthorized 거부 검증 (PASS)
  * `TestClient_CredentialFile`: `*_FILE` 경로 기반 보안 토큰 주입 검증 (PASS)
  * `TestClient_ResolveEndpoint`: 다양한 스킴 및 경로의 WSS 엔드포인트 정규화 검증 (PASS)

---

## 6. 다음 개발 진행 계획 (M1-B 착수)

* **Heartbeat Loop 구현 (`internal/connector/heartbeat/loop.go`)**:
  * `HELLO_ACK`로 전달받은 주기(기본 15초)로 `HEARTBEAT` 전송
  * 45초 무응답 시 OFFLINE 판단 및 재접속 트리거
* **지수 백오프 Reconnect 루프**:
  * WSS 단절 감지 시 Exponential Backoff + Jitter 기반 자동 재연결
* **Integration Checkpoint #1 준비**:
  * Control Layer + Provider Adapter 단일 프로세스 기동 검증
